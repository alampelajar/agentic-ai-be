package services

import "regexp"

const RedactedValue = "[REDACTED]"

var redactionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{8,}`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]*`),
	regexp.MustCompile(`\b(?:sk|pk|rk)-[A-Za-z0-9_-]{16,}`),
	regexp.MustCompile(`\b(?:ghp|gho|ghs|ghu|github_pat)_[A-Za-z0-9_]{16,}`),
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{30,}`),
	regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`),
	regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`),
	regexp.MustCompile(`(?i)\b(?:postgres(?:ql)?|mysql|mongodb(?:\+srv)?|redis|amqp)://[^\s:/@]+:[^\s@]+@`),
}

var redactionAssignment = regexp.MustCompile(`(?i)\b(password|passwd|pwd|secret|token|api[_-]?key|otp|authorization)\b(\s*[=:]\s*)("[^"]*"|'[^']*'|\S+)`)

// RedactSecrets hides credentials in free text such as command output. It is
// deliberately blunt: a false positive costs a few characters, a miss leaks a key.
func RedactSecrets(text string) string {
	for _, pattern := range redactionPatterns {
		text = pattern.ReplaceAllString(text, RedactedValue)
	}
	return redactionAssignment.ReplaceAllString(text, "${1}${2}"+RedactedValue)
}
