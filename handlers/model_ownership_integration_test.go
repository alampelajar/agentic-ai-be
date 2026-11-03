package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// A user must not be able to reference or list another user's custom AI model
// from tasks, task messages or the model lists. System models and the user's
// own models stay usable. Needs SKILLS_TEST_DATABASE_URL.
func TestModelReferencesRespectOwnership(t *testing.T) {
	dsn := os.Getenv("SKILLS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SKILLS_TEST_DATABASE_URL is not configured")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	config.DB = db
	if err := db.AutoMigrate(&models.User{}, &models.Agent{}, &models.AIProvider{}, &models.AIModel{}, &models.AgentModel{}, &models.Task{}, &models.TaskMessage{}); err != nil {
		t.Fatal(err)
	}

	suffix := strconv.Itoa(os.Getpid())
	caller := models.User{Name: "Caller", Email: "model-caller-" + suffix + "@example.test"}
	owner := models.User{Name: "Owner", Email: "model-owner-" + suffix + "@example.test"}
	for _, u := range []*models.User{&caller, &owner} {
		if err := db.Create(u).Error; err != nil {
			t.Fatal(err)
		}
	}
	agent := models.Agent{Slug: "model-agent-" + suffix, Name: "Shared agent", IsActive: true}
	if err := db.Create(&agent).Error; err != nil {
		t.Fatal(err)
	}
	newProvider := func(name string, userID *uint, system bool) models.AIProvider {
		p := models.AIProvider{UserID: userID, Name: name, Slug: "model-" + name + "-" + suffix, BaseURL: "https://example.test", Type: "custom", IsSystem: system, IsActive: true}
		if err := db.Create(&p).Error; err != nil {
			t.Fatal(err)
		}
		return p
	}
	newModel := func(name string, provider models.AIProvider, userID *uint, system bool) models.AIModel {
		m := models.AIModel{ProviderID: provider.ID, UserID: userID, Name: name, ModelID: name, IsSystem: system, IsActive: true}
		if err := db.Create(&m).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&models.AgentModel{AgentID: agent.ID, AIModelID: m.ID}).Error; err != nil {
			t.Fatal(err)
		}
		return m
	}
	systemProvider := newProvider("system", nil, true)
	callerProvider := newProvider("caller", &caller.ID, false)
	ownerProvider := newProvider("owner", &owner.ID, false)
	systemModel := newModel("SYSTEM-MODEL-"+suffix, systemProvider, nil, true)
	callerModel := newModel("CALLER-MODEL-"+suffix, callerProvider, &caller.ID, false)
	ownerModel := newModel("OWNER-ONLY-MODEL-"+suffix, ownerProvider, &owner.ID, false)
	defer func() {
		ids := []uint{systemModel.ID, callerModel.ID, ownerModel.ID}
		db.Where("task_id IN (?)", db.Model(&models.Task{}).Select("id").Where("user_id IN ?", []uint{caller.ID, owner.ID})).Delete(&models.TaskMessage{})
		db.Where("user_id IN ?", []uint{caller.ID, owner.ID}).Delete(&models.Task{})
		db.Where("ai_model_id IN ?", ids).Delete(&models.AgentModel{})
		db.Unscoped().Where("id IN ?", ids).Delete(&models.AIModel{})
		db.Unscoped().Where("id IN ?", []uint{systemProvider.ID, callerProvider.ID, ownerProvider.ID}).Delete(&models.AIProvider{})
		db.Delete(&agent)
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
	router.POST("/api/tasks/:id/messages", AddTaskMessage)
	router.GET("/api/ai/models", GetAIModels)
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
	missing := send("POST", "/api/tasks", `{"title":"t","model_id":999999999}`)
	expect("task with a model that does not exist", missing, http.StatusBadRequest)
	others := send("POST", "/api/tasks", `{"title":"t","model_id":`+id(ownerModel.ID)+`}`)
	expect("task with another user's model", others, http.StatusBadRequest)
	if others.Body.String() != missing.Body.String() {
		t.Fatalf("response reveals whether a model id exists:\n missing: %s\n others:  %s", missing.Body.String(), others.Body.String())
	}
	expect("task with own model", send("POST", "/api/tasks", `{"title":"t","model_id":`+id(callerModel.ID)+`}`), http.StatusCreated)
	expect("task with system model", send("POST", "/api/tasks", `{"title":"t","model_id":`+id(systemModel.ID)+`}`), http.StatusCreated)
	expect("task without model", send("POST", "/api/tasks", `{"title":"t"}`), http.StatusCreated)

	// Tasks: update keeps the previous model when the new one is refused.
	task := models.Task{UserID: caller.ID, Title: "update me", Status: "todo", Label: "feature", Priority: "medium", ModelID: &callerModel.ID}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	expect("update task to another user's model", send("PATCH", "/api/tasks/"+id(task.ID), `{"model_id":`+id(ownerModel.ID)+`}`), http.StatusBadRequest)
	var reloaded models.Task
	db.First(&reloaded, task.ID)
	if reloaded.ModelID == nil || *reloaded.ModelID != callerModel.ID {
		t.Fatalf("refused update changed the task model: %v", reloaded.ModelID)
	}
	expect("update task to system model", send("PATCH", "/api/tasks/"+id(task.ID), `{"model_id":`+id(systemModel.ID)+`}`), http.StatusOK)

	// Task messages.
	path := "/api/tasks/" + id(task.ID) + "/messages"
	expect("message with another user's model", send("POST", path, `{"role":"assistant","content":"hi","model_id":`+id(ownerModel.ID)+`}`), http.StatusBadRequest)
	expect("message with own model", send("POST", path, `{"role":"assistant","content":"hi","model_id":`+id(callerModel.ID)+`}`), http.StatusCreated)
	expect("message without model", send("POST", path, `{"role":"user","content":"hi"}`), http.StatusCreated)
	var foreign int64
	db.Model(&models.TaskMessage{}).Where("model_id = ?", ownerModel.ID).Count(&foreign)
	if foreign != 0 {
		t.Fatalf("a message was stored with another user's model")
	}

	// The model list never contains another user's model.
	list := send("GET", "/api/ai/models", "")
	expect("model list", list, http.StatusOK)
	if strings.Contains(list.Body.String(), ownerModel.Name) {
		t.Fatalf("model list leaks another user's model: %s", list.Body.String())
	}
	if !strings.Contains(list.Body.String(), callerModel.Name) || !strings.Contains(list.Body.String(), systemModel.Name) {
		t.Fatalf("model list is missing the user's own or system model: %s", list.Body.String())
	}
	available, err := getAvailableModelsForAgent(caller.ID, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range available {
		if m.Name == ownerModel.Name {
			t.Fatalf("agent model list leaks another user's model")
		}
	}
	if len(available) != 2 {
		t.Fatalf("agent model list has %d models, want the system and own model", len(available))
	}
}
