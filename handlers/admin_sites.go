package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"agentic-ai-backend/config"
	"agentic-ai-backend/internal/sitebuilder"
	"agentic-ai-backend/models"
)

// Admin views of the website builder: who is building a website right now, what the
// agent is doing, and every website people have made, with its address.

type adminSiteOwner struct {
	ID    uint   `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type adminRunStep struct {
	Summary   string    `json:"summary"`
	Kind      string    `json:"kind"`
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updated_at"`
}

type adminActiveRun struct {
	ID         uint            `json:"id"`
	Kind       string          `json:"kind"`
	Status     string          `json:"status"`
	Request    string          `json:"request"`
	ModelCalls int             `json:"model_calls"`
	StepCount  int64           `json:"step_count"`
	Steps      []adminRunStep  `json:"steps"`
	CreatedAt  time.Time       `json:"created_at"`
	StartedAt  *time.Time      `json:"started_at"`
	Site       gin.H           `json:"site"`
	User       *adminSiteOwner `json:"user"`
}

// adminRecentSteps is how many of a run's latest steps the admin sees.
const adminRecentSteps = 5

func siteOwners(ids []uint) map[uint]*adminSiteOwner {
	owners := map[uint]*adminSiteOwner{}
	if len(ids) == 0 {
		return owners
	}
	var users []models.User
	config.DB.Unscoped().Select("id", "name", "email").Where("id IN ?", ids).Find(&users)
	for _, u := range users {
		owners[u.ID] = &adminSiteOwner{ID: u.ID, Name: u.Name, Email: u.Email}
	}
	return owners
}

// AdminGetActiveSiteRuns lists the runs that are queued or running for people (not the
// background runs that build the looks' examples), with the agent's latest steps.
func AdminGetActiveSiteRuns(c *gin.Context) {
	var runs []models.AgentRun
	if err := config.DB.
		Where("status IN ? AND background = ?", []string{models.RunQueued, models.RunRunning}, false).
		Order("created_at ASC").Limit(100).Find(&runs).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "message": "Gagal mengambil pekerjaan yang sedang berjalan."})
		return
	}

	siteIDs, userIDs := make([]uint, 0, len(runs)), make([]uint, 0, len(runs))
	for _, run := range runs {
		siteIDs = append(siteIDs, run.SiteID)
		userIDs = append(userIDs, run.UserID)
	}
	sites := map[uint]models.Site{}
	if len(siteIDs) > 0 {
		var list []models.Site
		config.DB.Where("id IN ?", siteIDs).Find(&list)
		for _, s := range list {
			sites[s.ID] = s
		}
	}
	owners := siteOwners(userIDs)

	result := make([]adminActiveRun, 0, len(runs))
	for _, run := range runs {
		var steps []models.AgentRunStep
		config.DB.Where("agent_run_id = ?", run.ID).Order("\"index\" DESC").Limit(adminRecentSteps).Find(&steps)
		var count int64
		config.DB.Model(&models.AgentRunStep{}).Where("agent_run_id = ?", run.ID).Count(&count)

		recent := make([]adminRunStep, 0, len(steps))
		for _, s := range steps {
			recent = append(recent, adminRunStep{Summary: s.Summary, Kind: s.Kind, Status: s.Status, UpdatedAt: s.UpdatedAt})
		}
		site := sites[run.SiteID]
		result = append(result, adminActiveRun{
			ID: run.ID, Kind: run.Kind, Status: run.Status, Request: run.Request, ModelCalls: run.ModelCalls,
			StepCount: count, Steps: recent, CreatedAt: run.CreatedAt, StartedAt: run.StartedAt,
			Site: gin.H{"id": site.ID, "name": site.Name}, User: owners[run.UserID],
		})
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "runs": result})
}

// AdminGetSites lists every website, newest change first, with its owner and address.
func AdminGetSites(c *gin.Context) {
	var sites []models.Site
	if err := config.DB.Order("updated_at DESC").Limit(500).Find(&sites).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "message": "Gagal mengambil daftar website."})
		return
	}

	userIDs := make([]uint, 0, len(sites))
	for _, s := range sites {
		userIDs = append(userIDs, s.UserID)
	}
	owners := siteOwners(userIDs)
	settings := sitebuilder.LoadSettings()

	type versionCount struct {
		SiteID uint
		Count  int64
	}
	var counts []versionCount
	config.DB.Model(&models.SiteVersion{}).Select("site_id, count(*) as count").Group("site_id").Scan(&counts)
	versions := map[uint]int64{}
	for _, vc := range counts {
		versions[vc.SiteID] = vc.Count
	}

	versionNumbers, releases := siteVersionNumbers(sites)

	list := make([]gin.H, 0, len(sites))
	for _, s := range sites {
		liveURL, slug := "", ""
		if s.Slug != nil {
			slug = *s.Slug
		}
		if s.PublishedReleaseID != nil && s.Slug != nil {
			liveURL = settings.LiveURL(*s.Slug)
		}
		currentVersion, publishedVersion := 0, 0
		var publishedAt *time.Time
		if s.CurrentVersionID != nil {
			currentVersion = versionNumbers[*s.CurrentVersionID]
		}
		if s.PublishedReleaseID != nil {
			if release, found := releases[*s.PublishedReleaseID]; found {
				publishedVersion = versionNumbers[release.SiteVersionID]
				at := release.PublishedAt
				publishedAt = &at
			}
		}
		suggested := slug
		if suggested == "" {
			suggested = sitebuilder.SuggestSlug(s.Name)
		}
		list = append(list, gin.H{
			"id": s.ID, "name": s.Name, "description": s.Description, "status": s.Status,
			"kind": s.Kind, "design_direction": s.DesignDirection,
			"has_version": s.CurrentVersionID != nil, "versions": versions[s.ID],
			"current_version": currentVersion, "published_version": publishedVersion, "published_at": publishedAt,
			"slug": slug, "suggested_slug": suggested,
			"published": liveURL != "", "live_url": liveURL, "showcased": s.ShowcasedAt != nil,
			"created_at": s.CreatedAt, "updated_at": s.UpdatedAt, "user": owners[s.UserID],
		})
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "sites": list})
}

// AdminSitePreview gives an admin a signed preview address of any website that has a
// version, and keeps the preview running. Opening it is recorded in the audit log.
func AdminSitePreview(c *gin.Context) {
	siteID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "message": "ID website tidak valid."})
		return
	}
	var site models.Site
	if err := config.DB.First(&site, siteID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "message": "Website tidak ditemukan."})
		return
	}
	if site.CurrentVersionID == nil {
		c.JSON(http.StatusConflict, gin.H{"ok": false, "message": "Website belum punya versi untuk ditampilkan."})
		return
	}
	now := time.Now()
	config.DB.Model(&site).UpdateColumn("preview_requested_at", now)
	url, expires := sitebuilder.LoadSettings().PreviewURL(site.ID, now)
	recordAudit(c, "site.preview", "site", strconv.FormatUint(siteID, 10), site.Name, "Admin membuka pratinjau website")
	c.JSON(http.StatusOK, gin.H{"ok": true, "url": url, "expires_at": expires})
}
