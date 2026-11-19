package sitebuilder

import (
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAgentMayOnlyWriteItsOwnFiles(t *testing.T) {
	allowed := []string{"web/src/pages/Home.tsx", "web/src/components/Layout.tsx", "web/src/sections/Hero.tsx", "server/routes/menu.js", "server/schema.sql"}
	for _, path := range allowed {
		if _, err := CheckAgentPath(path); err != nil {
			t.Errorf("%s should be allowed: %v", path, err)
		}
	}
	refused := []string{
		"web/src/theme.css", "web/src/site.ts", "web/src/routes.tsx", "web/src/main.tsx", "web/src/App.tsx", "web/src/router.tsx",
		"web/src/components/SiteForm.tsx", "server/index.js", "server/db.js", "package.json", "site.json", "web/index.html",
		"../etc/passwd", "/etc/passwd", "web/src/pages/../../server/index.js", "web/src/pages/sub/Deep.tsx", ".output/server.mjs",
		"web/src/pages/.env", "server/routes/run.sh", "web/public/uploads/x.png", "Dockerfile", "",
	}
	for _, path := range refused {
		if _, err := CheckAgentPath(path); err == nil {
			t.Errorf("%s should be refused", path)
		}
	}
}

func TestParseFilesStripsFences(t *testing.T) {
	answer := "Here you go.\n<file path=\"web/src/pages/Home.tsx\">\n```tsx\nexport default function Home() { return null }\n```\n</file>\n<file path=\"server/schema.sql\">\nSELECT 1;\n</file>"
	edits := ParseFiles(answer)
	if len(edits) != 2 || edits[0].Path != "web/src/pages/Home.tsx" || strings.Contains(edits[0].Content, "```") || edits[1].Content != "SELECT 1;\n" {
		t.Fatalf("unexpected edits: %#v", edits)
	}
	if ParseSummary("x <summary> Menambahkan bagian acara. </summary>") != "Menambahkan bagian acara." || ParseSummary("<summary>...</summary>") != "" {
		t.Fatal("summary not read")
	}
}

func TestTokenCheck(t *testing.T) {
	bad := []string{
		`<div className="bg-[#ff0000]">`,
		`<div style={{ color: '#333' }}>`,
		`<p className="text-blue-500">`,
		`<h1 style={{ fontFamily: 'Arial' }}>`,
		`<div className="bg-linear-to-r from-primary">`,
		`<img src="https://images.unsplash.com/photo-1" />`,
		`Lorem ipsum dolor`,
	}
	for _, line := range bad {
		if len(CheckTokens("web/src/pages/Home.tsx", line)) == 0 {
			t.Errorf("not caught: %s", line)
		}
	}
	if len(CheckTokens("web/src/pages/Home.tsx", "import Layout from '../components/Layout'")) == 0 || len(CheckTokens("web/src/pages/Home.tsx", "<header className='x'>")) == 0 {
		t.Error("a page with its own layout or header was not caught")
	}
	if len(CheckTokens("web/src/components/Layout.tsx", "<header className='x'>")) != 0 {
		t.Error("the layout itself may have a header")
	}
	good := `<a href="#cafe" className="bg-primary text-primary-foreground hover:bg-primary/90 border-border rounded-lg font-heading">Menu</a>`
	if problems := CheckTokens("web/src/pages/Home.tsx", good); len(problems) != 0 {
		t.Errorf("false alarm: %v", problems)
	}
}

func TestBriefNormalizeAndGeneratedFiles(t *testing.T) {
	brief := Brief{Name: " Bengkel Jaya ", Language: "xx", Pages: []BriefPage{
		{Title: "Beranda", Slug: "home"}, {Title: "Layanan Kami"}, {Title: "Layanan Kami"}, {Title: ""}, {Title: "Kontak", Slug: "Kontak & Lokasi"},
	}}
	if err := brief.Normalize(); err != nil {
		t.Fatal(err)
	}
	if brief.Language != "id" || brief.Name != "Bengkel Jaya" || len(brief.Pages) != 4 {
		t.Fatalf("unexpected brief: %+v", brief)
	}
	if brief.Pages[1].Slug == brief.Pages[2].Slug {
		t.Fatal("duplicate slugs kept")
	}
	if got := brief.Pages[3].Slug; got != "kontak-lokasi" {
		t.Fatalf("slug %q", got)
	}
	if brief.Pages[0].Path(0) != "/" || brief.Pages[0].Component(0) != "Home" || brief.Pages[1].Component(1) != "LayananKami" {
		t.Fatal("unexpected page names")
	}
	routes := RoutesTSX(brief)
	if !strings.Contains(routes, "import LayananKami from './pages/LayananKami'") || !strings.Contains(routes, "path: '*'") {
		t.Fatalf("routes: %s", routes)
	}
	injected := Brief{Name: "x\"; alert(1); //", Language: "id", Pages: []BriefPage{{Title: "</script>", Slug: "home"}}}
	site := SiteTS(injected, []Image{{Src: "/uploads/a.png", Alt: "a'b"}})
	if strings.Contains(site, `"x"; alert`) {
		t.Fatal("name not escaped in site.ts")
	}
	if html := IndexHTML(injected); strings.Contains(html, "<title>x\"") {
		t.Fatal("name not escaped in index.html")
	}
}

func TestThemeHasOnlyTokens(t *testing.T) {
	for _, direction := range Directions() {
		css := ThemeCSS(direction, "", nil)
		for _, want := range []string{"--color-*: initial", "--color-primary:", "--font-heading:", direction.Heading.Package} {
			if !strings.Contains(css, want) {
				t.Errorf("%s: theme lacks %q", direction.Key, want)
			}
		}
	}
}

func TestPickDirectionsAvoidsLastUsedFirst(t *testing.T) {
	for seed := uint64(0); seed < 50; seed++ {
		first := PickDirections("food", "", nil, nil, rand.New(rand.NewPCG(seed, 1)))
		if len(first) != 3 {
			t.Fatal("expected three directions")
		}
		again := PickDirections("food", first[0].Direction.Key, nil, nil, rand.New(rand.NewPCG(seed, 1)))
		if again[0].Direction.Key == first[0].Direction.Key {
			t.Fatal("the last used direction was offered first")
		}
	}
}

func TestAColourWishIsHonoured(t *testing.T) {
	// "Undangan ulang tahun, tema kebun hijau": every look offered has green in it.
	colours := ColourTags("Website undangan ulang tahun, saya ingin temanya kebun hijau")
	if len(colours) != 1 || colours[0] != "hijau" {
		t.Fatalf("colours %v", colours)
	}
	for seed := uint64(0); seed < 20; seed++ {
		for _, pick := range PickDirections("event", "", colours, nil, rand.New(rand.NewPCG(seed, 2))) {
			tags := pick.Direction.Tags
			for _, variant := range pick.Direction.Variants {
				if variant.Key == pick.Variant {
					tags = variant.Tags
				}
			}
			if overlap(tags, colours) == 0 {
				t.Fatalf("%s/%s offered without green", pick.Direction.Key, pick.Variant)
			}
		}
	}
	if css := ThemeCSS(mustDirection(t, "warm-editorial"), "garden", nil); !strings.Contains(css, "#3F6B3A") {
		t.Fatal("the variant palette is not in the theme")
	}
}

func mustDirection(t *testing.T, key string) Direction {
	d, ok := DirectionByKey(key)
	if !ok {
		t.Fatalf("no direction %s", key)
	}
	return d
}

// Every palette, variants included, keeps body text and button text readable.
func TestPalettesAreReadable(t *testing.T) {
	for _, d := range Directions() {
		palettes := map[string]map[string]string{"base": d.Palette}
		for _, v := range d.Variants {
			palettes[v.Key] = v.Palette
		}
		for name, p := range palettes {
			for _, pair := range [][2]string{{"foreground", "background"}, {"foreground", "surface"}, {"primary_foreground", "primary"}, {"muted", "background"}} {
				minimum := 4.5
				if pair[0] == "muted" || pair[0] == "primary_foreground" {
					minimum = 3.0 // muted is helper text; buttons use large, bold text
				}
				if ratio := contrast(p[pair[0]], p[pair[1]]); ratio < minimum {
					t.Errorf("%s/%s: %s on %s has contrast %.2f, under %.1f", d.Key, name, pair[0], pair[1], ratio, minimum)
				}
			}
		}
	}
}

func contrast(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func luminance(hex string) float64 {
	var rgb [3]float64
	for i := 0; i < 3; i++ {
		var v int
		fmt.Sscanf(hex[1+2*i:3+2*i], "%02x", &v)
		c := float64(v) / 255
		if c <= 0.03928 {
			rgb[i] = c / 12.92
		} else {
			rgb[i] = math.Pow((c+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*rgb[0] + 0.7152*rgb[1] + 0.0722*rgb[2]
}

// The names and descriptions people read use no design or technical words.
func TestLooksUsePlainWords(t *testing.T) {
	banned := []string{"serif", "font", "editorial", "minimalis", "minimalist", "industrial", "nordik", "nordic", "tipografi", "typography", "palet", "palette", "layout", "template"}
	for _, d := range Directions() {
		texts := []string{d.Name["id"], d.Name["en"], d.Description["id"], d.Description["en"]}
		for _, v := range d.Variants {
			texts = append(texts, v.Name["id"], v.Name["en"])
		}
		for _, text := range texts {
			for _, word := range banned {
				if strings.Contains(strings.ToLower(text), word) {
					t.Errorf("%s: %q uses %q", d.Key, text, word)
				}
			}
		}
	}
}

func TestLanguageOfADescription(t *testing.T) {
	cases := map[string]string{
		"Website Undangan Ulang Tahun saya pada 18 agustus, saya ingin tema nya kebun hijau": "id",
		"Bengkel motor di Bandung, servis rutin dan ganti oli":                               "id",
		"A website for my bakery in London where people can order cakes":                     "en",
		"Toko roti": "id",
	}
	for text, want := range cases {
		if got := DetectLanguage(text); got != want {
			t.Errorf("%q: %s, want %s", text, got, want)
		}
	}
}

func TestPageInTheWrongLanguageIsCaught(t *testing.T) {
	english := "export default function Home() {\n  return <section className=\"bg-background text-foreground\"><h1>Welcome to our bakery</h1><p>We bake fresh bread for you every day and you can order it from us with WhatsApp in the morning.</p><p>Visit us and see more of the cakes that we have for your party.</p></section>\n}\n"
	if len(CheckLanguage("web/src/pages/Home.tsx", english, "id")) == 0 {
		t.Error("an English page on an Indonesian site was not caught")
	}
	if len(CheckLanguage("web/src/pages/Home.tsx", english, "en")) != 0 {
		t.Error("an English page on an English site was flagged")
	}
	indonesian := "export default function Home() {\n  return <section className=\"bg-background\"><h1>Roti hangat setiap pagi</h1><p>Kami memanggang roti dan kue untuk Anda setiap hari, pesan lewat WhatsApp dari rumah.</p><p>Datang ke toko kami dan lihat kue untuk acara Anda.</p></section>\n}\n"
	if len(CheckLanguage("web/src/pages/Home.tsx", indonesian, "id")) != 0 {
		t.Error("an Indonesian page was flagged")
	}
}

func TestSlugs(t *testing.T) {
	for _, slug := range []string{"bengkel-jaya", "abc", "toko123"} {
		if CheckSlug(slug) != nil {
			t.Errorf("%s should be valid", slug)
		}
	}
	for _, slug := range []string{"ab", "-abc", "abc-", "a--b", "Admin", "admin", "api", "p-12", "contoh-roti", "toko_jaya", strings.Repeat("a", 41)} {
		if CheckSlug(slug) == nil {
			t.Errorf("%s should be refused", slug)
		}
	}
}

func TestPreviewURLIsSignedAndExpires(t *testing.T) {
	t.Setenv("SITE_PREVIEW_SECRET", strings.Repeat("s", 40))
	settings := Settings{PreviewDomain: "preview.localhost", Scheme: "http", PublicPort: "8090"}
	now := time.Unix(1_800_000_000, 0)
	url, expires := settings.PreviewURL(42, now)
	if !strings.HasPrefix(url, "http://p-42-") || !strings.HasSuffix(url, ".preview.localhost:8090/") || !expires.Equal(now.Add(PreviewTTL)) {
		t.Fatalf("unexpected preview url %s", url)
	}
	other, _ := settings.PreviewURL(43, now)
	if strings.TrimPrefix(url, "http://p-42-") == strings.TrimPrefix(other, "http://p-43-") {
		t.Fatal("signature does not depend on the site")
	}
	label := strings.SplitN(strings.TrimPrefix(url, "http://"), ".", 2)[0]
	if len(label) > 63 {
		t.Fatalf("host label too long: %d", len(label))
	}
}

func TestVersionsRoundTrip(t *testing.T) {
	files := t.TempDir()
	if err := CopyTemplate(files); err != nil {
		t.Fatal(err)
	}
	if err := WriteSiteFile(files, ".output/server.mjs", "built"); err != nil {
		t.Fatal(err)
	}
	version := filepath.Join(t.TempDir(), "1")
	if err := SaveVersion(files, version); err != nil {
		t.Fatal(err)
	}
	_ = WriteSiteFile(files, "web/src/pages/Home.tsx", "changed")
	_ = WriteSiteFile(files, "web/src/pages/Extra.tsx", "new")
	if err := RestoreVersion(version, files); err != nil {
		t.Fatal(err)
	}
	if ReadSiteFile(files, "web/src/pages/Home.tsx") == "changed" || ReadSiteFile(files, "web/src/pages/Extra.tsx") != "" {
		t.Fatal("restore did not bring back the saved files")
	}
	if data, _ := os.ReadFile(filepath.Join(files, ".output", "server.mjs")); string(data) != "built" {
		t.Fatal("output not restored")
	}
}

func TestPartialFilesAreRefused(t *testing.T) {
	for _, partial := range []string{"export default function A() {\n  // ... rest unchanged\n}", "<div>\n{/* ... */}\n</div>", "a\n...\nb", "// The rest of the file stays unchanged"} {
		if !IsPartialFile(partial) {
			t.Errorf("not caught: %q", partial)
		}
	}
	for _, whole := range []string{"const text = 'Loading...'", "<p>Tunggu...</p>", "const all = [...items]"} {
		if IsPartialFile(whole) {
			t.Errorf("false alarm: %q", whole)
		}
	}
}

func TestOwnerRequestsStayInTheBrief(t *testing.T) {
	var brief Brief
	for index := 0; index < 12; index++ {
		brief.AddChange(" request ")
	}
	brief.AddChange("   ")
	if len(brief.ChangesAsked) != maxChangesKept || brief.ChangesAsked[0] != "request" {
		t.Fatalf("unexpected changes: %#v", brief.ChangesAsked)
	}
	if !strings.Contains(briefText(brief), "changes_asked") {
		t.Fatal("the prompts do not see the owner's requests")
	}
}

func TestPublishedSitesCanUseTheirOwnScheme(t *testing.T) {
	t.Setenv("SITE_PUBLIC_SCHEME", "http")
	t.Setenv("SITE_PUBLIC_PORT", "8090")
	t.Setenv("SITES_DOMAIN", "contoh.id")
	t.Setenv("SITE_PREVIEW_DOMAIN", "preview.localhost")
	t.Setenv("SITES_PUBLIC_SCHEME", "https")
	t.Setenv("SITES_PUBLIC_PORT", "")
	settings := LoadSettings()
	if got := settings.LiveURL("toko-roti"); got != "https://toko-roti.contoh.id/" {
		t.Fatalf("published address: %s", got)
	}
	if got, _ := settings.PreviewURL(7, time.Now()); !strings.HasPrefix(got, "http://p-7-") || !strings.HasSuffix(got, ".preview.localhost:8090/") {
		t.Fatalf("preview address: %s", got)
	}
}

func TestPublishedSitesFollowThePreviewSchemeByDefault(t *testing.T) {
	t.Setenv("SITE_PUBLIC_SCHEME", "http")
	t.Setenv("SITE_PUBLIC_PORT", "8090")
	t.Setenv("SITES_DOMAIN", "sites.localhost")
	if got := LoadSettings().LiveURL("bengkel"); got != "http://bengkel.sites.localhost:8090/" {
		t.Fatalf("published address: %s", got)
	}
}

func TestProgressBelongsToOneWork(t *testing.T) {
	file := filepath.Join(t.TempDir(), "site", "progress.json")
	key := ProgressKey("create", "", `{"name":"A"}`, "quiet-luxury", "")
	if key == ProgressKey("create", "", `{"name":"B"}`, "quiet-luxury", "") || key == ProgressKey("revise", "lebih ceria", `{"name":"A"}`, "quiet-luxury", "") || key == ProgressKey("create", "", `{"name":"A"}`, "romantic-soft", "") {
		t.Fatal("other work must get another key")
	}
	if _, found := LoadProgress(file); found {
		t.Fatal("no record yet")
	}
	if err := SaveProgress(file, Progress{Key: key, Layout: true, Pages: []string{"web/src/pages/Home.tsx"}}); err != nil {
		t.Fatal(err)
	}
	got, found := LoadProgress(file)
	if !found || got.Key != key || !got.Layout || !got.HasPage("web/src/pages/Home.tsx") || got.HasPage("web/src/pages/Acara.tsx") {
		t.Fatalf("record: %+v", got)
	}
	ClearProgress(file)
	if _, found := LoadProgress(file); found {
		t.Fatal("cleared record still found")
	}
}

func TestWeddingGetsLooksThatFit(t *testing.T) {
	for seed := uint64(1); seed <= 20; seed++ {
		picks := PickDirections("wedding", "", nil, MoodTags("Saya ingin tampilannya menarik, romantis dan elegan."), rand.New(rand.NewPCG(seed, 3)))
		if len(picks) != 3 {
			t.Fatalf("got %d looks", len(picks))
		}
		for _, pick := range picks {
			fits := false
			for _, fit := range pick.Direction.Fits {
				if fit == "wedding" || fit == "event" || fit == "engagement" || fit == "anniversary" || fit == "celebration" {
					fits = true
				}
			}
			if !fits {
				t.Fatalf("seed %d: %s does not fit a wedding", seed, pick.Direction.Key)
			}
		}
		if picks[0].Direction.Key != "romantic-soft" && picks[0].Direction.Key != "quiet-luxury" {
			t.Errorf("seed %d: a romantic, elegant wedding should start with a romantic or elegant look, got %s", seed, picks[0].Direction.Key)
		}
	}
}

func TestMoodWords(t *testing.T) {
	got := MoodTags("Tema yang ROMANTIS, elegan; pokoknya meriah!")
	for _, want := range []string{"elegan", "meriah", "romantis"} {
		if !contains(got, want) {
			t.Errorf("missing %s in %v", want, got)
		}
	}
	if len(MoodTags("toko roti di Bandung")) != 0 {
		t.Error("no mood in a plain description")
	}
}

func TestEveryLookNamesItsPiecesAndMoods(t *testing.T) {
	for _, d := range Directions() {
		if strings.TrimSpace(d.Kit) == "" || len(d.Moods) == 0 {
			t.Errorf("%s needs kit pieces and moods", d.Key)
		}
	}
}

func TestEmptyFramesAreRefused(t *testing.T) {
	page := "export default function Galeri() { return <div className='aspect-square rounded-md bg-surface' /> }"
	if problems := CheckTokens("web/src/pages/Galeri.tsx", page); len(problems) == 0 {
		t.Fatal("an empty frame passed")
	}
	full := "export default function Galeri() { return <div className='aspect-square'><img src={site.images[0].src} alt='' /></div> }"
	for _, problem := range CheckTokens("web/src/pages/Galeri.tsx", full) {
		if strings.Contains(problem, "empty picture frame") {
			t.Fatalf("a frame with a picture was refused: %s", problem)
		}
	}
}

func TestFixedFilesAreRefreshedAndProtected(t *testing.T) {
	dir := t.TempDir()
	if err := WriteSiteFile(dir, "web/src/pages/Home.tsx", "mine"); err != nil {
		t.Fatal(err)
	}
	if err := RefreshFixedFiles(dir); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"web/src/kit/Guestbook.tsx", "web/src/kit/Countdown.tsx", "web/src/components/SiteForm.tsx", "server/routes/forms.js"} {
		if ReadSiteFile(dir, rel) == "" {
			t.Errorf("%s was not written", rel)
		}
	}
	if ReadSiteFile(dir, "web/src/pages/Home.tsx") != "mine" || ReadSiteFile(dir, "site.json") != "" || ReadSiteFile(dir, "web/src/theme.css") != "" {
		t.Error("files of the agent or of the brief were touched")
	}
	for _, rel := range []string{"web/src/kit/Guestbook.tsx", "server/routes/forms.js"} {
		if _, err := CheckAgentPath(rel); err == nil {
			t.Errorf("the agent may write %s", rel)
		}
	}
	if !strings.Contains(ReadSiteFile(dir, "server/routes/forms.js"), "PUBLIC_FIELDS = ['name', 'message']") {
		t.Error("the guest book list must show only name and message")
	}
}

func TestGuestbookFormSurvivesTheBrief(t *testing.T) {
	b := Brief{Name: "Raka & Nadia", Pages: []BriefPage{{Title: "Beranda"}}, Forms: []BriefForm{{Name: "Ucapan", Fields: []string{"Nama", "Ucapan"}, Guestbook: true}, {Name: "rsvp"}}}
	if err := b.Normalize(); err != nil {
		t.Fatal(err)
	}
	if !b.Forms[0].Guestbook || b.Forms[0].Name != "ucapan" || b.Forms[1].Guestbook {
		t.Fatalf("forms: %+v", b.Forms)
	}
	if !strings.Contains(SiteJSON(b, "romantic-soft"), `"guestbook": true`) {
		t.Fatal("site.json must mark the guest book for the server")
	}
}

// A copied website's brief keeps the example's pages; other briefs say nothing of it.
func TestBriefPromptCopiesPages(t *testing.T) {
	direction := Directions()[0]
	plain := BriefPrompt(BriefInput{Description: "Toko roti", Language: "id", UILanguage: "id", Direction: direction})
	if strings.Contains(plain[1].Content, "example website") {
		t.Fatal("a brief without a copied website mentions one")
	}
	copied := BriefPrompt(BriefInput{Description: "Toko roti", Language: "id", UILanguage: "id", Direction: direction, PageTitles: []string{"Beranda", "Menu"}})
	if !strings.Contains(copied[1].Content, "Beranda | Menu") || !strings.Contains(copied[0].Content, "same pages in the same order") {
		t.Fatalf("copied brief prompt = %q / %q", copied[0].Content, copied[1].Content)
	}
}

// Every look has a motion level, the theme carries its tokens and the CSS behind the
// motion pieces, and visitors who turned movement off see everything at once.
func TestLooksMove(t *testing.T) {
	levels := map[string]int{}
	for _, d := range Directions() {
		style, found := MotionStyles[d.Motion]
		if !found {
			t.Fatalf("%s has no motion level", d.Key)
		}
		levels[d.Motion]++
		css := ThemeCSS(d, "", nil)
		for _, want := range []string{"--motion-distance: " + style.Distance, "@utility lift", "@utility float", "[data-reveal='up']:not([data-shown])", ".marquee-track [data-copy] { display: none; }"} {
			if !strings.Contains(css, want) {
				t.Errorf("%s theme lacks %q", d.Key, want)
			}
		}
		if strings.Contains(css, "%!") {
			t.Errorf("%s theme has a formatting mistake", d.Key)
		}
		prompt := CodingSystem(Brief{Language: "id"}, d)
		if !strings.Contains(prompt, style.Guide) || !strings.Contains(prompt, "Reveal {") {
			t.Errorf("%s coding prompt lacks its motion guide", d.Key)
		}
	}
	for _, level := range []string{"calm", "lively", "playful"} {
		if levels[level] == 0 {
			t.Errorf("no look moves in a %s way", level)
		}
	}
}

// Pages animate only through the kit: their own animation code is sent back.
func TestOwnAnimationIsRefused(t *testing.T) {
	for _, line := range []string{
		`<div className="animate-bounce">`,
		`requestAnimationFrame(step)`,
		`const css = "@keyframes spin {}"`,
	} {
		if len(CheckTokens("web/src/pages/Home.tsx", line)) == 0 {
			t.Errorf("not refused: %s", line)
		}
	}
	for _, line := range []string{
		`<Reveal from="up"><h2 className="font-heading">Menu</h2></Reveal>`,
		`<article className="lift rounded-lg border border-border">`,
		`<Loader2 className="animate-spin" />`,
	} {
		if problems := CheckTokens("web/src/pages/Home.tsx", line); len(problems) != 0 {
			t.Errorf("refused %s: %v", line, problems)
		}
	}
}

// A counting number is a fact: only numbers written in the brief may count up.
func TestCountUpShowsOnlyNumbersTheOwnerGave(t *testing.T) {
	known := KnownNumbers(`{"content_notes": "12 rasa gelato buatan sendiri, buka sejak 2019, harga mulai Rp 25.000"}`)
	for _, line := range []string{`<CountUp to={12} suffix=" rasa" />`, `<CountUp to={2019} />`, `<CountUp to={25000} prefix="Rp " />`} {
		if problems := CheckCountUp("web/src/pages/Home.tsx", line, known); len(problems) != 0 {
			t.Errorf("refused %s: %v", line, problems)
		}
	}
	for _, line := range []string{`<CountUp to={40000} prefix="Rp " />`, `<CountUp to={stat.value} />`, `<CountUp to={500} suffix="+ pelanggan" />`} {
		if len(CheckCountUp("web/src/pages/Home.tsx", line, known)) == 0 {
			t.Errorf("not refused: %s", line)
		}
	}
}

// Build errors keep the file, line and message, without TypeScript's long type shapes.
func TestShortenErrors(t *testing.T) {
	long := "web/src/components/Layout.tsx:91:19 Property 'address' does not exist on type '{ name: string; tagline: string; language: \"id\" | \"en\"; pages: SitePage[]; contact: { phone: string; whatsapp: string; email: string; address: string; hours: string; }; social: Record<string, string>; logo: string; }'."
	got := ShortenErrors(long)
	if got != "web/src/components/Layout.tsx:91:19 Property 'address' does not exist on type '{...}'." {
		t.Fatalf("got %q", got)
	}
	short := "web/src/pages/Home.tsx:3:1 Cannot find name 'Foo'."
	if ShortenErrors(short) != short {
		t.Fatal("a short error changed")
	}
}
