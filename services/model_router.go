package services

import (
	"sort"
	"strings"
)

// Automatic model choice: when the person picks "Auto", the chat ranks the models the agent may
// use for what this message needs. Models carry no capability fields, so what a model can do is
// read from its ID and name, the way providers name them (…-vl, gemini, flash, coder…).

// TaskNeed is what the message asks of a model.
type TaskNeed string

const (
	NeedVision TaskNeed = "vision" // a photo is attached: only models that read images
	NeedCoding TaskNeed = "coding" // a workspace is open: the agent edits and runs code with tools
	NeedLong   TaskNeed = "long"   // documents or a long message: a strong model with a large context
	NeedFast   TaskNeed = "fast"   // an ordinary question: the quickest good answer
)

// RouteCandidate is a model the agent may use, with what is known about its recent health.
type RouteCandidate struct {
	ModelDBID uint
	ModelID   string // the provider's ID, e.g. nvidia/nemotron-3.5-super-vl:free
	Name      string
	Provider  string
	Resting   bool    // failed several times in a row and is being rested
	FailRate  float64 // share of failed calls in the last hours (0..1), 0 when unknown
}

// RouteChoice is one candidate in the order the chat should try them.
type RouteChoice struct {
	RouteCandidate
	Score  int
	Reason string // why it was chosen first, in words for the person
}

var (
	visionMarkers = []string{"vision", "-vl", "_vl", "vl-", "omni", "gemini", "gpt-4o", "gpt-4.1", "gpt-5", "claude", "pixtral", "llava", "llama-4", "gemma-3", "gemma-4", "inkling", "grok-4", "multimodal"}
	codingMarkers = []string{"coder", "code", "gpt-oss", "ultra", "ernie", "deepseek", "qwen3", "kimi", "glm", "sonnet", "opus", "gpt-5", "devstral", "codestral"}
	strongMarkers = []string{"ultra", "pro", "opus", "sonnet", "gpt-5", "405b", "235b", "550b", "120b", "70b", "large"}
	fastMarkers   = []string{"flash", "lightning", "mini", "nano", "small", "lite", "instant", "haiku", "turbo"}
	tinyMarkers   = []string{"1.7b", "2.6b", "3b-", "1b-", "-1b", "-3b"}
	fastProviders = []string{"groq", "cerebras"}
)

func hasAny(text string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// CanReadImages says whether a model, by its ID or name, takes images.
func CanReadImages(modelID, name string) bool {
	return hasAny(strings.ToLower(modelID+" "+name), visionMarkers)
}

func scoreFor(candidate RouteCandidate, need TaskNeed) int {
	text := strings.ToLower(candidate.ModelID + " " + candidate.Name)
	provider := strings.ToLower(candidate.Provider)
	score := 0
	switch need {
	case NeedVision:
		score += 10 // every candidate here reads images; prefer the strong ones
		if hasAny(text, strongMarkers) {
			score += 2
		}
	case NeedCoding:
		if hasAny(text, codingMarkers) {
			score += 4
		}
		if hasAny(text, strongMarkers) {
			score += 2
		}
		if hasAny(text, fastMarkers) {
			score--
		}
	case NeedLong:
		if hasAny(text, strongMarkers) {
			score += 3
		}
		if hasAny(text, codingMarkers) {
			score++
		}
	case NeedFast:
		if hasAny(text, fastMarkers) || hasAny(provider, fastProviders) {
			score += 4
		}
		if hasAny(text, strongMarkers) && !hasAny(provider, fastProviders) {
			score--
		}
	}
	if hasAny(text, tinyMarkers) && need != NeedFast {
		score -= 3 // very small models write weak code and lose long documents
	}
	// Health: a model that keeps failing goes down the list, a resting one to the very end.
	score -= int(candidate.FailRate * 6)
	if candidate.Resting {
		score -= 100
	}
	return score
}

var needReasons = map[TaskNeed]string{
	NeedVision: "ada gambar, jadi dipilih model yang bisa membaca gambar",
	NeedCoding: "workspace terbuka, jadi dipilih model yang kuat untuk kode dan tool",
	NeedLong:   "ada dokumen atau pesan panjang, jadi dipilih model yang kuat",
	NeedFast:   "pertanyaan biasa, jadi dipilih model yang cepat",
}

// RankModels orders the candidates for the need, best first. For NeedVision, models that cannot
// read images are left out; the result is then empty when none can. Ties keep the given order.
func RankModels(candidates []RouteCandidate, need TaskNeed) []RouteChoice {
	ranked := make([]RouteChoice, 0, len(candidates))
	for _, candidate := range candidates {
		if need == NeedVision && !CanReadImages(candidate.ModelID, candidate.Name) {
			continue
		}
		ranked = append(ranked, RouteChoice{RouteCandidate: candidate, Score: scoreFor(candidate, need)})
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Score > ranked[j].Score })
	for i := range ranked {
		ranked[i].Reason = needReasons[need]
		if ranked[i].Resting {
			ranked[i].Reason += " (model ini sedang diistirahatkan setelah beberapa kali gagal)"
		}
	}
	return ranked
}

// NeedFor decides what a message needs, from what it carries.
func NeedFor(hasImages, workspaceOpen, hasDocuments bool, messageLength int) TaskNeed {
	switch {
	case hasImages:
		return NeedVision
	case workspaceOpen:
		return NeedCoding
	case hasDocuments || messageLength > 2000:
		return NeedLong
	default:
		return NeedFast
	}
}
