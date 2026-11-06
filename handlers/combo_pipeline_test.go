package handlers

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"agentic-ai-backend/models"
)

func TestComboPipelineLetsEveryModelWorkAndOnlyTheLastOneUseTools(t *testing.T) {
	f := newComboFixture(t, nil)
	status, resp := f.createCombo(t, f.user.ID, map[string]any{"name": "Berantai", "strategy": "pipeline", "agent_ids": []uint{f.agent.ID},
		"members": []map[string]any{{"ai_model_id": f.mine[0].ID}, {"ai_model_id": f.mine[1].ID}}})
	if status != http.StatusCreated {
		t.Fatalf("create: %d %v", status, resp)
	}
	virtual := f.virtualModel(t, resp)

	request := chatRequestFor("ignored")
	request.ToolChoice = "auto"
	request.Tools = []providerToolDefinition{{Type: "function"}}
	answer, used, trace, runErr := runComboRequest(f.user.ID, 0, virtual, request)
	if runErr != nil {
		t.Fatalf("runErr = %+v", runErr)
	}
	if f.provider.models() != "mine-a,mine-b" || used.ID != f.mine[1].ID || !strings.Contains(answer, "mine-b") {
		t.Fatalf("both models must be asked in order and the last answers: %q used %d", f.provider.models(), used.ID)
	}
	if strings.Join(f.provider.withTools, ",") != "mine-b" {
		t.Fatalf("only the last model may get tools, got %v", f.provider.withTools)
	}
	if len(trace) != 2 || trace[0].Stage != "draft" || trace[1].Stage != "final" {
		t.Fatalf("trace = %+v", trace)
	}
	lastMessages := f.provider.last["mine-b"]
	if len(lastMessages) != 2 || !strings.Contains(lastMessages[1].Content.(string), "jawaban dari mine-a") {
		t.Fatalf("the last model must see the first model's draft, got %+v", lastMessages)
	}
	var logs int64
	f.db.Model(&models.AIModelCallLog{}).Where("user_id = ?", f.user.ID).Count(&logs)
	if logs != 2 {
		t.Fatalf("every stage must be logged, got %d", logs)
	}
}

func TestComboPipelineNeedsTwoModelsAndFailsOnlyWhenTheLastOneFails(t *testing.T) {
	f := newComboFixture(t, map[string]int{"mine-a": http.StatusBadGateway})
	status, _ := f.createCombo(t, f.user.ID, map[string]any{"name": "Sendiri", "strategy": "pipeline",
		"members": []map[string]any{{"ai_model_id": f.mine[0].ID}}})
	if status != http.StatusBadRequest {
		t.Fatalf("a pipeline with one model must be refused, got %d", status)
	}
	_, resp := f.createCombo(t, f.user.ID, map[string]any{"name": "Berdua", "strategy": "pipeline", "agent_ids": []uint{f.agent.ID},
		"members": []map[string]any{{"ai_model_id": f.mine[0].ID}, {"ai_model_id": f.mine[1].ID}}})
	virtual := f.virtualModel(t, resp)
	comboHealth.Success(f.mine[0].ID)

	answer, used, trace, runErr := runComboRequest(f.user.ID, 0, virtual, chatRequestFor("x"))
	if runErr != nil || used.ID != f.mine[1].ID || !strings.Contains(answer, "mine-b") {
		t.Fatalf("a failing draft model must not stop the answer: %q %+v", answer, runErr)
	}
	if len(trace) != 2 || trace[0].Status != "failed" || trace[1].Status != "ok" {
		t.Fatalf("trace = %+v", trace)
	}

	// Last model down: the request fails.
	f2 := newComboFixture(t, map[string]int{"mine-b": http.StatusBadGateway})
	_, resp2 := f2.createCombo(t, f2.user.ID, map[string]any{"name": "Rusak", "strategy": "pipeline", "agent_ids": []uint{f2.agent.ID},
		"members": []map[string]any{{"ai_model_id": f2.mine[0].ID}, {"ai_model_id": f2.mine[1].ID}}})
	virtual2 := f2.virtualModel(t, resp2)
	if _, _, _, err := runComboRequest(f2.user.ID, 0, virtual2, chatRequestFor("x")); err == nil || err.Status != http.StatusBadGateway {
		t.Fatalf("a failing last model must fail the request, got %+v", err)
	}
}

func TestComboCannotBecomeAPipelineWithOneModel(t *testing.T) {
	f := newComboFixture(t, nil)
	_, resp := f.createCombo(t, f.user.ID, map[string]any{"name": "Biasa", "strategy": "fallback",
		"members": []map[string]any{{"ai_model_id": f.mine[0].ID}}})
	id := strconv.Itoa(int(comboOf(resp)["id"].(float64)))
	status, _ := callAs(t, f.user.ID, http.MethodPut, "/", gin.Params{{Key: "id", Value: id}}, map[string]any{"strategy": "pipeline"}, UpdateCombo)
	if status != http.StatusBadRequest {
		t.Fatalf("switching a one-model combo to pipeline must be refused, got %d", status)
	}
}

func TestPipelineMessagesAddOneInstructionAndKeepTheConversation(t *testing.T) {
	base := []chatMessage{{Role: "system", Content: "s"}, {Role: "user", Content: "tanya"}}
	if got := pipelineMessages(base, true, ""); len(got) != 2 {
		t.Fatalf("a final stage without a draft changes nothing: %+v", got)
	}
	for _, c := range []struct {
		final bool
		draft string
		want  string
	}{{false, "", "draf jawaban terbaik"}, {false, "d1", "draf dari model sebelumnya"}, {true, "d2", "Jangan menyebut draf"}} {
		got := pipelineMessages(base, c.final, c.draft)
		if len(got) != 3 || got[2].Role != "user" || !strings.Contains(got[2].Content.(string), c.want) {
			t.Fatalf("final=%v draft=%q -> %+v", c.final, c.draft, got)
		}
		if c.draft != "" && !strings.Contains(got[2].Content.(string), c.draft) {
			t.Fatalf("the draft must be in the instruction: %+v", got[2])
		}
	}
	if len(base) != 2 {
		t.Fatal("the original messages must not be modified")
	}
}
