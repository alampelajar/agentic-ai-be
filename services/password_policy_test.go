package services

import (
	"strings"
	"testing"
)

func TestPasswordPolicyRefusesWeakPasswords(t *testing.T) {
	cases := map[string]string{
		"short12":               "password_too_short",
		"ááááá":                 "password_too_short", // 10 bytes, 5 characters
		strings.Repeat("a", 73): "password_too_long",
		"password123":           "password_common",
		"Password123":           "password_common",
		"12345678":              "password_common",
		"87654321":              "password_common",
		"abcdefgh":              "password_common",
		"zzzzzzzzzz":            "password_common",
		"bismillah123":          "password_common",
		"agentic-ai-2026":       "password_personal",
		"rukmana-rahasia-2026":  "password_personal", // part of the e-mail address
		"kopi-pagi-Gunusha":     "password_personal", // a word of the name
	}
	for password, want := range cases {
		if code, _ := PasswordProblem(password, "rukmana.alam@example.test", "Gunusha Putra"); code != want {
			t.Errorf("%q: code %q, want %q", password, code, want)
		}
	}
}

func TestPasswordPolicyAcceptsPassphrases(t *testing.T) {
	for _, password := range []string{
		"kopi pagi di bandung",
		"Tujuh-Kucing-Lompat",
		"correct horse battery staple",
		"ñandú-salta-8", // characters, not bytes, are counted
		strings.Repeat("ab", 36),
	} {
		if code, message := PasswordProblem(password, "rukmana.alam@example.test", "Al"); code != "" {
			t.Errorf("%q refused: %s %s", password, code, message)
		}
	}
}
