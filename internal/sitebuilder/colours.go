package sitebuilder

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
)

// Colours the owner asked for. A look comes with a curated palette; when the owner asks
// for other colours in a change ("make the background cyan"), the model answers with the
// tokens to change in a <colours> block. The worker checks them, keeps text readable, and
// writes them into theme.css, so pages still use only the theme tokens.

var coloursTag = regexp.MustCompile(`(?s)<colours>(.*?)</colours>`)

// ParseColours returns the token colours inside a <colours> block: only known palette
// keys with a #RRGGBB value, upper-cased. Anything else is left out.
func ParseColours(answer string) map[string]string {
	match := coloursTag.FindStringSubmatch(answer)
	if match == nil {
		return nil
	}
	var raw map[string]string
	if json.Unmarshal([]byte(strings.TrimSpace(match[1])), &raw) != nil {
		return nil
	}
	colours := map[string]string{}
	for key, value := range raw {
		key = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(key)), "-", "_")
		value = strings.TrimSpace(value)
		if !isPaletteKey(key) || !hexColour.MatchString(value) {
			continue
		}
		colours[key] = strings.ToUpper(value)
	}
	if len(colours) == 0 {
		return nil
	}
	return colours
}

func isPaletteKey(key string) bool {
	for _, known := range PaletteKeys {
		if known == key {
			return true
		}
	}
	return false
}

// readablePairs are the text tokens and what they are read on, with the contrast each
// needs: body text 4.5, helper text and the large bold button text 3.
var readablePairs = []struct {
	text, on string
	minimum  float64
}{
	{"foreground", "background", 4.5},
	{"foreground", "surface", 4.5},
	{"primary_foreground", "primary", 3},
	{"muted", "background", 3},
}

// MergeColours puts the owner's new colours over their earlier ones and makes every text
// token readable on what it is read on, against the look's palette for whatever the owner
// did not set. A text colour that would be hard to read becomes near-black or white.
func MergeColours(palette, earlier, asked map[string]string) map[string]string {
	merged := map[string]string{}
	for key, value := range earlier {
		merged[key] = value
	}
	for key, value := range asked {
		merged[key] = value
	}
	if len(merged) == 0 {
		return nil
	}
	effective := func(key string) string {
		if value, ok := merged[key]; ok {
			return value
		}
		return palette[key]
	}
	for _, pair := range readablePairs {
		text, on := effective(pair.text), effective(pair.on)
		if !hexColour.MatchString(text) || !hexColour.MatchString(on) || Contrast(text, on) >= pair.minimum {
			continue
		}
		merged[pair.text] = readableOn(on, pair.text == "muted")
	}
	return merged
}

// readableOn is a text colour readable on background: near-black on light colours,
// white on dark ones; a softer grey for helper text.
func readableOn(background string, soft bool) string {
	light := Contrast("#111111", background) >= Contrast("#FFFFFF", background)
	switch {
	case light && soft:
		return "#4A4A4A"
	case light:
		return "#111111"
	case soft:
		return "#D4D4D4"
	default:
		return "#FFFFFF"
	}
}

// Contrast is the WCAG contrast ratio of two #RRGGBB colours (1 to 21).
func Contrast(a, b string) float64 {
	la, lb := relativeLuminance(a), relativeLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func relativeLuminance(hex string) float64 {
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

// coloursText describes the current colours for the model, so it knows what it changes.
func coloursText(palette map[string]string) string {
	parts := make([]string, 0, len(PaletteKeys))
	for _, key := range PaletteKeys {
		parts = append(parts, fmt.Sprintf("%s %s", key, palette[key]))
	}
	return strings.Join(parts, ", ")
}
