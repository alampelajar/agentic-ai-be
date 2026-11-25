package handlers

import (
	"os"
	"strconv"
	"testing"

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

// An owner's own account uses no credits, even with none left, and only the listed
// account is unlimited. Needs SKILLS_TEST_DATABASE_URL.
func TestUnlimitedAccountUsesNoCredits(t *testing.T) {
	db := creditsTestDB(t)
	suffix := strconv.Itoa(os.Getpid())
	owner := models.User{Name: "Owner", Email: "quota-owner-" + suffix + "@example.test"}
	other := models.User{Name: "Other", Email: "quota-other-" + suffix + "@example.test"}
	for _, u := range []*models.User{&owner, &other} {
		if err := db.Create(u).Error; err != nil {
			t.Fatal(err)
		}
	}
	ids := []uint{owner.ID, other.ID}
	defer func() {
		db.Where("user_id IN ?", ids).Delete(&models.CreditEntry{})
		db.Unscoped().Delete(&owner)
		db.Unscoped().Delete(&other)
	}()
	db.Model(&models.User{}).Where("id IN ?", ids).Update("credits", 0)
	t.Setenv(builderUnlimitedEnv, owner.Email)

	if plan := builderPlanFor(owner.ID); !plan.Unlimited || !plan.CanAfford(createCredits) {
		t.Fatalf("owner plan = %+v, want unlimited", plan)
	}
	if !isUnlimitedUser(owner.ID) || isUnlimitedUser(other.ID) {
		t.Fatal("only the listed account is unlimited")
	}
	if plan := builderPlanFor(other.ID); plan.Unlimited || plan.CanAfford(reviseCredits) {
		t.Fatalf("other plan = %+v, want no credits", plan)
	}
	ChargeRun(models.AgentRun{ID: 999999001, UserID: owner.ID, SiteID: 1, Kind: models.AgentRunCreate})
	var charged int64
	db.Model(&models.CreditEntry{}).Where("user_id = ?", owner.ID).Count(&charged)
	if charged != 0 {
		t.Error("an unlimited account was charged")
	}
}
