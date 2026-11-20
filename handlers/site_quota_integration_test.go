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

// Without premium a person may keep two websites and use 200,000 tokens a month; a
// premium account has neither limit. Needs SKILLS_TEST_DATABASE_URL.
func TestBuilderLimitsFollowThePlan(t *testing.T) {
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
	storage, err := cloudworkspace.NewLocalDevelopmentStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	UseCloudWorkspaceStorage(storage, cloudworkspace.DefaultLimits(), nil)
	t.Setenv("SITE_DATA_ROOT", t.TempDir())
	t.Setenv(builderQuotaEnvName, "")
	t.Setenv(builderPremiumQuotaEnv, "")

	suffix := strconv.Itoa(os.Getpid())
	free := models.User{Name: "Free", Email: "quota-free-" + suffix + "@example.test"}
	premium := models.User{Name: "Premium", Email: "quota-premium-" + suffix + "@example.test", IsPremium: true}
	for _, u := range []*models.User{&free, &premium} {
		if err := db.Create(u).Error; err != nil {
			t.Fatal(err)
		}
	}
	ids := []uint{free.ID, premium.ID}
	defer func() {
		db.Where("actor_user_id IN ?", ids).Delete(&models.AuditLog{})
		db.Where("user_id IN ?", ids).Delete(&models.AIModelCallLog{})
		db.Unscoped().Where("user_id IN ?", ids).Delete(&models.Site{})
		db.Unscoped().Where("user_id IN ?", ids).Delete(&models.CloudWorkspace{})
		db.Unscoped().Delete(&free)
		db.Unscoped().Delete(&premium)
	}()

	send := func(as uint, method, path, body string) (int, map[string]any) {
		r := gin.New()
		r.Use(func(c *gin.Context) { c.Set("user_id", as); c.Next() })
		r.GET("/sites", GetSites)
		r.POST("/sites", CreateSite)
		res := httptest.NewRecorder()
		r.ServeHTTP(res, httptest.NewRequest(method, path, bytes.NewBufferString(body)))
		var parsed map[string]any
		_ = json.Unmarshal(res.Body.Bytes(), &parsed)
		return res.Code, parsed
	}
	create := func(as uint) (int, string) {
		code, body := send(as, "POST", "/sites", `{"description":"Toko roti rumahan di Yogyakarta, pesan lewat WhatsApp."}`)
		reason, _ := body["code"].(string)
		return code, reason
	}

	for i := 0; i < freeSiteLimit; i++ {
		if code, reason := create(free.ID); code != http.StatusCreated {
			t.Fatalf("free website %d = %d %s", i+1, code, reason)
		}
	}
	if code, reason := create(free.ID); code != http.StatusConflict || reason != "site_limit" {
		t.Errorf("a third free website = %d %s, want 409 site_limit", code, reason)
	}
	for i := 0; i < freeSiteLimit+1; i++ {
		if code, reason := create(premium.ID); code != http.StatusCreated {
			t.Fatalf("premium website %d = %d %s, want no limit", i+1, code, reason)
		}
	}

	// The list tells the interface what is left.
	_, listed := send(free.ID, "GET", "/sites", "")
	plan, _ := listed["plan"].(map[string]any)
	if plan == nil || plan["premium"] != false || plan["site_limit"] != float64(freeSiteLimit) || plan["token_limit"] != float64(freeMonthlyTokens) {
		t.Errorf("plan = %v", plan)
	}

	// Tokens of this month count; last month's do not.
	lastMonth := monthStart(time.Now()).Add(-time.Hour)
	db.Create(&models.AIModelCallLog{UserID: free.ID, Attempt: 1, Status: "ok", Tokens: freeMonthlyTokens, CreatedAt: lastMonth})
	if builderPlanFor(free.ID).OutOfTokens() {
		t.Error("tokens of last month must not count")
	}
	db.Create(&models.AIModelCallLog{UserID: free.ID, Attempt: 1, Status: "ok", Tokens: freeMonthlyTokens})
	if !builderPlanFor(free.ID).OutOfTokens() {
		t.Error("a used-up allowance must stop the builder")
	}
	// Premium has a larger allowance: past the free one it goes on, at a million it stops.
	db.Create(&models.AIModelCallLog{UserID: premium.ID, Attempt: 1, Status: "ok", Tokens: freeMonthlyTokens * 2})
	if plan := builderPlanFor(premium.ID); plan.OutOfTokens() || plan.TokenLimit != premiumMonthlyTokens {
		t.Errorf("premium past the free allowance: plan = %+v", plan)
	}
	db.Create(&models.AIModelCallLog{UserID: premium.ID, Attempt: 1, Status: "ok", Tokens: premiumMonthlyTokens})
	if !builderPlanFor(premium.ID).OutOfTokens() {
		t.Error("premium must stop at its monthly allowance")
	}

	// A person without tokens left cannot start a new website either.
	var site models.Site
	db.Where("user_id = ?", free.ID).First(&site)
	db.Delete(&site)
	if code, reason := create(free.ID); code != http.StatusTooManyRequests || reason != "token_limit" {
		t.Errorf("a new website without tokens = %d %s, want 429 token_limit", code, reason)
	}

	// An admin gives premium for a time; it ends by itself.
	setPremium := func(body string) int {
		r := gin.New()
		r.PATCH("/users/:id/premium", AdminSetUserPremium)
		res := httptest.NewRecorder()
		r.ServeHTTP(res, httptest.NewRequest("PATCH", "/users/"+strconv.Itoa(int(free.ID))+"/premium", bytes.NewBufferString(body)))
		return res.Code
	}
	defer db.Where("entity_type = ? AND entity_id = ?", "user", strconv.Itoa(int(free.ID))).Delete(&models.AuditLog{})
	if code := setPremium(`{"is_premium":true,"premium_until":"2001-01-01T00:00:00Z"}`); code != http.StatusBadRequest {
		t.Errorf("premium ending in the past = %d, want 400", code)
	}
	until := time.Now().Add(30 * 24 * time.Hour).UTC().Format(time.RFC3339)
	if code := setPremium(`{"is_premium":true,"premium_until":"` + until + `"}`); code != http.StatusOK {
		t.Fatalf("premium for 30 days = %d", code)
	}
	if plan := builderPlanFor(free.ID); !plan.Premium || plan.PremiumUntil == nil || plan.OutOfTokens() {
		t.Errorf("premium for 30 days: plan = %+v", plan)
	}
	db.Model(&models.User{}).Where("id = ?", free.ID).Update("premium_until", time.Now().Add(-time.Minute))
	if plan := builderPlanFor(free.ID); plan.Premium || !plan.OutOfTokens() {
		t.Errorf("an ended premium must count as free again: plan = %+v", plan)
	}
	if code := setPremium(`{"is_premium":true}`); code != http.StatusOK || !isPremiumUser(free.ID) {
		t.Errorf("premium without end = %d", code)
	}
	if code := setPremium(`{"is_premium":false}`); code != http.StatusOK || isPremiumUser(free.ID) {
		t.Errorf("taking premium away = %d", code)
	}
}
