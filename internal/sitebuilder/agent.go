package sitebuilder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"agentic-ai-backend/internal/cloudworkspace"
	"agentic-ai-backend/internal/jobspec"
	"agentic-ai-backend/models"
	"agentic-ai-backend/services"
)

// Limits of one run, enforced here (WEBSITE-BUILDER.md, section 7).
const (
	MaxModelCalls   = 60
	MaxRepairs      = 5
	MaxReviewRounds = 2
	RunTimeout      = 20 * time.Minute
	// A build that nobody picks up means the execution worker is not running.
	BuildQueueLimit = 3 * time.Minute
	BuildTimeout    = 5 * time.Minute
	maxFileBytes    = 200 << 10
)

// When every model fails, the run waits and asks again for up to ModelWait before it
// fails (R5). Variables so tests can shorten them.
var (
	ModelWait       = 3 * time.Minute
	modelRetryEvery = 30 * time.Second
)

// Worker runs agent runs. It is its own process (cmd/agent-worker): it calls models
// and writes files into the site's workspace, and asks the execution worker to build
// and check the site in the sandbox. It never runs site code itself.
type Worker struct {
	DB            *gorm.DB
	Storage       cloudworkspace.WorkspaceStorage
	Models        ModelCaller
	// Illustrator draws the product illustrations; nil leaves products without pictures.
	Illustrator   Illustrator
	Settings      Settings
	Images        jobspec.Images
	MaxConcurrent int
	PollInterval  time.Duration
	Logf          func(format string, args ...any)
}

func (w *Worker) logf(format string, args ...any) {
	if w.Logf != nil {
		w.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

func (w *Worker) defaults() {
	if w.MaxConcurrent <= 0 {
		w.MaxConcurrent = 2
	}
	if w.PollInterval <= 0 {
		w.PollInterval = 2 * time.Second
	}
	if w.Images == (jobspec.Images{}) {
		w.Images = jobspec.DefaultImages()
		w.Images.Site = w.Settings.Image
	}
}

// Run works until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	w.defaults()
	slots := make(chan struct{}, w.MaxConcurrent)
	var running sync.WaitGroup
	ticker := time.NewTicker(w.PollInterval)
	defer ticker.Stop()
	lastReap := time.Time{}
	for {
		if time.Since(lastReap) > time.Minute {
			w.reapStale()
			lastReap = time.Now()
		}
	claim:
		for {
			select {
			case slots <- struct{}{}:
			default:
				break claim
			}
			run, err := w.ClaimNext(ctx)
			if err != nil || run == nil {
				<-slots
				if err != nil && ctx.Err() == nil {
					w.logf("[BUILDER] claim failed: %v", err)
				}
				break
			}
			running.Add(1)
			go func() {
				defer running.Done()
				defer func() { <-slots }()
				w.Execute(ctx, run)
			}()
		}
		select {
		case <-ctx.Done():
			running.Wait()
			return
		case <-ticker.C:
		}
	}
}

// ClaimNext takes the oldest queued run and marks it running.
func (w *Worker) ClaimNext(ctx context.Context) (*models.AgentRun, error) {
	var claimed *models.AgentRun
	err := w.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// A person's runs first, oldest first; a background run only when no other
		// background run is working.
		var backgroundBusy int64
		tx.Model(&models.AgentRun{}).Where("status = ? AND background = ?", models.RunRunning, true).Count(&backgroundBusy)
		query := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where("status = ?", models.RunQueued)
		if backgroundBusy > 0 {
			query = query.Where("background = ?", false)
		}
		var run models.AgentRun
		err := query.Order("background ASC").Order("id ASC").First(&run).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		now := time.Now()
		if run.CancelRequested {
			return tx.Model(&run).Updates(map[string]any{"status": models.RunCancelled, "finished_at": now}).Error
		}
		if err := tx.Model(&run).Updates(map[string]any{"status": models.RunRunning, "started_at": now}).Error; err != nil {
			return err
		}
		run.Status, run.StartedAt = models.RunRunning, &now
		claimed = &run
		return nil
	})
	return claimed, err
}

// RecoverOrphans fails the runs a previous worker left running when it stopped. Call
// it once at start, only where a single agent worker serves the database: with two
// workers it would fail the other one's runs.
func (w *Worker) RecoverOrphans() int64 {
	var orphans []models.AgentRun
	w.DB.Where("status = ?", models.RunRunning).Find(&orphans)
	for _, run := range orphans {
		var site models.Site
		language := "id"
		if w.DB.First(&site, run.SiteID).Error == nil {
			language = site.UILanguage
		}
		message, status := say(language, "failedFirst"), models.SiteStatusFailed
		if site.CurrentVersionID != nil {
			message, status = say(language, "failed"), models.SiteStatusReady
		}
		w.DB.Model(&run).Updates(map[string]any{"status": models.RunFailed, "error_message": message, "finished_at": time.Now()})
		w.DB.Model(&models.AgentRunStep{}).Where("agent_run_id = ? AND status = ?", run.ID, models.StepRunning).Update("status", models.StepFailed)
		w.DB.Model(&models.Site{}).Where("id = ?", run.SiteID).Update("status", status)
		w.DB.Model(&models.ExecutionJob{}).Where("agent_run_id = ? AND status IN ?", run.ID, []string{models.JobQueued, models.JobPreparing, models.JobRunning}).Update("cancel_requested", true)
	}
	return int64(len(orphans))
}

// reapStale fails runs whose worker died: running far past the run timeout.
func (w *Worker) reapStale() {
	cutoff := time.Now().Add(-(RunTimeout + 5*time.Minute))
	var stale []models.AgentRun
	w.DB.Where("status = ? AND started_at < ?", models.RunRunning, cutoff).Find(&stale)
	for _, run := range stale {
		w.DB.Model(&run).Updates(map[string]any{"status": models.RunFailed, "error_message": "The worker stopped before this run finished.", "finished_at": time.Now()})
		w.DB.Model(&models.Site{}).Where("id = ? AND status = ?", run.SiteID, models.SiteStatusBuilding).Update("status", models.SiteStatusReady)
	}
}

// job is the state of one run while it is being worked on.
type job struct {
	w         *Worker
	ctx       context.Context
	run       *models.AgentRun
	site      models.Site
	workspace models.CloudWorkspace
	filesDir  string
	brief     Brief
	direction Direction
	variant   string
	images    []Image
	lang      string // the language the person reads the builder in
	steps     int
	calls     int
	// progress records the finished parts of this work, so a stopped run continues.
	progressFile string
	progress     Progress
}

// userError is a failure whose message may be shown to the person as it is.
type userError struct{ message, detail string }

func (e *userError) Error() string { return e.message + ": " + e.detail }

// Execute works on one claimed run until it is finished, failed or cancelled.
func (w *Worker) Execute(parent context.Context, run *models.AgentRun) {
	w.defaults()
	ctx, cancel := context.WithTimeout(parent, RunTimeout)
	defer cancel()
	go func() { // a cancel request from the person stops the run between steps
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				var requested bool
				w.DB.Model(&models.AgentRun{}).Where("id = ?", run.ID).Select("cancel_requested").Scan(&requested)
				if requested {
					cancel()
					return
				}
			}
		}
	}()

	j := &job{w: w, ctx: ctx, run: run}
	err := j.load()
	if err == nil {
		err = func() (failure error) {
			defer func() {
				if recovered := recover(); recovered != nil {
					failure = fmt.Errorf("panic: %v", recovered)
				}
			}()
			return j.perform()
		}()
	}
	j.finish(err)
}

func (j *job) load() error {
	db := j.w.DB
	if err := db.First(&j.site, j.run.SiteID).Error; err != nil {
		return fmt.Errorf("site: %w", err)
	}
	j.lang = j.site.UILanguage
	if j.lang != "en" {
		j.lang = "id"
	}
	if err := db.Where("id = ? AND user_id = ?", j.site.WorkspaceID, j.site.UserID).First(&j.workspace).Error; err != nil {
		return fmt.Errorf("workspace: %w", err)
	}
	filesDir, err := j.w.Storage.FilesDir(j.site.UserID, j.workspace.StorageRef)
	if err != nil {
		return fmt.Errorf("workspace storage: %w", err)
	}
	j.filesDir = filesDir
	if err := json.Unmarshal([]byte(j.site.BriefJSON), &j.brief); err != nil {
		return fmt.Errorf("brief: %w", err)
	}
	if j.site.ImagesJSON != "" {
		_ = json.Unmarshal([]byte(j.site.ImagesJSON), &j.images)
	}
	key, variant := j.site.DesignDirection, j.site.DesignVariant
	if j.run.Direction != "" {
		key, variant = j.run.Direction, j.run.Variant
	}
	direction, ok := DirectionByKey(key)
	if !ok {
		return fmt.Errorf("unknown design direction %q", key)
	}
	if !direction.HasVariant(variant) {
		variant = ""
	}
	j.direction, j.variant = direction, variant
	j.loadProgress()
	db.Model(&models.Site{}).Where("id = ?", j.site.ID).Update("status", models.SiteStatusBuilding)
	return nil
}

// loadProgress finds what an earlier, stopped run of the same work finished. A record
// of other work is stale: the files it left are put back to the current version.
func (j *job) loadProgress() {
	j.progressFile = j.w.Settings.ProgressFile(j.site.ID)
	key := ProgressKey(j.run.Kind, j.run.Request, j.site.BriefJSON, j.direction.Key, j.variant)
	if found, ok := LoadProgress(j.progressFile); ok {
		if found.Key == key {
			j.progress = found
			if len(found.Brief) > 0 {
				var brief Brief
				if json.Unmarshal(found.Brief, &brief) == nil && brief.Name != "" {
					j.brief = brief
				}
			}
			return
		}
		if found.Written || found.Layout || len(found.Pages) > 0 {
			j.restoreCurrent()
		}
		ClearProgress(j.progressFile)
	}
	j.progress = Progress{Key: key}
}

// keep stores the progress record after a finished part.
func (j *job) keep() {
	if data, err := json.Marshal(j.brief); err == nil {
		j.progress.Brief = data
	}
	j.progress.Summary = j.run.Summary
	if err := SaveProgress(j.progressFile, j.progress); err != nil {
		j.w.logf("[BUILDER] run %d: saving progress failed: %v", j.run.ID, err)
	}
}

// started is true when an earlier run of this work left finished parts.
func (j *job) started() bool {
	return j.progress.Written || j.progress.Layout || len(j.progress.Pages) > 0
}

// restoreCurrent puts the workspace back to the site's current version.
func (j *job) restoreCurrent() {
	if j.site.CurrentVersionID == nil || j.filesDir == "" {
		return
	}
	var version models.SiteVersion
	if j.w.DB.First(&version, *j.site.CurrentVersionID).Error != nil {
		return
	}
	if err := RestoreVersion(j.w.Settings.VersionDir(j.site.ID, version.Number), j.filesDir); err != nil {
		j.w.logf("[BUILDER] run %d: restoring version %d failed: %v", j.run.ID, version.Number, err)
	}
}

// step records something the run does; the person sees summary.
func (j *job) step(kind, summary string) *models.AgentRunStep {
	step := &models.AgentRunStep{AgentRunID: j.run.ID, Index: j.steps, Kind: kind, Summary: clip(summary, 290), Status: models.StepRunning}
	j.steps++
	j.w.DB.Create(step)
	return step
}

func (j *job) done(step *models.AgentRunStep, status, detail string) {
	j.w.DB.Model(step).Updates(map[string]any{"status": status, "detail": clip(services.RedactSecrets(detail), 8000)})
}

// ask calls a model, within the run's limit of model calls.
func (j *job) ask(messages []Message, need Need) (string, string, error) {
	if err := j.ctx.Err(); err != nil {
		return "", "", err
	}
	if j.calls >= MaxModelCalls {
		return "", "", &userError{say(j.lang, "failed"), "the run used all its model calls"}
	}
	j.calls++
	j.w.DB.Model(j.run).UpdateColumn("model_calls", j.calls)
	answer, model, err := j.w.Models.Complete(j.ctx, j.site.UserID, need, messages)
	if errors.Is(err, ErrTokenLimit) {
		// Waiting does not help: the allowance starts again next month.
		return "", model, &userError{say(j.lang, "tokenLimit"), err.Error()}
	}
	if err != nil && j.ctx.Err() == nil {
		// Every model failed: wait and ask again for a while before giving up.
		var waiting *models.AgentRunStep
		deadline := time.Now().Add(ModelWait)
		for err != nil && j.ctx.Err() == nil && time.Now().Before(deadline) && j.calls < MaxModelCalls {
			if waiting == nil {
				waiting = j.step(models.StepInfo, say(j.lang, "waitModel"))
			}
			select {
			case <-j.ctx.Done():
			case <-time.After(modelRetryEvery):
			}
			if j.ctx.Err() != nil {
				break
			}
			j.calls++
			j.w.DB.Model(j.run).UpdateColumn("model_calls", j.calls)
			answer, model, err = j.w.Models.Complete(j.ctx, j.site.UserID, need, messages)
		}
		if waiting != nil {
			if err == nil {
				j.done(waiting, models.StepDone, "a model answered again")
			} else {
				j.done(waiting, models.StepFailed, err.Error())
			}
		}
	}
	if err != nil && j.ctx.Err() == nil {
		return "", model, &userError{say(j.lang, "noModel"), err.Error()}
	}
	return answer, model, err
}

// apply writes the files a model returned, where the agent may write.
func (j *job) apply(answer string) (written []string, refused []string) {
	for _, edit := range ParseFiles(answer) {
		rel, err := CheckAgentPath(edit.Path)
		if err != nil || len(edit.Content) > maxFileBytes || IsPartialFile(edit.Content) {
			refused = append(refused, edit.Path)
			continue
		}
		if err := WriteSiteFile(j.filesDir, rel, edit.Content); err != nil {
			refused = append(refused, edit.Path)
			continue
		}
		written = append(written, rel)
	}
	return written, refused
}

func describe(model string, written, refused []string) string {
	text := "model: " + model
	if len(written) > 0 {
		text += "\nwrote: " + strings.Join(written, ", ")
	}
	if len(refused) > 0 {
		text += "\nrefused (not allowed): " + strings.Join(refused, ", ")
	}
	return text
}

// writeGenerated writes the files that come from the brief and the direction.
func (j *job) writeGenerated() error {
	if err := RefreshFixedFiles(j.filesDir); err != nil {
		return err
	}
	files := map[string]string{
		"web/src/site.ts":    SiteTS(j.brief, j.images),
		"web/src/routes.tsx": RoutesTSX(j.brief),
		"web/index.html":     IndexHTML(j.brief),
		"site.json":          SiteJSON(j.brief, j.direction.Key),
		// Always written: a site made before the finished sections existed gets its look too.
		"web/src/look.ts": LookTS(j.direction),
	}
	// The theme is written on every run as well: it follows only from the look, its colour
	// variant and the owner's colours, and a site made earlier gets new theme rules (the
	// movement, the alternating sections) with its next change.
	files["web/src/theme.css"] = ThemeCSS(j.direction, j.variant, j.brief.Colours)
	for rel, content := range files {
		if err := WriteSiteFile(j.filesDir, rel, content); err != nil {
			return err
		}
	}
	return nil
}

func (j *job) perform() error {
	restyle := j.run.Direction != "" && (j.run.Direction != j.site.DesignDirection || j.run.Variant != j.site.DesignVariant)
	first := j.run.Kind == models.AgentRunCreate
	if restyle {
		// Another look brings its own colours; the owner's earlier ones belonged to the old look.
		j.brief.Colours = nil
	}

	step := j.step(models.StepInfo, say(j.lang, "prepare"))
	if err := j.writeGenerated(); err != nil {
		j.done(step, models.StepFailed, err.Error())
		return err
	}
	j.done(step, models.StepDone, "direction: "+j.direction.Key)

	// Product illustrations come before the pages, so the pages are written knowing
	// which products have a picture. Another look draws them again in its colours.
	if len(j.brief.Products) > 0 {
		j.illustrate()
		if err := j.writeGenerated(); err != nil { // site.ts lists the pictures
			return err
		}
	}

	switch {
	case j.progress.Written:
		// An earlier run of the same work wrote everything; continue at the check.
		step := j.step(models.StepInfo, say(j.lang, "resumed"))
		j.done(step, models.StepDone, "all files were written by an earlier run")
		if j.run.Summary == "" {
			j.run.Summary = j.progress.Summary
		}
	case first:
		if err := j.create(); err != nil {
			return err
		}
	default:
		if err := j.revise(restyle); err != nil {
			return err
		}
	}
	j.progress.Written = true
	j.keep()
	if err := j.fixTokens(); err != nil {
		return err
	}
	if err := j.buildAndRepair(); err != nil {
		return err
	}
	if err := j.review(); err != nil {
		return err
	}
	// Saving needs no model and must not be lost to a run that is out of time.
	j.ctx = context.WithoutCancel(j.ctx)
	return j.saveVersion(restyle)
}

func (j *job) create() error {
	layoutFile := "web/src/components/Layout.tsx"
	if j.started() {
		step := j.step(models.StepInfo, say(j.lang, "resumed"))
		j.done(step, models.StepDone, fmt.Sprintf("layout: %t, pages already written: %s", j.progress.Layout, strings.Join(j.progress.Pages, ", ")))
	}
	if !j.progress.Layout || ReadSiteFile(j.filesDir, layoutFile) == "" {
		step := j.step(models.StepModel, say(j.lang, "layout"))
		answer, model, err := j.ask(LayoutPrompt(j.brief, j.direction), NeedCoding)
		if err != nil {
			j.done(step, models.StepFailed, err.Error())
			return err
		}
		written, refused := j.apply(answer)
		j.done(step, models.StepDone, describe(model, written, refused))
		j.progress.Layout = true
		j.keep()
	}
	layout := ReadSiteFile(j.filesDir, layoutFile)

	for index, page := range j.brief.Pages {
		file := page.PageFile(index)
		if j.progress.HasPage(file) && ReadSiteFile(j.filesDir, file) != "" {
			continue
		}
		if err := j.writePage(index, page, layout); err != nil {
			return err
		}
		j.progress.Pages = append(j.progress.Pages, file)
		j.keep()
	}
	return nil
}

// writePage asks for one page, once more when the answer did not contain it.
func (j *job) writePage(index int, page BriefPage, layout string) error {
	step := j.step(models.StepModel, say(j.lang, "page", page.Title))
	file := page.PageFile(index)
	var detail []string
	for attempt := 0; attempt < 2; attempt++ {
		answer, model, err := j.ask(PagePrompt(j.brief, j.direction, index, layout), NeedCoding)
		if err != nil {
			j.done(step, models.StepFailed, err.Error())
			return err
		}
		written, refused := j.apply(answer)
		detail = append(detail, describe(model, written, refused))
		if ReadSiteFile(j.filesDir, file) != "" && contains(written, file) {
			j.done(step, models.StepDone, strings.Join(detail, "\n"))
			return nil
		}
	}
	j.done(step, models.StepFailed, strings.Join(detail, "\n"))
	return &userError{say(j.lang, "failed"), "the model did not write " + file}
}

// allPartial is true when every file in an answer was returned only in part.
func allPartial(answer string) bool {
	edits := ParseFiles(answer)
	for _, edit := range edits {
		if !IsPartialFile(edit.Content) {
			return false
		}
	}
	return len(edits) > 0
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

func (j *job) revise(restyle bool) error {
	summary := say(j.lang, "revise")
	if restyle {
		summary = say(j.lang, "restyle")
	}
	step := j.step(models.StepModel, summary)
	palette := ThemePalette(j.direction, j.variant, j.brief.Colours)
	prompt := RevisePrompt(j.brief, j.direction, palette, j.run.Request, AgentFiles(j.filesDir), restyle)
	answer, model, err := j.ask(prompt, NeedCoding)
	onlyColours := len(ParseFiles(answer)) == 0 && ParseColours(answer) != nil
	if err == nil && !onlyColours && (len(ParseFiles(answer)) == 0 || allPartial(answer)) {
		// No usable file: ask once more, insisting on complete files.
		retry := append(append([]Message(nil), prompt...), Message{Role: "assistant", Content: clip(answer, 2000)}, Message{Role: "user", Content: "Return every changed file complete, in <file path=\"...\"> blocks, with no part left out or replaced by \"...\"."})
		answer, model, err = j.ask(retry, NeedCoding)
	}
	// From here on (review, fixes, later runs) the request is part of the brief. The
	// brief is stored only when the run succeeds.
	j.brief.AddChange(j.run.Request)
	if err != nil {
		j.done(step, models.StepFailed, err.Error())
		return err
	}
	pagesChanged := false
	if raw := ParsePages(answer); raw != "" {
		var pages []BriefPage
		if json.Unmarshal([]byte(raw), &pages) == nil && len(pages) > 0 {
			updated := j.brief
			updated.Pages = pages
			if updated.Normalize() == nil {
				j.brief = updated
				pagesChanged = true
			}
		}
	}
	written, refused := j.apply(answer)
	if text := ParseSummary(answer); text != "" {
		j.run.Summary = clip(text, 500)
	}
	j.done(step, models.StepDone, describe(model, written, refused))
	// Colours the owner asked for go into the theme tokens, kept readable.
	coloursChanged := false
	if asked := ParseColours(answer); asked != nil {
		j.brief.Colours = MergeColours(j.direction.PaletteFor(j.variant), j.brief.Colours, asked)
		coloursChanged = true
		colourStep := j.step(models.StepInfo, say(j.lang, "colours"))
		j.done(colourStep, models.StepDone, coloursText(ThemePalette(j.direction, j.variant, j.brief.Colours)))
	}
	if pagesChanged || coloursChanged {
		if err := j.writeGenerated(); err != nil {
			return err
		}
	}
	// Every page the routes name must exist; a page the model forgot is written now.
	layout := ReadSiteFile(j.filesDir, "web/src/components/Layout.tsx")
	for index, page := range j.brief.Pages {
		if ReadSiteFile(j.filesDir, page.PageFile(index)) == "" {
			if err := j.writePage(index, page, layout); err != nil {
				return err
			}
		}
	}
	return nil
}

// fixTokens repairs what the plain-text design check finds (hex colours, palette
// classes, fonts in code, gradients, pictures from the internet, counting numbers the
// owner never gave).
func (j *job) fixTokens() error {
	for attempt := 0; attempt < 2; attempt++ {
		files := AgentFiles(j.filesDir)
		known := KnownNumbers(briefText(j.brief))
		var problems []string
		involved := map[string]string{}
		for rel, content := range files {
			found := append(CheckTokens(rel, content), CheckLanguage(rel, content, j.brief.Language)...)
			found = append(found, CheckCountUp(rel, content, known)...)
			found = append(found, CheckStats(rel, content, known)...)
			found = append(found, CheckUsesBlocks(rel, content)...)
			if len(found) > 0 {
				problems = append(problems, found...)
				involved[rel] = content
			}
		}
		if len(problems) == 0 {
			return nil
		}
		sort.Strings(problems)
		step := j.step(models.StepModel, say(j.lang, "tokens"))
		answer, model, err := j.ask(RepairPrompt(j.brief, j.direction, strings.Join(problems, "\n"), involved), NeedCoding)
		if err != nil {
			j.done(step, models.StepFailed, err.Error())
			return err
		}
		written, refused := j.apply(answer)
		j.done(step, models.StepDone, strings.Join(problems, "\n")+"\n"+describe(model, written, refused))
	}
	return nil // what is left is reported again by the review; it does not stop the site
}

var errorFile = regexp.MustCompile(`(web/src/[A-Za-z0-9_./-]+\.tsx?|server/[A-Za-z0-9_./-]+\.(?:js|sql))`)

// filesFor picks the files a build error is about, or every agent file when the
// error names none the agent can change.
func (j *job) filesFor(errorText string) map[string]string {
	all := AgentFiles(j.filesDir)
	picked := map[string]string{}
	for _, match := range errorFile.FindAllString(errorText, -1) {
		if content, ok := all[match]; ok {
			picked[match] = content
		}
	}
	if len(picked) == 0 {
		return all
	}
	return picked
}

func (j *job) buildAndRepair() error {
	for attempt := 0; ; attempt++ {
		step := j.step(models.StepBuild, say(j.lang, "check"))
		errorsText, infra, err := j.build()
		switch {
		case err != nil:
			j.done(step, models.StepFailed, err.Error())
			return err
		case infra != "":
			j.done(step, models.StepFailed, infra)
			return &userError{say(j.lang, "busy"), infra}
		case errorsText == "":
			j.done(step, models.StepDone, say(j.lang, "checked"))
			return nil
		}
		j.done(step, models.StepFailed, errorsText)
		if attempt >= MaxRepairs {
			return &userError{say(j.lang, "failed"), "the site still fails its check after repairs"}
		}
		repair := j.step(models.StepModel, say(j.lang, "repair"))
		answer, model, err := j.ask(RepairPrompt(j.brief, j.direction, clip(ShortenErrors(errorsText), 6000), j.filesFor(errorsText)), NeedCoding)
		if err != nil {
			j.done(repair, models.StepFailed, err.Error())
			return err
		}
		written, refused := j.apply(answer)
		j.done(repair, models.StepDone, describe(model, written, refused))
	}
}

// build queues the sandboxed build and check, and waits for it. It returns the
// problems to repair, or infra when the build could not run at all.
func (j *job) build() (problems, infra string, err error) {
	spec, resolveErr := jobspec.Resolve(jobspec.SiteBuildJob, j.w.Images, j.filesDir)
	if resolveErr != nil {
		return "", resolveErr.Error(), nil
	}
	runID := j.run.ID
	record := models.ExecutionJob{
		UserID: j.site.UserID, WorkspaceID: j.workspace.ID, AgentRunID: &runID, JobType: spec.JobType,
		Command: spec.Label, Runtime: spec.Runtime, Status: models.JobQueued, TimeoutSeconds: int(BuildTimeout.Seconds()),
	}
	if err := j.w.DB.Create(&record).Error; err != nil {
		return "", "", err
	}
	queuedAt := time.Now()
	ticker := time.NewTicker(1500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-j.ctx.Done():
			j.w.DB.Model(&record).Update("cancel_requested", true)
			return "", "", j.ctx.Err()
		case <-ticker.C:
		}
		var current models.ExecutionJob
		if err := j.w.DB.First(&current, record.ID).Error; err != nil {
			return "", "", err
		}
		switch current.Status {
		case models.JobQueued:
			if time.Since(queuedAt) > BuildQueueLimit {
				j.w.DB.Model(&record).Updates(map[string]any{"cancel_requested": true, "status": models.JobCancelled})
				return "", "the execution worker did not pick up the build (is cmd/execution-worker running with CLOUD_EXECUTION_ENABLED=true?)", nil
			}
		case models.JobPreparing, models.JobRunning:
		case models.JobCompleted:
			return "", "", nil
		case models.JobTimedOut:
			return "The check did not finish within 5 minutes. A page probably loops forever while it renders.", "", nil
		case models.JobFailed:
			if current.ExitCode == nil { // the sandbox itself failed, not the site
				return "", current.Error, nil
			}
			text := strings.TrimSpace(current.Stderr)
			if text == "" {
				text = strings.TrimSpace(current.Stdout + "\n" + current.Error)
			}
			return text, "", nil
		case models.JobCancelled:
			return "", "", context.Canceled
		}
	}
}

// review asks a reviewer for problems and fixes them. A fix that breaks the site is
// undone: the reviewed version that worked is kept.
// reviewTimeNeeded is how much of the run's time a review round needs; with less
// left, the website that passed its check is kept as it is.
const reviewTimeNeeded = 6 * time.Minute

// outOfTime is true when the run's own time limit ended (not a cancel by the person).
func (j *job) outOfTime() bool { return errors.Is(j.ctx.Err(), context.DeadlineExceeded) }

func (j *job) review() (failure error) {
	var before map[string]string
	// The site built and passed its check before the review. When the run's time runs
	// out during a review round, the files go back to that state and it is kept.
	// The same holds when no model can be reached for the review: the review only
	// polishes, so a website that passed its check is not lost to a busy model.
	defer func() {
		var user *userError
		noModel := errors.As(failure, &user) && user.message == say(j.lang, "noModel")
		if failure != nil && before != nil && (j.outOfTime() || noModel) {
			j.restoreFiles(before)
			step := j.step(models.StepInfo, say(j.lang, "reviewSkipped"))
			j.done(step, models.StepDone, "the review could not finish ("+failure.Error()+"); the checked website is kept")
			failure = nil
		}
	}()
	for round := 0; round < MaxReviewRounds; round++ {
		if deadline, ok := j.ctx.Deadline(); ok && time.Until(deadline) < reviewTimeNeeded {
			return nil
		}
		before = AgentFiles(j.filesDir)
		step := j.step(models.StepReview, say(j.lang, "review"))
		answer, model, err := j.ask(ReviewPrompt(j.brief, j.direction, before), NeedCoding)
		if err != nil {
			j.done(step, models.StepFailed, err.Error())
			return err
		}
		var result ReviewResult
		if DecodeJSON(answer, &result) != nil || result.Pass || len(result.Fixes) == 0 {
			j.done(step, models.StepDone, "model: "+model+"\n"+say(j.lang, "reviewed"))
			return nil
		}
		byFile := map[string][]string{}
		for _, fix := range result.Fixes {
			if _, ok := before[fix.File]; ok && len(byFile) < 4 {
				byFile[fix.File] = append(byFile[fix.File], fix.Problem)
			}
		}
		raw, _ := json.MarshalIndent(result.Fixes, "", "  ")
		j.done(step, models.StepDone, "model: "+model+"\n"+string(raw))
		if len(byFile) == 0 {
			return nil
		}
		files := make([]string, 0, len(byFile))
		for file := range byFile {
			files = append(files, file)
		}
		sort.Strings(files)
		for _, file := range files {
			fix := j.step(models.StepModel, say(j.lang, "improve", friendlyName(file, j.brief, j.lang)))
			answer, model, err := j.ask(FixPrompt(j.brief, j.direction, file, before[file], byFile[file]), NeedCoding)
			if err != nil {
				j.done(fix, models.StepFailed, err.Error())
				return err
			}
			written, refused := j.apply(answer)
			j.done(fix, models.StepDone, describe(model, written, refused))
		}
		if err := j.fixTokens(); err != nil {
			return err
		}
		if err := j.buildAndRepair(); err != nil {
			var user *userError
			if !errors.As(err, &user) || j.ctx.Err() != nil {
				return err
			}
			// Put the files back as they were before this round; that version built.
			j.restoreFiles(before)
			return j.buildAndRepair()
		}
	}
	return nil
}

// restoreFiles puts the agent's files back as they were: changed files are written
// again and files added since are removed. The built output is not touched.
func (j *job) restoreFiles(files map[string]string) {
	for rel := range AgentFiles(j.filesDir) {
		if _, existed := files[rel]; !existed {
			if absolute, _, err := cloudworkspace.Resolve(j.filesDir, rel); err == nil {
				_ = os.Remove(absolute)
			}
		}
	}
	for rel, content := range files {
		_ = WriteSiteFile(j.filesDir, rel, content)
	}
}

// friendlyName names a file the way the person knows it: the page title.
func friendlyName(file string, b Brief, language string) string {
	for index, page := range b.Pages {
		if page.PageFile(index) == file {
			return page.Title
		}
	}
	names := map[string][2]string{
		"Layout":   {"menu dan bagian bawah", "the menu and footer"},
		"NotFound": {"halaman tidak ditemukan", "the page-not-found page"},
	}
	pick := func(pair [2]string) string {
		if language == "en" {
			return pair[1]
		}
		return pair[0]
	}
	if pair, ok := names[strings.TrimSuffix(path.Base(file), path.Ext(file))]; ok {
		return pick(pair)
	}
	return pick([2]string{"bagian lain", "another part"})
}

func (j *job) saveVersion(restyle bool) error {
	step := j.step(models.StepInfo, say(j.lang, "save"))
	var last struct{ Number int }
	j.w.DB.Model(&models.SiteVersion{}).Where("site_id = ?", j.site.ID).Select("COALESCE(MAX(number), 0) AS number").Scan(&last)
	number := last.Number + 1
	if err := SaveVersion(j.filesDir, j.w.Settings.VersionDir(j.site.ID, number)); err != nil {
		j.done(step, models.StepFailed, err.Error())
		return err
	}
	summary := j.run.Summary
	if summary == "" {
		switch {
		case j.run.Kind == models.AgentRunCreate:
			summary = say(j.lang, "createdDone")
		case restyle:
			summary = say(j.lang, "restyleDone")
		default:
			summary = say(j.lang, "reviseDone")
		}
	}
	runID := j.run.ID
	version := models.SiteVersion{SiteID: j.site.ID, AgentRunID: &runID, Number: number, Summary: summary}
	if err := j.w.DB.Create(&version).Error; err != nil {
		j.done(step, models.StepFailed, err.Error())
		return err
	}
	briefJSON, _ := json.Marshal(j.brief)
	now := time.Now()
	j.w.DB.Model(&models.Site{}).Where("id = ?", j.site.ID).Updates(map[string]any{
		"current_version_id": version.ID, "status": models.SiteStatusReady, "brief_json": string(briefJSON),
		"design_direction": j.direction.Key, "design_variant": j.variant, "preview_requested_at": now, "name": j.brief.Name,
	})
	j.run.Summary = summary
	j.done(step, models.StepDone, fmt.Sprintf("version %d", number))
	return nil
}

// finish records how the run ended. After a failure the workspace goes back to the
// site's current version, so the preview and the files agree again.
func (j *job) finish(err error) {
	now := time.Now()
	db := j.w.DB
	if err == nil {
		db.Model(j.run).Updates(map[string]any{"status": models.RunCompleted, "finished_at": now, "summary": j.run.Summary})
		if j.progressFile != "" {
			ClearProgress(j.progressFile)
		}
		return
	}
	status, message, detail := models.RunFailed, "", err.Error()
	var user *userError
	switch {
	case errors.Is(err, context.Canceled) || (j.ctx != nil && j.ctx.Err() == context.Canceled):
		status, message = models.RunCancelled, say(j.lang, "cancelled")
	case errors.As(err, &user):
		message = user.message
	case j.site.CurrentVersionID == nil:
		message = say(j.lang, "failedFirst")
	default:
		message = say(j.lang, "failed")
	}
	j.w.logf("[BUILDER] run %d ended %s: %s", j.run.ID, status, services.RedactSecrets(detail))
	if j.steps > 0 || status == models.RunCancelled {
		step := j.step(models.StepInfo, message)
		j.done(step, models.StepFailed, detail)
	}
	db.Model(j.run).Updates(map[string]any{"status": status, "finished_at": now, "error_message": message})

	siteStatus := models.SiteStatusFailed
	if j.site.ID != 0 && j.site.CurrentVersionID != nil {
		siteStatus = models.SiteStatusReady
		// Finished parts are kept for "Continue"; the preview still shows the current
		// version, which a stopped run never changes.
		if !j.started() {
			j.restoreCurrent()
		}
	}
	if j.site.ID != 0 {
		db.Model(&models.Site{}).Where("id = ?", j.site.ID).Update("status", siteStatus)
	}
}
