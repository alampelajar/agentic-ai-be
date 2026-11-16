package sitebuilder

import (
	"errors"
	"regexp"
	"strings"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{1,38}[a-z0-9])$`)

// Reserved names are never given to a site: they are, or could be confused with,
// addresses of the platform itself.
var reservedSlugs = map[string]bool{
	"app": true, "api": true, "www": true, "admin": true, "mail": true, "email": true, "preview": true,
	"static": true, "assets": true, "cdn": true, "status": true, "help": true, "support": true, "docs": true,
	"blog": true, "login": true, "signin": true, "signup": true, "register": true, "account": true,
	"billing": true, "pay": true, "payment": true, "dashboard": true, "auth": true, "oauth": true,
	"sites": true, "site": true, "builder": true, "dev": true, "staging": true, "test": true, "ns1": true, "ns2": true,
	"smtp": true, "imap": true, "ftp": true, "localhost": true, "agentic": true, "security": true,
}

var (
	ErrSlugFormat   = errors.New("slug format")
	ErrSlugReserved = errors.New("slug reserved")
)

// CheckSlug validates a published address: 3 to 40 lower-case letters, digits and
// hyphens, not starting or ending with a hyphen, no double hyphen, not reserved.
func CheckSlug(slug string) error {
	if !slugPattern.MatchString(slug) || strings.Contains(slug, "--") {
		return ErrSlugFormat
	}
	// p-... names are previews and contoh-... names are the looks' examples; they may
	// share the published sites' domain.
	if reservedSlugs[slug] || strings.HasPrefix(slug, "p-") || strings.HasPrefix(slug, "contoh-") {
		return ErrSlugReserved
	}
	return nil
}

// SuggestSlug turns a site name into an address to start from.
func SuggestSlug(name string) string {
	slug := PageSlug(name)
	slug = strings.ReplaceAll(slug, "--", "-")
	if len(slug) > 40 {
		slug = strings.Trim(slug[:40], "-")
	}
	if len(slug) < 3 {
		slug = "situs-" + slug
		slug = strings.Trim(slug, "-")
	}
	return slug
}
