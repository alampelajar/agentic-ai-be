package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
	"agentic-ai-backend/services"
	"agentic-ai-backend/utils"
)

const githubStateTTL = 10 * time.Minute

// githubStateCookie holds the state in the browser that started the connection. The
// callback needs both: the state says whose account it is, and the cookie proves the
// browser coming back is the one that asked. Without it, someone could send another
// person their own authorize link and get that person's GitHub (scope "repo") linked
// to their account. The cookie is only sent to the callback.
const (
	githubStateCookie     = "github_oauth_state"
	githubStateCookiePath = "/api/integrations/github/callback"
)

func setGitHubStateCookie(c *gin.Context, value string, maxAge int) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     githubStateCookie,
		Value:    value,
		Path:     githubStateCookiePath,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   config.IsProduction(),
		// Lax: sent when GitHub sends the browser back (a top-level GET), never on
		// requests other sites make in the background.
		SameSite: http.SameSiteLaxMode,
	})
}

// githubStateMatchesBrowser reports whether the state GitHub sent back is the one this
// browser was given when it started connecting.
func githubStateMatchesBrowser(c *gin.Context, state string) bool {
	cookie, err := c.Cookie(githubStateCookie)
	if err != nil || len(cookie) != len(state) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookie), []byte(state)) == 1
}

// githubConnectionUpsertColumns are the columns a reconnect overwrites. They are database column
// names, not field names: GORM turns GitHubID into git_hub_id, so a guessed "github_id" fails on a
// real database (see TestGitHubUpsertColumnsExist).
var githubConnectionUpsertColumns = []string{"git_hub_id", "git_hub_login", "token_enc", "scope", "updated_at"}

// githubOAuth is rebuilt from the environment on each use: this package is initialised before
// main loads the .env file, so a package-level value would see empty settings.
var githubOAuth = services.NewGitHubOAuth

func githubFail(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{"ok": false, "code": code, "message": message})
}

func hashGitHubState(state string) string {
	sum := sha256.Sum256([]byte(state))
	return hex.EncodeToString(sum[:])
}

// frontendIntegrationsURL is where the browser lands after GitHub sends it back.
func frontendIntegrationsURL(result string) string {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("FRONTEND_URL")), "/")
	if base == "" {
		base = "http://localhost:5173"
	}
	return base + "/settings/integrations?github=" + url.QueryEscape(result)
}

// GetGitHubConnection reports whether the user has connected GitHub. It never returns the token.
func GetGitHubConnection(c *gin.Context) {
	configured := githubOAuth().Configured()
	var connection models.GitHubConnection
	err := config.DB.Where("user_id = ?", c.GetUint("user_id")).First(&connection).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusOK, gin.H{"ok": true, "configured": configured, "connected": false})
		return
	}
	if err != nil {
		c.Error(err)
		githubFail(c, http.StatusInternalServerError, "github_failed", "Status GitHub tidak dapat dibaca.")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok": true, "configured": configured, "connected": true,
		"github_login": connection.GitHubLogin, "scope": connection.Scope, "connected_at": connection.CreatedAt,
	})
}

// StartGitHubConnect creates a one-time state and returns the GitHub authorize address. The
// browser is sent there by the frontend (a plain redirect could not carry the Authorization header).
func StartGitHubConnect(c *gin.Context) {
	oauth := githubOAuth()
	if !oauth.Configured() {
		githubFail(c, http.StatusServiceUnavailable, "github_not_configured", "Integrasi GitHub belum dikonfigurasi di server.")
		return
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		c.Error(err)
		githubFail(c, http.StatusInternalServerError, "github_failed", "Tidak dapat memulai koneksi GitHub.")
		return
	}
	state := hex.EncodeToString(raw)
	userID := c.GetUint("user_id")

	err := config.DB.Transaction(func(tx *gorm.DB) error {
		// Old attempts of this user and expired ones are not needed any more.
		if err := tx.Where("user_id = ? OR expires_at < ?", userID, time.Now()).Delete(&models.GitHubOAuthState{}).Error; err != nil {
			return err
		}
		return tx.Create(&models.GitHubOAuthState{StateHash: hashGitHubState(state), UserID: userID, ExpiresAt: time.Now().Add(githubStateTTL)}).Error
	})
	if err != nil {
		c.Error(err)
		githubFail(c, http.StatusInternalServerError, "github_failed", "Tidak dapat memulai koneksi GitHub.")
		return
	}
	setGitHubStateCookie(c, state, int(githubStateTTL/time.Second))
	c.JSON(http.StatusOK, gin.H{"ok": true, "url": oauth.AuthorizeURL(state)})
}

// GitHubCallback receives the browser from GitHub. It has no login of its own: the one-time
// state, created for a signed-in user, says whose connection this is, and the state cookie
// shows this is the browser that user started from.
func GitHubCallback(c *gin.Context) {
	back := func(result string) { c.Redirect(http.StatusFound, frontendIntegrationsURL(result)) }

	if c.Query("error") != "" {
		back("denied")
		return
	}
	code, state := c.Query("code"), c.Query("state")
	if code == "" || len(state) != 64 {
		back("error")
		return
	}
	// Checked before the state is used up, so a link sent by someone else can neither
	// connect nor spoil the attempt of the person who made it.
	if !githubStateMatchesBrowser(c, state) {
		back("error")
		return
	}
	setGitHubStateCookie(c, "", -1)

	// The state works once: taking it deletes it.
	var pending models.GitHubOAuthState
	err := config.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("state_hash = ?", hashGitHubState(state)).First(&pending).Error; err != nil {
			return err
		}
		return tx.Delete(&pending).Error
	})
	if err != nil || !time.Now().Before(pending.ExpiresAt) {
		back("error")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	oauth := githubOAuth()
	token, scope, err := oauth.Exchange(ctx, code)
	if err != nil {
		log.Printf("[github] %v", err)
		back("error")
		return
	}
	githubID, login, err := oauth.User(ctx, token)
	if err != nil {
		log.Printf("[github] %v", err)
		back("error")
		return
	}
	encrypted, err := utils.EncryptString(token)
	if err != nil {
		log.Printf("[github] could not encrypt the token")
		back("error")
		return
	}

	connection := models.GitHubConnection{UserID: pending.UserID, GitHubID: githubID, GitHubLogin: login, TokenEnc: encrypted, Scope: scope}
	err = config.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "user_id"}},
			DoUpdates: clause.AssignmentColumns(githubConnectionUpsertColumns),
		}).Create(&connection).Error; err != nil {
			return err
		}
		userID := pending.UserID
		return tx.Create(&models.AuditLog{
			ActorUserID: &userID, ActorName: login, ActorRole: "user", Action: "integration.github_connected",
			EntityType: "user", EntityID: strconv.FormatUint(uint64(userID), 10), EntityName: login,
			Message: "Pengguna menghubungkan akun GitHub \"" + login + "\".", CreatedAt: time.Now(),
		}).Error
	})
	if err != nil {
		c.Error(err)
		back("error")
		return
	}
	back("connected")
}

// DeleteGitHubConnection disconnects GitHub: the token is revoked at GitHub (best effort) and deleted here.
func DeleteGitHubConnection(c *gin.Context) {
	userID := c.GetUint("user_id")
	var connection models.GitHubConnection
	if err := config.DB.Where("user_id = ?", userID).First(&connection).Error; err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": true, "message": "GitHub tidak terhubung."})
		return
	}

	if token, err := utils.DecryptString(connection.TokenEnc); err == nil {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
		defer cancel()
		if oauth := githubOAuth(); oauth.Configured() {
			if err := oauth.Revoke(ctx, token); err != nil {
				log.Printf("[github] %v", err)
			}
		}
	}

	audit := newAuditLog(c, "integration.github_disconnected", "user", strconv.FormatUint(uint64(userID), 10), connection.GitHubLogin,
		"Pengguna memutuskan akun GitHub \""+connection.GitHubLogin+"\".")
	err := config.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&connection).Error; err != nil {
			return err
		}
		return tx.Create(&audit).Error
	})
	if err != nil {
		c.Error(err)
		githubFail(c, http.StatusInternalServerError, "github_failed", "GitHub tidak dapat diputuskan.")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "message": "GitHub diputuskan."})
}

// githubTokenFor returns the user's GitHub access token, or "" when they have not connected GitHub
// (or the stored token cannot be read). Callers fall back to what works without it.
func githubTokenFor(userID uint) string {
	var connection models.GitHubConnection
	if err := config.DB.Where("user_id = ?", userID).First(&connection).Error; err != nil {
		return ""
	}
	token, err := utils.DecryptString(connection.TokenEnc)
	if err != nil {
		log.Printf("[github] the stored token could not be read")
		return ""
	}
	return token
}
