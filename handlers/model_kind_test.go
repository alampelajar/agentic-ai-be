package handlers

import "testing"

func TestNotChatModelError(t *testing.T) {
	for _, id := range []string{"nvidia/nemotron-3.5-embed-1b:free", "text-embedding-3-small", "cohere/Rerank-v3.5"} {
		if notChatModelError(id) == "" {
			t.Errorf("%s is not a chat model", id)
		}
	}
	for _, id := range []string{"google/gemini-2.5-flash", "recursive/flint-v1.0:free", "gpt-5.6"} {
		if msg := notChatModelError(id); msg != "" {
			t.Errorf("%s is a chat model, got %q", id, msg)
		}
	}
}
