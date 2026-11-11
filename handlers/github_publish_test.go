package handlers

import (
	"strings"
	"testing"
	"time"
)

func TestValidBranchName(t *testing.T) {
	for _, good := range []string{"agentic/fix-readme", "feature_1", "a", "release/v1.2", "Fix.Typo"} {
		if !validBranchName(good) {
			t.Errorf("%q should be accepted", good)
		}
	}
	for _, bad := range []string{"", "-x", "/x", "x/", "a..b", "a//b", "x.lock", "x.", "has space", "semi;colon", "tilde~1", "caret^", "colon:x", strings.Repeat("a", 101), "../main"} {
		if validBranchName(bad) {
			t.Errorf("%q should be refused", bad)
		}
	}
}

func TestDefaultBranchName(t *testing.T) {
	at := time.Date(2026, 11, 10, 9, 30, 15, 0, time.UTC)
	if got := defaultBranchName("Porto GW!", at); got != "agentic/porto-gw-20261110-093015" {
		t.Errorf("got %q", got)
	}
	if got := defaultBranchName("   ", at); got != "agentic/workspace-20261110-093015" {
		t.Errorf("an empty name should still give a branch, got %q", got)
	}
	if got := defaultBranchName(strings.Repeat("abc ", 30), at); !validBranchName(got) {
		t.Errorf("a long name must still give a valid branch, got %q", got)
	}
}
