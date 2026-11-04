package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Which model a user can pick for an agent must follow all of: the exact model
// id, the link to that agent, the active flag, and system-or-own. An OR that is
// not parenthesised used to let the user's own models bypass every other
// condition. Needs SKILLS_TEST_DATABASE_URL.
func TestModelSelectionHonoursAgentLinkActiveFlagAndOwnership(t *testing.T) {
	dsn := os.Getenv("SKILLS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SKILLS_TEST_DATABASE_URL is not configured")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	// Close the pool when the test ends: the package opens one per fixture and PostgreSQL has a connection cap.
	t.Cleanup(func() {
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	config.DB = db
	if err := migrateAll(db); err != nil {
		t.Fatal(err)
	}

	suffix := strconv.Itoa(os.Getpid())
	caller := models.User{Name: "Caller", Email: "selection-caller-" + suffix + "@example.test"}
	owner := models.User{Name: "Owner", Email: "selection-owner-" + suffix + "@example.test"}
	for _, u := range []*models.User{&caller, &owner} {
		if err := db.Create(u).Error; err != nil {
			t.Fatal(err)
		}
	}
	newAgent := func(slug string) models.Agent {
		a := models.Agent{Slug: "sel-" + slug + "-" + suffix, Name: slug, IsActive: true}
		if err := db.Create(&a).Error; err != nil {
			t.Fatal(err)
		}
		return a
	}
	agentA, agentB, agentEmpty := newAgent("a"), newAgent("b"), newAgent("empty")
	newProvider := func(name string, userID *uint, system bool) models.AIProvider {
		p := models.AIProvider{UserID: userID, Name: name, Slug: "sel-" + name + "-" + suffix, BaseURL: "https://example.test", Type: "custom", IsSystem: system, IsActive: true}
		if err := db.Create(&p).Error; err != nil {
			t.Fatal(err)
		}
		return p
	}
	systemProvider := newProvider("system", nil, true)
	callerProvider := newProvider("caller", &caller.ID, false)
	ownerProvider := newProvider("owner", &owner.ID, false)
	var created []uint
	newModel := func(name string, provider models.AIProvider, userID *uint, system bool, agent *models.Agent) models.AIModel {
		m := models.AIModel{ProviderID: provider.ID, UserID: userID, Name: name + "-" + suffix, ModelID: name + "-" + suffix, IsSystem: system, IsActive: true}
		if err := db.Create(&m).Error; err != nil {
			t.Fatal(err)
		}
		created = append(created, m.ID)
		if agent != nil {
			if err := db.Create(&models.AgentModel{AgentID: agent.ID, AIModelID: m.ID}).Error; err != nil {
				t.Fatal(err)
			}
		}
		return m
	}
	systemModel := newModel("SYSTEM", systemProvider, nil, true, &agentA)
	callerModel := newModel("CALLER", callerProvider, &caller.ID, false, &agentA)
	callerInactive := newModel("CALLER-INACTIVE", callerProvider, &caller.ID, false, &agentA)
	callerOtherAgent := newModel("CALLER-OTHER-AGENT", callerProvider, &caller.ID, false, &agentB)
	ownerModel := newModel("OWNER-ONLY", ownerProvider, &owner.ID, false, &agentA)
	deletedSystem := newModel("SYSTEM-DELETED", systemProvider, nil, true, &agentA)
	// is_active defaults to true on insert, so switch it off afterwards.
	db.Model(&callerInactive).Update("is_active", false)
	db.Delete(&deletedSystem)
	defer func() {
		db.Where("ai_model_id IN ?", created).Delete(&models.AgentModel{})
		db.Unscoped().Where("id IN ?", created).Delete(&models.AIModel{})
		db.Unscoped().Where("id IN ?", []uint{systemProvider.ID, callerProvider.ID, ownerProvider.ID}).Delete(&models.AIProvider{})
		db.Delete(&agentA)
		db.Delete(&agentB)
		db.Delete(&agentEmpty)
		db.Delete(&caller)
		db.Delete(&owner)
	}()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", caller.ID)
		c.Next()
	})
	router.POST("/api/chat", Chat)
	router.GET("/api/ai/models", GetAIModels)
	id := func(v uint) string { return strconv.FormatUint(uint64(v), 10) }
	chat := func(agent models.Agent, modelID uint) *httptest.ResponseRecorder {
		body := `{"message":"hi","agent_id":` + id(agent.ID) + `,"model_id":` + id(modelID) + `}`
		req, _ := http.NewRequest("POST", "/api/chat", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res
	}
	// A model that is accepted goes on to the provider key check. None of the
	// test providers has a key, so "accepted" shows up as 500 and never reaches
	// a real provider. A refused selection is a 400.
	refused := func(label string, res *httptest.ResponseRecorder) {
		t.Helper()
		if res.Code != http.StatusBadRequest {
			t.Errorf("%s: status=%d, want 400 (refused). body=%s", label, res.Code, res.Body.String())
		}
	}
	accepted := func(label string, res *httptest.ResponseRecorder) {
		t.Helper()
		if res.Code != http.StatusInternalServerError || !strings.Contains(res.Body.String(), "API key provider belum dikonfigurasi") {
			t.Errorf("%s: status=%d, want 500 from the provider key check (accepted). body=%s", label, res.Code, res.Body.String())
		}
	}

	// Chat with an explicit model.
	refused("another user's model", chat(agentA, ownerModel.ID))
	refused("a model id that does not exist", chat(agentA, 999999999))
	refused("own model linked only to a different agent", chat(agentA, callerOtherAgent.ID))
	refused("own model that is switched off", chat(agentA, callerInactive.ID))
	refused("system model that was deleted", chat(agentA, deletedSystem.ID))
	accepted("own model linked to the agent", chat(agentA, callerModel.ID))
	accepted("system model linked to the agent", chat(agentA, systemModel.ID))
	accepted("own model on its own agent", chat(agentB, callerOtherAgent.ID))

	// Chat without a model falls back to a default, which must also be linked to the agent.
	accepted("default model for an agent that has models", chat(agentA, 0))
	accepted("default model for the agent that only has the user's own model", chat(agentB, 0))
	refused("default model for an agent without any linked model", chat(agentEmpty, 0))

	// Model lists.
	names := func(list []availableModelResponse) []string {
		var out []string
		for _, m := range list {
			out = append(out, m.Name)
		}
		sort.Strings(out)
		return out
	}
	forA, err := getAvailableModelsForAgent(caller.ID, agentA.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantA := []string{callerModel.Name, systemModel.Name}
	sort.Strings(wantA)
	if got := names(forA); strings.Join(got, ",") != strings.Join(wantA, ",") {
		t.Errorf("models offered for agent A = %v, want %v", got, wantA)
	}
	forB, err := getAvailableModelsForAgent(caller.ID, agentB.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(forB); len(got) != 1 || got[0] != callerOtherAgent.Name {
		t.Errorf("models offered for agent B = %v, want only %q", got, callerOtherAgent.Name)
	}
	forEmpty, err := getAvailableModelsForAgent(caller.ID, agentEmpty.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(forEmpty) != 0 {
		t.Errorf("an agent without links is offered %v", names(forEmpty))
	}

	req, _ := http.NewRequest("GET", "/api/ai/models", nil)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("model list: status=%d", res.Code)
	}
	body := res.Body.String()
	for _, hidden := range []string{ownerModel.Name, deletedSystem.Name} {
		if strings.Contains(body, hidden) {
			t.Errorf("model list shows %q, which the user must not see", hidden)
		}
	}
	for _, shown := range []string{systemModel.Name, callerModel.Name, callerInactive.Name, callerOtherAgent.Name} {
		if !strings.Contains(body, shown) {
			t.Errorf("model list is missing %q", shown)
		}
	}
}
