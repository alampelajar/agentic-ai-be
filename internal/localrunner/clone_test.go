package localrunner

import "testing"

func TestValidatePublicGitHubURL(t *testing.T) {
	gotURL, owner, repo, err := validatePublicGitHubURL("https://github.com/example/project")
	if err != nil { t.Fatal(err) }
	if gotURL != "https://github.com/example/project.git" || owner != "example" || repo != "project" {
		t.Fatalf("unexpected parsed repo: %q %q %q", gotURL, owner, repo)
	}
}

func TestValidatePublicGitHubURLRejectsUnsafeURLs(t *testing.T) {
	cases := []string{
		"http://github.com/example/project",
		"https://github.com.example.invalid/example/project",
		"https://user:password@github.com/example/project",
		"https://github.com/example/project?token=secret",
		"https://github.com/example/project/tree/main",
		"https://github.com/../project",
	}
	for _, input := range cases {
		if _, _, _, err := validatePublicGitHubURL(input); err == nil {
			t.Errorf("expected URL to be rejected: %s", input)
		}
	}
}
