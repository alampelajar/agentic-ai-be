package services

import (
	"regexp"
	"strings"

	"agentic-ai-backend/models"
	"gorm.io/gorm"
)

// skillCommandToken matches one leading "/slug" the way a skill's manifest id is
// written (lowercase letters, digits and hyphens). It must be followed by
// whitespace or the end of the message, so a path such as "/etc/hosts" is not a
// command.
var skillCommandToken = regexp.MustCompile(`^/([a-z0-9][a-z0-9-]{1,99})(?:\s+|$)`)

// ParseSkillCommands splits the leading "/slug" tokens off a message. Only the
// start of the message counts: a slash later in the text is ordinary text.
func ParseSkillCommands(message string) (slugs []string, rest string) {
	rest = strings.TrimLeft(message, " \t\r\n")
	for {
		match := skillCommandToken.FindStringSubmatch(rest)
		if match == nil {
			return slugs, rest
		}
		slugs = append(slugs, match[1])
		rest = rest[len(match[0]):]
	}
}

// SkillCommandResult is what the chat needs after reading a message's commands.
type SkillCommandResult struct {
	// Skills are the invoked skills the user may use right now, in the order typed.
	Skills []ActiveSkillContext
	// Message is the text to send on: the message without the commands that were
	// understood. A command that named no skill of the user stays in the text.
	Message string
	// Unusable are slugs of the user's own skills that cannot run (disabled,
	// not installed, or no valid version).
	Unusable []string
}

// ResolveSkillCommands reads the leading "/slug" commands of a message and
// looks each one up among the caller's own skills. A user can only invoke
// their own skills; what an invoked skill may do is still limited to its
// validated tool allowlist, and mutating tools still need approval.
func ResolveSkillCommands(db *gorm.DB, userID uint, message string) (SkillCommandResult, error) {
	slugs, rest := ParseSkillCommands(message)
	result := SkillCommandResult{Message: strings.TrimSpace(message)}
	if len(slugs) == 0 {
		return result, nil
	}

	var skills []models.Skill
	err := db.Where("user_id = ? AND slug IN ?", userID, slugs).
		Preload("Versions", func(tx *gorm.DB) *gorm.DB { return tx.Order("created_at DESC, id DESC") }).
		Find(&skills).Error
	if err != nil {
		return result, err
	}
	bySlug := make(map[string]models.Skill, len(skills))
	for _, skill := range skills {
		bySlug[skill.Slug] = skill
	}

	seen := map[uint]bool{}
	for i, slug := range slugs {
		skill, known := bySlug[slug]
		if !known {
			// Not a skill of this user: keep this command and the ones after it as text.
			restored := make([]string, 0, len(slugs)-i)
			for _, left := range slugs[i:] {
				restored = append(restored, "/"+left)
			}
			if rest != "" {
				restored = append(restored, rest)
			}
			rest = strings.Join(restored, " ")
			break
		}
		ctx, usable := skillContextFor(userID, skill)
		if !usable {
			result.Unusable = append(result.Unusable, slug)
			continue
		}
		if !seen[ctx.SkillID] {
			seen[ctx.SkillID] = true
			result.Skills = append(result.Skills, ctx)
		}
	}
	result.Message = strings.TrimSpace(rest)
	return result, nil
}

// MergeInvokedSkills adds the invoked skills to the agent's assigned ones,
// without listing a skill twice.
func MergeInvokedSkills(assigned, invoked []ActiveSkillContext) []ActiveSkillContext {
	if len(invoked) == 0 {
		return assigned
	}
	merged := append([]ActiveSkillContext{}, assigned...)
	have := map[uint]bool{}
	for _, skill := range assigned {
		have[skill.SkillID] = true
	}
	for _, skill := range invoked {
		if !have[skill.SkillID] {
			merged = append(merged, skill)
		}
	}
	return merged
}

// RenderInvokedSkillNotice tells the model which skills the user chose for this
// message with a slash command. It is an instruction to apply those skills to
// the request, not a change to permissions.
func RenderInvokedSkillNotice(invoked []ActiveSkillContext) string {
	if len(invoked) == 0 {
		return ""
	}
	names := make([]string, 0, len(invoked))
	for _, skill := range invoked {
		names = append(names, skill.Name)
	}
	return "The user invoked these skills for this message with a slash command: " + strings.Join(names, ", ") + ". Apply their instructions to the request. Their tool allowlists, and the approval required for mutating tools, are unchanged."
}
