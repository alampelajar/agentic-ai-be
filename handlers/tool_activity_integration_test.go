package handlers

import (
	"bytes"
	"context"
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

func TestToolActivityOwnershipAndRedaction(t *testing.T) {
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
	suffix := strconv.FormatInt(int64(os.Getpid()), 10)
	user := models.User{Name: "Activity User", Email: "activity-" + suffix + "@example.test"}
	other := models.User{Name: "Other Activity User", Email: "activity-other-" + suffix + "@example.test"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	agent := models.Agent{Slug: "activity-agent-" + suffix, Name: "Activity Agent", IsActive: true}
	if err := db.Create(&agent).Error; err != nil {
		t.Fatal(err)
	}
	defer func() {
		db.Where("user_id IN ?", []uint{user.ID, other.ID}).Delete(&models.ToolActivity{})
		db.Delete(&agent)
		db.Delete(&user)
		db.Delete(&other)
	}()

	body := bytes.NewBufferString(`{"agent_id":` + strconv.FormatUint(uint64(agent.ID), 10) + `,"tool_name":"desktop.type","arguments":{"password":"do-not-store","otp":"123456","text":"Hello"},"approval_status":"approved_once","execution_status":"failed","error":"sidecar failed"}`)
	req := httptestRequest(http.MethodPost, "/api/tool-activities", body, user.ID)
	response := serveActivity(req)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
	}
	otherResponse := serveActivity(httptestRequest(http.MethodGet, "/api/tool-activities", nil, other.ID))
	if !bytes.Contains(otherResponse.Body.Bytes(), []byte(`"activities":[]`)) {
		t.Fatalf("activity leaked: %s", otherResponse.Body.String())
	}
	owned := serveActivity(httptestRequest(http.MethodGet, "/api/tool-activities", nil, user.ID))
	if bytes.Contains(owned.Body.Bytes(), []byte("do-not-store")) || bytes.Contains(owned.Body.Bytes(), []byte("123456")) {
		t.Fatalf("sensitive activity data leaked: %s", owned.Body.String())
	}
}

func httptestRequest(method, path string, body *bytes.Buffer, userID uint) *http.Request {
	if body == nil {
		body = bytes.NewBuffer(nil)
	}
	req, _ := http.NewRequest(method, path, body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-User", strconv.FormatUint(uint64(userID), 10))
	req = req.WithContext(context.Background())
	return req
}
func serveActivity(req *http.Request) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		id, _ := strconv.ParseUint(req.Header.Get("X-Test-User"), 10, 64)
		c.Set("user_id", uint(id))
		c.Next()
	})
	router.POST("/api/tool-activities", CreateToolActivity)
	router.GET("/api/tool-activities", GetToolActivities)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	return res
}
