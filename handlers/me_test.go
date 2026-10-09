package handlers

import "testing"

func TestValidProfileAvatar(t *testing.T) {
	valid := []string{
		"preset:adventurer",
		"preset:lorelei",
		"preset:pixel-art-neutral",
		"preset:icons",
	}
	for _, value := range valid {
		if !validProfileAvatar(value) {
			t.Errorf("validProfileAvatar(%q) = false, want true", value)
		}
	}

	invalid := []string{
		"",
		"lorelei",
		"preset:",
		"preset:../me",
		"preset:unknown-style",
		"https://example.com/avatar.svg",
	}
	for _, value := range invalid {
		if validProfileAvatar(value) {
			t.Errorf("validProfileAvatar(%q) = true, want false", value)
		}
	}
}
