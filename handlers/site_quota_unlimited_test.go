package handlers

import (
	"os"
	"strconv"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
)

func TestUnlimitedEmailReadsTheList(t *testing.T) {
	t.Setenv(builderUnlimitedEnv, " Owner@Example.test , second@example.test")
	for email, want := range map[string]bool{
		"owner@example.test":  true,
		"OWNER@example.test ": true,
		"second@example.test": true,
		"other@example.test":  false,
		"":                    false,
	} {
		if got := unlimitedEmail(email); got != want {
			t.Errorf("unlimitedEmail(%q) = %v, want %v", email, got, want)
		}
	}
	t.Setenv(builderUnlimitedEnv, "")
	if unlimitedEmail("owner@example.test") {
		t.Error("an empty list must not make anyone unlimited")
	}
}

// An owner's own account has no monthly allowance and no daily system-model quota,
// even when it has used far more than a premium account may. Needs
// SKILLS_TEST_DATABASE_URL.
func TestUnlimitedAccountHasNoBuilderLimits(t *testing.T) {
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
	t.Setenv(builderPremiumQuotaEnv, "")

	suffix := strconv.Itoa(os.Getpid())
	owner := models.User{Name: "Owner", Email: "quota-owner-" + suffix + "@example.test"}
	premium := models.User{Name: "Premium", Email: "quota-premium2-" + suffix + "@example.test", IsPremium: true}
	for _, u := range []*models.User{&owner, &premium} {
		if err := db.Create(u).Error; err != nil {
			t.Fatal(err)
		}
	}
	ids := []uint{owner.ID, premium.ID}
	defer func() {
		db.Where("user_id IN ?", ids).Delete(&models.AIModelCallLog{})
		db.Unscoped().Delete(&owner)
		db.Unscoped().Delete(&premium)
	}()
	// Both used twice the premium allowance this month.
	for _, id := range ids {
		if err := db.Create(&models.AIModelCallLog{UserID: id, Attempt: 1, Status: "ok", Tokens: 2 * premiumMonthlyTokens, CreatedAt: time.Now()}).Error; err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv(builderUnlimitedEnv, owner.Email)

	plan := builderPlanFor(owner.ID)
	if !plan.Unlimited || !plan.Premium || plan.TokenLimit != 0 || plan.OutOfTokens() {
		t.Fatalf("owner plan = %+v, want unlimited without a token limit", plan)
	}
	if !isUnlimitedUser(owner.ID) || isUnlimitedUser(premium.ID) {
		t.Fatal("only the listed account is unlimited")
	}
	if other := builderPlanFor(premium.ID); other.Unlimited || !other.OutOfTokens() {
		t.Fatalf("premium plan = %+v, want its allowance used up", other)
	}
}
