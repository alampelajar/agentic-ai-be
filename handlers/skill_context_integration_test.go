package handlers

import (
	"fmt"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
	"agentic-ai-backend/services"
)

var fixtureSeq atomic.Int32

type skillFixture struct {
	db     *gorm.DB
	suffix string
	owner  models.User
	other  models.User
	agent  models.Agent // shared by everyone
	second models.Agent
}

func newSkillFixture(t *testing.T) *skillFixture {
	t.Helper()
	dsn := os.Getenv("SKILLS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SKILLS_TEST_DATABASE_URL is not configured")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	// Close the pool when the test ends: the package opens one per fixture and PostgreSQL has a connection cap.
	t.Cleanup(func() {
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	config.DB = db
	if err := migrateAll(db); err != nil {
		t.Fatal(err)
	}
	f := &skillFixture{db: db, suffix: strconv.Itoa(os.Getpid()) + "-" + strconv.Itoa(int(fixtureSeq.Add(1)))}
	f.owner = models.User{Name: "Context Owner", Email: "ctx-owner-" + f.suffix + "@example.test", Password: "not-used"}
	f.other = models.User{Name: "Context Other", Email: "ctx-other-" + f.suffix + "@example.test", Password: "not-used"}
	f.agent = models.Agent{Slug: "ctx-agent-" + f.suffix, Name: "Context Agent", Status: "ready", IsActive: true}
	f.second = models.Agent{Slug: "ctx-second-" + f.suffix, Name: "Second Context Agent", Status: "ready", IsActive: true}
	for _, row := range []any{&f.owner, &f.other, &f.agent, &f.second} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		ids := []uint{f.owner.ID, f.other.ID}
		db.Unscoped().Where("user_id IN ?", ids).Delete(&models.AgentSkill{})
		db.Unscoped().Where("user_id IN ?", ids).Delete(&models.Skill{})
		db.Unscoped().Delete(&f.agent)
		db.Unscoped().Delete(&f.second)
		db.Unscoped().Delete(&f.owner)
		db.Unscoped().Delete(&f.other)
	})
	return f
}

// skill stores a skill with one version. It is enabled and valid unless the
// options say otherwise.
func (f *skillFixture) skill(t *testing.T, owner uint, slug string, tools string, mutate func(*models.Skill, *models.SkillVersion)) models.Skill {
	t.Helper()
	skill := models.Skill{UserID: owner, Slug: slug + "-" + f.suffix, Name: "Skill " + slug, Description: slug, Source: "created", Status: "enabled", IsEnabled: true}
	version := models.SkillVersion{Version: "1.0.0", Manifest: "{}", Instructions: "Instructions of " + slug, RequestedTools: tools, RequestedPermissions: "[]", PackageSHA256: "test", ValidationStatus: "valid"}
	if mutate != nil {
		mutate(&skill, &version)
	}
	skill.Versions = []models.SkillVersion{version}
	if err := f.db.Create(&skill).Error; err != nil {
		t.Fatal(err)
	}
	return skill
}

func (f *skillFixture) assign(t *testing.T, user, agent, skill uint, enabled bool) {
	t.Helper()
	row := models.AgentSkill{UserID: user, AgentID: agent, SkillID: skill, IsEnabled: true}
	if err := f.db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if !enabled {
		if err := f.db.Model(&row).Update("is_enabled", false).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func loadedNames(skills []services.ActiveSkillContext) []string {
	names := []string{}
	for _, skill := range skills {
		names = append(names, skill.Name)
	}
	return names
}

func TestAgentLoadsEverySkillItHasActive(t *testing.T) {
	f := newSkillFixture(t)
	reader := f.skill(t, f.owner.ID, "reader", `["desktop.status","desktop.inspect"]`, nil)
	desktop := f.skill(t, f.owner.ID, "desktop", `["desktop.status","desktop.windows"]`, nil)
	writer := f.skill(t, f.owner.ID, "writer", `["desktop.type"]`, nil)
	for _, skill := range []models.Skill{reader, desktop, writer} {
		f.assign(t, f.owner.ID, f.agent.ID, skill.ID, true)
	}

	loaded, err := services.LoadActiveAgentSkills(f.db, f.owner.ID, f.agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 3 {
		t.Fatalf("an agent with three active skills loaded %d: %v", len(loaded), loadedNames(loaded))
	}

	// The allowlist is what all of them grant together, each tool once.
	tools := services.UnionSkillTools(loaded)
	sort.Strings(tools)
	if want := []string{"desktop.inspect", "desktop.status", "desktop.type", "desktop.windows"}; !reflect.DeepEqual(tools, want) {
		t.Fatalf("allowlist = %v, want %v (each tool once)", tools, want)
	}

	// All of their instructions reach the model, not only the first skill's.
	context := services.RenderSkillContext(loaded)
	for _, slug := range []string{"reader", "desktop", "writer"} {
		if !strings.Contains(context, "Instructions of "+slug) {
			t.Fatalf("instructions of %s are missing from the context:\n%s", slug, context)
		}
	}
}

func TestSkillsThatAreNotActiveGrantNothing(t *testing.T) {
	f := newSkillFixture(t)
	active := f.skill(t, f.owner.ID, "active", `["desktop.status"]`, nil)
	unassignedOff := f.skill(t, f.owner.ID, "assignment-off", `["desktop.type"]`, nil)
	skillOff := f.skill(t, f.owner.ID, "skill-off", `["desktop.screenshot"]`, func(s *models.Skill, _ *models.SkillVersion) {
		s.IsEnabled = false
		s.Status = "disabled"
	})
	invalid := f.skill(t, f.owner.ID, "invalid", `["desktop.windows"]`, func(_ *models.Skill, v *models.SkillVersion) {
		v.ValidationStatus = "invalid"
	})
	notAllowed := f.skill(t, f.owner.ID, "not-allowed", `["desktop.status","shell.exec"]`, nil)
	f.assign(t, f.owner.ID, f.agent.ID, active.ID, true)
	f.assign(t, f.owner.ID, f.agent.ID, unassignedOff.ID, false)
	f.assign(t, f.owner.ID, f.agent.ID, skillOff.ID, true)
	f.assign(t, f.owner.ID, f.agent.ID, invalid.ID, true)
	f.assign(t, f.owner.ID, f.agent.ID, notAllowed.ID, true)

	loaded, err := services.LoadActiveAgentSkills(f.db, f.owner.ID, f.agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	names := loadedNames(loaded)
	sort.Strings(names)
	if !reflect.DeepEqual(names, []string{"Skill active", "Skill not-allowed"}) {
		t.Fatalf("only the active and the valid skill should load, got %v", names)
	}
	if tools := services.UnionSkillTools(loaded); !reflect.DeepEqual(tools, []string{"desktop.status"}) {
		t.Fatalf("only the active skills' allowed tools may remain, got %v", tools)
	}
}

func TestSkillsDoNotLeakAcrossAgentsOrUsers(t *testing.T) {
	f := newSkillFixture(t)
	mine := f.skill(t, f.owner.ID, "mine", `["desktop.status"]`, nil)
	onSecondAgent := f.skill(t, f.owner.ID, "second-agent", `["desktop.type"]`, nil)
	theirs := f.skill(t, f.other.ID, "theirs", `["desktop.screenshot"]`, nil)
	f.assign(t, f.owner.ID, f.agent.ID, mine.ID, true)
	f.assign(t, f.owner.ID, f.second.ID, onSecondAgent.ID, true)
	f.assign(t, f.other.ID, f.agent.ID, theirs.ID, true)
	// A row that points the owner's assignment at someone else's skill must not load it.
	f.assign(t, f.owner.ID, f.agent.ID, theirs.ID, true)

	loaded, err := services.LoadActiveAgentSkills(f.db, f.owner.ID, f.agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if names := loadedNames(loaded); !reflect.DeepEqual(names, []string{"Skill mine"}) {
		t.Fatalf("the owner's first agent should load only its own skill, got %v", names)
	}

	otherLoaded, err := services.LoadActiveAgentSkills(f.db, f.other.ID, f.agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if names := loadedNames(otherLoaded); !reflect.DeepEqual(names, []string{"Skill theirs"}) {
		t.Fatalf("another user on the same shared agent should load only their own skill, got %v", names)
	}

	secondLoaded, err := services.LoadActiveAgentSkills(f.db, f.owner.ID, f.second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if names := loadedNames(secondLoaded); !reflect.DeepEqual(names, []string{"Skill second-agent"}) {
		t.Fatalf("the second agent should load only its own skill, got %v", names)
	}
}

// The lists the UI shows must describe every skill, not only the first one.
func TestSkillListsDescribeEverySkill(t *testing.T) {
	f := newSkillFixture(t)
	first := f.skill(t, f.owner.ID, "list-one", `["desktop.status"]`, nil)
	second := f.skill(t, f.owner.ID, "list-two", `["desktop.status"]`, nil)
	f.assign(t, f.owner.ID, f.agent.ID, first.ID, true)
	f.assign(t, f.owner.ID, f.agent.ID, second.ID, true)

	type listed struct {
		Name  string   `json:"name"`
		Tools []string `json:"tools"`
		Valid string   `json:"validation_status"`
	}
	check := func(label string, items []listed) {
		t.Helper()
		if len(items) != 2 {
			t.Fatalf("%s: got %d skills, want 2", label, len(items))
		}
		for _, item := range items {
			if len(item.Tools) != 1 || item.Valid != "valid" {
				t.Fatalf("%s: %q is listed without its version (tools=%v, validation=%q)", label, item.Name, item.Tools, item.Valid)
			}
		}
	}

	var all struct {
		Skills []listed `json:"skills"`
	}
	decode(t, performJSON(t, http.MethodGet, "/api/skills", f.owner.ID, nil), &all)
	check("GET /api/skills", all.Skills)

	var assigned struct {
		Skills []struct {
			Skill listed `json:"skill"`
		} `json:"skills"`
	}
	decode(t, performJSON(t, http.MethodGet, fmt.Sprintf("/api/agents/%d/skills", f.agent.ID), f.owner.ID, nil), &assigned)
	items := []listed{}
	for _, entry := range assigned.Skills {
		items = append(items, entry.Skill)
	}
	check("GET /api/agents/:id/skills", items)
}
