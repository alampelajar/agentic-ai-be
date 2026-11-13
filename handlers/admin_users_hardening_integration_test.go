package handlers

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
)

// asAdmin routes admin user requests as if f.admin were signed in.
func (f *guardFixture) asAdmin() {
	signedIn := func(c *gin.Context) {
		c.Set("user_id", f.admin.ID)
		c.Set("user_email", f.admin.Email)
		c.Set("user_role", "admin")
	}
	f.router.POST("/admin/users", signedIn, AdminCreateUser)
	f.router.PUT("/admin/users/:id", signedIn, AdminUpdateUser)
}

func (f *guardFixture) send(method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, request)
	return rec
}

func TestAnAdminCannotDemoteThemselves(t *testing.T) {
	f := newGuardFixture(t)
	f.asAdmin()
	body := `{"name":"Guard Admin","email":"` + f.admin.Email + `","role":"user"}`
	if rec := f.send(http.MethodPut, "/admin/users/"+strconv.Itoa(int(f.admin.ID)), body); rec.Code != http.StatusConflict {
		t.Fatalf("self-demotion = %d %s, want 409", rec.Code, rec.Body.String())
	}
	var stored models.User
	config.DB.First(&stored, f.admin.ID)
	if stored.Role != "admin" {
		t.Fatalf("role after a refused self-demotion = %q", stored.Role)
	}
}

func TestAnAdminChangeIsAuditedAndANewAddressEndsSessions(t *testing.T) {
	f := newGuardFixture(t)
	f.asAdmin()
	session := models.RefreshToken{UserID: f.user.ID, TokenHash: "hardening-" + f.suffix, ExpiredAt: time.Now().Add(time.Hour)}
	if err := config.DB.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	newEmail := "guard-user-moved-" + f.suffix + "@example.test"
	t.Cleanup(func() {
		config.DB.Unscoped().Where("entity_type = ? AND entity_id = ?", "user", strconv.Itoa(int(f.user.ID))).Delete(&models.AuditLog{})
	})

	body := `{"name":"Guard User","email":"` + newEmail + `","role":"user"}`
	if rec := f.send(http.MethodPut, "/admin/users/"+strconv.Itoa(int(f.user.ID)), body); rec.Code != http.StatusOK {
		t.Fatalf("update = %d %s", rec.Code, rec.Body.String())
	}
	config.DB.First(&session, session.ID)
	if session.RevokedAt == nil {
		t.Fatal("a session of the old address is still valid")
	}
	var audit models.AuditLog
	if err := config.DB.Where("action = ? AND entity_id = ?", "user.updated_by_admin", strconv.Itoa(int(f.user.ID))).Order("id desc").First(&audit).Error; err != nil {
		t.Fatalf("no audit entry: %v", err)
	}
	if !strings.Contains(audit.Message, newEmail) {
		t.Fatalf("audit message %q does not name the new address", audit.Message)
	}
}

func TestAdminCreatedAccountsNeedEightCharacterPasswords(t *testing.T) {
	f := newGuardFixture(t)
	f.asAdmin()
	email := "guard-short-" + f.suffix + "@example.test"
	t.Cleanup(func() { config.DB.Unscoped().Where("email = ?", email).Delete(&models.User{}) })
	if rec := f.send(http.MethodPost, "/admin/users", `{"name":"Short","email":"`+email+`","password":"1234567","role":"user"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("a 7-character password = %d %s, want 400", rec.Code, rec.Body.String())
	}
}

func TestUserChangesDescribesOnlyWhatChanged(t *testing.T) {
	if got := userChanges("A", "A", "a@x.test", "A@X.test", "user", "user"); got != "" {
		t.Fatalf("no change = %q", got)
	}
	if got := userChanges("A", "B", "a@x.test", "b@x.test", "admin", "user"); !strings.Contains(got, "role admin menjadi user") || !strings.Contains(got, "b@x.test") {
		t.Fatalf("changes = %q", got)
	}
}

func TestAdminCreatedAccountsRefuseOverlongPasswords(t *testing.T) {
	f := newGuardFixture(t)
	f.asAdmin()
	email := "guard-long-" + f.suffix + "@example.test"
	t.Cleanup(func() { config.DB.Unscoped().Where("email = ?", email).Delete(&models.User{}) })
	long := strings.Repeat("a", 73)
	if rec := f.send(http.MethodPost, "/admin/users", `{"name":"Long","email":"`+email+`","password":"`+long+`","role":"user"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("a 73-byte password = %d %s, want 400", rec.Code, rec.Body.String())
	}
}

func TestAdminCreatedAccountsRefuseCommonPasswords(t *testing.T) {
	f := newGuardFixture(t)
	f.asAdmin()
	email := "guard-common-" + f.suffix + "@example.test"
	t.Cleanup(func() { config.DB.Unscoped().Where("email = ?", email).Delete(&models.User{}) })
	rec := f.send(http.MethodPost, "/admin/users", `{"name":"Common","email":"`+email+`","password":"password123","role":"user"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "password_common") {
		t.Fatalf("a common password = %d %s, want 400 password_common", rec.Code, rec.Body.String())
	}
}
