package services

import "testing"

func TestIsDisposableEmail(t *testing.T) {
	t.Setenv("DISPOSABLE_EMAIL_EXTRA_DOMAINS", " NewTempBox.example , other-temp.test ")
	cases := map[string]bool{
		// Throwaway services, also with odd case, spaces, a trailing dot or a subdomain.
		"someone@mailinator.com":       true,
		"someone@10minutemail.com":     true,
		"someone@temp-mail.org":        true,
		"someone@guerrillamail.com":    true,
		"Someone@MAILINATOR.COM":       true,
		"someone@mailinator.com.":      true,
		"someone@inbox.mailinator.com": true,
		// Added by the operator.
		"someone@newtempbox.example": true,
		"someone@x.other-temp.test":  true,
		// Ordinary providers and company domains stay allowed.
		"someone@gmail.com":        false,
		"someone@yahoo.co.id":      false,
		"someone@outlook.com":      false,
		"someone@icloud.com":       false,
		"someone@proton.me":        false,
		"someone@company.co.id":    false,
		"someone@mail.example.org": false,
		// Not an address at all.
		"no-at-sign": false,
		"someone@":   false,
	}
	for email, want := range cases {
		if got := IsDisposableEmail(email); got != want {
			t.Errorf("IsDisposableEmail(%q) = %v, want %v", email, got, want)
		}
	}
}

// The list is large and loaded once; a broken file would silently allow everything.
func TestDisposableListIsLoaded(t *testing.T) {
	disposableOnce.Do(loadDisposableDomains)
	if len(disposableDomains) < 5000 {
		t.Fatalf("only %d disposable domains loaded", len(disposableDomains))
	}
	for domain := range disposableDomains {
		if domain == "" || domain[0] == '#' {
			t.Fatalf("a comment or blank line was loaded as a domain: %q", domain)
		}
	}
}
