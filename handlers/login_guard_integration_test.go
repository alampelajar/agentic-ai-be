package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
)

type guardFixture struct {
	router *gin.Engine
	user   models.User
	admin  models.User
	suffix string
}

func newGuardFixture(t *testing.T) *guardFixture {
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
	t.Setenv("JWT_ACCESS_SECRET", strings.Repeat("a", 40))
	t.Setenv("JWT_REFRESH_SECRET", strings.Repeat("b", 40))

	suffix := strconv.FormatInt(int64(os.Getpid()), 10) + "-" + strconv.FormatInt(int64(len(t.Name())), 10)
	hash, err := bcrypt.GenerateFromPassword([]byte("correct horse"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	f := &guardFixture{suffix: suffix}
	f.user = models.User{Name: "Guard User", Email: "guard-user-" + suffix + "@example.test", Password: string(hash), Role: "user"}
	f.admin = models.User{Name: "Guard Admin", Email: "guard-admin-" + suffix + "@example.test", Password: string(hash), Role: "admin"}
	for _, user := range []*models.User{&f.user, &f.admin} {
		if err := db.Create(user).Error; err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		db.Unscoped().Where("user_id IN ?", []uint{f.user.ID, f.admin.ID}).Delete(&models.RefreshToken{})
		db.Unscoped().Delete(&f.user)
		db.Unscoped().Delete(&f.admin)
	})
	gin.SetMode(gin.TestMode)
	f.router = gin.New()
	f.router.POST("/login", Login)
	f.router.POST("/admin/login", AdminLogin)
	return f
}

func (f *guardFixture) post(path, email, password, ip string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"email":"`+email+`","password":"`+password+`"}`))
	request.Header.Set("Content-Type", "application/json")
	request.RemoteAddr = ip + ":4000"
	recorder := httptest.NewRecorder()
	f.router.ServeHTTP(recorder, request)
	return recorder
}

func TestLoginLocksAfterRepeatedFailuresWithoutTouchingOtherCallers(t *testing.T) {
	f := newGuardFixture(t)
	attacker, owner := "198.51.100.10", "198.51.100.11"
	for i := 0; i < 5; i++ {
		if got := f.post("/login", f.user.Email, "wrong-"+strconv.Itoa(i), attacker); got.Code != http.StatusUnauthorized {
			t.Fatalf("guess %d = %d", i+1, got.Code)
		}
	}
	// Locked: even the right password is refused, so a correct guess does not show itself.
	locked := f.post("/login", f.user.Email, "correct horse", attacker)
	if locked.Code != http.StatusTooManyRequests || locked.Header().Get("Retry-After") == "" {
		t.Fatalf("locked pair = %d %v %s", locked.Code, locked.Header(), locked.Body.String())
	}
	// The real owner, from their own address, can still sign in.
	if got := f.post("/login", f.user.Email, "correct horse", owner); got.Code != http.StatusOK {
		t.Fatalf("the owner from another address = %d %s", got.Code, got.Body.String())
	}
	// And the attacker's address can still sign in to an account it has not been guessing.
	if got := f.post("/login", f.admin.Email, "correct horse", attacker); got.Code != http.StatusOK {
		t.Fatalf("another account from the locked address = %d", got.Code)
	}
}

func TestLoginFailureLooksTheSameForUnknownAndKnownAccounts(t *testing.T) {
	f := newGuardFixture(t)
	unknown := f.post("/login", "nobody-"+f.suffix+"@example.test", "x", "198.51.100.20")
	wrong := f.post("/login", f.user.Email, "x", "198.51.100.21")
	if unknown.Code != wrong.Code || unknown.Body.String() != wrong.Body.String() {
		t.Fatalf("unknown = %d %s, wrong password = %d %s", unknown.Code, unknown.Body.String(), wrong.Code, wrong.Body.String())
	}
	// Failures against an address nobody owns count and lock too, so locking does not
	// reveal which emails exist.
	for i := 0; i < 5; i++ {
		f.post("/login", "ghost-"+f.suffix+"@example.test", "x", "198.51.100.22")
	}
	if got := f.post("/login", "ghost-"+f.suffix+"@example.test", "x", "198.51.100.22"); got.Code != http.StatusTooManyRequests {
		t.Fatalf("an unknown account was never locked: %d", got.Code)
	}
}

func TestAdminLoginDoesNotRevealWhichAccountsAreOrdinaryUsers(t *testing.T) {
	f := newGuardFixture(t)
	stranger := f.post("/admin/login", "nobody-"+f.suffix+"@example.test", "x", "198.51.100.30")
	ordinaryWrong := f.post("/admin/login", f.user.Email, "x", "198.51.100.31")
	if stranger.Code != http.StatusUnauthorized || stranger.Code != ordinaryWrong.Code || stranger.Body.String() != ordinaryWrong.Body.String() {
		t.Fatalf("an unknown address and an ordinary user with a wrong password differ: %d %s / %d %s", stranger.Code, stranger.Body.String(), ordinaryWrong.Code, ordinaryWrong.Body.String())
	}
	// Only someone who knows the password learns the account is not an admin's.
	if got := f.post("/admin/login", f.user.Email, "correct horse", "198.51.100.32"); got.Code != http.StatusForbidden {
		t.Fatalf("an ordinary user with the right password = %d", got.Code)
	}
	if got := f.post("/admin/login", f.admin.Email, "correct horse", "198.51.100.33"); got.Code != http.StatusOK {
		t.Fatalf("a real admin = %d %s", got.Code, got.Body.String())
	}
}

func TestAdminLoginLocksToo(t *testing.T) {
	f := newGuardFixture(t)
	for i := 0; i < 5; i++ {
		f.post("/admin/login", f.admin.Email, "wrong", "198.51.100.40")
	}
	if got := f.post("/admin/login", f.admin.Email, "correct horse", "198.51.100.40"); got.Code != http.StatusTooManyRequests {
		t.Fatalf("a guessed-at admin account was not locked: %d", got.Code)
	}
}
