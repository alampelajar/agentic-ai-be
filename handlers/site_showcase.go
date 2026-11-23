package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"agentic-ai-backend/config"
	"agentic-ai-backend/internal/sitebuilder"
	"agentic-ai-backend/models"
)

// The gallery: published websites an admin chose, shown to every person in the
// builder. A person can open one and start a new website with its look and page list
// ("Tiru"). The owner's description, contact details and answers are never shown or
// copied; the published website itself is public already.

// showcaseLimit is how many websites the gallery shows.
const showcaseLimit = 30

// showcasedSite finds a website that is in the gallery now: chosen by an admin and
// still published.
// ownerNotInDeletion leaves out websites whose owner's account waits to be deleted: they
// are offline (the site runner skips them) and must not show in the gallery either.
const ownerNotInDeletion = "user_id IN (SELECT id FROM users WHERE deleted_at IS NULL)"

func showcasedSite(id uint) (*models.Site, bool) {
	var site models.Site
	if config.DB.Where("id = ? AND showcased_at IS NOT NULL AND published_release_id IS NOT NULL", id).
		Where(ownerNotInDeletion).First(&site).Error != nil {
		return nil, false
	}
	return &site, true
}

// briefPageTitles returns the menu labels of a website's pages, in order.
func briefPageTitles(site *models.Site) []string {
	var brief sitebuilder.Brief
	if site.BriefJSON == "" || json.Unmarshal([]byte(site.BriefJSON), &brief) != nil {
		return nil
	}
	titles := make([]string, 0, len(brief.Pages))
	for _, page := range brief.Pages {
		titles = append(titles, page.Title)
	}
	return titles
}

// GetSiteShowcase lists the gallery, newest choice first. lang (id or en) is the
// interface's language: it picks the looks' names, and only websites written in that
// language are shown, so a visitor reads examples in their own language. When none
// is written in it yet, every website is shown rather than an empty gallery.
func GetSiteShowcase(c *gin.Context) {
	language := siteLanguage(c.Query("lang"))
	showcased := func() *gorm.DB {
		return config.DB.Where("showcased_at IS NOT NULL AND published_release_id IS NOT NULL AND slug IS NOT NULL").
			Where(ownerNotInDeletion).Order("showcased_at DESC").Limit(showcaseLimit)
	}
	var sites []models.Site
	showcased().Where("language = ?", language).Find(&sites)
	if len(sites) == 0 {
		showcased().Find(&sites)
	}
	settings := sitebuilder.LoadSettings()
	list := make([]gin.H, 0, len(sites))
	for index := range sites {
		site := &sites[index]
		look := ""
		if direction, found := sitebuilder.DirectionByKey(site.DesignDirection); found {
			look = direction.Name[language]
		}
		list = append(list, gin.H{
			"id": site.ID, "name": site.Name, "kind": site.Kind, "look": look,
			"pages": briefPageTitles(site), "live_url": settings.LiveURL(*site.Slug),
		})
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "sites": list})
}

// AdminSetSiteShowcase puts a published website in the gallery, or takes it out.
func AdminSetSiteShowcase(c *gin.Context) {
	siteID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "message": "ID website tidak valid."})
		return
	}
	var req struct {
		Showcased *bool `json:"showcased"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Showcased == nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "message": "Format permintaan tidak valid."})
		return
	}
	var site models.Site
	if err := config.DB.First(&site, siteID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "message": "Website tidak ditemukan."})
		return
	}
	id := strconv.FormatUint(siteID, 10)
	if !*req.Showcased {
		config.DB.Model(&site).UpdateColumn("showcased_at", nil)
		recordAudit(c, "site.unshowcase", "site", id, site.Name, "Website dikeluarkan dari galeri")
		c.JSON(http.StatusOK, gin.H{"ok": true, "showcased": false})
		return
	}
	if site.PublishedReleaseID == nil {
		c.JSON(http.StatusConflict, gin.H{"ok": false, "message": "Hanya website yang sudah diterbitkan yang bisa masuk galeri."})
		return
	}
	config.DB.Model(&site).UpdateColumn("showcased_at", time.Now())
	recordAudit(c, "site.showcase", "site", id, site.Name, "Website dimasukkan ke galeri")
	c.JSON(http.StatusOK, gin.H{"ok": true, "showcased": true})
}
