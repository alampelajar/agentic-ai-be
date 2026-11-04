package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The failure seen in the field: a small model answers a "write two files" task with
// a tool call whose arguments are cut off. Before, /api/chat answered 502 and the
// whole turn was lost. Now the model is told what went wrong and tries again.
func TestChatSurvivesAnUnusableToolCallFromTheModel(t *testing.T) {
	f := newToolFixture(t)
	t.Setenv("CLOUD_EXECUTION_ENABLED", "true")
	chat := func() (int, map[string]any) {
		response := toolRequest(t, http.MethodPost, "/api/chat", f.owner.ID, map[string]any{
			"message": "buat style.css", "agent_id": f.agents["coding"].ID, "model_id": f.model.ID, "workspace_id": f.ws.ID,
		})
		var body map[string]any
		_ = json.Unmarshal(response.Body.Bytes(), &body)
		return response.Code, body
	}

	recovering := newScriptedModel(t,
		toolReply("length", "workspace.write_file", `{"path":"style.css","content":"body { color: re`),
		toolReply("stop", "workspace.write_file", goodWrite),
	)
	f.provider.Config.Handler = recovering.server.Config.Handler
	code, body := chat()
	message, _ := body["message"].(string)
	if code != http.StatusOK || !strings.Contains(message, "<AGENT_TOOL>") || !strings.Contains(message, "workspace.write_file") || !strings.Contains(message, "style.css") {
		t.Fatalf("chat = %d %v", code, body)
	}
	if len(recovering.requests) != 2 {
		t.Fatalf("%d provider requests, want 2", len(recovering.requests))
	}
	// The retry still offers the tools and still carries the system prompt.
	second := recovering.requests[1]
	if len(second.Tools) == 0 || second.Messages[0].Role != "system" || !strings.Contains(lastUserMessage(second), "terpotong") {
		t.Fatalf("the retry lost something: tools=%d first=%s last=%s", len(second.Tools), second.Messages[0].Role, lastUserMessage(second))
	}

	// A model that never produces a usable call still ends the turn, with the reason,
	// after a bounded number of requests.
	hopeless := newScriptedModel(t, toolReply("stop", "workspace.write_file", `{`), toolReply("stop", "workspace.write_file", `{`), toolReply("stop", "workspace.write_file", `{`), toolReply("stop", "workspace.write_file", goodWrite))
	f.provider.Config.Handler = hopeless.server.Config.Handler
	code, body = chat()
	if code != http.StatusBadGateway || !strings.Contains(body["detail"].(string), "arguments invalid") {
		t.Fatalf("chat = %d %v", code, body)
	}
	if len(hopeless.requests) != 1+maxToolCallRepairs {
		t.Fatalf("%d provider requests for a hopeless model", len(hopeless.requests))
	}
}
