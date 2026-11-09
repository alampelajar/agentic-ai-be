package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
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
	c.JSON(http.StatusOK, gin.H{"ok": true, "url": oauth.AuthorizeURL(state)})
}

// GitHubCallback receives the browser from GitHub. It has no login of its own: the one-time
// state, created for a signed-in user, says whose connection this is.
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
			DoUpdates: clause.AssignmentColumns([]string{"github_id", "github_login", "token_enc", "scope", "updated_at"}),
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
