package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
)

type systemModelFixture struct {
	router *gin.Engine
	agents []models.Agent
	suffix string
}

func newSystemModelFixture(t *testing.T) *systemModelFixture {
	t.Helper()
	dsn := os.Getenv("SKILLS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SKILLS_TEST_DATABASE_URL is not configured")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	config.DB = db
	if err := migrateAll(db); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AI_ENCRYPTION_KEY", strings.Repeat("ab", 32))

	f := &systemModelFixture{suffix: strconv.Itoa(os.Getpid())}
	for _, slug := range []string{"one", "two", "three"} {
		agent := models.Agent{Slug: "sysmodel-" + slug + "-" + f.suffix, Name: "Agent " + slug, IsActive: true}
		if err := db.Create(&agent).Error; err != nil {
			t.Fatal(err)
		}
		f.agents = append(f.agents, agent)
	}
	// An inactive agent must never receive a model, even when "all agents" is chosen.
	inactive := models.Agent{Slug: "sysmodel-off-" + f.suffix, Name: "Agent off"}
	if err := db.Create(&inactive).Error; err != nil {
		t.Fatal(err)
	}
	db.Model(&inactive).Update("is_active", false)
	t.Cleanup(func() {
		ids := []uint{inactive.ID}
		for _, agent := range f.agents {
			ids = append(ids, agent.ID)
		}
		var modelIDs []uint
		db.Unscoped().Model(&models.AIModel{}).Where("model_id LIKE ?", "sysmodel-%-"+f.suffix).Pluck("id", &modelIDs)
		db.Where("ai_model_id IN ? OR agent_id IN ?", modelIDs, ids).Delete(&models.AgentModel{})
		db.Unscoped().Where("id IN ?", modelIDs).Delete(&models.AIModel{})
		db.Unscoped().Where("slug = ?", "sysmodel-provider-"+f.suffix).Delete(&models.AIProvider{})
		db.Unscoped().Where("id IN ?", ids).Delete(&models.Agent{})
	})

	gin.SetMode(gin.TestMode)
	f.router = gin.New()
	f.router.POST("/models", AdminCreateSystemAIModel)
	f.router.PATCH("/models/:id", AdminUpdateSystemAIModel)
	return f
}

func (f *systemModelFixture) send(method, path, body string) (*httptest.ResponseRecorder, models.AIModel) {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, request)
	var out struct {
		Model models.AIModel `json:"model"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out.Model
}

func (f *systemModelFixture) create(extra, name string) (*httptest.ResponseRecorder, models.AIModel) {
	body := `{"provider_name":"sysmodel-provider-` + f.suffix + `","provider_type":"openrouter","base_url":"https://openrouter.ai/api/v1",` +
		`"api_key":"test-key","name":"` + name + `","model_id":"sysmodel-` + name + `-` + f.suffix + `"` + extra + `}`
	return f.send(http.MethodPost, "/models", body)
}

// linkedAgents lists which of this fixture's agents a model is linked to.
func (f *systemModelFixture) linkedAgents(t *testing.T, modelID uint) []uint {
	t.Helper()
	var all []uint
	config.DB.Model(&models.AgentModel{}).Where("ai_model_id = ?", modelID).Order("agent_id").Pluck("agent_id", &all)
	return all
}

func TestSystemModelCanGoToOneAgentOrAllOfThem(t *testing.T) {
	f := newSystemModelFixture(t)

	// One agent, as before.
	rec, one := f.create(`,"agent_id":`+strconv.Itoa(int(f.agents[0].ID)), "single")
	if rec.Code != http.StatusCreated {
		t.Fatalf("create for one agent: %d %s", rec.Code, rec.Body.String())
	}
	if got := f.linkedAgents(t, one.ID); len(got) != 1 || got[0] != f.agents[0].ID {
		t.Fatalf("one-agent model linked to %v, want only %d", got, f.agents[0].ID)
	}

	// Every active agent, and not the inactive one.
	rec, all := f.create(`,"all_agents":true`, "everyone")
	if rec.Code != http.StatusCreated {
		t.Fatalf("create for all agents: %d %s", rec.Code, rec.Body.String())
	}
	var active int64
	config.DB.Model(&models.Agent{}).Where("is_active = ?", true).Count(&active)
	got := f.linkedAgents(t, all.ID)
	if int64(len(got)) != active {
		t.Fatalf("all-agents model linked to %d agents, want every active one (%d)", len(got), active)
	}
	for _, agent := range f.agents {
		found := false
		for _, id := range got {
			found = found || id == agent.ID
		}
		if !found {
			t.Fatalf("agent %d is missing from %v", agent.ID, got)
		}
	}

	// Neither an agent nor "all": refused.
	if rec, _ := f.create("", "nobody"); rec.Code != http.StatusBadRequest {
		t.Fatalf("no agent chosen: %d, want 400", rec.Code)
	}

	// Editing: one agent -> everyone -> back to one, replacing the old links each time.
	path := "/models/" + strconv.Itoa(int(one.ID))
	if rec, _ := f.send(http.MethodPatch, path, `{"all_agents":true}`); rec.Code != http.StatusOK {
		t.Fatalf("update to all: %d %s", rec.Code, rec.Body.String())
	}
	if got := f.linkedAgents(t, one.ID); int64(len(got)) != active {
		t.Fatalf("after update to all: %d links, want %d", len(got), active)
	}
	if rec, _ := f.send(http.MethodPatch, path, `{"agent_id":`+strconv.Itoa(int(f.agents[1].ID))+`}`); rec.Code != http.StatusOK {
		t.Fatalf("update back to one: %d %s", rec.Code, rec.Body.String())
	}
	if got := f.linkedAgents(t, one.ID); len(got) != 1 || got[0] != f.agents[1].ID {
		t.Fatalf("after update to one: linked to %v, want only %d", got, f.agents[1].ID)
	}
}
