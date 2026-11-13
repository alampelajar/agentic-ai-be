package handlers

import (
	"bytes"
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

// A chat straight to a shared system model counts toward the same daily limit as
// combos, and is refused once the limit is reached, before any provider is called.
func TestDirectSystemModelChatsHaveADailyLimit(t *testing.T) {
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
	t.Setenv("COMBO_SYSTEM_DAILY_LIMIT", "2")
	t.Setenv("OPENROUTER_API_KEY", "test-key-not-real")

	suffix := strconv.Itoa(os.Getpid())
	caller := models.User{Name: "Quota", Email: "quota-caller-" + suffix + "@example.test"}
	if err := db.Create(&caller).Error; err != nil {
		t.Fatal(err)
	}
	agent := models.Agent{Slug: "quota-agent-" + suffix, Name: "quota", IsActive: true}
	db.Create(&agent)
	// .test never resolves, so a call that gets through fails at once without leaving the machine.
	provider := models.AIProvider{Name: "quota", Slug: "quota-" + suffix, BaseURL: "https://quota.example.test", Type: "openrouter", IsSystem: true, IsActive: true}
	db.Create(&provider)
	model := models.AIModel{ProviderID: provider.ID, Name: "quota-" + suffix, ModelID: "quota-" + suffix, IsSystem: true, IsActive: true}
	db.Create(&model)
	db.Create(&models.AgentModel{AgentID: agent.ID, AIModelID: model.ID})
	t.Cleanup(func() {
		db.Where("user_id = ?", caller.ID).Delete(&models.AIModelCallLog{})
		db.Where("ai_model_id = ?", model.ID).Delete(&models.AgentModel{})
		db.Unscoped().Delete(&model)
		db.Unscoped().Delete(&provider)
		db.Unscoped().Delete(&agent)
		db.Unscoped().Delete(&caller)
	})

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("user_id", caller.ID); c.Next() })
	router.POST("/api/chat", Chat)
	chat := func() *httptest.ResponseRecorder {
		body := `{"message":"hi","agent_id":` + strconv.Itoa(int(agent.ID)) + `,"model_id":` + strconv.Itoa(int(model.ID)) + `}`
		req, _ := http.NewRequest(http.MethodPost, "/api/chat", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res
	}

	// Under the limit the chat is let through (the unreachable provider then fails) and counted.
	if res := chat(); res.Code == http.StatusTooManyRequests {
		t.Fatalf("the first chat of the day was refused: %s", res.Body.String())
	}
	if got := systemCallsToday(caller.ID); got != 1 {
		t.Fatalf("a direct system chat counted %d times, want 1", got)
	}
	// A combo attempt from earlier today shares the same budget.
	db.Create(&models.AIModelCallLog{UserID: caller.ID, AIModelID: model.ID, IsSystem: true, Attempt: 1, Status: "ok"})
	res := chat()
	if res.Code != http.StatusTooManyRequests || !strings.Contains(res.Body.String(), "system_model_daily_limit") {
		t.Fatalf("over the limit = %d %s, want 429 system_model_daily_limit", res.Code, res.Body.String())
	}
	if got := systemCallsToday(caller.ID); got != 2 {
		t.Fatalf("a refused chat was counted: %d", got)
	}
}
