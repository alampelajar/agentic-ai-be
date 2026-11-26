package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	mathrand "math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"agentic-ai-backend/config"
	"agentic-ai-backend/internal/cloudworkspace"
	"agentic-ai-backend/internal/sitebuilder"
	"agentic-ai-backend/models"
)

// Website Builder API. Every route works only on the signed-in person's own sites.
// Errors carry a code the interface translates; the message is a fallback.

const (
	maxSiteImages        = 8
	maxSiteImageBytes    = 3 << 20
	maxActiveRunsPerUser = 2
)

var builderModels sitebuilder.ModelCaller = BuilderModels{}

func siteError(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{"ok": false, "code": code, "error": message})
}

func siteLanguage(raw string) string {
	if strings.TrimSpace(raw) == "en" {
		return "en"
	}
	return "id"
}

func loadOwnedSite(c *gin.Context) (*models.Site, uint, bool) {
	userID, ok := getCurrentUserID(c)
	if !ok {
		siteError(c, http.StatusUnauthorized, "unauthorized", "User tidak ditemukan.")
		return nil, 0, false
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		siteError(c, http.StatusNotFound, "not_found", "Website tidak ditemukan.")
		return nil, 0, false
	}
	var site models.Site
	if err := config.DB.Where("id = ? AND user_id = ?", id, userID).First(&site).Error; err != nil {
		siteError(c, http.StatusNotFound, "not_found", "Website tidak ditemukan.")
		return nil, 0, false
	}
	return &site, userID, true
}

func siteFilesDir(site *models.Site) (string, error) {
	storage, _, err := workspaceStorage()
	if err != nil {
		return "", err
	}
	var workspace models.CloudWorkspace
	if err := config.DB.Where("id = ? AND user_id = ?", site.WorkspaceID, site.UserID).First(&workspace).Error; err != nil {
		return "", err
	}
	return storage.FilesDir(site.UserID, workspace.StorageRef)
}

func activeRun(siteID uint) *models.AgentRun {
	var run models.AgentRun
	if err := config.DB.Where("site_id = ? AND status IN ?", siteID, []string{models.RunQueued, models.RunRunning}).Order("id DESC").First(&run).Error; err != nil {
		return nil
	}
	return &run
}

func siteImages(site *models.Site) []sitebuilder.Image {
	var images []sitebuilder.Image
	if site.ImagesJSON != "" {
		_ = json.Unmarshal([]byte(site.ImagesJSON), &images)
	}
	return images
}

type directionView struct {
	Key         string            `json:"key"`
	Variant     string            `json:"variant"`
	VariantName string            `json:"variant_name"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Palette     map[string]string `json:"palette"`
	Serif       bool              `json:"serif"`
	Radius      string            `json:"radius"`
	SampleURL   string            `json:"sample_url"`
	// Blocks lets the look's card draw where things sit on its pages.
	Blocks sitebuilder.Blocks `json:"blocks"`
}

func viewDirection(d sitebuilder.Direction, variant, language string) directionView {
	view := directionView{
		Key: d.Key, Variant: variant, Name: d.Name[language], Description: d.Description[language], Palette: d.PaletteFor(variant),
		Serif: strings.Contains(d.Heading.Fallback, "serif") && !strings.Contains(d.Heading.Fallback, "sans-serif"), Radius: d.Radius["lg"],
		SampleURL: sitebuilder.LoadSettings().SampleURL(d.Key), Blocks: d.Blocks,
	}
	if view.SampleURL == "" {
		view.SampleURL = showcasedExample(d.Key, language)
	}
	for _, v := range d.Variants {
		if v.Key == variant {
			view.VariantName = v.Name[language]
		}
	}
	return view
}

// showcasedExample is the live address of a website in the gallery made with the
// look, preferring one written in the person's language, for a look whose own
// example website has not been made yet. "" when no such website is shown.
func showcasedExample(key, language string) string {
	var sites []models.Site
	config.DB.Where("design_direction = ? AND showcased_at IS NOT NULL AND published_release_id IS NOT NULL AND slug IS NOT NULL", key).
		Where(ownerNotInDeletion).Order("showcased_at DESC").Limit(10).Find(&sites)
	chosen := -1
	for index := range sites {
		if sites[index].Language == language {
			chosen = index
			break
		}
	}
	if chosen < 0 && len(sites) > 0 {
		chosen = 0
	}
	if chosen < 0 {
		return ""
	}
	return sitebuilder.LoadSettings().LiveURL(*sites[chosen].Slug)
}

// bestVariant is the palette of d that matches the wished colours best: a variant,
// or "" for the base palette.
func bestVariant(d sitebuilder.Direction, colours []string) string {
	count := func(tags []string) int {
		matched := 0
		for _, tag := range tags {
			for _, colour := range colours {
				if tag == colour {
					matched++
				}
			}
		}
		return matched
	}
	variant, best := "", count(d.Tags)
	for _, v := range d.Variants {
		if matched := count(v.Tags); matched > best {
			variant, best = v.Key, matched
		}
	}
	return variant
}

func siteView(site *models.Site) gin.H {
	settings := sitebuilder.LoadSettings()
	view := gin.H{
		"id": site.ID, "name": site.Name, "description": site.Description, "language": site.Language, "ui_language": site.UILanguage, "kind": site.Kind, "design_variant": site.DesignVariant,
		"status": site.Status, "design_direction": site.DesignDirection, "created_at": site.CreatedAt, "updated_at": site.UpdatedAt,
		"has_version": site.CurrentVersionID != nil, "published": site.PublishedReleaseID != nil, "slug": site.Slug, "live_url": "",
	}
	if site.PublishedReleaseID != nil && site.Slug != nil {
		view["live_url"] = settings.LiveURL(*site.Slug)
	}
	// A stopped run left finished parts: "Continue" reuses them (R5).
	if progress, found := sitebuilder.LoadProgress(settings.ProgressFile(site.ID)); found {
		view["can_continue"] = progress.Written || progress.Layout || len(progress.Pages) > 0
	}
	if run := activeRun(site.ID); run != nil {
		view["active_run_id"] = run.ID
	}
	return view
}

// GetSites lists the person's sites, newest first.
func GetSites(c *gin.Context) {
	userID, ok := getCurrentUserID(c)
	if !ok {
		siteError(c, http.StatusUnauthorized, "unauthorized", "User tidak ditemukan.")
		return
	}
	var sites []models.Site
	config.DB.Where("user_id = ?", userID).Order("updated_at DESC").Find(&sites)
	list := make([]gin.H, 0, len(sites))
	for index := range sites {
		list = append(list, siteView(&sites[index]))
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "sites": list, "plan": builderPlanFor(userID)})
}

// refuseWithoutCredits answers 402 credit_limit when the person cannot pay for work
// that uses cost credits, and reports whether it did.
func refuseWithoutCredits(c *gin.Context, userID uint, cost int) bool {
	if builderPlanFor(userID).CanAfford(cost) {
		return false
	}
	siteError(c, http.StatusPaymentRequired, "credit_limit", "Kredit Anda tidak cukup. Tambah kredit untuk melanjutkan.")
	return true
}

// CreateSite starts a site from a description: a workspace with the template and a draft.
func CreateSite(c *gin.Context) {
	userID, ok := getCurrentUserID(c)
	if !ok {
		siteError(c, http.StatusUnauthorized, "unauthorized", "User tidak ditemukan.")
		return
	}
	var req struct {
		Description string `json:"description"`
		Language    string `json:"language"`
		// RemixOf is a gallery website whose look and page list the new one copies.
		RemixOf *uint `json:"remix_of"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		siteError(c, http.StatusBadRequest, "invalid", "Format permintaan tidak valid.")
		return
	}
	description := strings.TrimSpace(req.Description)
	if length := utf8.RuneCountInString(description); length < 10 || length > 2000 {
		siteError(c, http.StatusBadRequest, "description_length", "Ceritakan website Anda dalam 10 sampai 2000 karakter.")
		return
	}
	if req.RemixOf != nil {
		if _, found := showcasedSite(*req.RemixOf); !found {
			siteError(c, http.StatusNotFound, "not_found", "Website contoh ini tidak lagi ada di galeri.")
			return
		}
	}
	plan := builderPlanFor(userID)
	if plan.AtSiteLimit() {
		siteError(c, http.StatusConflict, "site_limit", "Jumlah website sudah mencapai batas. Hapus website yang tidak dipakai.")
		return
	}
	if !plan.CanAfford(createCredits) {
		siteError(c, http.StatusPaymentRequired, "credit_limit", "Kredit Anda tidak cukup. Tambah kredit untuk melanjutkan.")
		return
	}
	var count int64
	storage, limits, err := workspaceStorage()
	if err != nil {
		siteError(c, http.StatusInternalServerError, "storage", "Penyimpanan tidak tersedia.")
		return
	}
	config.DB.Model(&models.CloudWorkspace{}).Where("user_id = ?", userID).Count(&count)
	// Every website owns one workspace, so the plan's number of websites is allowed
	// even where it is more than the general workspace limit (premium: 50, not 10).
	if int(count) >= max(limits.MaxWorkspacesPerUser, plan.SiteLimit) {
		siteError(c, http.StatusConflict, "workspace_limit", "Jumlah proyek sudah mencapai batas. Hapus workspace yang tidak dipakai.")
		return
	}

	ref, err := storage.Create(userID)
	if err != nil {
		siteError(c, http.StatusInternalServerError, "storage", "Gagal menyiapkan website.")
		return
	}
	filesDir, _ := storage.FilesDir(userID, ref)
	metaDir, _ := storage.MetaDir(userID, ref)
	cleanup := func() { _ = storage.Delete(userID, ref) }
	if err := sitebuilder.CopyTemplate(filesDir); err != nil {
		cleanup()
		log.Printf("[SITES] template: %v", err)
		siteError(c, http.StatusInternalServerError, "storage", "Gagal menyiapkan website.")
		return
	}
	_ = cloudworkspace.SaveBaseline(filesDir, metaDir, limits)
	bytes, files, _ := storage.Usage(userID, ref)

	name := firstWords(description, 6)
	// The website's language comes from the description; the builder speaks the
	// person's interface language.
	language, uiLanguage := sitebuilder.DetectLanguage(description), siteLanguage(req.Language)
	var site models.Site
	err = config.DB.Transaction(func(tx *gorm.DB) error {
		workspace := models.CloudWorkspace{UserID: userID, Name: "Website: " + clipRunes(name, 80), Description: clipRunes(description, 480),
			SourceType: models.WorkspaceSourceEmpty, Status: models.WorkspaceStatusReady, StorageRef: ref, DefaultRuntime: "node", FileCount: files, SizeBytes: bytes}
		if err := tx.Create(&workspace).Error; err != nil {
			return err
		}
		site = models.Site{UserID: userID, WorkspaceID: workspace.ID, Name: clipRunes(name, 100), Description: description, Language: language, UILanguage: uiLanguage, Status: models.SiteStatusDraft, RemixOf: req.RemixOf}
		return tx.Create(&site).Error
	})
	if err != nil {
		cleanup()
		siteError(c, http.StatusInternalServerError, "storage", "Gagal menyiapkan website.")
		return
	}
	recordAudit(c, "site.create", "site", strconv.FormatUint(uint64(site.ID), 10), site.Name, "Website dibuat")
	c.JSON(http.StatusCreated, gin.H{"ok": true, "site": siteView(&site)})
}

func firstWords(text string, count int) string {
	words := strings.Fields(text)
	if len(words) > count {
		words = words[:count]
	}
	return strings.TrimRight(strings.Join(words, " "), ".,;:")
}

func clipRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return text
}

// GetSite returns one site with what the builder screen needs.
func GetSite(c *gin.Context) {
	site, userID, ok := loadOwnedSite(c)
	if !ok {
		return
	}
	view := siteView(site)
	var questions sitebuilder.QuestionSet
	if site.QuestionsJSON != "" {
		_ = json.Unmarshal([]byte(site.QuestionsJSON), &questions)
		view["questions"] = questions.Questions
	}
	if site.BriefJSON != "" {
		var brief sitebuilder.Brief
		if json.Unmarshal([]byte(site.BriefJSON), &brief) == nil {
			pages := make([]string, 0, len(brief.Pages))
			for _, page := range brief.Pages {
				pages = append(pages, page.Title)
			}
			view["brief"] = gin.H{"name": brief.Name, "tagline": brief.Tagline, "summary": brief.Summary, "pages": pages, "contact": brief.Contact}
		}
	}
	if direction, found := sitebuilder.DirectionByKey(site.DesignDirection); found {
		view["direction"] = viewDirection(direction, site.DesignVariant, uiLanguageOf(site))
	}
	view["images"] = siteImages(site)
	var versions []models.SiteVersion
	config.DB.Where("site_id = ?", site.ID).Order("number DESC").Limit(30).Find(&versions)
	view["versions"] = versions
	current := 0
	for _, version := range versions {
		if site.CurrentVersionID != nil && version.ID == *site.CurrentVersionID {
			current = version.Number
		}
	}
	view["current_version"] = current
	if site.PublishedReleaseID != nil {
		var release models.SiteRelease
		if config.DB.First(&release, *site.PublishedReleaseID).Error == nil {
			var version models.SiteVersion
			config.DB.First(&version, release.SiteVersionID)
			view["published_version"] = version.Number
			view["published_at"] = release.PublishedAt
		}
	}
	var last models.AgentRun
	if config.DB.Where("site_id = ?", site.ID).Order("id DESC").First(&last).Error == nil {
		view["last_run"] = last
	}
	view["suggested_slug"] = sitebuilder.SuggestSlug(site.Name)
	view["plan"] = builderPlanFor(userID)
	c.JSON(http.StatusOK, gin.H{"ok": true, "site": view})
}

// UpdateSite renames a site.
func UpdateSite(c *gin.Context) {
	site, _, ok := loadOwnedSite(c)
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Name) == "" || utf8.RuneCountInString(req.Name) > 100 {
		siteError(c, http.StatusBadRequest, "invalid_name", "Nama tidak valid.")
		return
	}
	config.DB.Model(site).Update("name", strings.TrimSpace(req.Name))
	c.JSON(http.StatusOK, gin.H{"ok": true, "site": siteView(site)})
}

// DeleteSite removes a site, its workspace, versions and data. The site runner stops
// its containers within seconds, because the site is no longer in the database.
func DeleteSite(c *gin.Context) {
	site, _, ok := loadOwnedSite(c)
	if !ok {
		return
	}
	if err := removeSite(site); err != nil {
		siteError(c, http.StatusInternalServerError, "delete_failed", "Website gagal dihapus.")
		return
	}
	recordAudit(c, "site.delete", "site", strconv.FormatUint(uint64(site.ID), 10), site.Name, "Website dihapus")
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// removeSite deletes a site with its runs, versions, releases and workspace, then its
// folders in the background. Used by the owner and by admins.
func removeSite(site *models.Site) error {
	userID := site.UserID
	config.DB.Model(&models.AgentRun{}).Where("site_id = ? AND status IN ?", site.ID, []string{models.RunQueued, models.RunRunning}).Update("cancel_requested", true)
	var workspace models.CloudWorkspace
	hasWorkspace := config.DB.Where("id = ? AND user_id = ?", site.WorkspaceID, userID).First(&workspace).Error == nil
	err := config.DB.Transaction(func(tx *gorm.DB) error {
		var runIDs []uint
		tx.Model(&models.AgentRun{}).Where("site_id = ?", site.ID).Pluck("id", &runIDs)
		if len(runIDs) > 0 {
			tx.Where("agent_run_id IN ?", runIDs).Delete(&models.AgentRunStep{})
		}
		tx.Where("site_id = ?", site.ID).Delete(&models.AgentRun{})
		tx.Where("site_id = ?", site.ID).Delete(&models.SiteRelease{})
		tx.Where("site_id = ?", site.ID).Delete(&models.SiteVersion{})
		if hasWorkspace {
			tx.Where("workspace_id = ?", workspace.ID).Delete(&models.ExecutionJob{})
			tx.Delete(&workspace)
		}
		return tx.Delete(site).Error
	})
	if err != nil {
		return err
	}
	siteDir := sitebuilder.LoadSettings().SiteDir(site.ID)
	go func() {
		// The runner needs a moment to stop the containers that use these folders.
		for attempt := 0; attempt < 10; attempt++ {
			time.Sleep(6 * time.Second)
			if os.RemoveAll(siteDir) == nil {
				break
			}
		}
		if hasWorkspace {
			if storage, _, err := workspaceStorage(); err == nil {
				_ = storage.Delete(userID, workspace.StorageRef)
			}
		}
	}()
	return nil
}

func uiLanguageOf(site *models.Site) string {
	if site.UILanguage == "en" {
		return "en"
	}
	return "id"
}

// SiteQuestions returns what the planner understood, the questions still open (zero
// to four), the website's language and three looks, coloured as the person wished.
func SiteQuestions(c *gin.Context) {
	site, userID, ok := loadOwnedSite(c)
	if !ok {
		return
	}
	var set sitebuilder.QuestionSet
	if site.QuestionsJSON != "" && c.Query("refresh") != "1" {
		_ = json.Unmarshal([]byte(site.QuestionsJSON), &set)
	} else {
		// Asking the AI is free, but only for someone who can pay for what follows.
		if refuseWithoutCredits(c, userID, reviseCredits) {
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), builderFastTimeout+20*time.Second)
		defer cancel()
		answer, _, err := builderModels.Complete(ctx, userID, sitebuilder.NeedFast, sitebuilder.QuestionsPrompt(site.Description, uiLanguageOf(site)))
		decoded := err == nil && sitebuilder.DecodeJSON(answer, &set) == nil
		if !decoded || !usableQuestions(set) {
			if err != nil {
				log.Printf("[SITES] questions for site %d: %v", site.ID, err)
			}
			fallback := sitebuilder.DefaultQuestions(uiLanguageOf(site))
			if decoded {
				fallback.Kind, fallback.SuggestedName, fallback.Language = set.Kind, set.SuggestedName, set.Language
				fallback.Understood, fallback.KnownContact, fallback.LookWish = set.Understood, set.KnownContact, set.LookWish
			}
			set = fallback
		}
		set = cleanQuestions(set, site.Description)
		raw, _ := json.Marshal(set)
		updates := map[string]any{"questions_json": string(raw), "kind": clipRunes(set.Kind, 30)}
		if site.BriefJSON == "" {
			updates["language"] = set.Language
			if set.SuggestedName != "" {
				updates["name"] = clipRunes(set.SuggestedName, 100)
			}
		}
		// Two requests at once (a page opened twice) must show the same questions: only the
		// first answer is stored, and every request returns what is stored.
		query := config.DB.Model(&models.Site{}).Where("id = ?", site.ID)
		if c.Query("refresh") != "1" {
			query = query.Where("questions_json = ''")
		}
		query.Updates(updates)
		var stored models.Site
		if config.DB.First(&stored, site.ID).Error == nil {
			_ = json.Unmarshal([]byte(stored.QuestionsJSON), &set)
			site.Kind, site.Name, site.Language = stored.Kind, stored.Name, stored.Language
		}
	}
	language := uiLanguageOf(site)
	colours := sitebuilder.ColourTags(site.Description, set.LookWish)
	var last string
	config.DB.Model(&models.Site{}).Where("user_id = ? AND id <> ? AND design_direction <> ''", userID, site.ID).Order("updated_at DESC").Limit(1).Pluck("design_direction", &last)
	moods := sitebuilder.MoodTags(site.Description, set.LookWish)
	picked := sitebuilder.PickDirections(site.Kind, last, colours, moods, mathrand.New(mathrand.NewPCG(uint64(time.Now().UnixNano()), uint64(site.ID))))
	directions := make([]directionView, 0, len(picked)+1)
	// A copied website offers the look of the website it copies first.
	var remix gin.H
	if source := remixSource(site); source != nil {
		if direction, found := sitebuilder.DirectionByKey(source.DesignDirection); found && direction.HasVariant(source.DesignVariant) {
			directions = append(directions, viewDirection(direction, source.DesignVariant, language))
			remix = gin.H{"name": source.Name, "direction": direction.Key, "variant": source.DesignVariant}
		}
	}
	for _, pick := range picked {
		if len(directions) >= len(picked) {
			break
		}
		if remix != nil && pick.Direction.Key == remix["direction"] {
			continue
		}
		directions = append(directions, viewDirection(pick.Direction, pick.Variant, language))
	}
	all := make([]directionView, 0)
	for _, direction := range sitebuilder.Directions() {
		all = append(all, viewDirection(direction, bestVariant(direction, colours), language))
	}
	c.JSON(http.StatusOK, gin.H{
		"ok": true, "questions": set.Questions, "kind": set.Kind, "suggested_name": set.SuggestedName,
		"language": site.Language, "understood": set.Understood, "known_contact": set.KnownContact, "look_wish": set.LookWish,
		"colours": colours, "directions": directions, "all_directions": all, "remix": remix,
	})
}

// remixSource is the website a copied website copies, when it still exists. Only its
// look and the names of its pages are used.
func remixSource(site *models.Site) *models.Site {
	if site.RemixOf == nil {
		return nil
	}
	var source models.Site
	if config.DB.First(&source, *site.RemixOf).Error != nil {
		return nil
	}
	return &source
}

// usableQuestions accepts no question at all (a detailed description), but not a
// question without text or choices.
func usableQuestions(set sitebuilder.QuestionSet) bool {
	if len(set.Questions) == 0 && len(set.Understood) == 0 {
		return false
	}
	for _, question := range set.Questions {
		if strings.TrimSpace(question.Text) == "" || (len(question.Options) < 2 && !question.Free) {
			return false
		}
	}
	return true
}

func cleanQuestions(set sitebuilder.QuestionSet, description string) sitebuilder.QuestionSet {
	if set.Language != "id" && set.Language != "en" {
		set.Language = sitebuilder.DetectLanguage(description)
	}
	if len(set.Understood) > 6 {
		set.Understood = set.Understood[:6]
	}
	facts := set.Understood[:0]
	for _, fact := range set.Understood {
		fact.Label, fact.Value = clipRunes(strings.TrimSpace(fact.Label), 40), clipRunes(strings.TrimSpace(fact.Value), 200)
		if fact.Label != "" && fact.Value != "" {
			facts = append(facts, fact)
		}
	}
	set.Understood = facts
	given := set.KnownContact
	set.KnownContact = sitebuilder.Contact{Phone: clipRunes(strings.TrimSpace(given.Phone), 40), WhatsApp: clipRunes(strings.TrimSpace(given.WhatsApp), 40),
		Email: clipRunes(strings.TrimSpace(given.Email), 120), Address: clipRunes(strings.TrimSpace(given.Address), 300), Hours: clipRunes(strings.TrimSpace(given.Hours), 200)}
	set.LookWish = clipRunes(strings.TrimSpace(set.LookWish), 200)
	known := map[string]bool{}
	for _, kind := range sitebuilder.Kinds() {
		known[kind] = true
	}
	set.Kind = strings.ToLower(strings.TrimSpace(set.Kind))
	if !known[set.Kind] {
		set.Kind = "general"
	}
	if len(set.Questions) > 4 {
		set.Questions = set.Questions[:4]
	}
	seen := map[string]bool{}
	for index := range set.Questions {
		question := &set.Questions[index]
		question.ID = sitebuilder.PageSlug(question.ID)
		if question.ID == "" || seen[question.ID] {
			question.ID = fmt.Sprintf("q%d", index+1)
		}
		seen[question.ID] = true
		question.Text = clipRunes(strings.TrimSpace(question.Text), 200)
		if question.Free {
			// One question in their own words at most; it has no choices.
			if seen["_free"] {
				question.Free = false
			} else {
				seen["_free"] = true
				question.Options, question.Multiple = nil, false
			}
		}
		if len(question.Options) > 5 {
			question.Options = question.Options[:5]
		}
		for option := range question.Options {
			question.Options[option] = clipRunes(strings.TrimSpace(question.Options[option]), 80)
		}
	}
	set.SuggestedName = clipRunes(strings.TrimSpace(set.SuggestedName), 100)
	return set
}

// SiteBrief turns the answers into the brief and returns its summary for the person.
func SiteBrief(c *gin.Context) {
	site, userID, ok := loadOwnedSite(c)
	if !ok {
		return
	}
	if activeRun(site.ID) != nil {
		siteError(c, http.StatusConflict, "run_active", "Website sedang dikerjakan.")
		return
	}
	cost := createCredits
	if site.CurrentVersionID != nil {
		cost = reviseCredits
	}
	if refuseWithoutCredits(c, userID, cost) {
		return
	}
	var req struct {
		Name      string              `json:"name"`
		Answers   map[string][]string `json:"answers"`
		Extra     string              `json:"extra"`
		Contact   sitebuilder.Contact `json:"contact"`
		Direction string              `json:"direction"`
		Variant   string              `json:"variant"`
		Language  string              `json:"language"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		siteError(c, http.StatusBadRequest, "invalid", "Format permintaan tidak valid.")
		return
	}
	direction, found := sitebuilder.DirectionByKey(req.Direction)
	if !found || !direction.HasVariant(req.Variant) {
		siteError(c, http.StatusBadRequest, "direction_required", "Pilih salah satu tampilan.")
		return
	}
	language := site.Language
	if req.Language == "id" || req.Language == "en" {
		language = req.Language
	}
	name := clipRunes(strings.TrimSpace(req.Name), 100)
	if name == "" {
		name = site.Name
	}
	contact := sitebuilder.Contact{
		Phone: clipRunes(strings.TrimSpace(req.Contact.Phone), 40), WhatsApp: clipRunes(strings.TrimSpace(req.Contact.WhatsApp), 40),
		Email: clipRunes(strings.TrimSpace(req.Contact.Email), 120), Address: clipRunes(strings.TrimSpace(req.Contact.Address), 300),
		Hours: clipRunes(strings.TrimSpace(req.Contact.Hours), 200),
	}
	var set sitebuilder.QuestionSet
	_ = json.Unmarshal([]byte(site.QuestionsJSON), &set)
	answers := map[string][]string{}
	for _, question := range set.Questions {
		for _, answer := range req.Answers[question.ID] {
			if answer = clipRunes(strings.TrimSpace(answer), 200); answer != "" && len(answers[question.ID]) < 6 {
				answers[question.ID] = append(answers[question.ID], answer)
			}
		}
	}
	if extra := clipRunes(strings.TrimSpace(req.Extra), 1000); extra != "" {
		answers["_extra"] = []string{extra}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), builderFastTimeout+20*time.Second)
	defer cancel()
	input := sitebuilder.BriefInput{Description: site.Description, Language: language, UILanguage: uiLanguageOf(site), Kind: site.Kind, Name: name,
		Understood: set.Understood, LookWish: set.LookWish, Questions: set.Questions, Answers: answers, Contact: contact, Images: siteImages(site), Direction: direction}
	if source := remixSource(site); source != nil {
		input.PageTitles = briefPageTitles(source)
	}
	answer, _, err := builderModels.Complete(ctx, userID, sitebuilder.NeedFast, sitebuilder.BriefPrompt(input))
	var brief sitebuilder.Brief
	if err == nil {
		err = sitebuilder.DecodeJSON(answer, &brief)
	}
	if err == nil {
		brief.Name = name
		brief.Language, brief.Kind, brief.Contact = language, site.Kind, contact
		brief.ForgetIllustrations() // only the worker draws them; never a path from the model
		err = brief.Normalize()
	}
	if err != nil {
		log.Printf("[SITES] brief for site %d: %v", site.ID, err)
		siteError(c, http.StatusBadGateway, "plan_failed", "Rencana website belum berhasil dibuat. Coba lagi.")
		return
	}
	briefJSON, _ := json.Marshal(brief)
	answersJSON, _ := json.Marshal(answers)
	config.DB.Model(site).Updates(map[string]any{"brief_json": string(briefJSON), "answers_json": string(answersJSON), "design_direction": direction.Key, "design_variant": req.Variant, "name": name, "language": language})
	pages := make([]string, 0, len(brief.Pages))
	for _, page := range brief.Pages {
		pages = append(pages, page.Title)
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "brief": gin.H{"name": brief.Name, "tagline": brief.Tagline, "summary": brief.Summary, "pages": pages, "contact": brief.Contact, "language": language}})
}

// UploadSiteImage stores a photo or the logo in the site's public folder.
func UploadSiteImage(c *gin.Context) {
	site, _, ok := loadOwnedSite(c)
	if !ok {
		return
	}
	images := siteImages(site)
	if len(images) >= maxSiteImages {
		siteError(c, http.StatusConflict, "image_limit", "Maksimal 8 gambar.")
		return
	}
	header, err := c.FormFile("file")
	if err != nil || header.Size <= 0 || header.Size > maxSiteImageBytes {
		siteError(c, http.StatusBadRequest, "image_size", "Gambar harus berukuran sampai 3 MB.")
		return
	}
	file, err := header.Open()
	if err != nil {
		siteError(c, http.StatusBadRequest, "image_type", "Gambar tidak dapat dibaca.")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxSiteImageBytes+1))
	if err != nil || len(data) > maxSiteImageBytes {
		siteError(c, http.StatusBadRequest, "image_size", "Gambar harus berukuran sampai 3 MB.")
		return
	}
	extension := map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/webp": ".webp"}[http.DetectContentType(data)]
	if extension == "" {
		siteError(c, http.StatusBadRequest, "image_type", "Gunakan gambar PNG, JPG atau WEBP.")
		return
	}
	filesDir, err := siteFilesDir(site)
	if err != nil {
		siteError(c, http.StatusInternalServerError, "storage", "Penyimpanan tidak tersedia.")
		return
	}
	random := make([]byte, 8)
	_, _ = rand.Read(random)
	name := hex.EncodeToString(random) + extension
	if err := sitebuilder.WriteSiteFile(filesDir, "web/public/uploads/"+name, string(data)); err != nil {
		siteError(c, http.StatusInternalServerError, "storage", "Gambar gagal disimpan.")
		return
	}
	logo := c.PostForm("logo") == "true"
	alt := clipRunes(strings.TrimSpace(c.PostForm("alt")), 120)
	if alt == "" {
		alt = strings.TrimSuffix(filepath.Base(header.Filename), filepath.Ext(header.Filename))
		alt = clipRunes(strings.NewReplacer("-", " ", "_", " ").Replace(alt), 120)
	}
	if logo { // one logo: a new one replaces the old
		kept := images[:0]
		for _, image := range images {
			if !image.Logo {
				kept = append(kept, image)
			}
		}
		images = kept
	}
	images = append(images, sitebuilder.Image{Src: "/uploads/" + name, Alt: alt, Logo: logo})
	raw, _ := json.Marshal(images)
	config.DB.Model(site).Update("images_json", string(raw))
	c.JSON(http.StatusCreated, gin.H{"ok": true, "images": images})
}

// DeleteSiteImage forgets an uploaded image (the next run stops using it).
func DeleteSiteImage(c *gin.Context) {
	site, _, ok := loadOwnedSite(c)
	if !ok {
		return
	}
	src := c.Query("src")
	images := siteImages(site)
	kept := make([]sitebuilder.Image, 0, len(images))
	for _, image := range images {
		if image.Src != src {
			kept = append(kept, image)
		}
	}
	raw, _ := json.Marshal(kept)
	config.DB.Model(site).Update("images_json", string(raw))
	c.JSON(http.StatusOK, gin.H{"ok": true, "images": kept})
}

// StartSiteRun queues a create or revise run for the agent worker.
func StartSiteRun(c *gin.Context) {
	site, userID, ok := loadOwnedSite(c)
	if !ok {
		return
	}
	if !executionEnabled() {
		siteError(c, http.StatusServiceUnavailable, "builder_disabled", "Pembuat website belum diaktifkan di server ini.")
		return
	}
	var req struct {
		Kind      string `json:"kind"`
		Request   string `json:"request"`
		Direction string `json:"direction"`
		Variant   string `json:"variant"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		siteError(c, http.StatusBadRequest, "invalid", "Format permintaan tidak valid.")
		return
	}
	if site.BriefJSON == "" {
		siteError(c, http.StatusConflict, "brief_required", "Selesaikan pertanyaan terlebih dahulu.")
		return
	}
	if activeRun(site.ID) != nil {
		siteError(c, http.StatusConflict, "run_active", "Website sedang dikerjakan.")
		return
	}
	var active int64
	config.DB.Model(&models.AgentRun{}).Where("user_id = ? AND status IN ?", userID, []string{models.RunQueued, models.RunRunning}).Count(&active)
	if active >= maxActiveRunsPerUser {
		siteError(c, http.StatusTooManyRequests, "run_limit", "Tunggu sampai website lain selesai dikerjakan.")
		return
	}
	run := models.AgentRun{SiteID: site.ID, UserID: userID, Status: models.RunQueued}
	request := strings.TrimSpace(req.Request)
	switch req.Kind {
	case models.AgentRunCreate:
		run.Kind = models.AgentRunCreate
	case models.AgentRunRevise:
		if site.CurrentVersionID == nil {
			siteError(c, http.StatusConflict, "no_version", "Website belum punya versi untuk diubah.")
			return
		}
		run.Kind = models.AgentRunRevise
		if req.Direction != "" {
			direction, found := sitebuilder.DirectionByKey(req.Direction)
			if !found || !direction.HasVariant(req.Variant) {
				siteError(c, http.StatusBadRequest, "direction_required", "Tampilan tidak dikenal.")
				return
			}
			run.Direction, run.Variant = req.Direction, req.Variant
		}
		if run.Direction == "" && (utf8.RuneCountInString(request) < 3) {
			siteError(c, http.StatusBadRequest, "request_required", "Tuliskan perubahan yang Anda inginkan.")
			return
		}
	default:
		siteError(c, http.StatusBadRequest, "invalid", "Jenis pekerjaan tidak dikenal.")
		return
	}
	if utf8.RuneCountInString(request) > 2000 {
		siteError(c, http.StatusBadRequest, "request_length", "Permintaan terlalu panjang.")
		return
	}
	// Paid when it succeeds; the work already started counts against the balance now.
	if refuseWithoutCredits(c, userID, runCredits(run.Kind)) {
		return
	}
	run.Request = request
	if err := config.DB.Create(&run).Error; err != nil {
		siteError(c, http.StatusInternalServerError, "run_failed", "Pekerjaan gagal dimulai.")
		return
	}
	config.DB.Model(site).Update("status", models.SiteStatusBuilding)
	c.JSON(http.StatusAccepted, gin.H{"ok": true, "run": run})
}

func loadSiteRun(c *gin.Context, site *models.Site) (*models.AgentRun, bool) {
	id, err := strconv.ParseUint(c.Param("runId"), 10, 64)
	var run models.AgentRun
	if err != nil || config.DB.Where("id = ? AND site_id = ?", id, site.ID).First(&run).Error != nil {
		siteError(c, http.StatusNotFound, "not_found", "Pekerjaan tidak ditemukan.")
		return nil, false
	}
	return &run, true
}

func runState(runID uint) (models.AgentRun, []models.AgentRunStep) {
	var run models.AgentRun
	var steps []models.AgentRunStep
	config.DB.First(&run, runID)
	config.DB.Where("agent_run_id = ?", runID).Order("index ASC").Find(&steps)
	return run, steps
}

func runFinished(status string) bool {
	return status == models.RunCompleted || status == models.RunFailed || status == models.RunCancelled
}

// GetSiteRun returns a run and its steps.
func GetSiteRun(c *gin.Context) {
	site, _, ok := loadOwnedSite(c)
	if !ok {
		return
	}
	run, ok := loadSiteRun(c, site)
	if !ok {
		return
	}
	current, steps := runState(run.ID)
	c.JSON(http.StatusOK, gin.H{"ok": true, "run": current, "steps": steps})
}

// SiteRunEvents streams the run's state as server-sent events until it ends.
func SiteRunEvents(c *gin.Context) {
	site, _, ok := loadOwnedSite(c)
	if !ok {
		return
	}
	run, ok := loadSiteRun(c, site)
	if !ok {
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")
	ctx := c.Request.Context()
	deadline := time.Now().Add(25 * time.Minute)
	last := ""
	lastSent := time.Now()
	for time.Now().Before(deadline) {
		current, steps := runState(run.ID)
		payload, _ := json.Marshal(gin.H{"run": current, "steps": steps})
		if string(payload) != last {
			last = string(payload)
			lastSent = time.Now()
			fmt.Fprintf(c.Writer, "event: state\ndata: %s\n\n", payload)
			c.Writer.Flush()
		} else if time.Since(lastSent) > 15*time.Second {
			fmt.Fprint(c.Writer, ": keep-alive\n\n")
			c.Writer.Flush()
			lastSent = time.Now()
		}
		if runFinished(current.Status) {
			fmt.Fprint(c.Writer, "event: end\ndata: {}\n\n")
			c.Writer.Flush()
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

// CancelSiteRun asks the worker to stop a run.
func CancelSiteRun(c *gin.Context) {
	site, _, ok := loadOwnedSite(c)
	if !ok {
		return
	}
	run, ok := loadSiteRun(c, site)
	if !ok {
		return
	}
	if runFinished(run.Status) {
		c.JSON(http.StatusOK, gin.H{"ok": true, "run": run})
		return
	}
	updates := map[string]any{"cancel_requested": true}
	if run.Status == models.RunQueued {
		updates["status"], updates["finished_at"] = models.RunCancelled, time.Now()
		status := models.SiteStatusDraft
		if site.CurrentVersionID != nil {
			status = models.SiteStatusReady
		}
		config.DB.Model(site).Update("status", status)
	}
	config.DB.Model(run).Updates(updates)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// RestoreSiteVersion makes an older version the current one again. The published
// site changes only when the person publishes again.
func RestoreSiteVersion(c *gin.Context) {
	site, _, ok := loadOwnedSite(c)
	if !ok {
		return
	}
	if activeRun(site.ID) != nil {
		siteError(c, http.StatusConflict, "run_active", "Website sedang dikerjakan.")
		return
	}
	number, _ := strconv.Atoi(c.Param("number"))
	var version models.SiteVersion
	if config.DB.Where("site_id = ? AND number = ?", site.ID, number).First(&version).Error != nil {
		siteError(c, http.StatusNotFound, "not_found", "Versi tidak ditemukan.")
		return
	}
	filesDir, err := siteFilesDir(site)
	if err == nil {
		err = sitebuilder.RestoreVersion(sitebuilder.LoadSettings().VersionDir(site.ID, version.Number), filesDir)
		// The parts of a stopped run belong to the version left behind.
		sitebuilder.ClearProgress(sitebuilder.LoadSettings().ProgressFile(site.ID))
	}
	if err != nil {
		log.Printf("[SITES] restore site %d version %d: %v", site.ID, number, err)
		siteError(c, http.StatusInternalServerError, "restore_failed", "Versi gagal dipulihkan.")
		return
	}
	// The brief goes back with the files: its pages must match the restored routes.
	if raw, readErr := os.ReadFile(filepath.Join(sitebuilder.LoadSettings().VersionDir(site.ID, version.Number), "source", "site.json")); readErr == nil {
		var saved struct {
			Direction string          `json:"direction"`
			Brief     json.RawMessage `json:"brief"`
		}
		if json.Unmarshal(raw, &saved) == nil && len(saved.Brief) > 0 {
			config.DB.Model(site).Updates(map[string]any{"brief_json": string(saved.Brief), "design_direction": saved.Direction})
		}
	}
	now := time.Now()
	config.DB.Model(site).Updates(map[string]any{"current_version_id": version.ID, "preview_requested_at": now, "status": models.SiteStatusReady})
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// SitePreview returns a fresh signed preview address and keeps the preview running.
func SitePreview(c *gin.Context) {
	site, _, ok := loadOwnedSite(c)
	if !ok {
		return
	}
	if site.CurrentVersionID == nil {
		siteError(c, http.StatusConflict, "no_version", "Website belum punya versi untuk ditampilkan.")
		return
	}
	now := time.Now()
	config.DB.Model(site).UpdateColumn("preview_requested_at", now)
	url, expires := sitebuilder.LoadSettings().PreviewURL(site.ID, now)
	c.JSON(http.StatusOK, gin.H{"ok": true, "url": url, "expires_at": expires})
}

func slugProblem(slug string, siteID uint) string {
	switch err := sitebuilder.CheckSlug(slug); {
	case errors.Is(err, sitebuilder.ErrSlugFormat):
		return "slug_format"
	case errors.Is(err, sitebuilder.ErrSlugReserved):
		return "slug_reserved"
	}
	var count int64
	config.DB.Model(&models.Site{}).Where("slug = ? AND id <> ?", slug, siteID).Count(&count)
	if count > 0 {
		return "slug_taken"
	}
	return ""
}

// SiteSlugAvailable checks an address before publishing.
func SiteSlugAvailable(c *gin.Context) {
	userID, ok := getCurrentUserID(c)
	if !ok {
		siteError(c, http.StatusUnauthorized, "unauthorized", "User tidak ditemukan.")
		return
	}
	siteID, _ := strconv.ParseUint(c.Query("site_id"), 10, 64)
	var own int64
	config.DB.Model(&models.Site{}).Where("id = ? AND user_id = ?", siteID, userID).Count(&own)
	if own == 0 {
		siteID = 0
	}
	slug := strings.ToLower(strings.TrimSpace(c.Query("slug")))
	problem := slugProblem(slug, uint(siteID))
	c.JSON(http.StatusOK, gin.H{"ok": true, "available": problem == "", "code": problem, "url": sitebuilder.LoadSettings().LiveURL(slug)})
}

// PublishSite publishes the current version at <slug>.<SITES_DOMAIN>.
func PublishSite(c *gin.Context) {
	site, _, ok := loadOwnedSite(c)
	if !ok {
		return
	}
	if site.CurrentVersionID == nil {
		siteError(c, http.StatusConflict, "no_version", "Website belum punya versi untuk diterbitkan.")
		return
	}
	var req struct {
		Slug string `json:"slug"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		siteError(c, http.StatusBadRequest, "invalid", "Format permintaan tidak valid.")
		return
	}
	release, url, problem := publishSiteVersion(site, req.Slug)
	if problem != "" {
		siteError(c, http.StatusConflict, problem, "Alamat ini tidak dapat dipakai.")
		return
	}
	recordAudit(c, "site.publish", "site", strconv.FormatUint(uint64(site.ID), 10), site.Name, fmt.Sprintf("Rilis %d diterbitkan di %s", release.Number, url))
	c.JSON(http.StatusOK, gin.H{"ok": true, "url": url, "release": release})
}

// publishSiteVersion makes a release of the site's current version at the address and
// serves it there. It answers the slug problem code when the address cannot be used.
func publishSiteVersion(site *models.Site, rawSlug string) (models.SiteRelease, string, string) {
	slug := strings.ToLower(strings.TrimSpace(rawSlug))
	if problem := slugProblem(slug, site.ID); problem != "" {
		return models.SiteRelease{}, "", problem
	}
	var release models.SiteRelease
	err := config.DB.Transaction(func(tx *gorm.DB) error {
		var last struct{ Number int }
		tx.Model(&models.SiteRelease{}).Where("site_id = ?", site.ID).Select("COALESCE(MAX(number), 0) AS number").Scan(&last)
		tx.Model(&models.SiteRelease{}).Where("site_id = ? AND status = ?", site.ID, models.ReleaseLive).Update("status", models.ReleaseSuperseded)
		release = models.SiteRelease{SiteID: site.ID, SiteVersionID: *site.CurrentVersionID, Number: last.Number + 1, Slug: slug, Status: models.ReleaseLive, PublishedAt: time.Now()}
		if err := tx.Create(&release).Error; err != nil {
			return err
		}
		return tx.Model(site).Updates(map[string]any{"slug": slug, "published_release_id": release.ID}).Error
	})
	if err != nil {
		return models.SiteRelease{}, "", "slug_taken"
	}
	return release, sitebuilder.LoadSettings().LiveURL(slug), ""
}

// UnpublishSite takes the site offline; its address stays reserved for it.
func UnpublishSite(c *gin.Context) {
	site, _, ok := loadOwnedSite(c)
	if !ok {
		return
	}
	unpublishSiteRelease(site)
	recordAudit(c, "site.unpublish", "site", strconv.FormatUint(uint64(site.ID), 10), site.Name, "Website tidak lagi diterbitkan")
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// unpublishSiteRelease takes the site offline; its address stays reserved for it.
func unpublishSiteRelease(site *models.Site) {
	config.DB.Model(&models.SiteRelease{}).Where("site_id = ? AND status = ?", site.ID, models.ReleaseLive).Update("status", models.ReleaseSuperseded)
	config.DB.Model(site).Update("published_release_id", nil)
}

// loadOwnedVersion finds a version of an owned site by the :number parameter, and the
// directory of the version before it ("" when there is none).
func loadOwnedVersion(c *gin.Context) (dir, previousDir string, ok bool) {
	site, _, ok := loadOwnedSite(c)
	if !ok {
		return "", "", false
	}
	number, _ := strconv.Atoi(c.Param("number"))
	var version models.SiteVersion
	if config.DB.Where("site_id = ? AND number = ?", site.ID, number).First(&version).Error != nil {
		siteError(c, http.StatusNotFound, "not_found", "Versi tidak ditemukan.")
		return "", "", false
	}
	settings := sitebuilder.LoadSettings()
	var previous models.SiteVersion
	if config.DB.Where("site_id = ? AND number < ?", site.ID, number).Order("number DESC").First(&previous).Error == nil {
		previousDir = settings.VersionDir(site.ID, previous.Number)
	}
	return settings.VersionDir(site.ID, version.Number), previousDir, true
}

// SiteVersionFiles lists the readable files of a version, marked against the version
// before it, for the builder's code and changes views.
func SiteVersionFiles(c *gin.Context) {
	dir, previousDir, ok := loadOwnedVersion(c)
	if !ok {
		return
	}
	files, err := sitebuilder.CompareVersions(dir, previousDir)
	if err != nil {
		log.Printf("[SITES] list version files %s: %v", dir, err)
		siteError(c, http.StatusInternalServerError, "files_failed", "Isi website gagal dibaca.")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "files": files, "has_previous": previousDir != ""})
}

// SiteVersionFile returns one readable file of a version and, when the version before
// it has the same file, that file too, so the change can be shown.
func SiteVersionFile(c *gin.Context) {
	dir, previousDir, ok := loadOwnedVersion(c)
	if !ok {
		return
	}
	rel := c.Query("path")
	content, found, err := sitebuilder.ReadVersionFile(dir, rel)
	if errors.Is(err, sitebuilder.ErrNotReadable) {
		siteError(c, http.StatusBadRequest, "file_not_readable", "File ini tidak bisa dibuka.")
		return
	}
	if err != nil {
		log.Printf("[SITES] read version file %s %q: %v", dir, rel, err)
		siteError(c, http.StatusInternalServerError, "files_failed", "Isi website gagal dibaca.")
		return
	}
	previous, previousFound := "", false
	if previousDir != "" {
		previous, previousFound, _ = sitebuilder.ReadVersionFile(previousDir, rel)
	}
	if !found && !previousFound {
		siteError(c, http.StatusNotFound, "not_found", "File tidak ditemukan.")
		return
	}
	view := gin.H{"ok": true, "path": rel, "exists": found, "content": content}
	if previousFound {
		view["previous_content"] = previous
	}
	c.JSON(http.StatusOK, view)
}
