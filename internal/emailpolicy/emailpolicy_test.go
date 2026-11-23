package emailpolicy

import "testing"

func TestCanonical(t *testing.T) {
	cases := map[string]string{
		"namif@gmail.com":            "namif@gmail.com",
		"Na.Mif@Gmail.com":           "namif@gmail.com",
		"namif+promo@gmail.com":      "namif@gmail.com",
		"n.a.m.i.f+1@googlemail.com": "namif@gmail.com",
		"rina+shop@outlook.com":      "rina@outlook.com",
		// Dots matter outside Gmail.
		"ri.na@outlook.com": "ri.na@outlook.com",
		"ri.na@yahoo.co.id": "ri.na@yahoo.co.id",
		// A leading plus is part of the name, not a tag.
		"+rina@gmail.com": "+rina@gmail.com",
		"rina@gmail.com.": "rina@gmail.com",
	}
	for in, want := range cases {
		if got := Canonical(in); got != want {
			t.Errorf("Canonical(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDomainAllowed(t *testing.T) {
	t.Setenv("EMAIL_DOMAIN_POLICY", "")
	t.Setenv("EMAIL_ALLOWED_DOMAINS", " Perusahaan.co.id ")
	allowed := []string{"a@gmail.com", "a@GOOGLEMAIL.com", "a@yahoo.co.id", "a@outlook.com", "a@hotmail.com", "a@icloud.com", "a@proton.me", "a@perusahaan.co.id"}
	refused := []string{"namif29282@kranove.com", "a@mailinator.com", "a@random-new-domain.xyz", "a@sub.perusahaan.co.id", "not-an-address", "a@"}
	for _, email := range allowed {
		if !DomainAllowed(email) {
			t.Errorf("%q was refused", email)
		}
	}
	for _, email := range refused {
		if DomainAllowed(email) {
			t.Errorf("%q was allowed", email)
		}
	}
	// Switched off by the operator: any address passes this check.
	t.Setenv("EMAIL_DOMAIN_POLICY", "open")
	if !DomainAllowed("a@random-new-domain.xyz") {
		t.Error("the open policy still refused an address")
	}
}

func TestSignupKeyIsPerMailboxAndKeyed(t *testing.T) {
	if SignupKey("namif+1@gmail.com", "s") != SignupKey("na.mif@gmail.com", "s") {
		t.Error("two spellings of one mailbox got different keys")
	}
	if SignupKey("namif@gmail.com", "s") == SignupKey("other@gmail.com", "s") {
		t.Error("two mailboxes share a key")
	}
	if SignupKey("namif@gmail.com", "s") == SignupKey("namif@gmail.com", "t") {
		t.Error("the key does not depend on the secret")
	}
	if len(SignupKey("namif@gmail.com", "s")) != 64 {
		t.Error("the key is not a sha256 hex digest")
	}
}
