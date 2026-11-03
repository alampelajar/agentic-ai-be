package handlers

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
)

func TestSkillsLifecycleAndAgentIsolation(t *testing.T) {
	dsn := os.Getenv("SKILLS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SKILLS_TEST_DATABASE_URL is not configured")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	config.DB = db
	if err := migrateAll(db); err != nil {
		t.Fatal(err)
	}

	suffix := strconv.FormatInt(int64(os.Getpid()), 10)
	user := models.User{Name: "Skill Test User", Email: "skills-" + suffix + "@example.test", Password: "not-used"}
	otherUser := models.User{Name: "Other Skill Test User", Email: "skills-other-" + suffix + "@example.test", Password: "not-used"}
	agent := models.Agent{Slug: "skill-test-" + suffix, Name: "Skill Test Agent", Status: "ready", IsActive: true}
	privateAgent := models.Agent{Slug: "private-skill-test-" + suffix, Name: "Private Skill Test Agent", Status: "ready", IsActive: true, UserID: &user.ID}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&otherUser).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&agent).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&privateAgent).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Where("user_id IN ?", []uint{user.ID, otherUser.ID}).Delete(&models.AgentSkill{})
		db.Where("user_id IN ?", []uint{user.ID, otherUser.ID}).Delete(&models.Skill{})
		db.Delete(&agent)
		db.Delete(&privateAgent)
		db.Delete(&user)
		db.Delete(&otherUser)
	})

	createBody := map[string]any{"manifest": map[string]any{"id": "lifecycle-" + suffix, "name": "Lifecycle Skill", "version": "1.0.0", "description": "Integration skill", "instructions": "SKILL.md", "tools": []string{"workspace.read"}}, "instructions": "# Lifecycle\nRead only."}
	createResponse := performJSON(t, http.MethodPost, "/api/skills", user.ID, createBody)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body=%s", createResponse.Code, createResponse.Body.String())
	}
	var created struct {
		Skill struct {
			ID uint `json:"id"`
		} `json:"skill"`
	}
	decode(t, createResponse, &created)
	if created.Skill.ID == 0 {
		t.Fatal("created skill ID is empty")
	}

	validateResponse := performJSON(t, http.MethodPost, "/api/skills/"+strconv.FormatUint(uint64(created.Skill.ID), 10)+"/validate", user.ID, nil)
	if validateResponse.Code != http.StatusOK {
		t.Fatalf("validate status = %d", validateResponse.Code)
	}

	assignResponse := performJSON(t, http.MethodPost, fmt.Sprintf("/api/agents/%d/skills", agent.ID), user.ID, map[string]any{"skill_id": created.Skill.ID})
	if assignResponse.Code != http.StatusCreated {
		t.Fatalf("assign status = %d, body=%s", assignResponse.Code, assignResponse.Body.String())
	}
	assignedResponse := performJSON(t, http.MethodGet, fmt.Sprintf("/api/agents/%d/skills", agent.ID), user.ID, nil)
	if assignedResponse.Code != http.StatusOK || !bytes.Contains(assignedResponse.Body.Bytes(), []byte("lifecycle-")) {
		t.Fatalf("assignment was not returned: %s", assignedResponse.Body.String())
	}
	otherUserResponse := performJSON(t, http.MethodGet, fmt.Sprintf("/api/agents/%d/skills", agent.ID), otherUser.ID, nil)
	if otherUserResponse.Code != http.StatusOK || bytes.Contains(otherUserResponse.Body.Bytes(), []byte("lifecycle-")) {
		t.Fatalf("assignment leaked across users: %s", otherUserResponse.Body.String())
	}
	privateAgentResponse := performJSON(t, http.MethodGet, fmt.Sprintf("/api/agents/%d/skills", privateAgent.ID), otherUser.ID, nil)
	if privateAgentResponse.Code != http.StatusNotFound {
		t.Fatalf("private agent was accessible by another user: status=%d body=%s", privateAgentResponse.Code, privateAgentResponse.Body.String())
	}
	privateAssignResponse := performJSON(t, http.MethodPost, fmt.Sprintf("/api/agents/%d/skills", privateAgent.ID), otherUser.ID, map[string]any{"skill_id": created.Skill.ID})
	if privateAssignResponse.Code != http.StatusNotFound {
		t.Fatalf("private agent accepted assignment from another user: status=%d body=%s", privateAssignResponse.Code, privateAssignResponse.Body.String())
	}

	skillID := strconv.FormatUint(uint64(created.Skill.ID), 10)
	if response := performJSON(t, http.MethodPatch, fmt.Sprintf("/api/agents/%d/skills/%s", agent.ID, skillID), user.ID, map[string]any{"is_enabled": false}); response.Code != http.StatusOK {
		t.Fatalf("disable status = %d", response.Code)
	}
	if response := performJSON(t, http.MethodPatch, fmt.Sprintf("/api/agents/%d/skills/%s", agent.ID, skillID), user.ID, map[string]any{"is_enabled": true}); response.Code != http.StatusOK {
		t.Fatalf("enable status = %d", response.Code)
	}
	if response := performJSON(t, http.MethodDelete, "/api/skills/"+skillID, user.ID, nil); response.Code != http.StatusOK {
		t.Fatalf("delete status = %d", response.Code)
	}
	if response := performJSON(t, http.MethodGet, "/api/skills", user.ID, nil); bytes.Contains(response.Body.Bytes(), []byte("lifecycle-")) {
		t.Fatalf("deleted skill still listed: %s", response.Body.String())
	}

	zipBody, contentType := skillZipMultipart(t)
	importRequest := httptest.NewRequest(http.MethodPost, "/api/skills/import/zip", zipBody)
	importRequest.Header.Set("Content-Type", contentType)
	importResponse := performRequest(t, importRequest, user.ID)
	if importResponse.Code != http.StatusCreated {
		t.Fatalf("ZIP import status = %d, body=%s", importResponse.Code, importResponse.Body.String())
	}
}

func performJSON(t *testing.T, method, path string, userID uint, body any) *httptest.ResponseRecorder {
	var payload bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&payload).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, &payload)
	request.Header.Set("Content-Type", "application/json")
	return performRequest(t, request, userID)
}

func performRequest(t *testing.T, request *http.Request, userID uint) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("user_id", userID); c.Next() })
	router.GET("/api/skills", GetSkills)
	router.POST("/api/skills", CreateSkill)
	router.GET("/api/skills/:id", GetSkill)
	router.PUT("/api/skills/:id", UpdateSkill)
	router.DELETE("/api/skills/:id", DeleteSkill)
	router.POST("/api/skills/import/zip", ImportSkillZIP)
	router.POST("/api/skills/:id/validate", ValidateSkill)
	router.GET("/api/agents/:id/skills", GetAgentSkills)
	router.POST("/api/agents/:id/skills", AssignSkillToAgent)
	router.PATCH("/api/agents/:id/skills/:skill_id", UpdateAgentSkill)
	router.DELETE("/api/agents/:id/skills/:skill_id", RemoveSkillFromAgent)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func decode(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatal(err)
	}
}

func skillZipMultipart(t *testing.T) (*bytes.Buffer, string) {
	t.Helper()
	var archive bytes.Buffer
	zipWriter := zip.NewWriter(&archive)
	manifest := `{"id":"zip-lifecycle","name":"ZIP Lifecycle","version":"1.0.0","instructions":"SKILL.md","tools":["workspace.read"]}`
	entry, err := zipWriter.Create("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = entry.Write([]byte(manifest))
	entry, err = zipWriter.Create("SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = entry.Write([]byte("# ZIP Lifecycle"))
	if err := zipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "skill.zip")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(archive.Bytes())
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	return &body, form.FormDataContentType()
}
