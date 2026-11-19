package sitebuilder

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"regexp"
	"sort"
	"strings"
)

// Font is one family that ships in the site image (deploy/site-image/package.json).
type Font struct {
	Package  string `json:"package"`
	Family   string `json:"family"`
	Fallback string `json:"fallback"`
}

// Direction is one curated look. The system chooses three that fit a site, the
// person picks one, and the worker writes it into the site as tokens.
type Direction struct {
	Key         string            `json:"key"`
	Name        map[string]string `json:"name"`
	Description map[string]string `json:"description"`
	Fits        []string          `json:"fits"`
	Heading     Font              `json:"heading"`
	Body        Font              `json:"body"`
	Palette     map[string]string `json:"palette"`
	Radius      map[string]string `json:"radius"`
	Shape       string            `json:"shape"`
	Composition []string          `json:"composition"`
	Voice       map[string]string `json:"voice"`
	// Tags are the colours of the base palette, as ColourTags names them.
	Tags []string `json:"tags"`
	// Variants are curated other palettes of the same look (a green one, a pink one).
	// The model never invents colours; a wish for a colour picks a variant.
	Variants []Variant `json:"variants"`
	// Moods are everyday words for the feeling of the look (romantis, tegas, ceria),
	// written by hand. A mood in the description ranks the look higher (MoodTags).
	Moods []string `json:"moods"`
	// Kit names the template's finished pieces this look uses and how (kit/*.tsx).
	Kit string `json:"kit"`
	// Motion is how much the look moves: "calm", "lively" or "playful". It sets the
	// theme's --motion-* tokens and how many motion pieces the agent uses.
	Motion string `json:"motion"`
}

// MotionStyle is one motion level: the tokens written into theme.css and what the
// agent is told to use.
type MotionStyle struct {
	Duration, Fast, Ease, Distance, Stagger string
	Guide                                   string
}

// MotionStyles are the three levels a look can have.
var MotionStyles = map[string]MotionStyle{
	"calm": {Duration: "700ms", Fast: "200ms", Ease: "cubic-bezier(0.22, 1, 0.36, 1)", Distance: "14px", Stagger: "90ms",
		Guide: "Quiet: Reveal (from 'fade' or 'up') on the heading and content of each section, Stagger for groups of cards or steps, lift on cards. No Marquee, no RotatingWords, no float, no Parallax."},
	"lively": {Duration: "550ms", Fast: "180ms", Ease: "cubic-bezier(0.16, 1, 0.3, 1)", Distance: "28px", Stagger: "70ms",
		Guide: "Clear and confident: Reveal on every section (from 'up', or 'left' and 'right' for the two halves of a two-column section), Stagger for cards, steps and lists, lift on cards and buttons, CountUp for numbers the brief gives, one Marquee on the home page when the site has many services or products, Parallax on one picture when there are photos. RotatingWords only when it fits the hero naturally."},
	"playful": {Duration: "650ms", Fast: "220ms", Ease: "cubic-bezier(0.34, 1.56, 0.64, 1)", Distance: "36px", Stagger: "90ms",
		Guide: "Lively and fun: Reveal (from 'zoom' or 'up') on every section, Stagger wherever there is a group, lift on cards and buttons, float on one or two decorative shapes (never on text), RotatingWords in the home hero, one Marquee on the home page, CountUp for numbers the brief gives, Parallax on one picture or decoration."},
}

// MotionOf returns a look's motion level, "calm" when it has none.
func (d Direction) MotionOf() MotionStyle {
	if style, found := MotionStyles[d.Motion]; found {
		return style
	}
	return MotionStyles["calm"]
}

// Variant is one curated palette of a direction.
type Variant struct {
	Key     string            `json:"key"`
	Name    map[string]string `json:"name"`
	Palette map[string]string `json:"palette"`
	Tags    []string          `json:"tags"`
}

// PaletteFor returns the palette of a variant, or the base palette for "" or an
// unknown key.
func (d Direction) PaletteFor(variant string) map[string]string {
	for _, v := range d.Variants {
		if v.Key == variant {
			return v.Palette
		}
	}
	return d.Palette
}

// HasVariant reports whether variant is "" (the base palette) or one of d's variants.
func (d Direction) HasVariant(variant string) bool {
	if variant == "" {
		return true
	}
	for _, v := range d.Variants {
		if v.Key == variant {
			return true
		}
	}
	return false
}

//go:embed directions.json
var directionsJSON []byte

var directions []Direction

// PaletteKeys are the colour tokens every direction defines, in theme order.
var PaletteKeys = []string{"background", "surface", "foreground", "muted", "primary", "primary_foreground", "accent", "border"}

var (
	hexColour  = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
	cssLength  = regexp.MustCompile(`^[0-9]{1,2}px$`)
	fontFamily = regexp.MustCompile(`^[A-Za-z0-9 ]{2,60}$`)
	fallback   = regexp.MustCompile(`^[A-Za-z0-9 ,-]{2,80}$`)
	fontPkg    = regexp.MustCompile(`^@fontsource(-variable)?/[a-z0-9-]{2,40}$`)
)

func init() {
	if err := json.Unmarshal(directionsJSON, &directions); err != nil {
		panic(fmt.Sprintf("sitebuilder: directions.json: %v", err))
	}
	seen := map[string]bool{}
	for _, d := range directions {
		if err := d.validate(); err != nil {
			panic(fmt.Sprintf("sitebuilder: direction %q: %v", d.Key, err))
		}
		if seen[d.Key] {
			panic("sitebuilder: duplicate direction " + d.Key)
		}
		seen[d.Key] = true
	}
}

// validate keeps everything that ends up in theme.css to plain, known values.
func (d Direction) validate() error {
	if d.Key == "" || d.Name["id"] == "" || d.Name["en"] == "" || d.Description["id"] == "" || d.Description["en"] == "" || len(d.Fits) == 0 || len(d.Composition) == 0 {
		return fmt.Errorf("missing fields")
	}
	for _, font := range []Font{d.Heading, d.Body} {
		if !fontPkg.MatchString(font.Package) || !fontFamily.MatchString(font.Family) || !fallback.MatchString(font.Fallback) {
			return fmt.Errorf("invalid font %+v", font)
		}
	}
	palettes := []map[string]string{d.Palette}
	for _, variant := range d.Variants {
		if variant.Key == "" || variant.Name["id"] == "" || variant.Name["en"] == "" {
			return fmt.Errorf("variant without key or name")
		}
		palettes = append(palettes, variant.Palette)
	}
	for _, palette := range palettes {
		for _, key := range PaletteKeys {
			if !hexColour.MatchString(palette[key]) {
				return fmt.Errorf("invalid colour %s", key)
			}
		}
	}
	for _, key := range []string{"sm", "md", "lg", "xl"} {
		if !cssLength.MatchString(d.Radius[key]) {
			return fmt.Errorf("invalid radius %s", key)
		}
	}
	if _, found := MotionStyles[d.Motion]; !found {
		return fmt.Errorf("invalid motion %q", d.Motion)
	}
	return nil
}

// Directions returns every direction.
func Directions() []Direction { return append([]Direction(nil), directions...) }

// DirectionByKey finds one direction.
func DirectionByKey(key string) (Direction, bool) {
	for _, d := range directions {
		if d.Key == key {
			return d, true
		}
	}
	return Direction{}, false
}

// Kinds are the sorts of site the planner may name; they match the directions' fits.
// relatedKinds are kinds whose looks also suit each other (a wedding look fits an
// engagement party).
var relatedKinds = map[string]map[string]bool{
	"wedding":     {"event": true, "engagement": true, "anniversary": true, "celebration": true},
	"engagement":  {"wedding": true, "event": true, "anniversary": true},
	"anniversary": {"wedding": true, "event": true, "celebration": true},
	"event":       {"celebration": true, "community": true, "wedding": true},
	"celebration": {"event": true, "wedding": true, "community": true},
	"cafe":        {"food": true, "coffee": true, "bakery": true},
	"food":        {"cafe": true, "restaurant": true, "bakery": true, "snack": true},
	"bakery":      {"food": true, "cafe": true},
	"shop":        {"retail": true, "catalog": true, "fashion": true},
	"clinic":      {"health": true, "dentist": true, "wellness": true},
	"portfolio":   {"creative": true, "photography": true},
}

// MoodTags finds the looks' mood words (romantis, elegan, ceria...) in what the person
// wrote, the way ColourTags finds colours.
func MoodTags(texts ...string) []string {
	joined := " " + strings.ToLower(strings.Join(texts, " ")) + " "
	joined = moodSeparator.ReplaceAllString(joined, " ")
	set := map[string]bool{}
	for _, d := range directions {
		for _, mood := range d.Moods {
			if strings.Contains(joined, " "+mood+" ") {
				set[mood] = true
			}
		}
	}
	found := make([]string, 0, len(set))
	for mood := range set {
		found = append(found, mood)
	}
	sort.Strings(found)
	return found
}

var moodSeparator = regexp.MustCompile(`[^\p{L}\p{N}]+`)

func Kinds() []string {
	set := map[string]bool{}
	for _, d := range directions {
		for _, kind := range d.Fits {
			set[kind] = true
		}
	}
	kinds := make([]string, 0, len(set))
	for kind := range set {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds
}

// Pick is a direction with the palette chosen for this site ("" is the base palette).
type Pick struct {
	Direction Direction
	Variant   string
}

func overlap(tags, wanted []string) int {
	count := 0
	for _, tag := range tags {
		for _, want := range wanted {
			if tag == want {
				count++
			}
		}
	}
	return count
}

// PickDirections offers three looks for a kind of site and the colours the person
// asked for. A look that fits the kind scores, a palette with the wished colours
// scores more (the person said so), and the best palette of each look is chosen.
// Random order breaks ties, so two similar sites do not get the same look; the look
// the person used last is never offered first.
func PickDirections(kind, lastUsed string, colours, moods []string, rng *rand.Rand) []Pick {
	kind = strings.ToLower(strings.TrimSpace(kind))
	known := kind != "" && kind != "general" && contains(Kinds(), kind)
	type scored struct {
		pick  Pick
		score float64
		fits  bool
	}
	list := make([]scored, 0, len(directions))
	fitting := 0
	for _, d := range directions {
		score := rng.Float64() // breaks ties at random
		fits := false
		for _, fit := range d.Fits {
			switch {
			case fit == kind:
				score += 10
				fits = true
			case known && relatedKinds[kind][fit]:
				score += 5
				fits = true
			case fit == "general" && !known:
				score += 2
			}
		}
		if fits {
			fitting++
		}
		variant, best := "", overlap(d.Tags, colours)
		for _, v := range d.Variants {
			if matched := overlap(v.Tags, colours); matched > best {
				variant, best = v.Key, matched
			}
		}
		score += float64(best) * 12
		score += float64(overlap(d.Moods, moods)) * 6
		list = append(list, scored{Pick{d, variant}, score, fits})
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].score > list[j].score })
	picked := make([]Pick, 0, 3)
	for _, item := range list {
		if len(picked) == 3 {
			break
		}
		// A look made for another kind of website is offered only when fewer than
		// three looks fit this kind.
		if known && fitting >= 3 && !item.fits {
			continue
		}
		picked = append(picked, item.pick)
	}
	if len(picked) > 1 && picked[0].Direction.Key == lastUsed {
		picked[0], picked[1] = picked[1], picked[0]
	}
	return picked
}

// ThemePalette is the site's colours: the variant's palette with the owner's colours over it.
func ThemePalette(d Direction, variant string, owner map[string]string) map[string]string {
	palette := map[string]string{}
	for key, value := range d.PaletteFor(variant) {
		palette[key] = value
	}
	for key, value := range owner {
		if isPaletteKey(key) && hexColour.MatchString(value) {
			palette[key] = value
		}
	}
	return palette
}

// motionCSS is the movement part of theme.css: the look's motion tokens, and the
// rules behind the kit's motion pieces (Reveal, Stagger, Marquee, RotatingWords) and
// the lift and float classes. Visitors who turned movement off see everything at once.
func motionCSS(m MotionStyle) string {
	return fmt.Sprintf(`
:root {
  --motion-duration: %s;
  --motion-fast: %s;
  --motion-ease: %s;
  --motion-distance: %s;
  --motion-stagger: %s;
}

@utility lift {
  transition: transform var(--motion-fast) var(--motion-ease);
  &:hover { transform: translateY(calc(var(--motion-distance) * -0.2)); }
}

@utility float {
  animation: site-float 6s ease-in-out infinite alternate;
}

@keyframes site-float {
  from { transform: translateY(0); }
  to { transform: translateY(calc(var(--motion-distance) * -0.5)); }
}

@keyframes site-marquee {
  from { transform: translateX(0); }
  to { transform: translateX(calc(-50%% - 1rem)); }
}

@keyframes site-word {
  from { opacity: 0; transform: translateY(0.35em); }
  to { opacity: 1; transform: translateY(0); }
}

.marquee-track { animation: site-marquee var(--marquee-duration, 40s) linear infinite; }
.marquee-track:hover { animation-play-state: paused; }
.word-swap { animation: site-word var(--motion-duration) var(--motion-ease) both; }

@media (prefers-reduced-motion: no-preference) {
  [data-reveal] {
    transition: opacity var(--motion-duration) var(--motion-ease), transform var(--motion-duration) var(--motion-ease);
  }
  [data-reveal]:not([data-shown]) { opacity: 0; }
  [data-reveal='up']:not([data-shown]) { transform: translateY(var(--motion-distance)); }
  [data-reveal='down']:not([data-shown]) { transform: translateY(calc(var(--motion-distance) * -1)); }
  [data-reveal='left']:not([data-shown]) { transform: translateX(calc(var(--motion-distance) * -1)); }
  [data-reveal='right']:not([data-shown]) { transform: translateX(var(--motion-distance)); }
  [data-reveal='zoom']:not([data-shown]) { transform: scale(0.94); }
}

@media (prefers-reduced-motion: reduce) {
  .marquee-track, .float, .word-swap { animation: none !important; }
  .marquee-track { flex-wrap: wrap; width: auto; justify-content: center; }
  .marquee-track [data-copy] { display: none; }
  .lift:hover { transform: none; }
}
`, m.Duration, m.Fast, m.Ease, m.Distance, m.Stagger)
}

// ThemeCSS is web/src/theme.css for a direction. Tailwind's own colours and fonts
// are switched off (--color-*: initial), so the only colours a page can use are the
// direction's tokens: a palette class like bg-blue-500 produces nothing. owner holds
// the colours the owner asked for in a change (MergeColours); they replace the
// direction's colours of the same tokens.
func ThemeCSS(d Direction, variant string, owner map[string]string) string {
	palette := ThemePalette(d, variant, owner)
	var b strings.Builder
	b.WriteString("/* Written by the builder from the design direction \"" + d.Key + "\" " + variant + ". The agent cannot change it. */\n")
	b.WriteString("@import 'tailwindcss';\n")
	b.WriteString("@import '" + d.Heading.Package + "';\n")
	if d.Body.Package != d.Heading.Package {
		b.WriteString("@import '" + d.Body.Package + "';\n")
	}
	b.WriteString("\n@theme {\n  --color-*: initial;\n  --font-*: initial;\n  --radius-*: initial;\n")
	for _, key := range PaletteKeys {
		fmt.Fprintf(&b, "  --color-%s: %s;\n", strings.ReplaceAll(key, "_", "-"), palette[key])
	}
	b.WriteString("  --color-white: #ffffff;\n  --color-black: #000000;\n  --color-transparent: transparent;\n  --color-current: currentColor;\n")
	fmt.Fprintf(&b, "  --font-heading: '%s', %s;\n", d.Heading.Family, d.Heading.Fallback)
	fmt.Fprintf(&b, "  --font-body: '%s', %s;\n", d.Body.Family, d.Body.Fallback)
	for _, key := range []string{"sm", "md", "lg", "xl"} {
		fmt.Fprintf(&b, "  --radius-%s: %s;\n", key, d.Radius[key])
	}
	fmt.Fprintf(&b, "  --radius-2xl: calc(%s * 1.5);\n  --radius-3xl: calc(%s * 2);\n", d.Radius["xl"], d.Radius["xl"])
	b.WriteString("}\n\n@layer base {\n  html {\n    font-family: var(--font-body);\n    background-color: var(--color-background);\n    color: var(--color-foreground);\n    -webkit-font-smoothing: antialiased;\n  }\n  h1, h2, h3, h4 {\n    font-family: var(--font-heading);\n  }\n  :focus-visible {\n    outline: 2px solid var(--color-primary);\n    outline-offset: 2px;\n  }\n}\n\n@media (prefers-reduced-motion: reduce) {\n  *, *::before, *::after {\n    animation-duration: 0.01ms !important;\n    transition-duration: 0.01ms !important;\n    scroll-behavior: auto !important;\n  }\n}\n")
	b.WriteString(motionCSS(d.MotionOf()))
	return b.String()
}
