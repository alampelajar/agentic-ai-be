package handlers

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"

	"agentic-ai-backend/models"
)

func TestParsePackagesKeepsSensibleChoices(t *testing.T) {
	got := parsePackages(" 25, 10,abc,10, -5, 0, 50, 999999")
	want := []int{10, 25, 50}
	if len(got) != len(want) {
		t.Fatalf("packages = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("packages = %v, want %v", got, want)
		}
	}
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// A person pays by hand and sends the screenshot; an admin approves it once and the
// credits are added, or rejects it with a reason. The screenshot is shown only to the
// person and to admins. Needs SKILLS_TEST_DATABASE_URL.
func TestTopUpByHand(t *testing.T) {
	db := creditsTestDB(t)
	dir := t.TempDir()
	wd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil { // proofs are written under uploads/topups
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	t.Setenv(builderUnlimitedEnv, "")

	suffix := strconv.Itoa(os.Getpid())
	person := models.User{Name: "Buyer", Email: "topup-" + suffix + "@example.test"}
	stranger := models.User{Name: "Stranger", Email: "topup-stranger-" + suffix + "@example.test"}
	admin := models.User{Name: "Admin", Email: "topup-admin-" + suffix + "@example.test", Role: "admin"}
	for _, u := range []*models.User{&person, &stranger, &admin} {
		if err := db.Create(u).Error; err != nil {
			t.Fatal(err)
		}
	}
	var saved []models.TopUpSettings
	db.Find(&saved)
	ids := []uint{person.ID, stranger.ID, admin.ID}
	t.Cleanup(func() {
		db.Where("user_id IN ?", ids).Delete(&models.TopUpRequest{})
		db.Where("user_id IN ?", ids).Delete(&models.CreditEntry{})
		db.Where("actor_user_id IN ?", ids).Delete(&models.AuditLog{})
		db.Where("1 = 1").Delete(&models.TopUpSettings{})
		for _, settings := range saved {
			db.Create(&settings)
		}
		db.Unscoped().Delete(&person)
		db.Unscoped().Delete(&stranger)
		db.Unscoped().Delete(&admin)
	})
	db.Where("1 = 1").Delete(&models.TopUpSettings{})

	router := gin.New()
	router.Use(func(c *gin.Context) {
		id, _ := strconv.Atoi(c.GetHeader("X-As"))
		c.Set("user_id", uint(id))
		c.Next()
	})
	router.GET("/topup", GetTopUp)
	router.POST("/topups", CreateTopUp)
	router.GET("/topups/:id/proof", GetMyTopUpProof)
	router.GET("/admin/topups", AdminListTopUps)
	router.PATCH("/admin/topups/:id", AdminReviewTopUp)
	router.PUT("/admin/topup-settings", AdminSaveTopUpSettings)
	call := func(as uint, method, path string, body *bytes.Buffer, contentType string) (int, map[string]any) {
		if body == nil {
			body = &bytes.Buffer{}
		}
		req := httptest.NewRequest(method, path, body)
		req.Header.Set("X-As", strconv.Itoa(int(as)))
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		var parsed map[string]any
		_ = json.Unmarshal(res.Body.Bytes(), &parsed)
		return res.Code, parsed
	}
	send := func(credits string, proof []byte) (int, map[string]any) {
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		_ = form.WriteField("credits", credits)
		part, _ := form.CreateFormFile("proof", "bukti.png")
		_, _ = part.Write(proof)
		_ = form.Close()
		return call(person.ID, "POST", "/topups", &body, form.FormDataContentType())
	}

	// Not set up yet: not available, and nothing can be sent.
	if code, body := call(person.ID, "GET", "/topup", nil, ""); code != http.StatusOK || body["topup"].(map[string]any)["available"] != false {
		t.Fatalf("before setup = %d %v", code, body)
	}
	if code, _ := send("10", pngBytes(t)); code != http.StatusConflict {
		t.Errorf("a top-up before setup = %d, want 409", code)
	}

	// The admin sets the price, the packages and where to pay.
	settings := `{"enabled":true,"price_per_credit":1000,"packages":"25, 10","payment_info":"BCA 0000000000 a.n. Contoh"}`
	if code, body := call(admin.ID, "PUT", "/admin/topup-settings", bytes.NewBufferString(settings), "application/json"); code != http.StatusOK || body["available"] != true {
		t.Fatalf("settings = %d %v", code, body)
	}

	// Besides the packages, a person may type their own number within the bounds.
	for _, wrong := range []string{"0", "-5", "1001", "abc"} {
		if code, _ := send(wrong, pngBytes(t)); code != http.StatusBadRequest {
			t.Errorf("a top-up of %q credits = %d, want 400", wrong, code)
		}
	}
	if code, body := call(person.ID, "GET", "/topup", nil, ""); code != http.StatusOK || body["topup"].(map[string]any)["custom_max"] != float64(maxCustomTopUp) {
		t.Errorf("custom bounds = %d %v", code, body)
	}
	if code, _ := send("10", []byte("not a picture")); code != http.StatusBadRequest {
		t.Errorf("a proof that is not a picture = %d, want 400", code)
	}
	code, body := send("25", pngBytes(t))
	if code != http.StatusCreated {
		t.Fatalf("a top-up = %d %v", code, body)
	}
	request := body["request"].(map[string]any)
	id := strconv.Itoa(int(request["id"].(float64)))
	// A new account pays half in its first 30 days.
	if request["status"] != models.TopUpPending || request["price"] != float64(12500) || request["discount_percent"] != float64(newAccountDiscountPercent) {
		t.Errorf("request = %v, want 25 credits for Rp12.500 with the new-account discount", request)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "uploads", "topups")); len(entries) != 1 {
		t.Errorf("stored proofs = %d, want 1", len(entries))
	}

	// The proof: the person sees it, a stranger does not.
	if code, _ := call(person.ID, "GET", "/topups/"+id+"/proof", nil, ""); code != http.StatusOK {
		t.Errorf("own proof = %d, want 200", code)
	}
	if code, _ := call(stranger.ID, "GET", "/topups/"+id+"/proof", nil, ""); code != http.StatusNotFound {
		t.Errorf("someone else's proof = %d, want 404", code)
	}

	// The admin sees it waiting, approves it once.
	if code, body := call(admin.ID, "GET", "/admin/topups", nil, ""); code != http.StatusOK || body["pending"].(float64) < 1 {
		t.Fatalf("admin list = %d %v", code, body)
	}
	if code, _ := call(admin.ID, "PATCH", "/admin/topups/"+id, bytes.NewBufferString(`{"action":"reject"}`), "application/json"); code != http.StatusBadRequest {
		t.Errorf("a rejection without a reason = %d, want 400", code)
	}
	if code, body := call(admin.ID, "PATCH", "/admin/topups/"+id, bytes.NewBufferString(`{"action":"approve"}`), "application/json"); code != http.StatusOK {
		t.Fatalf("approve = %d %v", code, body)
	}
	if code, _ := call(admin.ID, "PATCH", "/admin/topups/"+id, bytes.NewBufferString(`{"action":"approve"}`), "application/json"); code != http.StatusConflict {
		t.Errorf("approving twice = %d, want 409", code)
	}
	if got := builderPlanFor(person.ID).Credits; got != welcomeCredits+25 {
		t.Errorf("credits after the top-up = %d, want %d", got, welcomeCredits+25)
	}
	var entry models.CreditEntry
	db.Where("user_id = ?", person.ID).Order("id DESC").First(&entry)
	if entry.Reason != models.CreditReasonTopUp || entry.Amount != 25 {
		t.Errorf("history entry = %+v", entry)
	}

	// Without the promo, the full price.
	db.Model(&models.User{}).Where("id = ?", person.ID).Update("promo_eligible", false)
	if _, body := call(person.ID, "GET", "/topup", nil, ""); body["discount"].(map[string]any)["percent"] != float64(0) {
		t.Errorf("discount without the promo = %v", body["discount"])
	}

	// A second request, rejected with a reason: no credits.
	_, body = send("10", pngBytes(t))
	if price := body["request"].(map[string]any)["price"]; price != float64(10000) {
		t.Errorf("price without the promo = %v, want 10000", price)
	}
	second := strconv.Itoa(int(body["request"].(map[string]any)["id"].(float64)))
	if code, _ := call(admin.ID, "PATCH", "/admin/topups/"+second, bytes.NewBufferString(`{"action":"reject","note":"Bukti tidak terbaca"}`), "application/json"); code != http.StatusOK {
		t.Fatalf("reject = %d", code)
	}
	if got := builderPlanFor(person.ID).Credits; got != welcomeCredits+25 {
		t.Errorf("credits after a rejection = %d, want unchanged", got)
	}
	_, body = call(person.ID, "GET", "/topup", nil, "")
	if latest := body["requests"].([]any)[0].(map[string]any); latest["status"] != models.TopUpRejected || latest["admin_note"] != "Bukti tidak terbaca" {
		t.Errorf("the person's latest request = %v", latest)
	}
}
