package cli

import (
	"strings"
	"testing"
)

// TestZshSnippetApostropheWarning verifies that the emitted zsh init
// snippet contains the apostrophe/single-quote limitation warning.
// This is a first-impression regression guard: the warning must be
// visible in every .zshrc that sources the integration.
func TestZshSnippetApostropheWarning(t *testing.T) {
	mustContain := []string{
		"Apostrophe",
		"double quotes",
		"noglob",
	}
	for _, want := range mustContain {
		if !strings.Contains(zshSnippet, want) {
			t.Errorf("zshSnippet missing %q — apostrophe warning may be incomplete", want)
		}
	}
}
