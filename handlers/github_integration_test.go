package handlers

import (
	"sync"
	"testing"

	"gorm.io/gorm/schema"

	"agentic-ai-backend/models"
)

// The upsert in GitHubCallback names columns as strings, so a wrong spelling is only found by a real
// database (it was: "github_id" against the column git_hub_id). This needs no database: it checks every
// name against the columns GORM derives from the model, and that the identifying columns are there.
func TestGitHubUpsertColumnsExist(t *testing.T) {
	parsed, err := schema.Parse(&models.GitHubConnection{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	columns := map[string]bool{}
	for _, field := range parsed.Fields {
		columns[field.DBName] = true
	}
	for _, name := range githubConnectionUpsertColumns {
		if !columns[name] {
			t.Errorf("upsert column %q is not a column of github_connections (columns: %v)", name, columns)
		}
	}
	for _, name := range []string{"user_id", "token_enc"} {
		if !columns[name] {
			t.Errorf("github_connections has no column %q", name)
		}
	}
	// A reconnect must refresh the token and who it belongs to.
	required := map[string]bool{"git_hub_id": false, "git_hub_login": false, "token_enc": false}
	for _, name := range githubConnectionUpsertColumns {
		if _, ok := required[name]; ok {
			required[name] = true
		}
	}
	for name, present := range required {
		if !present {
			t.Errorf("the upsert does not update %q", name)
		}
	}
}
