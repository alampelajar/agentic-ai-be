package handlers

import (
	"strings"
	"testing"

	"agentic-ai-backend/models"
)

func TestBuildAgentSystemPromptUsesSpecializedRole(t *testing.T) {
	tests := []struct {
		name       string
		slug       string
		wantPhrase string
	}{
		{name: "coding", slug: "coding", wantPhrase: "ROLE: CODING AGENT"},
		{name: "testing", slug: "testing", wantPhrase: "ROLE: TESTING AGENT"},
		{name: "review", slug: "review", wantPhrase: "ROLE: CODE REVIEW AGENT"},
		{name: "planning", slug: "planning", wantPhrase: "ROLE: PLANNING AGENT"},
		{name: "debugging", slug: "debugging", wantPhrase: "ROLE: DEBUGGING AGENT"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompt := buildAgentSystemPrompt(&models.Agent{Slug: tt.slug})
			if !strings.Contains(prompt, tt.wantPhrase) {
				t.Fatalf("prompt for %q does not contain %q", tt.slug, tt.wantPhrase)
			}
		})
	}
}

func TestBuildAgentSystemPromptFallsBackForUnknownAgent(t *testing.T) {
	prompt := buildAgentSystemPrompt(&models.Agent{Slug: "custom-agent"})
	if prompt != baseAgentSystemPrompt {
		t.Fatal("unknown agent should use the generic system prompt")
	}
}

func TestBuildAgentSystemPromptHandlesNilAgent(t *testing.T) {
	prompt := buildAgentSystemPrompt(nil)
	if prompt != baseAgentSystemPrompt {
		t.Fatal("nil agent should use the generic system prompt")
	}
}
