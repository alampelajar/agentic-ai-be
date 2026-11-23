// Package emailpolicy decides which e-mail addresses may open an account, and gives
// each address the canonical form used to recognise the same mailbox written in
// different ways. It has no dependencies, so models, config and handlers can all use it.
package emailpolicy

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
)

// wellKnownDomains are the large public mail providers people actually use. New
// accounts are limited to these (see DomainAllowed): a throwaway service can be made
// in minutes on any domain, so a blocklist alone always lags behind.
var wellKnownDomains = map[string]bool{
	// Google
	"gmail.com": true, "googlemail.com": true,
	// Yahoo
	"yahoo.com": true, "yahoo.co.id": true, "yahoo.co.uk": true, "yahoo.com.sg": true, "yahoo.com.my": true,
	"ymail.com": true, "rocketmail.com": true,
	// Microsoft
	"outlook.com": true, "outlook.co.id": true, "hotmail.com": true, "hotmail.co.uk": true, "live.com": true, "msn.com": true,
	// Apple
	"icloud.com": true, "me.com": true, "mac.com": true,
	// Proton
	"proton.me": true, "protonmail.com": true, "pm.me": true,
	// Others with a long record
	"aol.com": true, "zoho.com": true, "yandex.com": true, "gmx.com": true, "gmx.net": true, "mail.com": true,
	"tutanota.com": true, "tuta.io": true, "fastmail.com": true,
}

// domainAliases are domains that deliver to the same mailboxes as another.
var domainAliases = map[string]string{"googlemail.com": "gmail.com"}

// ignoresDots are providers where dots in the name do not matter (j.ane = jane).
var ignoresDots = map[string]bool{"gmail.com": true}

func split(email string) (local, domain string, ok bool) {
	email = strings.ToLower(strings.TrimSpace(email))
	at := strings.LastIndex(email, "@")
	if at <= 0 || at == len(email)-1 {
		return "", "", false
	}
	return email[:at], strings.TrimSuffix(email[at+1:], "."), true
}

func listFromEnv(name string) map[string]bool {
	set := map[string]bool{}
	for _, item := range strings.Split(os.Getenv(name), ",") {
		if item = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(item)), "."); item != "" {
			set[item] = true
		}
	}
	return set
}

// Open reports whether the domain limit is switched off (EMAIL_DOMAIN_POLICY=open),
// for a deployment that wants any address that is not a throwaway one.
func Open() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("EMAIL_DOMAIN_POLICY")), "open")
}

// DomainAllowed reports whether a new account may use this address: its domain is a
// well-known provider, or one the operator lists in EMAIL_ALLOWED_DOMAINS (a company
// domain, say). Accounts that already exist are not affected.
func DomainAllowed(email string) bool {
	if Open() {
		return true
	}
	_, domain, ok := split(email)
	if !ok {
		return false
	}
	return wellKnownDomains[domain] || listFromEnv("EMAIL_ALLOWED_DOMAINS")[domain]
}

// Canonical is the address in the form that identifies the mailbox: lower case, the
// "+anything" tag removed, a domain alias replaced, and for Gmail the dots removed.
// namif+1@gmail.com, Na.Mif@googlemail.com and namif@gmail.com all give
// namif@gmail.com, so one mailbox cannot open several accounts.
func Canonical(email string) string {
	local, domain, ok := split(email)
	if !ok {
		return strings.ToLower(strings.TrimSpace(email))
	}
	if alias, found := domainAliases[domain]; found {
		domain = alias
	}
	if plus := strings.Index(local, "+"); plus > 0 {
		local = local[:plus]
	}
	if ignoresDots[domain] {
		local = strings.ReplaceAll(local, ".", "")
	}
	return local + "@" + domain
}

// SignupKey is the canonical address as a keyed hash, for the record of how many
// accounts an address has opened. The record outlives deleted accounts, so it keeps
// no readable address. secret is a server-side secret (the OTP pepper).
func SignupKey(email, secret string) string {
	mac := hmac.New(sha256.New, []byte("email-signup:"+secret))
	mac.Write([]byte(Canonical(email)))
	return hex.EncodeToString(mac.Sum(nil))
}

// Secret is the server-side secret for SignupKey: OTP_PEPPER, or PASSWORD_RESET_PEPPER,
// the same one the e-mail codes use. Changing it starts the signup record afresh.
func Secret() string {
	for _, name := range []string{"OTP_PEPPER", "PASSWORD_RESET_PEPPER"} {
		if value := strings.TrimSpace(os.Getenv(name)); len(value) >= 32 {
			return value
		}
	}
	return ""
}
