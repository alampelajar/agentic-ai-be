package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"agentic-ai-backend/config"
	"agentic-ai-backend/internal/sitebuilder"
	"agentic-ai-backend/models"

	"github.com/gin-gonic/gin"
)

// The builder never makes a gambling, betting or pornographic website
// (sitebuilder/forbidden.go). Every text a person writes is checked before any model
// is asked: the description, the answers, a new name, every change request and the
// address. A site found forbidden, by the word list or the planner's own judgement,
// is marked (Kind "forbidden-<topic>") and goes no further. Every refusal is in the
// audit log as "site.refused", which the console's Logs page shows.

// forbiddenKindPrefix marks a site found forbidden; the topic follows it.
const forbiddenKindPrefix = "forbidden-"

var forbiddenTopicNames = map[string]string{
	sitebuilder.TopicGambling: "judi atau taruhan",
	sitebuilder.TopicAdult:    "pornografi atau konten dewasa",
}

// siteForbiddenTopic is the forbidden topic of a site and the texts given with this
// request, or "" when there is none.
func siteForbiddenTopic(site *models.Site, texts ...string) string {
	if site != nil && strings.HasPrefix(site.Kind, forbiddenKindPrefix) {
		return strings.TrimPrefix(site.Kind, forbiddenKindPrefix)
	}
	if site != nil {
		texts = append(texts, site.Description, site.Name)
	}
	return sitebuilder.ForbiddenTopic(texts...)
}

// refuseForbidden answers that the builder does not make this website and records
// the attempt. site may be nil (a description refused before a site exists).
func refuseForbidden(c *gin.Context, topic string, site *models.Site, text string) {
	entityID, name := "", ""
	if site != nil {
		entityID, name = strconv.FormatUint(uint64(site.ID), 10), site.Name
		if kind := forbiddenKindPrefix + topic; site.Kind != kind {
			config.DB.Model(&models.Site{}).Where("id = ?", site.ID).Update("kind", kind)
			site.Kind = kind
		}
	}
	what := forbiddenTopicNames[topic]
	if what == "" {
		what = "konten terlarang"
	}
	recordAudit(c, "site.refused", "site", entityID, name, "Ditolak ("+what+"): "+clipRunes(strings.Join(strings.Fields(text), " "), 200))
	c.JSON(http.StatusUnprocessableEntity, gin.H{
		"ok": false, "code": "forbidden_topic", "topic": topic,
		"error": "Kami tidak membuat website untuk " + what + ". Hal ini dilarang.",
	})
}

// answerTexts is everything a person typed into the answers, for the check.
func answerTexts(answers map[string][]string, extra ...string) []string {
	texts := append([]string(nil), extra...)
	for _, values := range answers {
		texts = append(texts, values...)
	}
	return texts
}
