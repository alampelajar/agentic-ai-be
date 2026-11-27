package sitebuilder

import (
	"regexp"
	"strings"
)

// Websites the builder never makes, whoever asks: gambling and betting of any kind,
// and pornography. Online gambling and pornography are against the law in Indonesia
// (UU ITE article 27, UU Pornografi), and a published website carries the platform's
// domain.
//
// Two checks guard every text a person writes (the description, the answers, every
// change request): ForbiddenTopic, a word list that runs before any model is asked,
// and the planner's own judgement ("forbidden" in QuestionSet), which catches what a
// word list cannot.

// Forbidden topics, as the codes the interface translates.
const (
	TopicGambling = "gambling"
	TopicAdult    = "adult"
)

// IsForbiddenTopic reports whether a planner's answer names a topic the builder refuses.
func IsForbiddenTopic(topic string) bool {
	return topic == TopicGambling || topic == TopicAdult
}

var forbiddenWords = []struct {
	topic string
	words *regexp.Regexp
}{
	{TopicGambling, regexp.MustCompile(`\b(` + strings.Join([]string{
		`judi`, `berjudi`, `perjudian`, `penjudi`, `judol`, `gambling`, `gamble`, `gambler`,
		`taruhan`, `bertaruh`, `betting`, `sportsbook`, `bookmaker`, `bandar (bola|togel|judi|slot|ceme|poker|sabung)`,
		`kasino`, `casino`, `togel`, `toto ?(gelap|macau|sgp|hk|sdy)`, `situs toto`,
		`slot (gacor|online|maxwin|deposit|88|777|thailand|server)`, `(judi|situs|agen|link|daftar) slot`, `slot\d+`, `(situs|link|akun|bocoran) gacor`, `maxwin`, `scatter hitam`,
		`rtp (slot|live|gacor)`, `poker online`, `domino ?qq`, `bandarq`, `ceme online`, `capsa susun`,
		`roulette`, `rolet`, `baccarat`, `bakarat`, `sabung ayam online`, `sv388`, `parlay`, `mix parlay`,
		`deposit (pulsa|dana|ovo|gopay) (slot|judi|togel)`,
	}, "|") + `)\b`)},
	{TopicAdult, regexp.MustCompile(`\b(` + strings.Join([]string{
		`porn`, `porno`, `porn[a-z]*`, `pornografi`, `bokep`, `bkp ?indo`, `hentai`, `(video|film|situs|web) xxx`, `nsfw`, `jav`,
		`sex`, `seks`, `ngewe`, `ngentot`, `bugil`, `mesum`, `cabul`, `onlyfans`, `open ?bo`, `vcs`,
		`(video|film|konten|situs|web|website|cerita|foto) (dewasa|panas|syur|18\+?)`, `cerita sex`, `cerita seks`, `camgirl`, `webcam sex`,
	}, "|") + `)\b`)},
}

// leetLetters turns the digits people use to hide a word back into letters
// ("p0rn0", "jud1", "sl0t g4c0r").
var leetLetters = strings.NewReplacer("0", "o", "1", "i", "3", "e", "4", "a", "5", "s", "7", "t", "@", "a", "$", "s")

var (
	notLetter    = regexp.MustCompile(`[^a-z0-9+\s]+`)
	spacedLetter = regexp.MustCompile(`\b([a-z]) (?:[a-z] )+[a-z]\b`)
	manySpaces   = regexp.MustCompile(`\s+`)
)

// normalizeForCheck lowers the text, folds accents, drops punctuation between letters
// ("p.o.r.n", "ju-di") and joins letters written apart ("j u d i").
func normalizeForCheck(text string) string {
	text = strings.ToLower(asciiFold(text))
	text = notLetter.ReplaceAllString(text, " ")
	text = manySpaces.ReplaceAllString(text, " ")
	text = spacedLetter.ReplaceAllStringFunc(text, func(run string) string { return strings.ReplaceAll(run, " ", "") })
	return strings.TrimSpace(text)
}

// ForbiddenTopic returns the forbidden topic a text is about, or "" when none. It
// looks at the text as written and with digits read as letters, so "slot88" and
// "p0rn0" are both caught.
func ForbiddenTopic(texts ...string) string {
	plain := normalizeForCheck(strings.Join(texts, "\n"))
	if plain == "" {
		return ""
	}
	// "slot88" keeps its digits; "p0rn0" needs them read as letters.
	for _, version := range []string{plain, leetLetters.Replace(plain)} {
		for _, rule := range forbiddenWords {
			if rule.words.MatchString(version) {
				return rule.topic
			}
		}
	}
	return ""
}
