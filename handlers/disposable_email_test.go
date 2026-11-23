package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func postJSON(router *gin.Engine, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, request)
	return rec
}

// Sign-up and changing the address refuse throwaway e-mail before anything is
// stored or sent (there is no database here: reaching it would panic).
func TestThrowawayAddressesAreRefused(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/register/request", RequestRegisterOTP)
	router.POST("/me/email/change/request", func(c *gin.Context) { c.Set("user_id", uint(1)) }, RequestChangeEmailOTP)

	for name, rec := range map[string]*httptest.ResponseRecorder{
		"sign-up":      postJSON(router, "/register/request", `{"name":"Rina","email":"rina@mailinator.com","password":"a-long-and-good-password-77"}`),
		"subdomain":    postJSON(router, "/register/request", `{"name":"Rina","email":"rina@box.10minutemail.com","password":"a-long-and-good-password-77"}`),
		"change email": postJSON(router, "/me/email/change/request", `{"new_email":"rina@temp-mail.org","password":"whatever"}`),
		// The address from the owner's test on 2026-11-23.
		"kranove": postJSON(router, "/register/request", `{"name":"Rina","email":"namif29282@kranove.com","password":"a-long-and-good-password-77"}`),
	} {
		var body struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if rec.Code != http.StatusBadRequest || body.Code != "disposable_email" {
			t.Errorf("%s: %d %s, want 400 disposable_email", name, rec.Code, rec.Body.String())
		}
	}
}

// A domain that is not a well-known provider is refused even when no list knows it
// as a throwaway service yet.
func TestUnknownDomainsAreRefused(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("EMAIL_DOMAIN_POLICY", "")
	t.Setenv("EMAIL_ALLOWED_DOMAINS", "")
	router := gin.New()
	router.POST("/register/request", RequestRegisterOTP)
	router.POST("/me/email/change/request", func(c *gin.Context) { c.Set("user_id", uint(1)) }, RequestChangeEmailOTP)

	for name, rec := range map[string]*httptest.ResponseRecorder{
		"sign-up":      postJSON(router, "/register/request", `{"name":"Rina","email":"rina@brand-new-mailer.xyz","password":"a-long-and-good-password-77"}`),
		"change email": postJSON(router, "/me/email/change/request", `{"new_email":"rina@brand-new-mailer.xyz","password":"whatever"}`),
	} {
		var body struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if rec.Code != http.StatusBadRequest || body.Code != "email_domain_not_allowed" {
			t.Errorf("%s: %d %s, want 400 email_domain_not_allowed", name, rec.Code, rec.Body.String())
		}
	}
}
