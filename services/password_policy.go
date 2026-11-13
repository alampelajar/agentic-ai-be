package services

import (
	"strings"
	"unicode/utf8"
)

// Password rules, after NIST SP 800-63B: a minimum length counted in characters, room for
// long passphrases, no composition rules (no "one capital, one digit"), and a check
// against values that are commonly used, expected or tied to the account.
const (
	PasswordMinChars = 8
	PasswordMaxBytes = 72 // bcrypt reads no further
)

// PasswordProblem says why a new password is refused, as an error code and a message for
// the person, or "", "" when it is accepted. personal holds words tied to the account,
// such as its e-mail address and name; a password built on them is refused.
func PasswordProblem(password string, personal ...string) (code, message string) {
	if utf8.RuneCountInString(password) < PasswordMinChars {
		return "password_too_short", "Password minimal 8 karakter."
	}
	if len(password) > PasswordMaxBytes {
		return "password_too_long", "Password maksimal 72 byte."
	}
	folded := strings.ToLower(strings.TrimSpace(password))
	if commonPasswords[folded] || oneRepeatedCharacter(folded) || isSequence(folded) {
		return "password_common", "Password ini terlalu umum dan mudah ditebak. Pilih yang lain, misalnya gabungan beberapa kata."
	}
	for _, word := range personalWords(personal) {
		if strings.Contains(folded, word) {
			return "password_personal", "Password tidak boleh memuat nama, alamat email atau nama aplikasi."
		}
	}
	return "", ""
}

// personalWords are the parts of the account's details that a password must not contain:
// the name of the app, the e-mail address before the @, and each word of the name, when
// at least four characters long (shorter ones occur in ordinary words).
func personalWords(personal []string) []string {
	words := []string{"agentic"}
	for _, value := range personal {
		value = strings.ToLower(strings.TrimSpace(value))
		if at := strings.IndexByte(value, '@'); at >= 0 {
			value = value[:at]
		}
		for _, part := range strings.FieldsFunc(value, func(r rune) bool {
			return r == ' ' || r == '.' || r == '_' || r == '-' || r == '+'
		}) {
			if utf8.RuneCountInString(part) >= 4 {
				words = append(words, part)
			}
		}
	}
	return words
}

func oneRepeatedCharacter(value string) bool {
	first, size := utf8.DecodeRuneInString(value)
	return strings.Count(value, string(first))*size == len(value)
}

// isSequence is true for a run such as 12345678, 87654321 or abcdefgh.
func isSequence(value string) bool {
	if len(value) < 2 {
		return false
	}
	step := int(value[1]) - int(value[0])
	if step != 1 && step != -1 {
		return false
	}
	for i := 1; i < len(value); i++ {
		if int(value[i])-int(value[i-1]) != step {
			return false
		}
	}
	return true
}

// commonPasswords are among the most used passwords of at least eight characters, in
// global breach lists and in Indonesian ones, compared in lower case.
var commonPasswords = func() map[string]bool {
	list := []string{
		"password", "password1", "password12", "password123", "password1234", "passw0rd", "p@ssw0rd", "p@ssword",
		"12345678", "123456789", "1234567890", "0987654321", "11111111", "00000000", "12341234", "11223344",
		"123123123", "12121212", "88888888", "87654321", "123qweasd", "1q2w3e4r", "1q2w3e4r5t", "1qaz2wsx",
		"qwerty123", "qwertyuiop", "qwerty12", "qwertyui", "asdfghjk", "asdfghjkl", "zxcvbnm1", "abcd1234",
		"abc12345", "a1b2c3d4", "iloveyou", "iloveyou1", "sunshine", "princess", "football", "baseball",
		"superman", "starwars", "whatever", "trustno1", "letmein1", "welcome1", "welcome123", "monkey123",
		"dragon123", "computer", "internet", "michelle", "jennifer", "corvette", "mercedes", "liverpool",
		"chelsea1", "arsenal1", "manchester", "barcelona", "admin123", "admin1234", "administrator", "changeme",
		"default1", "secret123", "test1234", "testtest", "user1234", "login123", "guest123", "root1234",
		"bismillah", "bismillah123", "indonesia", "indonesia1", "rahasia123", "sayangku", "sayang123", "cintaku",
		"katasandi", "katasandi123", "jakarta1", "bandung1", "surabaya", "garuda123", "merdeka45", "pancasila",
		"alhamdulillah", "assalamualaikum", "masuk123", "akuganteng", "persib1933", "persija1928",
	}
	set := make(map[string]bool, len(list))
	for _, item := range list {
		set[item] = true
	}
	return set
}()
