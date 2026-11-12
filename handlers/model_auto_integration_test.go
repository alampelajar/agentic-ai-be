package handlers

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"agentic-ai-backend/models"
)

func autoChat(t *testing.T, router *gin.Engine, agentID uint, query string, fields map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("message", "Apa itu portofolio?")
	_ = writer.WriteField("agent_id", strconv.Itoa(int(agentID)))
	_ = writer.WriteField("auto_model", "1")
	for key, value := range fields {
		_ = writer.WriteField(key, value)
	}
	writer.Close()
	request := httptest.NewRequest(http.MethodPost, "/api/chat"+query, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestAutoChoosesAModelAndMovesOnWhenItFails(t *testing.T) {
	f := newComboFixture(t, nil)
	// The agent may use these models (the combo fixture does not link them).
	for _, model := range []models.AIModel{f.system, f.mine[0], f.mine[1], f.foreign} {
		if err := f.db.Create(&models.AgentModel{AgentID: f.agent.ID, AIModelID: model.ID}).Error; err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { f.db.Where("agent_id = ?", f.agent.ID).Delete(&models.AgentModel{}) })
	// No marker in these names, so the order is the query's: system first, then the user's own.
	f.provider.failFor = map[string]int{f.system.ModelID: http.StatusServiceUnavailable}
	comboHealth.Success(f.system.ID)
	comboHealth.Success(f.mine[0].ID)

	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("user_id", f.user.ID); c.Set("userID", f.user.ID); c.Next() })
	router.POST("/api/chat", Chat)
	router.GET("/api/ai/models/health", GetModelHealth)

	recorder := autoChat(t, router, f.agent.ID, "", nil)
	var reply map[string]any
	_ = json.Unmarshal(recorder.Body.Bytes(), &reply)
	if recorder.Code != http.StatusOK || reply["model"] != f.mine[0].ModelID {
		t.Fatalf("auto = %d %s", recorder.Code, recorder.Body.String())
	}
	auto, _ := reply["auto"].(map[string]any)
	if auto == nil || auto["fallback"] != true || !strings.Contains(auto["reason"].(string), "pertanyaan biasa") {
		t.Fatalf("the reply must say auto chose it, why, and that it fell back: %v", reply["auto"])
	}
	if asked := f.provider.models(); !strings.HasPrefix(asked, f.system.ModelID+","+f.mine[0].ModelID) || strings.Contains(asked, f.foreign.ModelID) {
		t.Fatalf("asked %s: the failing model first, then the next; never another user's model", asked)
	}

	// The same through a stream: the failure before any text moves on, and "done" says so.
	streamed := autoChat(t, router, f.agent.ID, "?stream=1", nil)
	if !strings.Contains(streamed.Body.String(), "event: done") || !strings.Contains(streamed.Body.String(), `"fallback":true`) || strings.Contains(streamed.Body.String(), "event: error") {
		t.Fatalf("stream = %s", streamed.Body.String())
	}

	// Every call was logged, the failures with their status.
	var failures []models.AIModelCallLog
	f.db.Where("ai_model_id = ? AND status = ?", f.system.ID, "failed").Find(&failures)
	if len(failures) < 2 || failures[0].HTTPStatus != http.StatusServiceUnavailable || failures[0].ComboID != 0 {
		t.Fatalf("failures = %+v", failures)
	}

	// The health page counts them.
	health := httptest.NewRecorder()
	router.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/api/ai/models/health", nil))
	var page struct {
		Models []modelHealthRow `json:"models"`
	}
	_ = json.Unmarshal(health.Body.Bytes(), &page)
	rows := map[uint]modelHealthRow{}
	for _, row := range page.Models {
		rows[row.ID] = row
	}
	if system := rows[f.system.ID]; system.Busy < 2 || system.Failed < 2 || system.Status == "healthy" || system.LastStatus != http.StatusServiceUnavailable {
		t.Fatalf("system model health = %+v", system)
	}
	if mine := rows[f.mine[0].ID]; mine.Succeeded < 2 || mine.Status != "healthy" {
		t.Fatalf("own model health = %+v", mine)
	}
	if _, listed := rows[f.foreign.ID]; listed {
		t.Fatal("another user's model must not be listed")
	}

	// A photo with no model that reads images: auto says so instead of guessing.
	if problem := func() string {
		_, _, problem := chooseAutoModels(f.user.ID, f.agent.ID, true, false, false, 10)
		return problem
	}(); !strings.Contains(problem, "membaca gambar") {
		t.Fatalf("problem = %q", problem)
	}
}
