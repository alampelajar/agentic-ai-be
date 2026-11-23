package services

import (
	_ "embed"
	"os"
	"strings"
	"sync"
)

// disposableDomainList is the list of throwaway e-mail services (10minutemail,
// mailinator, temp-mail and thousands more) that sign-up refuses. See the file's
// header for its source and how to update it.
//
//go:embed disposable_domains.txt
var disposableDomainList string

var (
	disposableOnce    sync.Once
	disposableDomains map[string]bool
)

func loadDisposableDomains() {
	disposableDomains = make(map[string]bool, 12000)
	for _, line := range strings.Split(disposableDomainList, "\n") {
		if domain := normalizeDomain(line); domain != "" && !strings.HasPrefix(domain, "#") {
			disposableDomains[domain] = true
		}
	}
}

func normalizeDomain(value string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
}

// extraDisposableDomains are domains an operator adds without a new release, in
// DISPOSABLE_EMAIL_EXTRA_DOMAINS (comma separated): new throwaway services appear
// faster than the list is updated.
func extraDisposableDomains() map[string]bool {
	extra := map[string]bool{}
	for _, item := range strings.Split(os.Getenv("DISPOSABLE_EMAIL_EXTRA_DOMAINS"), ",") {
		if domain := normalizeDomain(item); domain != "" {
			extra[domain] = true
		}
	}
	return extra
}

// IsDisposableEmail reports whether an address belongs to a throwaway e-mail
// service. A subdomain counts as its parent (anything.mailinator.com is
// mailinator.com), since many such services hand out subdomains freely.
func IsDisposableEmail(email string) bool {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return false
	}
	domain := normalizeDomain(email[at+1:])
	if domain == "" {
		return false
	}
	disposableOnce.Do(loadDisposableDomains)
	extra := extraDisposableDomains()
	for {
		if disposableDomains[domain] || extra[domain] {
			return true
		}
		dot := strings.Index(domain, ".")
		// Stop before the bare top-level domain: "com" itself is never listed.
		if dot < 0 || !strings.Contains(domain[dot+1:], ".") {
			return false
		}
		domain = domain[dot+1:]
	}
}

// DisposableEmailCode and DisposableEmailMessage are the answer given when a
// throwaway address is refused; the app translates the code.
const (
	DisposableEmailCode    = "disposable_email"
	DisposableEmailMessage = "Email sementara tidak bisa dipakai. Gunakan email yang biasa Anda pakai."
)
