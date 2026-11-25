package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
)

func TestSignalHashesHideTheValue(t *testing.T) {
	t.Setenv("JWT_ACCESS_SECRET", "test-secret")
	first, again := signalHash("203.0.113.7"), signalHash("203.0.113.7")
	if first == "" || first != again || strings.Contains(first, "203") {
		t.Fatalf("hash = %q, want the same keyed hash without the address", first)
	}
	if signalHash("") != "" {
		t.Error("an empty value must not be hashed")
	}
	t.Setenv("JWT_ACCESS_SECRET", "another-secret")
	if signalHash("203.0.113.7") == first {
		t.Error("the hash must depend on the server's secret")
	}
}

func TestDiscountLastsThirtyDaysForEligibleAccounts(t *testing.T) {
	t.Setenv(builderUnlimitedEnv, "")
	now := time.Now()
	fresh := models.User{Email: "a@example.test", PromoEligible: true, CreatedAt: now.Add(-24 * time.Hour)}
	if percent, until := discountFor(fresh, now); percent != newAccountDiscountPercent || !until.After(now) {
		t.Errorf("a day-old account = %d until %v", percent, until)
	}
	old := fresh
	old.CreatedAt = now.Add(-31 * 24 * time.Hour)
	if percent, _ := discountFor(old, now); percent != 0 {
		t.Errorf("a 31-day-old account = %d, want 0", percent)
	}
	refused := fresh
	refused.PromoEligible = false
	if percent, _ := discountFor(refused, now); percent != 0 {
		t.Errorf("an account without the promo = %d, want 0", percent)
	}
	if got := discounted(25000, 50); got != 12500 {
		t.Errorf("discounted = %d", got)
	}
}

// A second account from the same browser gets no promo; an address gets two accounts
// with the promo in 30 days, and the third none. Needs SKILLS_TEST_DATABASE_URL.
func TestSecondAccountGetsNoPromo(t *testing.T) {
	db := creditsTestDB(t)
	t.Setenv("JWT_ACCESS_SECRET", "promo-test-"+strconv.Itoa(os.Getpid()))
	suffix := strconv.Itoa(os.Getpid())
	made := []uint{}
	t.Cleanup(func() {
		for _, id := range made {
			db.Unscoped().Delete(&models.User{}, id)
		}
	})
	signUp := func(name, address, device string) models.User {
		user := models.User{Name: name, Email: name + "-" + suffix + "@example.test"}
		if err := db.Create(&user).Error; err != nil {
			t.Fatal(err)
		}
		made = append(made, user.ID)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/register", nil)
		c.Request.RemoteAddr = address + ":5000"
		if device != "" {
			c.Request.Header.Set(deviceHeader, device)
		}
		if err := applySignupPromo(config.DB, c, user.ID); err != nil {
			t.Fatal(err)
		}
		var saved models.User
		db.First(&saved, user.ID)
		return saved
	}
	device := "device-" + suffix + "-aaaaaaaaaaaa"

	first := signUp("first", "198.51.100.10", device)
	if !first.PromoEligible || first.Credits != welcomeCredits || first.SignupDevice == "" || first.SignupIP == "" {
		t.Fatalf("first account = eligible %v, credits %d", first.PromoEligible, first.Credits)
	}
	// Same browser, another address: no promo, no free credits.
	second := signUp("second", "198.51.100.99", device)
	if second.PromoEligible || second.Credits != 0 || !strings.Contains(second.PromoNote, "Perangkat") {
		t.Fatalf("same device = eligible %v, credits %d, note %q", second.PromoEligible, second.Credits, second.PromoNote)
	}

	// One shared address: two more accounts keep the promo, the next does not.
	address := "198.51.100.20"
	a := signUp("shared-a", address, "device-"+suffix+"-bbbbbbbbbbbb")
	b := signUp("shared-b", address, "device-"+suffix+"-cccccccccccc")
	c := signUp("shared-c", address, "device-"+suffix+"-dddddddddddd")
	if !a.PromoEligible || !b.PromoEligible {
		t.Errorf("two accounts from one address must keep the promo: %v %v", a.PromoEligible, b.PromoEligible)
	}
	if c.PromoEligible || c.Credits != 0 {
		t.Errorf("the third from one address = eligible %v, credits %d", c.PromoEligible, c.Credits)
	}
	// A device id that is not one of ours is ignored, not trusted.
	odd := signUp("odd", "198.51.100.30", "<script>")
	if odd.SignupDevice != "" || !odd.PromoEligible {
		t.Errorf("an odd device id = %q, eligible %v", odd.SignupDevice, odd.PromoEligible)
	}
}

// The usage summary has the last six months, newest first, and counts this month's
// credits. Needs SKILLS_TEST_DATABASE_URL.
func TestAdminUsageCountsTheMonths(t *testing.T) {
	db := creditsTestDB(t)
	user := models.User{Name: "Usage", Email: "usage-" + strconv.Itoa(os.Getpid()) + "@example.test"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Where("user_id = ?", user.ID).Delete(&models.CreditEntry{})
		db.Unscoped().Delete(&user)
	})
	db.Create(&models.CreditEntry{UserID: user.ID, Amount: -createCredits, Balance: 8, Reason: models.CreditReasonCreate})

	res := httptest.NewRecorder()
	r := gin.New()
	r.GET("/admin/usage", AdminUsage)
	r.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/admin/usage", nil))
	var body struct {
		Months []usageMonth `json:"months"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil || res.Code != http.StatusOK {
		t.Fatalf("usage = %d %s", res.Code, res.Body.String())
	}
	if len(body.Months) != 6 {
		t.Fatalf("months = %d, want 6", len(body.Months))
	}
	if this := time.Now().In(quotaZone()).Format("2006-01"); body.Months[0].Month != this || body.Months[0].Websites < 1 || body.Months[0].CreditsUsed < createCredits {
		t.Errorf("this month = %+v", body.Months[0])
	}
}
