package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"agentic-ai-backend/config"
	"agentic-ai-backend/internal/httpguard"
	"agentic-ai-backend/models"
	"agentic-ai-backend/services"
	"agentic-ai-backend/utils"
)

// loginGuard slows password guessing for both Login and AdminLogin: see
// httpguard.LoginGuard for what it counts and its limits.
var loginGuard = httpguard.NewLoginGuard()

// wrongCredentials records a failed attempt and sends the one answer used for
// every kind of failure, so the response never says which part was wrong.
// dummyPasswordHash is compared against when no account has the address, so a
// wrong address takes as long to answer as a wrong password and the timing does
// not tell which addresses are registered.
var dummyPasswordHash = func() []byte {
	hash, err := bcrypt.GenerateFromPassword([]byte("not-a-real-password"), bcrypt.DefaultCost)
	if err != nil {
		panic(err)
	}
	return hash
}()

// spendPasswordCheck does the work of a password check that is bound to fail.
func spendPasswordCheck(password string) {
	_ = bcrypt.CompareHashAndPassword(dummyPasswordHash, []byte(password))
}

// refreshReuseGrace is how long a just-rotated refresh token is still accepted.
// Two tabs can refresh at the same moment with the same cookie; the second one
// must not be taken for a stolen token. After this, using a rotated token again
// means it was copied, and every session of that account is ended.
const refreshReuseGrace = 30 * time.Second

func wrongCredentials(c *gin.Context, email string) {
	loginGuard.Fail(email, c.ClientIP())
	c.JSON(http.StatusUnauthorized, gin.H{
		"ok":      false,
		"message": "Email atau password salah",
	})
}

type RegisterRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func hashToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

func Register(c *gin.Context) {
	// When e-mail verification is configured, accounts must go through
	// /register/request and /register/verify; this route would skip it.
	if _, _, _, ok := services.EmailOTPConfig(); ok {
		c.JSON(http.StatusGone, gin.H{
			"ok":      false,
			"code":    "register_requires_otp",
			"message": "Pendaftaran memerlukan verifikasi email.",
		})
		return
	}

	var req RegisterRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":      false,
			"message": "Format JSON tidak valid",
		})
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))

	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":      false,
			"message": "Nama wajib diisi",
		})
		return
	}

	if req.Email == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":      false,
			"message": "Email wajib diisi",
		})
		return
	}

	if len(req.Password) < 8 {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":      false,
			"message": "Password minimal 8 karakter",
		})
		return
	}

	var existingUser models.User

	result := config.DB.
		Where("email = ?", req.Email).
		First(&existingUser)

	if result.Error == nil {
		c.JSON(http.StatusConflict, gin.H{
			"ok":      false,
			"message": "Email sudah terdaftar",
		})
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword(
		[]byte(req.Password),
		bcrypt.DefaultCost,
	)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "Gagal mengenkripsi password",
		})
		return
	}

	user := models.User{
		Name:     req.Name,
		Email:    req.Email,
		Password: string(hashedPassword),
		Role:     "user",
	}

	if err := config.DB.Create(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "Gagal membuat pengguna",
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"ok":      true,
		"message": "Registrasi berhasil",
		"user": gin.H{
			"id":     user.ID,
			"name":   user.Name,
			"email":  user.Email,
			"avatar": user.Avatar,
			"role":   user.Role,
		},
	})
}

func Login(c *gin.Context) {
	var req LoginRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":      false,
			"message": "Format JSON tidak valid",
		})
		return
	}

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))

	if req.Email == "" || req.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":      false,
			"message": "Email dan password wajib diisi",
		})
		return
	}

	// Refused before the password is looked at, so a correct guess made during a
	// lock does not reveal itself.
	if locked, wait := loginGuard.Check(req.Email, c.ClientIP()); locked {
		httpguard.TooManyRequests(c, wait)
		return
	}

	var user models.User

	result := config.DB.
		Where("email = ?", req.Email).
		First(&user)

	if result.Error != nil {
		spendPasswordCheck(req.Password)
		wrongCredentials(c, req.Email)
		return
	}

	err := bcrypt.CompareHashAndPassword(
		[]byte(user.Password),
		[]byte(req.Password),
	)

	if err != nil {
		wrongCredentials(c, req.Email)
		return
	}

	loginGuard.Success(req.Email, c.ClientIP())

	if user.Role == "" {
		user.Role = "user"

		if err := config.DB.
			Model(&user).
			Update("role", "user").
			Error; err != nil {

			c.JSON(http.StatusInternalServerError, gin.H{
				"ok":      false,
				"message": "Gagal memperbarui role pengguna",
			})
			return
		}
	}

	accessToken, err := utils.GenerateAccessToken(
		user.ID,
		user.Email,
		user.Role,
	)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "Gagal membuat access token",
		})
		return
	}

	refreshToken, err := utils.GenerateRefreshToken(
		user.ID,
		user.Email,
		user.Role,
	)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "Gagal membuat refresh token",
		})
		return
	}

	refreshTokenModel := models.RefreshToken{
		UserID:    user.ID,
		TokenHash: hashToken(refreshToken),
		ExpiredAt: time.Now().Add(7 * 24 * time.Hour),
	}

	if err := config.DB.Create(&refreshTokenModel).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "Gagal menyimpan refresh token",
		})
		return
	}

	setRefreshCookie(c, userRefresh, refreshToken, refreshCookieMaxAge)

	c.JSON(http.StatusOK, gin.H{
	"ok":           true,
	"message":      "Login berhasil",
	"access_token": accessToken,
	"user": gin.H{
		"id":     user.ID,
		"name":   user.Name,
		"email":  user.Email,
		"avatar": user.Avatar,
		"role":   user.Role,
	},
})
}

// ============================================================
// ADMIN LOGIN
// ============================================================

func AdminLogin(c *gin.Context) {
	var req LoginRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":      false,
			"message": "Format JSON tidak valid",
		})
		return
	}

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))

	if req.Email == "" || req.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":      false,
			"message": "Email dan password wajib diisi",
		})
		return
	}

	// Refused before the password is looked at, so a correct guess made during a
	// lock does not reveal itself.
	if locked, wait := loginGuard.Check(req.Email, c.ClientIP()); locked {
		httpguard.TooManyRequests(c, wait)
		return
	}

	var user models.User

	result := config.DB.
		Where("email = ?", req.Email).
		First(&user)

	if result.Error != nil {
		spendPasswordCheck(req.Password)
		wrongCredentials(c, req.Email)
		return
	}

	err := bcrypt.CompareHashAndPassword(
		[]byte(user.Password),
		[]byte(req.Password),
	)

	if err != nil {
		wrongCredentials(c, req.Email)
		return
	}

	loginGuard.Success(req.Email, c.ClientIP())

	// Only after the password is right: answering earlier would tell anyone which
	// addresses belong to ordinary accounts.
	if user.Role != "admin" {
		c.JSON(http.StatusForbidden, gin.H{
			"ok":      false,
			"message": "Akun ini bukan akun admin",
		})
		return
	}

	accessToken, err := utils.GenerateAccessToken(
		user.ID,
		user.Email,
		user.Role,
	)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "Gagal membuat access token",
		})
		return
	}

	refreshToken, err := utils.GenerateRefreshToken(
		user.ID,
		user.Email,
		user.Role,
	)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "Gagal membuat refresh token",
		})
		return
	}

	refreshTokenModel := models.RefreshToken{
		UserID:    user.ID,
		TokenHash: hashToken(refreshToken),
		ExpiredAt: time.Now().Add(7 * 24 * time.Hour),
	}

	if err := config.DB.Create(&refreshTokenModel).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "Gagal menyimpan refresh token",
		})
		return
	}

	setRefreshCookie(c, adminRefresh, refreshToken, refreshCookieMaxAge)

	c.JSON(http.StatusOK, gin.H{
		"ok":           true,
		"message":      "Login admin berhasil",
		"access_token": accessToken,
		"user": gin.H{
			"id":     user.ID,
			"name":   user.Name,
			"email":  user.Email,
			"avatar": user.Avatar,
			"role":   user.Role,
		},
	})
}

// The admin console and the user app are two apps on one host, and a cookie is
// shared across ports. Each keeps its own refresh cookie, so signing in to one
// never replaces the session of the other (and an admin session can never be
// refreshed into an ordinary user's).
const (
	adminRefreshCookie = "admin_refresh_token"
	adminCookiePath    = "/api/admin"
)

type refreshCookie struct {
	name      string
	path      string
	adminOnly bool
}

var (
	userRefresh  = refreshCookie{name: "refresh_token", path: "/"}
	adminRefresh = refreshCookie{name: adminRefreshCookie, path: adminCookiePath, adminOnly: true}
)

// refreshCookieMaxAge matches the refresh token's own lifetime (seven days).
const refreshCookieMaxAge = 7 * 24 * 60 * 60

// setRefreshCookie writes (or, with maxAge -1, clears) a refresh cookie. It is
// HttpOnly so scripts never see it, Secure in production so it never travels
// over plain HTTP, and SameSite=Strict so other sites cannot make the browser
// send it: the apps call the API from the same site, so they are not affected.
func setRefreshCookie(c *gin.Context, cookie refreshCookie, value string, maxAge int) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     cookie.name,
		Value:    value,
		Path:     cookie.path,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   config.IsProduction(),
		SameSite: http.SameSiteStrictMode,
	})
}

func Refresh(c *gin.Context) { refreshSession(c, userRefresh) }

func Logout(c *gin.Context) { logoutSession(c, userRefresh) }

func AdminRefresh(c *gin.Context) { refreshSession(c, adminRefresh) }

func AdminLogout(c *gin.Context) { logoutSession(c, adminRefresh) }

func refreshSession(c *gin.Context, cookie refreshCookie) {
	refreshToken, err := c.Cookie(cookie.name)

	if err != nil || refreshToken == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":      false,
			"message": "Refresh token tidak ditemukan",
		})
		return
	}

	claims, err := utils.ParseRefreshToken(refreshToken)

	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":      false,
			"message": "Refresh token tidak valid atau sudah expired",
		})
		return
	}

	var storedToken models.RefreshToken

	result := config.DB.
		Where(
			"token_hash = ? AND user_id = ?",
			hashToken(refreshToken),
			claims.UserID,
		).
		First(&storedToken)

	if result.Error != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":      false,
			"message": "Refresh token tidak ditemukan",
		})
		return
	}

	inGrace := storedToken.ReplacedAt != nil && time.Since(*storedToken.ReplacedAt) <= refreshReuseGrace
	if storedToken.RevokedAt != nil && !inGrace {
		// A revoked token used again: someone holds a copy. End every session of this
		// account, so the copy and anything issued from it stop working, and take away
		// the grace period of tokens rotated just now.
		now := time.Now()
		config.DB.Model(&models.RefreshToken{}).
			Where("user_id = ? AND revoked_at IS NULL", storedToken.UserID).
			Update("revoked_at", now)
		config.DB.Model(&models.RefreshToken{}).
			Where("user_id = ? AND replaced_at IS NOT NULL", storedToken.UserID).
			Update("replaced_at", nil)
		log.Printf("[auth] reuse of a revoked refresh token; all sessions of user %d ended", storedToken.UserID)
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":      false,
			"message": "Refresh token sudah tidak berlaku",
		})
		return
	}

	if time.Now().After(storedToken.ExpiredAt) {
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":      false,
			"message": "Refresh token sudah expired",
		})
		return
	}

	var user models.User

	if err := config.DB.First(&user, claims.UserID).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":      false,
			"message": "Pengguna tidak ditemukan",
		})
		return
	}

	if cookie.adminOnly && user.Role != "admin" {
		c.JSON(http.StatusForbidden, gin.H{
			"ok":      false,
			"message": "Akun ini bukan akun admin",
		})
		return
	}

	if user.Role == "" {
		user.Role = "user"

		if err := config.DB.
			Model(&user).
			Update("role", "user").
			Error; err != nil {

			c.JSON(http.StatusInternalServerError, gin.H{
				"ok":      false,
				"message": "Gagal memperbarui role pengguna",
			})
			return
		}
	}

	newAccessToken, err := utils.GenerateAccessToken(
		user.ID,
		user.Email,
		user.Role,
	)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "Gagal membuat access token",
		})
		return
	}

	newRefreshToken, err := utils.GenerateRefreshToken(
		user.ID,
		user.Email,
		user.Role,
	)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "Gagal membuat refresh token",
		})
		return
	}

	// Only revokes a token that is not revoked yet: a second refresh within the
	// grace period keeps the first rotation time, so the grace does not extend.
	rotatedAt := time.Now()
	if err := config.DB.Model(&models.RefreshToken{}).
		Where("id = ? AND revoked_at IS NULL", storedToken.ID).
		Updates(map[string]any{"revoked_at": rotatedAt, "replaced_at": rotatedAt}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "Gagal memperbarui refresh token",
		})
		return
	}

	newStoredToken := models.RefreshToken{
		UserID:    user.ID,
		TokenHash: hashToken(newRefreshToken),
		ExpiredAt: time.Now().Add(7 * 24 * time.Hour),
	}

	if err := config.DB.Create(&newStoredToken).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "Gagal menyimpan refresh token baru",
		})
		return
	}

	setRefreshCookie(c, cookie, newRefreshToken, refreshCookieMaxAge)

	c.JSON(http.StatusOK, gin.H{
		"ok":           true,
		"message":      "Token berhasil diperbarui",
		"access_token": newAccessToken,
	})
}

func logoutSession(c *gin.Context, cookie refreshCookie) {
	refreshToken, err := c.Cookie(cookie.name)

	if err == nil && refreshToken != "" {
		var storedToken models.RefreshToken

		result := config.DB.
			Where(
				"token_hash = ?",
				hashToken(refreshToken),
			).
			First(&storedToken)

		if result.Error == nil &&
			storedToken.RevokedAt == nil {

			now := time.Now()
			storedToken.RevokedAt = &now

			config.DB.Save(&storedToken)
		}
	}

	setRefreshCookie(c, cookie, "", -1)

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"message": "Logout berhasil",
	})
}