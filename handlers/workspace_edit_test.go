package handlers

import (
	"strings"
	"testing"
)

func TestApplyEdit(t *testing.T) {
	got, err := applyEdit("a\nb\nc\n", "b\n", "B\nB2\n")
	if err != nil || got != "a\nB\nB2\nc\n" {
		t.Fatalf("got %q %v", got, err)
	}
	if got, err := applyEdit("x = 1\r\ny = 2\r\n", "x = 1\ny", "x = 10\ny"); err != nil || got != "x = 10\r\ny = 2\r\n" {
		t.Fatalf("a CRLF file must match an LF edit and keep CRLF: %q %v", got, err)
	}
	if got, err := applyEdit("keep remove keep", " remove", ""); err != nil || got != "keep keep" {
		t.Fatalf("an empty new_text removes the text: %q %v", got, err)
	}
	if _, err := applyEdit("abc", "zzz", "y"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing text: %v", err)
	}
	if _, err := applyEdit("aa aa", "aa", "b"); err == nil || !strings.Contains(err.Error(), "appears 2 times") {
		t.Fatalf("ambiguous text: %v", err)
	}
}
