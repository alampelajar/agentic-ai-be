package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"agentic-ai-backend/config"
	"agentic-ai-backend/internal/cloudworkspace"
	"agentic-ai-backend/models"
)

func creditsTestDB(t *testing.T) *gorm.DB {
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
	return db
}

// A new account starts with 10 credits; a new website needs 2 and a change 1, paid
// only when the work succeeded and only once; work already started holds its credits;
// an admin tops up. Needs SKILLS_TEST_DATABASE_URL.
func TestBuilderCredits(t *testing.T) {
	db := creditsTestDB(t)
	storage, err := cloudworkspace.NewLocalDevelopmentStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	UseCloudWorkspaceStorage(storage, cloudworkspace.DefaultLimits(), nil)
	t.Setenv("SITE_DATA_ROOT", t.TempDir())
	t.Setenv(builderUnlimitedEnv, "")

	suffix := strconv.Itoa(os.Getpid())
	person := models.User{Name: "Credits", Email: "credits-" + suffix + "@example.test"}
	admin := models.User{Name: "Admin", Email: "credits-admin-" + suffix + "@example.test", Role: "admin"}
	for _, u := range []*models.User{&person, &admin} {
		if err := db.Create(u).Error; err != nil {
			t.Fatal(err)
		}
	}
	ids := []uint{person.ID, admin.ID}
	defer func() {
		db.Where("user_id IN ?", ids).Delete(&models.CreditEntry{})
		db.Where("user_id IN ?", ids).Delete(&models.AgentRun{})
		db.Where("actor_user_id IN ?", ids).Delete(&models.AuditLog{})
		db.Where("entity_type = ? AND entity_id = ?", "user", strconv.Itoa(int(person.ID))).Delete(&models.AuditLog{})
		db.Unscoped().Where("user_id IN ?", ids).Delete(&models.Site{})
		db.Unscoped().Where("user_id IN ?", ids).Delete(&models.CloudWorkspace{})
		db.Unscoped().Delete(&person)
		db.Unscoped().Delete(&admin)
	}()

	send := func(as uint, method, path, body string) (int, map[string]any) {
		r := gin.New()
		r.Use(func(c *gin.Context) { c.Set("user_id", as); c.Next() })
		r.GET("/sites", GetSites)
		r.POST("/sites", CreateSite)
		r.GET("/me/credits", GetMyCredits)
		r.PATCH("/admin/users/:id/credits", AdminChangeCredits)
		res := httptest.NewRecorder()
		r.ServeHTTP(res, httptest.NewRequest(method, path, bytes.NewBufferString(body)))
		var parsed map[string]any
		_ = json.Unmarshal(res.Body.Bytes(), &parsed)
		return res.Code, parsed
	}
	create := func() (int, string) {
		code, body := send(person.ID, "POST", "/sites", `{"description":"Toko roti rumahan di Yogyakarta, pesan lewat WhatsApp."}`)
		reason, _ := body["code"].(string)
		return code, reason
	}
	topUp := func(body string) (int, map[string]any) {
		return send(admin.ID, "PATCH", "/admin/users/"+strconv.Itoa(int(person.ID))+"/credits", body)
	}

	// A new account starts with the welcome credits.
	plan := builderPlanFor(person.ID)
	if plan.Credits != welcomeCredits || plan.Available != welcomeCredits || plan.CreateCost != createCredits || plan.ReviseCost != reviseCredits {
		t.Fatalf("new account plan = %+v", plan)
	}
	if code, reason := create(); code != http.StatusCreated {
		t.Fatalf("a website with 10 credits = %d %s", code, reason)
	}
	var site models.Site
	db.Where("user_id = ?", person.ID).First(&site)

	// Paid when it succeeds, once.
	createRun := models.AgentRun{SiteID: site.ID, UserID: person.ID, Kind: models.AgentRunCreate, Status: models.RunCompleted}
	reviseRun := models.AgentRun{SiteID: site.ID, UserID: person.ID, Kind: models.AgentRunRevise, Status: models.RunCompleted}
	db.Create(&createRun)
	db.Create(&reviseRun)
	ChargeRun(createRun)
	ChargeRun(createRun) // a repeat must not charge again
	ChargeRun(reviseRun)
	if got := builderPlanFor(person.ID).Credits; got != welcomeCredits-createCredits-reviseCredits {
		t.Fatalf("after a website and a change: %d credits, want %d", got, welcomeCredits-createCredits-reviseCredits)
	}

	// Work already started holds its credits: with 2 left and a website being made,
	// nothing else can start.
	db.Model(&models.User{}).Where("id = ?", person.ID).Update("credits", 2)
	queued := models.AgentRun{SiteID: site.ID, UserID: person.ID, Kind: models.AgentRunCreate, Status: models.RunQueued}
	db.Create(&queued)
	if plan := builderPlanFor(person.ID); plan.Available != 0 || plan.CanAfford(reviseCredits) {
		t.Errorf("with a website being made: plan = %+v", plan)
	}
	db.Model(&queued).Update("status", models.RunFailed)

	// Not enough credits: no new website.
	db.Model(&models.User{}).Where("id = ?", person.ID).Update("credits", 1)
	if code, reason := create(); code != http.StatusPaymentRequired || reason != "credit_limit" {
		t.Errorf("a website with 1 credit = %d %s, want 402 credit_limit", code, reason)
	}

	// An admin tops up; taking away never goes below zero.
	if code, body := topUp(`{"amount":20,"note":"Transfer"}`); code != http.StatusOK || body["credits"] != float64(21) {
		t.Fatalf("top-up = %d %v", code, body)
	}
	if code, _ := topUp(`{"amount":0}`); code != http.StatusBadRequest {
		t.Errorf("a top-up of 0 = %d, want 400", code)
	}
	if code, body := topUp(`{"amount":-500}`); code != http.StatusOK || body["credits"] != float64(0) {
		t.Errorf("taking away more than there is = %d %v, want 0", code, body)
	}
	var audits int64
	db.Model(&models.AuditLog{}).Where("action = ? AND entity_id = ?", "user.credits", strconv.Itoa(int(person.ID))).Count(&audits)
	if audits != 2 {
		t.Errorf("audit entries = %d, want 2", audits)
	}

	// The person sees the balance and the history, newest first, with the website's name.
	code, body := send(person.ID, "GET", "/me/credits", "")
	history, _ := body["history"].([]any)
	if code != http.StatusOK || len(history) != 4 {
		t.Fatalf("history = %d %v", code, body)
	}
	if first := history[0].(map[string]any); first["reason"] != models.CreditReasonAdmin || first["balance"] != float64(0) {
		t.Errorf("newest entry = %v", first)
	}
	if oldest := history[3].(map[string]any); oldest["reason"] != models.CreditReasonCreate || oldest["amount"] != float64(-createCredits) || oldest["site_name"] == "" {
		t.Errorf("oldest entry = %v", oldest)
	}
}

// An admin gives premium for a time; it ends by itself. Premium now only widens the
// models the builder may use. Needs SKILLS_TEST_DATABASE_URL.
func TestPremiumComesAndGoes(t *testing.T) {
	db := creditsTestDB(t)
	user := models.User{Name: "Premium", Email: "quota-premium-" + strconv.Itoa(os.Getpid()) + "@example.test"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	defer db.Unscoped().Delete(&user)
	defer db.Where("entity_type = ? AND entity_id = ?", "user", strconv.Itoa(int(user.ID))).Delete(&models.AuditLog{})

	setPremium := func(body string) int {
		r := gin.New()
		r.PATCH("/users/:id/premium", AdminSetUserPremium)
		res := httptest.NewRecorder()
		r.ServeHTTP(res, httptest.NewRequest("PATCH", "/users/"+strconv.Itoa(int(user.ID))+"/premium", bytes.NewBufferString(body)))
		return res.Code
	}
	if code := setPremium(`{"is_premium":true,"premium_until":"2001-01-01T00:00:00Z"}`); code != http.StatusBadRequest {
		t.Errorf("premium ending in the past = %d, want 400", code)
	}
	until := time.Now().Add(30 * 24 * time.Hour).UTC().Format(time.RFC3339)
	if code := setPremium(`{"is_premium":true,"premium_until":"` + until + `"}`); code != http.StatusOK || !isPremiumUser(user.ID) {
		t.Fatalf("premium for 30 days = %d", code)
	}
	db.Model(&models.User{}).Where("id = ?", user.ID).Update("premium_until", time.Now().Add(-time.Minute))
	if isPremiumUser(user.ID) {
		t.Error("an ended premium must count as free again")
	}
	if code := setPremium(`{"is_premium":false}`); code != http.StatusOK || isPremiumUser(user.ID) {
		t.Errorf("taking premium away = %d", code)
	}
}
