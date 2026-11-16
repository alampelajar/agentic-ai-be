package sitebuilder

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Words that are common in one language and rare in the other. Counting them is
// enough to tell Indonesian from English in a description or in a page's text.
var (
	indonesianWords = wordSet("yang dan dengan untuk kami anda saya di ke dari ini itu akan bisa ada kita lebih atau pada juga tidak sudah agar karena tempat kota usaha jual menjual buka setiap hari harga pesan layanan kunjungi hubungi tentang")
	englishWords    = wordSet("the and with your our you we are is of from this that will can for to in my me it be have has more about contact us at an on by open every day price order services visit")
	wordPattern     = regexp.MustCompile(`[A-Za-z]+`)
)

func wordSet(words string) map[string]bool {
	set := map[string]bool{}
	for _, word := range strings.Fields(words) {
		set[word] = true
	}
	return set
}

func countLanguages(text string) (indonesian, english int) {
	for _, word := range wordPattern.FindAllString(strings.ToLower(text), -1) {
		if indonesianWords[word] {
			indonesian++
		}
		if englishWords[word] {
			english++
		}
	}
	return indonesian, english
}

// DetectLanguage tells the language of a text: "en" when it is clearly English,
// "id" otherwise (the builder's people write mostly in Indonesian).
func DetectLanguage(text string) string {
	indonesian, english := countLanguages(text)
	if english > indonesian && english >= 2 {
		return "en"
	}
	return "id"
}

var (
	classAttribute = regexp.MustCompile("className=(\"[^\"]*\"|'[^']*'|\\{`[^`]*`\\})")
	importLine     = regexp.MustCompile(`(?m)^\s*import .*$`)
	jsxText        = regexp.MustCompile(`>([^<>{}]*[A-Za-z][^<>{}]*)<`)
	quotedText     = regexp.MustCompile(`'([^'\n]*\s[^'\n]*)'|"([^"\n]*\s[^"\n]*)"`)
)

// visibleText is a rough cut of what a page shows: text between tags and quoted
// strings with a space in them, without class names and imports.
func visibleText(content string) string {
	content = importLine.ReplaceAllString(classAttribute.ReplaceAllString(content, ""), "")
	var parts []string
	for _, match := range jsxText.FindAllStringSubmatch(content, -1) {
		parts = append(parts, match[1])
	}
	for _, match := range quotedText.FindAllStringSubmatch(content, -1) {
		parts = append(parts, match[1]+match[2])
	}
	return strings.Join(parts, " ")
}

// CheckLanguage finds a page or component whose visible text is mostly in the other
// language than the site's.
func CheckLanguage(rel, content, language string) []string {
	if !strings.HasSuffix(rel, ".tsx") {
		return nil
	}
	indonesian, english := countLanguages(visibleText(content))
	switch {
	case language == "id" && english >= 8 && english > 2*indonesian:
		return []string{fmt.Sprintf("%s: the visible text is in English, but this website is in Indonesian. Write every visible text in Indonesian (Bahasa Indonesia)", rel)}
	case language == "en" && indonesian >= 8 && indonesian > 2*english:
		return []string{fmt.Sprintf("%s: the visible text is in Indonesian, but this website is in English. Write every visible text in English", rel)}
	}
	return nil
}

// colourWords maps what people write about colour, in both languages, to one tag.
var colourWords = map[string]string{
	"hijau": "hijau", "green": "hijau", "sage": "hijau", "daun": "hijau", "kebun": "hijau", "garden": "hijau", "alam": "hijau",
	"biru": "biru", "blue": "biru", "navy": "biru", "laut": "biru", "ocean": "biru", "langit": "biru",
	"merah": "merah", "red": "merah", "maroon": "maroon", "marun": "maroon", "burgundy": "maroon",
	"pink": "pink", "merah muda": "pink", "rose": "pink", "peach": "pink",
	"ungu": "ungu", "purple": "ungu", "violet": "ungu", "lavender": "ungu", "lilac": "ungu",
	"kuning": "kuning", "yellow": "kuning", "emas": "emas", "gold": "emas", "golden": "emas",
	"oranye": "oranye", "orange": "oranye", "jingga": "oranye", "terakota": "oranye",
	"cokelat": "cokelat", "coklat": "cokelat", "brown": "cokelat", "kayu": "cokelat", "wood": "cokelat",
	"hitam": "hitam", "black": "hitam", "gelap": "gelap", "dark": "gelap",
	"putih": "putih", "white": "putih", "krem": "krem", "cream": "krem", "gading": "krem", "ivory": "krem",
	"abu": "abu", "abu-abu": "abu", "grey": "abu", "gray": "abu",
	"tosca": "tosca", "toska": "tosca", "teal": "tosca", "turquoise": "tosca",
}

var colourPhrase = regexp.MustCompile(`merah muda|abu-abu|[a-z]+`)

// ColourTags finds the colours a text asks for, as tags ("hijau", "biru"...).
func ColourTags(texts ...string) []string {
	set := map[string]bool{}
	for _, text := range texts {
		for _, word := range colourPhrase.FindAllString(strings.ToLower(text), -1) {
			if tag, ok := colourWords[word]; ok {
				set[tag] = true
			}
		}
	}
	tags := make([]string, 0, len(set))
	for tag := range set {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	return tags
}
