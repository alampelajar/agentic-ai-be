package sitebuilder

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"agentic-ai-backend/internal/cloudworkspace"
)

//go:embed template
var templateFS embed.FS

// CopyTemplate writes the site template into an empty workspace.
func CopyTemplate(filesDir string) error {
	return fs.WalkDir(templateFS, "template", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(name, "template"), "/")
		if rel == "" {
			return nil
		}
		target := filepath.Join(filesDir, filepath.FromSlash(rel))
		if entry.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		data, err := templateFS.ReadFile(name)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o640)
	})
}

// fixedPrefixes are template files that every run writes again, so a site made
// before a template change gets the new version of them (the kit, the form route).
var fixedPrefixes = []string{"web/src/kit/", "web/src/blocks/"}

// isFixed is true for a template file that is the same in every site.
func isFixed(rel string) bool {
	if protectedFiles[rel] && rel != "site.json" && rel != "web/index.html" && rel != "web/src/theme.css" && rel != "web/src/look.ts" && rel != "web/src/site.ts" && rel != "web/src/routes.tsx" && rel != "package.json" {
		return true
	}
	for _, prefix := range fixedPrefixes {
		if strings.HasPrefix(rel, prefix) {
			return true
		}
	}
	return false
}

// RefreshFixedFiles writes the template's fixed files (the kit, the form component,
// the server and the router) into a site again. The agent's own files and the files
// written from the brief are not touched.
func RefreshFixedFiles(filesDir string) error {
	return fs.WalkDir(templateFS, "template", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel := strings.TrimPrefix(name, "template/")
		if !isFixed(rel) {
			return nil
		}
		data, err := templateFS.ReadFile(name)
		if err != nil {
			return err
		}
		return WriteSiteFile(filesDir, rel, string(data))
	})
}

// Generated files are written by the worker from the brief and the direction. The
// agent may not write them (WEBSITE-BUILDER.md, section 6).
var protectedFiles = map[string]bool{
	"web/src/theme.css":               true,
	"web/src/look.ts":                 true,
	"web/src/site.ts":                 true,
	"web/src/routes.tsx":              true,
	"web/src/main.tsx":                true,
	"web/src/App.tsx":                 true,
	"web/src/router.tsx":              true,
	"web/src/vite-env.d.ts":           true,
	"web/src/components/SiteForm.tsx": true,
	"web/index.html":                  true,
	"server/index.js":                 true,
	"server/db.js":                    true,
	"server/routes/forms.js":          true,
	"site.json":                       true,
	"package.json":                    true,
}

var (
	agentPrefixes = []string{"web/src/pages/", "web/src/components/", "web/src/sections/", "server/routes/"}
	agentName     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,60}\.(tsx|ts|js)$`)
)

// ErrNotAllowed is returned for a path outside what the agent may write.
var ErrNotAllowed = errors.New("the agent may not write this file")

// CheckAgentPath enforces where the agent may write: pages, components, sections,
// server routes and the schema. Everything else is fixed by the template or written
// by the worker.
func CheckAgentPath(raw string) (string, error) {
	rel, err := cloudworkspace.CleanRelative(raw)
	if err != nil || rel == "" {
		return "", ErrNotAllowed
	}
	if protectedFiles[rel] {
		return "", fmt.Errorf("%w: %s is written by the builder", ErrNotAllowed, rel)
	}
	if rel == "server/schema.sql" {
		return rel, nil
	}
	for _, prefix := range agentPrefixes {
		if strings.HasPrefix(rel, prefix) && !strings.Contains(strings.TrimPrefix(rel, prefix), "/") && agentName.MatchString(path.Base(rel)) {
			return rel, nil
		}
	}
	return "", fmt.Errorf("%w: %s", ErrNotAllowed, rel)
}

// WriteSiteFile writes one file inside the workspace through the workspace path
// checks (no "..", no symlinks).
func WriteSiteFile(filesDir, rel, content string) error {
	absolute, _, err := cloudworkspace.Resolve(filesDir, rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o750); err != nil {
		return err
	}
	return os.WriteFile(absolute, []byte(content), 0o640)
}

// ReadSiteFile reads one file, "" when it does not exist.
func ReadSiteFile(filesDir, rel string) string {
	absolute, _, err := cloudworkspace.Resolve(filesDir, rel)
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(absolute)
	if err != nil {
		return ""
	}
	return string(data)
}

// AgentFiles lists the files the agent wrote or may change, sorted, with their content.
func AgentFiles(filesDir string) map[string]string {
	files := map[string]string{}
	for _, dir := range []string{"web/src/pages", "web/src/components", "web/src/sections", "server/routes"} {
		entries, err := os.ReadDir(filepath.Join(filesDir, filepath.FromSlash(dir)))
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.Type().IsRegular() && agentName.MatchString(entry.Name()) {
				rel := dir + "/" + entry.Name()
				if !protectedFiles[rel] {
					files[rel] = ReadSiteFile(filesDir, rel)
				}
			}
		}
	}
	files["server/schema.sql"] = ReadSiteFile(filesDir, "server/schema.sql")
	return files
}

// FileEdit is one file the model returned.
type FileEdit struct {
	Path    string
	Content string
}

var (
	fileBlock  = regexp.MustCompile(`(?s)<file\s+path="([^"]+)"\s*>\n?(.*?)</file>`)
	fence      = regexp.MustCompile("(?s)^\\s*```[a-zA-Z]*\\n(.*?)\\n?```\\s*$")
	tagBlock   = func(tag string) *regexp.Regexp { return regexp.MustCompile(`(?s)<` + tag + `>(.*?)</` + tag + `>`) }
	summaryTag = tagBlock("summary")
	pagesTag   = tagBlock("pages")
)

// ParseFiles reads the <file path="...">...</file> blocks of a model answer. A
// Markdown fence around the content is removed.
func ParseFiles(answer string) []FileEdit {
	var edits []FileEdit
	for _, match := range fileBlock.FindAllStringSubmatch(answer, -1) {
		content := match[2]
		if inner := fence.FindStringSubmatch(content); inner != nil {
			content = inner[1]
		}
		content = strings.TrimRight(content, " \t\n") + "\n"
		edits = append(edits, FileEdit{Path: strings.TrimSpace(match[1]), Content: content})
	}
	return edits
}

// lazyMarker finds a file a model returned only in part ("// ... rest unchanged"):
// writing it would throw away the rest of the file.
var lazyMarker = regexp.MustCompile(`(?im)^\s*(//|/\*|\{/\*)\s*\.\.\.|^\s*\.\.\.\s*$|(rest of|remaining|existing) (the )?(code|file|content|sections?) (is |stays? |remains? )?(unchanged|as before|the same)`)

// IsPartialFile reports content that is not a complete file.
func IsPartialFile(content string) bool { return lazyMarker.MatchString(content) }

// ParseSummary returns the text of a <summary> block, if any.
// A summary with fewer letters than this ("...", "Done") is not shown to the person.
func ParseSummary(answer string) string {
	match := summaryTag.FindStringSubmatch(answer)
	if match == nil {
		return ""
	}
	text := strings.TrimSpace(match[1])
	letters := 0
	for _, r := range text {
		if unicode.IsLetter(r) {
			letters++
		}
	}
	if letters < 12 {
		return ""
	}
	return text
}

// ParsePages returns the JSON inside a <pages> block, if any.
func ParsePages(answer string) string {
	if match := pagesTag.FindStringSubmatch(answer); match != nil {
		return strings.TrimSpace(match[1])
	}
	return ""
}

var tokenRules = []struct {
	pattern *regexp.Regexp
	problem string
}{
	{regexp.MustCompile(`\[#[0-9A-Fa-f]{3,8}\]`), "a colour written as a hex value in a class; use the theme tokens (bg-primary, text-muted...)"},
	{regexp.MustCompile(`(?i)(color|background|fill|stroke|border)[a-z]*\s*:\s*['"]?(#[0-9A-Fa-f]{3,8}|rgba?\(|hsla?\(|oklch\()`), "a colour in an inline style; use the theme tokens in classes"},
	{regexp.MustCompile(`\b(bg|text|border|from|to|via|ring|fill|stroke|outline|decoration|divide|placeholder|caret|shadow)-(slate|gray|zinc|neutral|stone|red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose)-[0-9]{2,3}\b`), "a Tailwind palette colour; only the theme tokens exist on this site"},
	{regexp.MustCompile(`font-\[|fontFamily|font-family`), "a font set in the code; use font-heading or font-body"},
	{regexp.MustCompile(`\bbg-(linear|gradient|radial|conic)-`), "a gradient; the design direction uses flat colour"},
	{regexp.MustCompile(`(?i)lorem ipsum`), "placeholder text"},
	{regexp.MustCompile(`\banimate-(bounce|ping|pulse)\b|requestAnimationFrame|@keyframes`), "own animation code: use the kit's motion pieces (Reveal, Stagger, CountUp, Marquee, RotatingWords, Parallax, Typewriter, Slideshow, Confetti) and the lift and float classes instead"},
	{regexp.MustCompile(`(?i)https?://(images\.unsplash|source\.unsplash|picsum\.photos|placehold|via\.placeholder|placekitten|dummyimage)`), "a picture from the internet; use only site.images and site.logo"},
}

// pageRules apply to pages only: every page is already shown inside Layout (App.tsx),
// so a page that adds the layout, a header or a footer shows them twice.
var pageRules = []struct {
	pattern *regexp.Regexp
	problem string
}{
	{regexp.MustCompile(`import\s+Layout\b|<Layout[\s>]`), "the page wraps itself in Layout, but every page is already shown inside Layout: remove it"},
	{regexp.MustCompile(`<header[\s>]|<footer[\s>]`), "the page has its own header or footer, but Layout already shows them: remove it"},
	{regexp.MustCompile(`<div[^>]*\baspect-[^>]*(/>|>\s*</div>)`), "an empty picture frame: show one of site.images with FramedPhoto or PhotoGallery, or leave the frame out"},
}

// CheckTokens finds what breaks the design rules in one file, by plain text search.
func CheckTokens(rel, content string) []string {
	if !strings.HasSuffix(rel, ".tsx") && !strings.HasSuffix(rel, ".ts") {
		return nil
	}
	rules := tokenRules
	if strings.HasPrefix(rel, "web/src/pages/") {
		rules = append(append(rules[:0:0], tokenRules...), pageRules...)
	}
	var problems []string
	for number, line := range strings.Split(content, "\n") {
		for _, rule := range rules {
			if rule.pattern.MatchString(line) {
				problems = append(problems, fmt.Sprintf("%s:%d %s", rel, number+1, rule.problem))
			}
		}
	}
	return problems
}

// Version files: everything needed to go back (source) and to serve (output).
var versionSources = []string{"web/src", "web/public", "web/index.html", "server", "site.json"}

// SaveVersion copies the site's source and its built output into a version directory.
func SaveVersion(filesDir, versionDir string) error {
	if err := os.RemoveAll(versionDir); err != nil {
		return err
	}
	for _, rel := range versionSources {
		if err := copyTree(filepath.Join(filesDir, filepath.FromSlash(rel)), filepath.Join(versionDir, "source", filepath.FromSlash(rel))); err != nil {
			return err
		}
	}
	return copyTree(filepath.Join(filesDir, ".output"), filepath.Join(versionDir, "output"))
}

// RestoreVersion puts a version's source back into the workspace, and its output.
func RestoreVersion(versionDir, filesDir string) error {
	for _, rel := range versionSources {
		target := filepath.Join(filesDir, filepath.FromSlash(rel))
		if err := os.RemoveAll(target); err != nil {
			return err
		}
		if err := copyTree(filepath.Join(versionDir, "source", filepath.FromSlash(rel)), target); err != nil {
			return err
		}
	}
	output := filepath.Join(filesDir, ".output")
	if err := os.RemoveAll(output); err != nil {
		return err
	}
	return copyTree(filepath.Join(versionDir, "output"), output)
}

// copyTree copies regular files and folders; symlinks and anything else are skipped.
// A missing source is not an error (web/public may not exist).
func copyTree(source, target string) error {
	info, err := os.Lstat(source)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode().IsRegular() {
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return err
		}
		data, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o640)
	}
	if !info.IsDir() {
		return nil
	}
	return filepath.WalkDir(source, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, _ := filepath.Rel(source, current)
		destination := filepath.Join(target, rel)
		switch {
		case entry.IsDir():
			if entry.Name() == "node_modules" {
				return fs.SkipDir
			}
			return os.MkdirAll(destination, 0o750)
		case entry.Type().IsRegular():
			data, err := os.ReadFile(current)
			if err != nil {
				return err
			}
			return os.WriteFile(destination, data, 0o640)
		}
		return nil
	})
}

var (
	countUpTag   = regexp.MustCompile(`<CountUp\b[^>]*>`)
	countUpTo    = regexp.MustCompile(`\bto=\{\s*([0-9][0-9_.,]*)\s*\}`)
	writtenValue = regexp.MustCompile(`[0-9][0-9.,]*[0-9]|[0-9]`)
)

// KnownNumbers are the numbers written in a text (the brief), without separators:
// "25.000" and "25,000" both give "25000".
func KnownNumbers(text string) map[string]bool {
	known := map[string]bool{}
	for _, number := range writtenValue.FindAllString(text, -1) {
		known[strings.NewReplacer(".", "", ",", "", "_", "").Replace(number)] = true
	}
	return known
}

// CheckCountUp refuses a CountUp whose number the owner never gave: a counting number
// on a page reads as a fact (a price, a number of customers), and the brief is the
// only source of facts. The number must be written directly, so it can be checked.
func CheckCountUp(rel, content string, known map[string]bool) []string {
	var problems []string
	for number, line := range strings.Split(content, "\n") {
		for _, tag := range countUpTag.FindAllString(line, -1) {
			match := countUpTo.FindStringSubmatch(tag)
			if match == nil {
				problems = append(problems, fmt.Sprintf("%s:%d CountUp must have its number written directly, as to={12}, and only a number the brief gives", rel, number+1))
				continue
			}
			value := strings.NewReplacer(".", "", ",", "", "_", "").Replace(match[1])
			if !known[value] {
				problems = append(problems, fmt.Sprintf("%s:%d CountUp shows %s, a number the owner never gave: remove it or show a number from the brief", rel, number+1, match[1]))
			}
		}
	}
	return problems
}

var (
	statsTag   = regexp.MustCompile(`(?s)<Stats\b.*?/>`)
	statsValue = regexp.MustCompile(`\bvalue\s*:\s*([^,}\s]+)`)
	plainValue = regexp.MustCompile(`^[0-9][0-9_.,]*$`)
)

// CheckStats is CheckCountUp for the Stats section, whose numbers count up the same
// way: each value must be written directly and be a number the brief gives.
func CheckStats(rel, content string, known map[string]bool) []string {
	var problems []string
	for _, tag := range statsTag.FindAllStringIndex(content, -1) {
		block := content[tag[0]:tag[1]]
		for _, match := range statsValue.FindAllStringSubmatchIndex(block, -1) {
			raw := block[match[2]:match[3]]
			line := strings.Count(content[:tag[0]+match[0]], "\n") + 1
			if !plainValue.MatchString(raw) {
				problems = append(problems, fmt.Sprintf("%s:%d Stats values must be written directly, as value: 12, and only numbers the brief gives", rel, line))
				continue
			}
			if !known[strings.NewReplacer(".", "", ",", "", "_", "").Replace(raw)] {
				problems = append(problems, fmt.Sprintf("%s:%d Stats shows %s, a number the owner never gave: remove it or show a number from the brief", rel, line, raw))
			}
		}
	}
	return problems
}

// CheckUsesBlocks refuses a page or layout written without the finished sections: the
// sections carry the look, so a page built by hand looks plainer than the rest of the
// site. The page-not-found page may stay simple.
func CheckUsesBlocks(rel, content string) []string {
	needs := rel == "web/src/components/Layout.tsx" || (strings.HasPrefix(rel, "web/src/pages/") && rel != "web/src/pages/NotFound.tsx" && strings.HasSuffix(rel, ".tsx"))
	if !needs || strings.Contains(content, "../blocks") {
		return nil
	}
	if rel == "web/src/components/Layout.tsx" {
		return []string{rel + ":1 the layout must use Header and Footer from '../blocks'"}
	}
	return []string{rel + ":1 the page is not built from the finished sections: use Hero, PageIntro or EventHero and the other sections from '../blocks'"}
}
