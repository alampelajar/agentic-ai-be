package sitebuilder

import (
	"strings"
	"testing"
)

func TestOwnerColoursAreReadAndChecked(t *testing.T) {
	answer := `<file path="web/src/pages/Home.tsx">x</file>
<colours>{"background": "#e0ffff", "primary-foreground": "#FFFFFF", "font": "#000000", "accent": "cyan"}</colours>`
	got := ParseColours(answer)
	if got["background"] != "#E0FFFF" || got["primary_foreground"] != "#FFFFFF" {
		t.Fatalf("colours = %v, want the background and the button text", got)
	}
	if _, ok := got["font"]; ok {
		t.Error("an unknown token must be left out")
	}
	if _, ok := got["accent"]; ok {
		t.Error("a colour that is not #RRGGBB must be left out")
	}
	if ParseColours("no block here") != nil || ParseColours("<colours>not json</colours>") != nil {
		t.Error("no block, or a broken one, gives no colours")
	}
}

func TestOwnerColoursKeepTextReadable(t *testing.T) {
	palette := map[string]string{"background": "#FFFFFF", "surface": "#F5F5F5", "foreground": "#1A1A1A", "muted": "#666666", "primary": "#1A1A1A", "primary_foreground": "#FFFFFF", "accent": "#C00000", "border": "#DDDDDD"}
	// A dark background with the look's dark text: the text must turn light.
	merged := MergeColours(palette, nil, map[string]string{"background": "#0B1E3F", "surface": "#13294F"})
	for _, pair := range readablePairs {
		text, on := merged[pair.text], merged[pair.on]
		if text == "" {
			text = palette[pair.text]
		}
		if on == "" {
			on = palette[pair.on]
		}
		if ratio := Contrast(text, on); ratio < pair.minimum {
			t.Errorf("%s on %s: contrast %.2f, under %.1f", pair.text, pair.on, ratio, pair.minimum)
		}
	}
	// Earlier colours stay; a newer request for the same token wins.
	again := MergeColours(palette, map[string]string{"background": "#E0FFFF", "accent": "#0891B2"}, map[string]string{"background": "#FFF8E1"})
	if again["background"] != "#FFF8E1" || again["accent"] != "#0891B2" {
		t.Errorf("merged = %v", again)
	}
	if MergeColours(palette, nil, nil) != nil {
		t.Error("no colours at all stays empty")
	}
}

func TestThemeUsesOwnerColours(t *testing.T) {
	d := mustDirection(t, "nordic-minimal")
	css := ThemeCSS(d, "", map[string]string{"background": "#E0FFFF", "bogus": "#000000"})
	if !strings.Contains(css, "--color-background: #E0FFFF;") {
		t.Error("the owner's background must replace the look's")
	}
	if strings.Contains(css, "bogus") {
		t.Error("an unknown token must not reach the theme")
	}
	if !strings.Contains(css, "--color-primary: "+d.Palette["primary"]+";") {
		t.Error("tokens the owner did not set keep the look's colour")
	}
	if tags := ColourTags("ubah background nya jadi warna cyan"); len(tags) != 1 || tags[0] != "tosca" {
		t.Errorf("cyan should be understood as a colour, got %v", tags)
	}
}

func TestRevisePromptOffersColourTokens(t *testing.T) {
	d := mustDirection(t, "nordic-minimal")
	brief := Brief{Name: "Tadika", Language: "id", Pages: []BriefPage{{Title: "Beranda"}}}
	messages := RevisePrompt(brief, d, ThemePalette(d, "", nil), "background cyan", map[string]string{}, false)
	if !strings.Contains(messages[1].Content, "<colours>") || !strings.Contains(messages[1].Content, "background "+d.Palette["background"]) {
		t.Error("the change prompt must show the colours now and how to change them")
	}
}

// What the owner asked for wins over the look in the review and in its fixes.
func TestOwnerRequestsWinInReview(t *testing.T) {
	d := mustDirection(t, "nordic-minimal")
	brief := Brief{Name: "Tadika", Language: "id", Pages: []BriefPage{{Title: "Beranda"}}}
	if strings.Contains(ReviewPrompt(brief, d, nil)[0].Content, "THE OWNER'S REQUESTS COME FIRST") {
		t.Error("with no request there is nothing to protect")
	}
	brief.AddChange("tambahkan warna pelangi")
	review := ReviewPrompt(brief, d, nil)[0].Content
	if !strings.Contains(review, "THE OWNER'S REQUESTS COME FIRST") || !strings.Contains(review, "- tambahkan warna pelangi") {
		t.Error("the review must name the owner's requests and put them first")
	}
	fix := FixPrompt(brief, d, "web/src/pages/Home.tsx", "x", []string{"remove the rainbow"})[1].Content
	if !strings.Contains(fix, "tambahkan warna pelangi") || !strings.Contains(fix, "ignore that problem") {
		t.Error("a fix must not undo what the owner asked for")
	}
}
