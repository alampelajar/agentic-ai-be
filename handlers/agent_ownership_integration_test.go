package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Another user's custom agent (agents.user_id = that user) must not be usable
// from tasks or the model/agent endpoints. Global agents (user_id IS NULL) and
// the caller's own agents stay usable. Needs SKILLS_TEST_DATABASE_URL.
func TestAgentReferencesRejectOtherUsersAgents(t *testing.T) {
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
	caller := models.User{Name: "Caller", Email: "ownership-caller-" + suffix + "@example.test"}
	owner := models.User{Name: "Owner", Email: "ownership-owner-" + suffix + "@example.test"}
	for _, u := range []*models.User{&caller, &owner} {
		if err := db.Create(u).Error; err != nil {
			t.Fatal(err)
		}
	}
	global := models.Agent{Slug: "own-global-" + suffix, Name: "Global", IsActive: true}
	callerAgent := models.Agent{Slug: "own-caller-" + suffix, Name: "Caller agent", IsActive: true, UserID: &caller.ID}
	ownerAgent := models.Agent{Slug: "own-owner-" + suffix, Name: "Owner agent", IsActive: true, UserID: &owner.ID}
	for _, a := range []*models.Agent{&global, &callerAgent, &ownerAgent} {
		if err := db.Create(a).Error; err != nil {
			t.Fatal(err)
		}
	}
	provider := models.AIProvider{UserID: &caller.ID, Name: "P", Slug: "own-provider-" + suffix, BaseURL: "https://example.test", Type: "custom", IsActive: true}
	if err := db.Create(&provider).Error; err != nil {
		t.Fatal(err)
	}
	callerModel := models.AIModel{ProviderID: provider.ID, UserID: &caller.ID, Name: "M", ModelID: "m-" + suffix, IsActive: true}
	if err := db.Create(&callerModel).Error; err != nil {
		t.Fatal(err)
	}
	link := models.AgentModel{AgentID: global.ID, AIModelID: callerModel.ID}
	if err := db.Create(&link).Error; err != nil {
		t.Fatal(err)
	}
	defer func() {
		db.Where("user_id IN ?", []uint{caller.ID, owner.ID}).Delete(&models.Task{})
		db.Where("ai_model_id IN (?)", db.Model(&models.AIModel{}).Select("id").Where("user_id = ?", caller.ID)).Delete(&models.AgentModel{})
		db.Where("user_id = ?", caller.ID).Delete(&models.AIModel{})
		db.Where("user_id = ?", caller.ID).Delete(&models.AIProvider{})
		db.Delete(&global)
		db.Delete(&callerAgent)
		db.Delete(&ownerAgent)
		db.Delete(&caller)
		db.Delete(&owner)
	}()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", caller.ID)
		c.Next()
	})
	router.POST("/api/tasks", CreateTask)
	router.PATCH("/api/tasks/:id", UpdateTask)
	router.POST("/api/ai/models", CreateAIModel)
	router.PUT("/api/ai/models/:id", UpdateAIModel)
	router.DELETE("/api/ai/models/:id/agents", RemoveModelFromAgent)
	send := func(method, path, body string) *httptest.ResponseRecorder {
		req, _ := http.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res
	}
	id := func(v uint) string { return strconv.FormatUint(uint64(v), 10) }
	expect := func(label string, res *httptest.ResponseRecorder, want int) {
		t.Helper()
		if res.Code != want {
			t.Fatalf("%s: status=%d want %d body=%s", label, res.Code, want, res.Body.String())
		}
	}

	// Tasks: create.
	expect("task with other user's agent", send("POST", "/api/tasks", `{"title":"t","agent_id":`+id(ownerAgent.ID)+`}`), http.StatusBadRequest)
	expect("task with global agent", send("POST", "/api/tasks", `{"title":"t","agent_id":`+id(global.ID)+`}`), http.StatusCreated)
	expect("task with own agent", send("POST", "/api/tasks", `{"title":"t","agent_id":`+id(callerAgent.ID)+`}`), http.StatusCreated)
	expect("task without agent", send("POST", "/api/tasks", `{"title":"t"}`), http.StatusCreated)

	// Tasks: update.
	task := models.Task{UserID: caller.ID, Title: "update me", Status: "todo", Label: "feature", Priority: "medium"}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	expect("update task to other user's agent", send("PATCH", "/api/tasks/"+id(task.ID), `{"agent_id":`+id(ownerAgent.ID)+`}`), http.StatusBadRequest)
	expect("update task to global agent", send("PATCH", "/api/tasks/"+id(task.ID), `{"agent_id":`+id(global.ID)+`}`), http.StatusOK)

	// Models: create must be refused before any provider is created.
	var providersBefore, modelsBefore int64
	db.Model(&models.AIProvider{}).Where("user_id = ?", caller.ID).Count(&providersBefore)
	db.Model(&models.AIModel{}).Where("user_id = ?", caller.ID).Count(&modelsBefore)
	expect("create model on other user's agent", send("POST", "/api/ai/models",
		`{"provider_name":"New Provider","provider_type":"custom","base_url":"https://example.test","api_key":"x","name":"N","model_id":"n-`+suffix+`","agent_ids":[`+id(ownerAgent.ID)+`]}`), http.StatusNotFound)
	var providersAfter, modelsAfter int64
	db.Model(&models.AIProvider{}).Where("user_id = ?", caller.ID).Count(&providersAfter)
	db.Model(&models.AIModel{}).Where("user_id = ?", caller.ID).Count(&modelsAfter)
	if providersAfter != providersBefore || modelsAfter != modelsBefore {
		t.Fatalf("refused request left side effects: providers %d->%d models %d->%d", providersBefore, providersAfter, modelsBefore, modelsAfter)
	}
	var linksToOwner int64
	db.Model(&models.AgentModel{}).Where("agent_id = ?", ownerAgent.ID).Count(&linksToOwner)
	if linksToOwner != 0 {
		t.Fatalf("a model was linked to another user's agent")
	}

	// Models: move to another user's agent must fail and keep the existing link.
	expect("move model to other user's agent", send("PUT", "/api/ai/models/"+id(callerModel.ID), `{"agent_ids":[`+id(ownerAgent.ID)+`]}`), http.StatusNotFound)
	var stillLinked int64
	db.Model(&models.AgentModel{}).Where("ai_model_id = ? AND agent_id = ?", callerModel.ID, global.ID).Count(&stillLinked)
	db.Model(&models.AgentModel{}).Where("agent_id = ?", ownerAgent.ID).Count(&linksToOwner)
	if stillLinked != 1 || linksToOwner != 0 {
		t.Fatalf("failed move changed links: stillLinked=%d linksToOwner=%d", stillLinked, linksToOwner)
	}

	// Models: removing a relation through another user's agent is refused; the global one works.
	expect("remove model via other user's agent", send("DELETE", "/api/ai/models/"+id(callerModel.ID)+"/agents", `{"agent_id":`+id(ownerAgent.ID)+`}`), http.StatusNotFound)
	expect("remove model via global agent", send("DELETE", "/api/ai/models/"+id(callerModel.ID)+"/agents", `{"agent_id":`+id(global.ID)+`}`), http.StatusOK)
}
