package handlers

import "strings"

// notChatModelMarkers are parts of model IDs that name models which cannot hold a conversation:
// embedding and rerank models only turn text into numbers or scores, so an agent using one fails on
// every message.
var notChatModelMarkers = []string{"embed", "rerank"}

// notChatModelError explains why a model ID cannot be added to an agent, or is "" when it can.
func notChatModelError(modelID string) string {
	id := strings.ToLower(modelID)
	for _, marker := range notChatModelMarkers {
		if strings.Contains(id, marker) {
			return "Model " + modelID + " adalah model embedding/rerank, bukan model chat, jadi tidak bisa dipakai oleh agent. Pilih model chat."
		}
	}
	return ""
}
