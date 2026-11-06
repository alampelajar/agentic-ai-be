package services

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"agentic-ai-backend/models"
)

func TestParseSkillCommands(t *testing.T) {
	cases := []struct {
		name      string
		message   string
		wantSlugs []string
		wantRest  string
	}{
		{"no command", "halo dunia", nil, "halo dunia"},
		{"one command with text", "/desktop-control buka notepad", []string{"desktop-control"}, "buka notepad"},
		{"command only", "/desktop-control", []string{"desktop-control"}, ""},
		{"two commands", "/desktop-control /code-review cek ini", []string{"desktop-control", "code-review"}, "cek ini"},
		{"leading spaces", "  \n/desktop-control tes", []string{"desktop-control"}, "tes"},
		{"command in the middle is text", "tolong jalankan /desktop-control", nil, "tolong jalankan /desktop-control"},
		{"a path is not a command", "/etc/hosts isinya apa", nil, "/etc/hosts isinya apa"},
		{"uppercase is not a slug", "/Desktop-Control tes", nil, "/Desktop-Control tes"},
		{"a single character is not a slug", "/a tes", nil, "/a tes"},
		{"slash alone", "/ tes", nil, "/ tes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			slugs, rest := ParseSkillCommands(tc.message)
			if strings.Join(slugs, ",") != strings.Join(tc.wantSlugs, ",") {
				t.Fatalf("slugs = %v, want %v", slugs, tc.wantSlugs)
			}
			if rest != tc.wantRest {
				t.Fatalf("rest = %q, want %q", rest, tc.wantRest)
			}
		})
	}
}

func TestMergeInvokedSkillsDoesNotListASkillTwice(t *testing.T) {
	assigned := []ActiveSkillContext{{SkillID: 1, Name: "a"}, {SkillID: 2, Name: "b"}}
	invoked := []ActiveSkillContext{{SkillID: 2, Name: "b"}, {SkillID: 3, Name: "c"}}
	merged := MergeInvokedSkills(assigned, invoked)
	if len(merged) != 3 || merged[2].SkillID != 3 {
		t.Fatalf("merged = %+v", merged)
	}
	if got := MergeInvokedSkills(assigned, nil); len(got) != 2 {
		t.Fatalf("without invoked skills the assigned ones must stay as they are, got %+v", got)
	}
	if len(assigned) != 2 {
		t.Fatalf("the assigned slice must not be changed, got %+v", assigned)
	}
}

func TestResolveSkillCommandsOnlyUsesTheUsersOwnUsableSkills(t *testing.T) {
	dsn := os.Getenv("SKILLS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SKILLS_TEST_DATABASE_URL is not configured")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	if err := db.AutoMigrate(&models.User{}, &models.Skill{}, &models.SkillVersion{}); err != nil {
		t.Fatal(err)
	}

	suffix := strconv.Itoa(os.Getpid())
	owner := models.User{Name: "Slash Owner", Email: "slash-owner-" + suffix + "@example.test", Password: "not-used"}
	other := models.User{Name: "Slash Other", Email: "slash-other-" + suffix + "@example.test", Password: "not-used"}
	for _, u := range []*models.User{&owner, &other} {
		if err := db.Create(u).Error; err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		db.Where("user_id IN ?", []uint{owner.ID, other.ID}).Delete(&models.Skill{})
		db.Delete(&owner)
		db.Delete(&other)
	})

	mk := func(userID uint, slug string, enabled bool, validation string) models.Skill {
		skill := models.Skill{UserID: userID, Slug: slug, Name: "Skill " + slug, Status: "installed", IsEnabled: enabled}
		if err := db.Create(&skill).Error; err != nil {
			t.Fatal(err)
		}
		version := models.SkillVersion{SkillID: skill.ID, Version: "1.0.0", Manifest: "{}", Instructions: "do " + slug, RequestedTools: `["read-file"]`, RequestedPermissions: "[]", PackageSHA256: "x", ValidationStatus: validation}
		if err := db.Create(&version).Error; err != nil {
			t.Fatal(err)
		}
		return skill
	}
	mk(owner.ID, "desktop-control", true, "valid")
	mk(owner.ID, "switched-off", false, "valid")
	mk(owner.ID, "broken-skill", true, "invalid")
	mk(other.ID, "someone-elses", true, "valid")

	result, err := ResolveSkillCommands(db, owner.ID, "/desktop-control buka notepad")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Skills) != 1 || result.Skills[0].Name != "Skill desktop-control" || result.Message != "buka notepad" {
		t.Fatalf("a usable own skill must be invoked and removed from the text: %+v", result)
	}

	result, err = ResolveSkillCommands(db, owner.ID, "/desktop-control")
	if err != nil || len(result.Skills) != 1 || result.Message != "" {
		t.Fatalf("a command alone leaves an empty message for the caller to fill: %+v %v", result, err)
	}

	result, err = ResolveSkillCommands(db, owner.ID, "/switched-off /broken-skill tes")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Skills) != 0 || strings.Join(result.Unusable, ",") != "switched-off,broken-skill" {
		t.Fatalf("a disabled or invalid skill must be reported, not run: %+v", result)
	}

	result, err = ResolveSkillCommands(db, owner.ID, "/someone-elses tes")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Skills) != 0 || len(result.Unusable) != 0 || result.Message != "/someone-elses tes" {
		t.Fatalf("another user's skill must not be reachable and stays plain text: %+v", result)
	}

	result, err = ResolveSkillCommands(db, owner.ID, "/desktop-control /unknown-thing halo")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Skills) != 1 || result.Message != "/unknown-thing halo" {
		t.Fatalf("an unknown command must stay in the text: %+v", result)
	}
}
