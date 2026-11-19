package sitebuilder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Message is one chat message for a model.
type Message struct {
	Role    string
	Content string
}

// Need tells the model caller what kind of model fits: "fast" for short planning
// answers, "coding" for writing the site.
type Need string

const (
	NeedFast   Need = "fast"
	NeedCoding Need = "coding"
)

// ModelCaller calls one of the person's usable models (handlers provides it).
// It returns the answer and the name of the model that gave it.
type ModelCaller interface {
	Complete(ctx context.Context, userID uint, need Need, messages []Message) (answer, model string, err error)
}

// ErrTokenLimit is returned by a ModelCaller when the person has used their monthly
// allowance of tokens. A run stops at once with a plain message; its finished parts are
// kept for "Continue" when the allowance starts again.
var ErrTokenLimit = errors.New("monthly token allowance used up")

func languageName(language string) string {
	if language == "en" {
		return "English"
	}
	return "Indonesian (Bahasa Indonesia)"
}

// ---------------------------------------------------------------------------
// Questions

// Question is one clarifying question with choices. The interface always adds
// "Other" with free text.
type Question struct {
	ID       string   `json:"id"`
	Text     string   `json:"text"`
	Multiple bool     `json:"multiple"`
	Options  []string `json:"options"`
	// Free questions are answered in the person's own words, without choices: the
	// personal content a site lives on (how a couple met). At most one per set.
	Free bool `json:"free,omitempty"`
}

// Fact is something the description already says, shown back to the person as
// "what we understood" so they correct it instead of repeating it.
type Fact struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// QuestionSet is the planner's first answer.
type QuestionSet struct {
	Kind          string `json:"kind"`
	SuggestedName string `json:"suggested_name"`
	// Language is the language the description is written in: the website's language.
	Language     string     `json:"language"`
	Understood   []Fact     `json:"understood"`
	KnownContact Contact    `json:"known_contact"`
	LookWish     string     `json:"look_wish"`
	Questions    []Question `json:"questions"`
}

// PlainWordsRule is the language rule for every text a person reads in the builder.
const PlainWordsRule = `Write for someone who left school early and has never made a website: short everyday words, one idea per sentence. Never use design or technical words: serif, font, typeface, typography, editorial, minimalist, industrial, layout, template, palette, website builder, SEO, domain, hosting, backend, database, server, deploy.`

// QuestionsPrompt asks the planner what it understood and what it still needs to
// know. The questions are written in the language the person reads the builder in.
func QuestionsPrompt(description, uiLanguage string) []Message {
	system := fmt.Sprintf(`You help people with no technical knowledge plan a website. You read their description, note what it already tells you, and ask only what is still missing to decide what the website contains.

Reply with one JSON object and nothing else:
{"kind": "<one of: %s>",
 "language": "<the language the description is written in: id for Indonesian, en for English>",
 "suggested_name": "<the business, event or person name if the description gives one, else empty>",
 "understood": [{"label": "<what it is, e.g. Usaha, Tempat, Tanggal, Tema, Halaman>", "value": "<what the description says>"}],
 "known_contact": {"phone": "", "whatsapp": "", "email": "", "address": "", "hours": ""},
 "look_wish": "<colours or mood the person asked for, in their own words, else empty>",
 "questions": [{"id": "<short id>", "text": "<question>", "multiple": <true when several answers make sense>, "options": ["<2 to 5 options>"], "free": <true only for the one question answered in their own words, with no options>}]}

Rules:
- Write labels, questions and options in %s. %s
- "understood": 2 to 6 facts the description states (what it is, place, date, theme, what is sold, which pages). Never guess.
- "known_contact": only details written in the description; leave the rest empty. For an event or a wedding, the place where it is held goes in "address".
- Ask 0 to 4 questions, only about what the description does not answer and what changes the website's content: which pages, what visitors should do (call, order on WhatsApp, come, book), what to show first. A detailed description needs no question at all.
- Never ask about something listed in "understood", and never ask about colours, style, the name, address, phone or opening hours: those are handled separately.
- A question has at most 12 words; an option has at most 4 words.
- For a wedding, an event, a portfolio or a personal website, the site lives on personal content. When the description does not give it, you may use one question with "free": true and no options to ask for it in their own words, for example "Ceritakan singkat bagaimana kalian bertemu" or "Apa yang paling ingin Anda tunjukkan?".`, strings.Join(Kinds(), ", "), languageName(uiLanguage), PlainWordsRule)
	return []Message{{Role: "system", Content: system}, {Role: "user", Content: description}}
}

// DefaultQuestions are used when the model does not answer usefully.
func DefaultQuestions(language string) QuestionSet {
	if language == "en" {
		return QuestionSet{Kind: "general", Questions: []Question{
			{ID: "pages", Text: "Which pages do you want?", Multiple: true, Options: []string{"About us", "What we offer", "Photos", "Contact"}},
			{ID: "goal", Text: "What should visitors do first?", Options: []string{"Chat on WhatsApp", "Call", "Come by", "Fill in a form"}},
		}}
	}
	return QuestionSet{Kind: "general", Questions: []Question{
		{ID: "pages", Text: "Halaman apa saja yang Anda inginkan?", Multiple: true, Options: []string{"Tentang kami", "Yang kami tawarkan", "Foto", "Kontak"}},
		{ID: "goal", Text: "Apa yang sebaiknya dilakukan pengunjung?", Options: []string{"Chat WhatsApp", "Menelepon", "Datang langsung", "Isi formulir"}},
	}}
}

// ---------------------------------------------------------------------------
// Brief

// BriefInput is everything the person told the builder.
type BriefInput struct {
	Description string
	Language    string // the website's language
	UILanguage  string // the language of the summary the person reads
	Kind        string
	Name        string
	Understood  []Fact
	LookWish    string
	Questions   []Question
	Answers     map[string][]string
	Contact     Contact
	Images      []Image
	Direction   Direction
	// PageTitles are the pages of a gallery website the person chose to copy, in order.
	PageTitles []string
}

func BriefPrompt(in BriefInput) []Message {
	system := fmt.Sprintf(`You plan a small website for a person with no technical knowledge. From what they told you, write the brief that a web designer will build from.

Reply with one JSON object and nothing else:
{"name": "...", "tagline": "<one short line under the name>", "summary": "<one or two sentences to the person describing the website you will make, starting with what it is for>", "audience": "<who visits>", "pages": [{"slug": "<lowercase-words>", "title": "<menu label>", "purpose": "<what this page does>", "sections": ["<section: what it shows, concretely>"]}], "forms": [{"name": "<lowercase-words, e.g. contact or booking>", "purpose": "...", "fields": ["<field label>"], "guestbook": <true only for a form whose messages visitors read on the site>}], "content_notes": "<facts and wording to use, tone, what to emphasise>"}

Rules:
- The website is in %s: write name, tagline, titles, sections and content_notes in %s.
- Write "summary" in %s. %s
- 1 to 5 pages. The first page is the home page. A small business usually needs 3 or 4. A one-page site is fine when the person needs little (an invitation, a single offer).
- Each page lists 3 to 6 concrete sections in the order they appear.
- Add a form only when it serves the goal (contact, booking, order request, RSVP). Forms store answers on the site; only the owner can read them.
- When visitors' messages should appear on the site (wishes and prayers for a wedding, a guest book), mark that form "guestbook": true. Only the visitor's name and message are ever shown; other fields of the same form (attendance, number of guests, phone) stay private. Never plan that answers of any other form appear on the site.
- Plan a gallery page or a photo section only when photos were uploaded; plan a story page only when the person told the story. Otherwise leave it out or fold what there is into another page, and say in "summary" that it can be added after they upload photos or tell the story. Never plan a section that would stay empty.
- Use only facts the person gave. Never invent prices, awards, years in business, numbers of customers, testimonials, or claims about ingredients, materials, guarantees or certifications. Where a fact is missing, plan wording that does not need it.
- A wish about the look (colours, mood, theme) goes into content_notes; the colours themselves are already set.
- The look is already chosen: %s (%s). Plan sections that suit it: %s`,
		languageName(in.Language), languageName(in.Language), languageName(in.UILanguage), PlainWordsRule, in.Direction.Name["en"], in.Direction.Description["en"], strings.Join(in.Direction.Composition, " "))

	var user strings.Builder
	fmt.Fprintf(&user, "Description: %s\n", in.Description)
	if in.Name != "" {
		fmt.Fprintf(&user, "Name: %s\n", in.Name)
	}
	for _, fact := range in.Understood {
		fmt.Fprintf(&user, "%s: %s\n", fact.Label, fact.Value)
	}
	if in.LookWish != "" {
		fmt.Fprintf(&user, "Wish about the look: %s\n", in.LookWish)
	}
	for _, question := range in.Questions {
		if answers := in.Answers[question.ID]; len(answers) > 0 {
			fmt.Fprintf(&user, "%s %s\n", question.Text, strings.Join(answers, ", "))
		}
	}
	if extra := in.Answers["_extra"]; len(extra) > 0 && strings.TrimSpace(extra[0]) != "" {
		fmt.Fprintf(&user, "More from the person (this corrects anything above): %s\n", extra[0])
	}
	c := in.Contact
	fmt.Fprintf(&user, "Contact details given: phone=%q whatsapp=%q email=%q address=%q hours=%q\n", c.Phone, c.WhatsApp, c.Email, c.Address, c.Hours)
	fmt.Fprintf(&user, "Photos uploaded: %d, logo: %t\n", photoCount(in.Images), hasLogo(in.Images))
	if len(in.PageTitles) > 0 {
		system += "\n- The person chose to copy the page list of an example website. Plan the same pages in the same order, with their titles in the website's language. Only the structure is copied: write every page for this person's own description and facts, and still leave out a page that needs photos or a story the person did not give."
		fmt.Fprintf(&user, "Pages of the example website to copy: %s\n", strings.Join(in.PageTitles, " | "))
	}
	return []Message{{Role: "system", Content: system}, {Role: "user", Content: user.String()}}
}

func photoCount(images []Image) int {
	count := 0
	for _, image := range images {
		if !image.Logo {
			count++
		}
	}
	return count
}

func hasLogo(images []Image) bool {
	for _, image := range images {
		if image.Logo {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Coding

// ownerRequestsRule tells a model that what the owner asked for in a change wins over the
// look: a reviewer once removed a rainbow the owner had asked for, because the look was
// plain. Empty when the owner has asked for nothing yet.
func ownerRequestsRule(b Brief) string {
	if len(b.ChangesAsked) == 0 {
		return ""
	}
	return fmt.Sprintf(`THE OWNER'S REQUESTS COME FIRST
The owner asked for these changes, newest last:
- %s
What they asked for wins over the design direction, its shape and composition, and the "never" list. Something on a page that carries out one of these requests (a colour, a decoration, a section, a word) is not a problem, even when the direction or the "never" list would forbid it: keep it, and never ask to remove, tone down or replace it.`, strings.Join(b.ChangesAsked, "\n- "))
}

const neverList = `Never, unless the design direction or the owner's requests say so:
- a gradient as background or on buttons;
- every section centred, or the same card grid repeated down the page;
- emoji in headings or used as icons;
- vague hero text that could belong to any business ("Welcome to our website", "Quality you can trust");
- heavy shadows, or corners rounder than the direction's shape;
- animation scattered over the page (at most one purposeful moment, and only with motion-safe:);
- placeholder text or pictures.`

// CodingSystem is the system prompt for every model call that writes site code.
func CodingSystem(b Brief, d Direction) string {
	var palette []string
	for _, key := range PaletteKeys {
		palette = append(palette, strings.ReplaceAll(key, "_", "-"))
	}
	return fmt.Sprintf(`You are a senior web designer and front-end developer. You build one page or component at a time of a small website for a real business, following a fixed design direction exactly. The person who owns the site has no technical knowledge and will judge it only by how it looks and reads.

STACK (fixed, nothing else exists)
- React 19 + TypeScript function components, Tailwind CSS v4 classes, lucide-react icons. No other package: do not import anything else.
- Pages are web/src/pages/<Name>.tsx with a default export. App already shows every page inside Layout, so a page never renders Layout, a header or a footer. Shared pieces go in web/src/components/<Name>.tsx (named or default export).
- Allowed imports: 'react', 'lucide-react', '../site', '../router', '../components/<Name>', '../kit/<Name>', './<Name>' inside components.
- '../site' exports: site = { name: string, tagline: string, language: 'id' | 'en', pages: {path, label}[], contact: {phone, whatsapp, email, address, hours} (all strings, '' when not given), social: Record<string, string>, images: {src, alt}[], logo: string (a picture path, '' when there is no logo) } and whatsappLink(message?: string): string, which returns '' when there is no WhatsApp number.
- '../router' exports: Link (props: to, className, children... use it for every link to a page of this site), navigate(path), usePath().
- '../components/SiteForm' exports: SiteForm with props { name: string; fields: {name, label, type?: 'text'|'email'|'tel'|'textarea'|'select'|'date'|'number', required?, placeholder?, options?}[]; submitLabel?; successMessage?; className? }. Use it for every form; it stores answers on the site's server, where only the owner reads them. A form marked guestbook in the brief must have a field named exactly "name" (the visitor's name) and one named exactly "message" (the wish or message); it may have more, private fields.
- '../kit/<Name>' are finished pieces you may use and may not change (default and named export of the same name):
  Guestbook { form } shows the messages of a guestbook form, newest first, and updates after a message is sent: put it beside or under that form. Never write text promising that answers appear on the page unless the form is a guestbook and Guestbook is shown.
  Countdown { to: "YYYY-MM-DDTHH:MM" } days, hours and minutes to a date.
  Timeline { items: {time, title, place?, note?}[] } the plan of a day.
  AddToCalendar { title, start: "YYYY-MM-DDTHH:MM", end?, location?, details?, label? } a button that saves the event to the visitor's calendar.
  MapLink { address, label? } a link that opens the address in a map app.
  Ornament { kind?: 'line'|'diamond'|'flourish'|'dots' } a decorative divider in the accent colour.
  Monogram { letters, frame?: 'circle'|'arch'|'square' } initials in a frame.
  FramedPhoto { src, alt, frame?: 'thin'|'arch'|'offset' } one of site.images in a frame.
  PhotoGallery { limit? } the owner's photos in a grid; it shows nothing when there are none.
  Each accepts className to replace its default classes.
- Icons: import only names from this list, which exist in the installed lucide-react: %s. There are no brand logos (Instagram, Facebook, WhatsApp): use AtSign or MessageCircle.
- Do not touch window, document or localStorage while rendering; only inside useEffect or event handlers.

COLOURS, FONTS AND SHAPE COME ONLY FROM THE THEME
- Colour classes exist only for these tokens: %s, white, black, transparent, current. Example: bg-surface, text-muted, border-border, bg-primary text-primary-foreground, text-accent, bg-primary/10. Palette classes such as bg-blue-500 do not exist here and render nothing.
- Never write a hex, rgb or hsl colour, never set fontFamily or font-[...]. Use font-heading for display text (h1-h4 already use it) and font-body for text.
- Radius classes: rounded-sm, rounded-md, rounded-lg, rounded-xl, rounded-2xl, rounded-3xl, rounded-full (sized by the direction).

DESIGN DIRECTION: %s — %s
Shape: %s
Composition (follow it, it is what makes this site look different from others):
- %s
Voice of the text: %s
Finished pieces this look uses (from '../kit/'): %s

%s

CONTENT
- Write all visible text in %s, for this business specifically, using the brief. Real sentences a visitor would find useful.
- The brief's "changes_asked" are the owner's later requests, newest last: keep what they asked for, even where the design direction or the "never" list says otherwise.
- Use only the facts in the brief and in site (name, contact details). Never invent prices, awards, years, numbers of customers, testimonials, names, or claims about ingredients, materials, guarantees or certifications (such as "no preservatives" or "halal") that the brief does not state. Show a contact detail only when it is not empty (check the value).
- Pictures: use only site.logo and site.images (uploaded by the owner), with their alt text. When there are none, design with type, colour blocks, borders, ornaments and icons. Never use pictures from the internet.
- Never show an empty frame, an empty box where a picture would go, or a gallery without photos: use PhotoGallery (it shows nothing without photos) or leave the section out. Never write text that tells visitors something will be added later or that the owner will write it.
- For an invitation or an event, make it feel like one: the date and place clearly at the top, the look's ornaments, and the finished pieces that fit (Countdown, AddToCalendar, Timeline for the schedule, MapLink for the place, Guestbook for wishes).

QUALITY
- Mobile first: works from 360px wide with no horizontal scroll, then sm:, md:, lg:. The header menu collapses into a button with aria-expanded on phones.
- Semantic HTML: one h1 per page, sections with headings, buttons for actions, links for navigation; visible focus; alt text on images.
- Content width with mx-auto max-w-6xl px-4 (or the direction's variant); sections with clear vertical rhythm (py-16 to py-28).
- Keep components small and readable. No comments that explain the obvious.

OUTPUT
Reply with complete files only, each in this exact form, and nothing outside the blocks:
<file path="web/src/pages/Example.tsx">
...the whole file...
</file>`,
		knownIcons, strings.Join(palette, ", "), d.Name["en"], d.Description["en"], d.Shape, strings.Join(d.Composition, "\n- "), d.Voice["en"], kitLine(d), neverList, languageName(b.Language))
}

// kitLine names the finished pieces a look uses.
func kitLine(d Direction) string {
	if strings.TrimSpace(d.Kit) == "" {
		return "use them where they help (Ornament, Timeline, PhotoGallery...)."
	}
	return d.Kit
}

// knownIcons were checked against lucide-react in the site image; a name outside the
// list may not exist, and a missing icon fails the type check.
const knownIcons = "Wrench, Phone, PhoneCall, MapPin, Clock, Mail, MessageCircle, MessageSquare, Check, CircleCheck, ChevronRight, ChevronDown, ArrowRight, ArrowUpRight, Menu, X, Star, Heart, Camera, Calendar, CalendarDays, Users, User, ShieldCheck, Sparkles, Truck, ShoppingBag, ShoppingCart, Coffee, Utensils, UtensilsCrossed, Scissors, Stethoscope, GraduationCap, Bike, Car, Droplet, Droplets, Settings, Cog, House, Store, Building2, Leaf, Flower2, Sun, Gift, Award, Package, Hammer, Paintbrush, Palette, Music, Mic, Image, Images, Video, Globe, AtSign, Send, Quote, Timer, Gauge, Fuel, BadgeCheck, HandHeart, Smile, Baby, Dumbbell, Pill, Syringe, Shirt, Gem, Cake, CakeSlice, Croissant, Pizza, Soup, Wine, Plane, Hotel, Bed, Mountain, Waves, TreePalm, Map, Navigation, Info, CircleHelp, Tag, Percent, Wallet, CreditCard, Banknote, Laptop, Code, Cpu, Briefcase, Scale, Landmark, BookOpen, Lightbulb, Target, Rocket, Zap, Flame"

func briefText(b Brief) string {
	data, _ := json.MarshalIndent(b, "", "  ")
	return string(data)
}

func filesText(files map[string]string, only func(string) bool) string {
	keys := make([]string, 0, len(files))
	for key := range files {
		if only == nil || only(key) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		fmt.Fprintf(&b, "<file path=\"%s\">\n%s</file>\n\n", key, files[key])
	}
	return b.String()
}

// LayoutPrompt asks for the header, navigation and footer around every page.
func LayoutPrompt(b Brief, d Direction) []Message {
	return []Message{
		{Role: "system", Content: CodingSystem(b, d)},
		{Role: "user", Content: fmt.Sprintf(`Brief:
%s

Write web/src/components/Layout.tsx: the default export Layout({ children }: { children: React.ReactNode }) that wraps every page. It holds the header with the site name (or site.logo when not empty) and navigation built from site.pages (Link, the current page marked with usePath and aria-current="page"), a phone menu button, <main>{children}</main>, and the footer with the contact details that are not empty. Follow the direction's header and footer exactly.`, briefText(b))},
	}
}

// PagePrompt asks for one page.
func PagePrompt(b Brief, d Direction, index int, layout string) []Message {
	page := b.Pages[index]
	formNote := ""
	if len(b.Forms) > 0 {
		formNote = "\nForms of this site (use SiteForm with these names where the page needs one): "
		for _, form := range b.Forms {
			guestbook := ""
			if form.Guestbook {
				guestbook = ", guestbook: fields \"name\" and \"message\", show it with Guestbook"
			}
			formNote += fmt.Sprintf("%s (%s: %s%s); ", form.Name, form.Purpose, strings.Join(form.Fields, ", "), guestbook)
		}
	}
	return []Message{
		{Role: "system", Content: CodingSystem(b, d)},
		{Role: "user", Content: fmt.Sprintf(`Brief:
%s

The layout around every page (header and footer are already there; do not repeat them):
<file path="web/src/components/Layout.tsx">
%s</file>

Write %s: the page "%s" at %s. Purpose: %s
Sections, in order:
- %s
%s
Make this page feel designed for this business, following the composition. The page is shown inside Layout already: do not import or render Layout, and do not add a header, navigation bar or footer. Return only this file (and a new component file only if the page really needs one).`,
			briefText(b), layout, page.PageFile(index), page.Title, page.Path(index), page.Purpose, strings.Join(page.Sections, "\n- "), formNote)},
	}
}

// RepairPrompt gives the model the build errors and the files involved.
func RepairPrompt(b Brief, d Direction, errors string, files map[string]string) []Message {
	return []Message{
		{Role: "system", Content: CodingSystem(b, d)},
		{Role: "user", Content: fmt.Sprintf(`The site does not pass its check. The problems:

%s

The files involved:

%s
Fix every problem. Return each file you change, complete. Keep the design and the text as they are apart from the fix.`, errors, filesText(files, nil))},
	}
}

// ReviewFix is one problem the reviewer found.
type ReviewFix struct {
	File    string `json:"file"`
	Problem string `json:"problem"`
}

// ReviewResult is the reviewer's answer.
type ReviewResult struct {
	Pass  bool        `json:"pass"`
	Fixes []ReviewFix `json:"fixes"`
}

// ReviewPrompt asks a reviewer to check the pages against the direction.
func ReviewPrompt(b Brief, d Direction, files map[string]string) []Message {
	system := fmt.Sprintf(`You review a small website before its owner sees it. You check the code of each page against the brief and the design direction and list only problems that matter.

First: every visible text must be in %s. Any text in another language is the most important problem.

Design direction: %s — %s
Shape: %s
Composition:
- %s
Voice: %s

%s

The brief's "changes_asked" are the owner's own later requests, newest last. They are part of the brief and win over the first plan: never ask to remove or undo what they asked for.

%s

Also check: no empty frame, empty picture box or gallery without photos is shown, and no text tells visitors that content will come later; a page only promises that answers appear when the form is a guestbook shown with Guestbook; an invitation or event page uses the finished pieces the look names (%s);
the composition is followed (not a generic layout); text is specific to the business, in %s, and uses only facts from the brief; contact details are only shown when present; each page has one h1; the phone layout has no horizontal scroll and the menu works on phones; nothing references pictures from the internet.

Reply with one JSON object and nothing else:
{"pass": <true when nothing important is wrong>, "fixes": [{"file": "<path>", "problem": "<what to change, concretely>"}]}
At most 6 fixes, most important first. Do not list matters of taste.`,
		languageName(b.Language), d.Name["en"], d.Description["en"], d.Shape, strings.Join(d.Composition, "\n- "), d.Voice["en"], neverList, ownerRequestsRule(b), kitLine(d), languageName(b.Language))
	return []Message{
		{Role: "system", Content: system},
		{Role: "user", Content: fmt.Sprintf("Brief:\n%s\n\nFiles:\n\n%s", briefText(b), filesText(files, func(path string) bool { return strings.HasSuffix(path, ".tsx") }))},
	}
}

// FixPrompt applies the reviewer's problems to one file.
func FixPrompt(b Brief, d Direction, file, content string, problems []string) []Message {
	return []Message{
		{Role: "system", Content: CodingSystem(b, d)},
		{Role: "user", Content: fmt.Sprintf(`Brief:
%s

<file path="%s">
%s</file>

A reviewer found these problems in this file:
- %s

%s
If a problem above asks to remove, tone down or replace something the owner asked for, ignore that problem and keep it.

Return the corrected file, complete.`, briefText(b), file, content, strings.Join(problems, "\n- "), ownerRequestsRule(b))},
	}
}

// RevisePrompt carries out a change the person asked for. palette is the site's colours
// now (the look's palette with the owner's earlier colours over it).
func RevisePrompt(b Brief, d Direction, palette map[string]string, request string, files map[string]string, directionChanged bool) []Message {
	task := fmt.Sprintf(`The owner asks for this change, in their own words:
"%s"

Carry it out. Change only what the request needs; keep everything else as it is.`, request)
	if directionChanged {
		task = `The owner chose a new design direction (above). The theme tokens are already switched. Rework the layout and every page so they follow the new direction's composition, shape and voice, keeping the same content and facts.`
		if strings.TrimSpace(request) != "" {
			task += fmt.Sprintf("\nThey also said: %q", request)
		}
	}
	return []Message{
		{Role: "system", Content: CodingSystem(b, d)},
		{Role: "user", Content: fmt.Sprintf(`Brief:
%s

The current files of the site:

%s
%s

To add, remove, rename or reorder pages, also return the complete new list of pages (the first one is the home page) as:
<pages>[{"slug": "...", "title": "...", "purpose": "...", "sections": ["..."]}]</pages>
and return the file of every new page (web/src/pages/<Name>.tsx, where Name is the slug in PascalCase, Home for the first page).
To keep data on the server, you may add server/routes/<name>.js exporting a register function (api, db) with Hono routes under the api (it is mounted at /api) and call it from server/routes/index.js; tables go in server/schema.sql with CREATE TABLE IF NOT EXISTS. db is a node:sqlite DatabaseSync.

The site's colours now are: %s.
When the owner asks for other colours (a background, buttons, text, a colour they name), do it by changing the theme tokens, not by writing colours in the pages: return the tokens to change with their new value as #RRGGBB, for example
<colours>{"background": "#E0FFFF", "primary": "#0891B2"}</colours>
Pick the shade that fits the request (a soft tint for a page background, a stronger one for buttons). Use only these token names: %s. Text tokens are kept readable automatically. Pages keep using the token classes (bg-background, bg-primary...), so they change with the theme. Leave out the block when the colours do not change.

End with one sentence for the owner, in %s, saying what you changed, as <summary>...</summary>.`, briefText(b), filesText(files, nil), task, coloursText(palette), strings.Join(PaletteKeys, ", "), languageName(b.Language))},
	}
}
