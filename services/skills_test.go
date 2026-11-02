package services

import (
	"archive/zip"
	"bytes"
	"testing"

	"agentic-ai-backend/models"
)

func TestValidateSkillManifestRejectsUnknownTool(t *testing.T) {
	manifest := models.SkillManifest{ID: "demo-skill", Name: "Demo", Version: "1.0.0", Instructions: "SKILL.md", Tools: []string{"shell.exec"}}
	if err := ValidateSkillManifest(manifest, "# Demo"); err == nil {
		t.Fatal("expected unknown tool to be rejected")
	}
}

func TestValidateSkillArchiveAcceptsNestedPackage(t *testing.T) {
	data := makeSkillZip(t, map[string]string{
		"demo-skill/manifest.json": `{"id":"demo-skill","name":"Demo","version":"1.0.0","instructions":"SKILL.md","tools":["workspace.read"]}`,
		"demo-skill/SKILL.md":      "# Demo\nRead files safely.",
	})
	pkg, err := ValidateSkillArchive(data)
	if err != nil {
		t.Fatalf("expected valid package: %v", err)
	}
	if pkg.Manifest.ID != "demo-skill" || pkg.Instructions == "" || pkg.SHA256 == "" {
		t.Fatalf("unexpected package: %#v", pkg)
	}
}

func TestValidateSkillArchiveRejectsTraversal(t *testing.T) {
	data := makeSkillZip(t, map[string]string{
		"../manifest.json": `{"id":"demo-skill","name":"Demo","version":"1.0.0","instructions":"SKILL.md"}`,
		"SKILL.md":         "# Demo",
	})
	if _, err := ValidateSkillArchive(data); err == nil {
		t.Fatal("expected traversal path to be rejected")
	}
}

func makeSkillZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
