package sitebuilder

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"regexp"
	"strings"
	"unicode"
)

// Contact holds the facts the person typed. Only these are ever put on the site as
// contact details; the agent is told not to invent any.
type Contact struct {
	Phone    string `json:"phone"`
	WhatsApp string `json:"whatsapp"`
	Email    string `json:"email"`
	Address  string `json:"address"`
	Hours    string `json:"hours"`
}

type BriefPage struct {
	Slug     string   `json:"slug"`
	Title    string   `json:"title"`
	Purpose  string   `json:"purpose"`
	Sections []string `json:"sections"`
}

type BriefForm struct {
	Name    string   `json:"name"`
	Purpose string   `json:"purpose"`
	Fields  []string `json:"fields"`
	// Guestbook forms show what visitors wrote on the site (wishes, a guest book):
	// only their fields "name" and "message" are ever public (server/routes/forms.js).
	Guestbook bool `json:"guestbook,omitempty"`
}

// Image is a picture the person uploaded, served by the site from /uploads.
type Image struct {
	Src  string `json:"src"`
	Alt  string `json:"alt"`
	Logo bool   `json:"logo,omitempty"`
}

// Brief is what the site must be. The planner writes it from the description and
// the answers; the person confirms its summary before anything is built.
type Brief struct {
	Name         string            `json:"name"`
	Tagline      string            `json:"tagline"`
	Summary      string            `json:"summary"`
	Audience     string            `json:"audience"`
	Language     string            `json:"language"`
	Kind         string            `json:"kind"`
	Pages        []BriefPage       `json:"pages"`
	Forms        []BriefForm       `json:"forms"`
	ContentNotes string            `json:"content_notes"`
	Contact      Contact           `json:"contact"`
	Social       map[string]string `json:"social,omitempty"`
	// ChangesAsked are the owner's own later requests, newest last. They are part of
	// the brief and win over the first plan, so a review never undoes them.
	ChangesAsked []string `json:"changes_asked,omitempty"`
	// Colours are theme tokens the owner changed in a change ("a cyan background"),
	// over the look's palette. Choosing another look clears them.
	Colours map[string]string `json:"colours,omitempty"`
}

// maxChangesKept bounds how many of the owner's requests the brief carries.
const maxChangesKept = 10

// AddChange records a request of the owner in the brief.
func (b *Brief) AddChange(request string) {
	request = strings.TrimSpace(request)
	if request == "" {
		return
	}
	b.ChangesAsked = append(b.ChangesAsked, request)
	if len(b.ChangesAsked) > maxChangesKept {
		b.ChangesAsked = b.ChangesAsked[len(b.ChangesAsked)-maxChangesKept:]
	}
}

var slugPart = regexp.MustCompile(`[^a-z0-9]+`)

// PageSlug turns a title or slug into a path segment.
func PageSlug(raw string) string {
	slug := strings.Trim(slugPart.ReplaceAllString(strings.ToLower(asciiFold(raw)), "-"), "-")
	if len(slug) > 30 {
		slug = strings.Trim(slug[:30], "-")
	}
	return slug
}

func asciiFold(text string) string {
	var b strings.Builder
	for _, r := range text {
		if r < unicode.MaxASCII {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	return b.String()
}

// Path is the page's address: "/" for the first page (home).
func (p BriefPage) Path(index int) string {
	if index == 0 {
		return "/"
	}
	return "/" + p.Slug
}

var reservedComponents = map[string]bool{"App": true, "Layout": true, "NotFound": true, "Link": true, "SiteForm": true}

// Component is the React component name and file name of the page: web/src/pages/<Component>.tsx.
func (p BriefPage) Component(index int) string {
	if index == 0 {
		return "Home"
	}
	var b strings.Builder
	for _, part := range strings.Split(p.Slug, "-") {
		if part == "" {
			continue
		}
		b.WriteString(strings.ToUpper(part[:1]) + part[1:])
	}
	name := b.String()
	if name == "" || unicode.IsDigit(rune(name[0])) {
		name = "Page" + name
	}
	if reservedComponents[name] || name == "Home" {
		name += "Page"
	}
	return name
}

// PageFile is where the page's code lives.
func (p BriefPage) PageFile(index int) string { return "web/src/pages/" + p.Component(index) + ".tsx" }

// Normalize cleans a brief from the planner: slugs, limits, the home page first.
func (b *Brief) Normalize() error {
	b.Name = strings.TrimSpace(b.Name)
	if b.Name == "" {
		return errors.New("the brief has no name")
	}
	if b.Language != "en" {
		b.Language = "id"
	}
	seen := map[string]bool{}
	pages := make([]BriefPage, 0, len(b.Pages))
	for _, page := range b.Pages {
		page.Title = strings.TrimSpace(page.Title)
		if page.Title == "" {
			continue
		}
		page.Slug = PageSlug(page.Slug)
		if page.Slug == "" {
			page.Slug = PageSlug(page.Title)
		}
		if page.Slug == "" || seen[page.Slug] {
			page.Slug = fmt.Sprintf("page-%d", len(pages)+1)
		}
		seen[page.Slug] = true
		if len(page.Sections) > 8 {
			page.Sections = page.Sections[:8]
		}
		pages = append(pages, page)
		if len(pages) == 6 {
			break
		}
	}
	if len(pages) == 0 {
		return errors.New("the brief has no pages")
	}
	b.Pages = pages
	forms := make([]BriefForm, 0, len(b.Forms))
	for _, form := range b.Forms {
		form.Name = PageSlug(form.Name)
		if form.Name != "" && len(forms) < 4 {
			forms = append(forms, form)
		}
	}
	b.Forms = forms
	return nil
}

func jsString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

// SiteTS is web/src/site.ts: the facts every page reads.
func SiteTS(b Brief, images []Image) string {
	var out strings.Builder
	out.WriteString("// Written by the builder from the brief. Pages read the site's facts from here.\n")
	out.WriteString("export type SitePage = { path: string; label: string }\n\n")
	out.WriteString("export const site = {\n")
	fmt.Fprintf(&out, "  name: %s,\n  tagline: %s,\n  language: %s as 'id' | 'en',\n", jsString(b.Name), jsString(b.Tagline), jsString(b.Language))
	out.WriteString("  pages: [\n")
	for index, page := range b.Pages {
		fmt.Fprintf(&out, "    { path: %s, label: %s },\n", jsString(page.Path(index)), jsString(page.Title))
	}
	out.WriteString("  ] as SitePage[],\n")
	c := b.Contact
	fmt.Fprintf(&out, "  contact: { phone: %s, whatsapp: %s, email: %s, address: %s, hours: %s },\n", jsString(c.Phone), jsString(WhatsAppNumber(c.WhatsApp)), jsString(c.Email), jsString(c.Address), jsString(c.Hours))
	out.WriteString("  social: {")
	first := true
	for _, key := range []string{"instagram", "facebook", "tiktok", "youtube", "x", "linkedin"} {
		if value := strings.TrimSpace(b.Social[key]); value != "" {
			if !first {
				out.WriteString(",")
			}
			fmt.Fprintf(&out, " %s: %s", key, jsString(value))
			first = false
		}
	}
	out.WriteString(" } as Record<string, string>,\n")
	logo := ""
	out.WriteString("  images: [\n")
	for _, image := range images {
		if image.Logo {
			logo = image.Src
			continue
		}
		fmt.Fprintf(&out, "    { src: %s, alt: %s },\n", jsString(image.Src), jsString(image.Alt))
	}
	out.WriteString("  ] as { src: string; alt: string }[],\n")
	fmt.Fprintf(&out, "  logo: %s,\n}\n\n", jsString(logo))
	out.WriteString(`/** A WhatsApp link with a prepared message, or '' when the site has no WhatsApp number. */
export function whatsappLink(message = ''): string {
  const number = site.contact.whatsapp.replace(/[^0-9]/g, '')
  if (!number) return ''
  return ` + "`https://wa.me/${number}${message ? `?text=${encodeURIComponent(message)}` : ''}`" + `
}
`)
	return out.String()
}

// WhatsAppNumber turns what the person typed into the international digits wa.me
// needs: "0812-3456" becomes "628123456" (an Indonesian number written locally).
func WhatsAppNumber(raw string) string {
	var digits strings.Builder
	for _, r := range raw {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	number := digits.String()
	if strings.HasPrefix(number, "0") {
		number = "62" + strings.TrimLeft(number, "0")
	}
	return number
}

// RoutesTSX is web/src/routes.tsx: one route per page of the brief.
func RoutesTSX(b Brief) string {
	var out strings.Builder
	out.WriteString("// Written by the builder from the brief: one route per page.\nimport type { ComponentType } from 'react'\n")
	for index, page := range b.Pages {
		fmt.Fprintf(&out, "import %s from './pages/%s'\n", page.Component(index), page.Component(index))
	}
	out.WriteString("import NotFound from './pages/NotFound'\n\n")
	out.WriteString("export const routes: { path: string; title: string; component: ComponentType }[] = [\n")
	for index, page := range b.Pages {
		fmt.Fprintf(&out, "  { path: %s, title: %s, component: %s },\n", jsString(page.Path(index)), jsString(page.Title), page.Component(index))
	}
	notFound := "Halaman tidak ditemukan"
	if b.Language == "en" {
		notFound = "Page not found"
	}
	fmt.Fprintf(&out, "  { path: '*', title: %s, component: NotFound },\n]\n", jsString(notFound))
	return out.String()
}

// IndexHTML is web/index.html with the site's language and name.
func IndexHTML(b Brief) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="%s">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <meta name="description" content="%s" />
    <title>%s</title>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
`, b.Language, html.EscapeString(b.Tagline), html.EscapeString(b.Name))
}

// SiteJSON is site.json: the brief and the direction in machine form.
func SiteJSON(b Brief, direction string) string {
	data, _ := json.MarshalIndent(map[string]any{"direction": direction, "brief": b}, "", "  ")
	return string(data) + "\n"
}
