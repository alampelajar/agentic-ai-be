package handlers

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
)

// Admins manage any website from the console's Websites page: publish it (at a new
// address or with its newest version), rename it, take it offline, or delete it. The
// Logs page shows when websites were made, published and deleted.

// siteVersionNumbers returns the number of every version the sites point at (current
// and published), and the sites' published releases by id.
func siteVersionNumbers(sites []models.Site) (map[uint]int, map[uint]models.SiteRelease) {
	versionIDs, releaseIDs := []uint{}, []uint{}
	for _, s := range sites {
		if s.CurrentVersionID != nil {
			versionIDs = append(versionIDs, *s.CurrentVersionID)
		}
		if s.PublishedReleaseID != nil {
			releaseIDs = append(releaseIDs, *s.PublishedReleaseID)
		}
	}
	releases := map[uint]models.SiteRelease{}
	if len(releaseIDs) > 0 {
		var list []models.SiteRelease
		config.DB.Where("id IN ?", releaseIDs).Find(&list)
		for _, r := range list {
			releases[r.ID] = r
			versionIDs = append(versionIDs, r.SiteVersionID)
		}
	}
	numbers := map[uint]int{}
	if len(versionIDs) > 0 {
		var list []models.SiteVersion
		config.DB.Select("id", "number").Where("id IN ?", versionIDs).Find(&list)
		for _, v := range list {
			numbers[v.ID] = v.Number
		}
	}
	return numbers, releases
}

// adminSite finds the website named by the :id parameter, or answers 400 or 404.
func adminSite(c *gin.Context) (*models.Site, bool) {
	siteID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "message": "ID website tidak valid."})
		return nil, false
	}
	var site models.Site
	if err := config.DB.First(&site, siteID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "message": "Website tidak ditemukan."})
		return nil, false
	}
	return &site, true
}

func siteIDText(site *models.Site) string { return strconv.FormatUint(uint64(site.ID), 10) }

// AdminUpdateSite renames any website.
func AdminUpdateSite(c *gin.Context) {
	site, ok := adminSite(c)
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	name := ""
	if err := c.ShouldBindJSON(&req); err == nil {
		name = strings.TrimSpace(req.Name)
	}
	if name == "" || utf8.RuneCountInString(name) > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "message": "Nama harus diisi, paling banyak 100 karakter."})
		return
	}
	old := site.Name
	config.DB.Model(site).Update("name", name)
	recordAudit(c, "site.rename", "site", siteIDText(site), name, fmt.Sprintf("Admin mengganti nama website dari %q", old))
	c.JSON(http.StatusOK, gin.H{"ok": true, "name": name})
}

// adminSlugMessages explains why an address cannot be used.
var adminSlugMessages = map[string]string{
	"slug_format":   "Alamat hanya boleh huruf kecil, angka dan tanda hubung, 3 sampai 40 karakter.",
	"slug_reserved": "Alamat ini dipakai sistem.",
	"slug_taken":    "Alamat ini sudah dipakai website lain.",
}

// AdminPublishSite publishes the current version of any website at the address given,
// or at the one it has. It puts a website online, moves it to another address, or
// publishes its newest version.
func AdminPublishSite(c *gin.Context) {
	site, ok := adminSite(c)
	if !ok {
		return
	}
	if site.CurrentVersionID == nil {
		c.JSON(http.StatusConflict, gin.H{"ok": false, "message": "Website belum punya versi untuk diterbitkan."})
		return
	}
	var req struct {
		Slug string `json:"slug"`
	}
	_ = c.ShouldBindJSON(&req)
	slug := req.Slug
	if strings.TrimSpace(slug) == "" && site.Slug != nil {
		slug = *site.Slug
	}
	release, url, problem := publishSiteVersion(site, slug)
	if problem != "" {
		c.JSON(http.StatusConflict, gin.H{"ok": false, "code": problem, "message": adminSlugMessages[problem]})
		return
	}
	recordAudit(c, "site.publish", "site", siteIDText(site), site.Name, fmt.Sprintf("Admin menerbitkan rilis %d di %s", release.Number, url))
	c.JSON(http.StatusOK, gin.H{"ok": true, "url": url, "release": release})
}

// AdminUnpublishSite takes any website offline (WEBSITE-BUILDER.md, section 11, rule 8).
// Its address stays reserved for it, and it leaves the gallery, which shows published
// websites only.
func AdminUnpublishSite(c *gin.Context) {
	site, ok := adminSite(c)
	if !ok {
		return
	}
	if site.PublishedReleaseID == nil {
		c.JSON(http.StatusConflict, gin.H{"ok": false, "message": "Website ini tidak sedang diterbitkan."})
		return
	}
	unpublishSiteRelease(site)
	recordAudit(c, "site.unpublish", "site", siteIDText(site), site.Name, "Admin menghentikan website")
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// AdminDeleteSite deletes any website with its versions, data and workspace.
func AdminDeleteSite(c *gin.Context) {
	site, ok := adminSite(c)
	if !ok {
		return
	}
	if err := removeSite(site); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "message": "Website gagal dihapus."})
		return
	}
	recordAudit(c, "site.delete", "site", siteIDText(site), site.Name, "Website dihapus oleh admin")
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// siteLogActions are the events the console's Logs page shows: a website was made,
// published, deleted, or refused as forbidden (gambling, pornography).
var siteLogActions = []string{"site.create", "site.publish", "site.delete", "site.refused"}

// siteLogLimit is how many events the Logs page shows.
const siteLogLimit = 500

type siteLogEntry struct {
	ID        string          `json:"id"`
	Action    string          `json:"action"`
	Time      time.Time       `json:"time"`
	SiteID    string          `json:"site_id"`
	SiteName  string          `json:"site_name"`
	Message   string          `json:"message"`
	Actor     *adminSiteOwner `json:"actor"`
	ActorRole string          `json:"actor_role"`
}

// AdminGetSiteLogs lists when websites were made, published and deleted, newest first.
// A website made before making was recorded gets its event from the website itself.
func AdminGetSiteLogs(c *gin.Context) {
	var audits []models.AuditLog
	if err := config.DB.Where("action IN ?", siteLogActions).Order("created_at DESC").Limit(siteLogLimit).Find(&audits).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "message": "Gagal mengambil log website."})
		return
	}
	entries := make([]siteLogEntry, 0, len(audits))
	for _, a := range audits {
		var actor *adminSiteOwner
		if a.ActorUserID != nil || a.ActorEmail != "" {
			actor = &adminSiteOwner{Name: a.ActorName, Email: a.ActorEmail}
			if a.ActorUserID != nil {
				actor.ID = *a.ActorUserID
			}
		}
		entries = append(entries, siteLogEntry{
			ID: "audit-" + strconv.FormatUint(uint64(a.ID), 10), Action: a.Action, Time: a.CreatedAt,
			SiteID: a.EntityID, SiteName: a.EntityName, Message: a.Message, Actor: actor, ActorRole: a.ActorRole,
		})
	}

	recorded := map[string]bool{}
	var createdIDs []string
	config.DB.Model(&models.AuditLog{}).Where("action = ?", "site.create").Pluck("entity_id", &createdIDs)
	for _, id := range createdIDs {
		recorded[id] = true
	}
	var sites []models.Site
	config.DB.Select("id", "name", "user_id", "created_at").Order("created_at DESC").Limit(siteLogLimit).Find(&sites)
	missing, userIDs := []models.Site{}, []uint{}
	for _, s := range sites {
		if !recorded[siteIDText(&s)] {
			missing = append(missing, s)
			userIDs = append(userIDs, s.UserID)
		}
	}
	owners := siteOwners(userIDs)
	for _, s := range missing {
		entries = append(entries, siteLogEntry{
			ID: "site-" + siteIDText(&s), Action: "site.create", Time: s.CreatedAt,
			SiteID: siteIDText(&s), SiteName: s.Name, Message: "Website dibuat", Actor: owners[s.UserID], ActorRole: "user",
		})
	}

	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Time.After(entries[j].Time) })
	if len(entries) > siteLogLimit {
		entries = entries[:siteLogLimit]
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "logs": entries})
}
