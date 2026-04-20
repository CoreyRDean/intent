package models

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCustomRoundTrip(t *testing.T) {
	dir := t.TempDir()
	entries, err := LoadCustom(dir)
	if err != nil {
		t.Fatalf("LoadCustom on empty dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("empty dir should return no entries, got %d", len(entries))
	}

	m := Model{
		ID:    "bartowski_phi-3.5-mini-instruct-gguf-q6_k",
		Repo:  "bartowski/Phi-3.5-mini-instruct-GGUF",
		File:  "Phi-3.5-mini-instruct-Q6_K.gguf",
		Quant: "Q6_K",
	}

	list, err := AddCustom(dir, m)
	if err != nil {
		t.Fatalf("AddCustom: %v", err)
	}
	if len(list) != 1 || list[0].ID != m.ID {
		t.Fatalf("after add: got %+v", list)
	}

	// Adding the same ID should update, not append.
	m.Quant = "Q8_0"
	list, err = AddCustom(dir, m)
	if err != nil {
		t.Fatalf("AddCustom update: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("duplicate add should update not append; got len=%d", len(list))
	}
	if list[0].Quant != "Q8_0" {
		t.Errorf("update did not stick: quant=%q", list[0].Quant)
	}

	// Reload from disk must match.
	list2, err := LoadCustom(dir)
	if err != nil {
		t.Fatalf("LoadCustom after add: %v", err)
	}
	if len(list2) != 1 || list2[0].Quant != "Q8_0" {
		t.Errorf("round-trip mismatch: %+v", list2)
	}

	// Removing a built-in ID must fail loudly — the user shouldn't
	// accidentally unregister a catalog entry they can't restore.
	if _, err := RemoveCustom(dir, "qwen2.5-coder-3b"); err == nil {
		t.Error("RemoveCustom on built-in ID should error")
	}

	// Remove our custom entry.
	list3, err := RemoveCustom(dir, m.ID)
	if err != nil {
		t.Fatalf("RemoveCustom: %v", err)
	}
	if len(list3) != 0 {
		t.Errorf("after remove: got %d entries, want 0", len(list3))
	}
}

func TestLoadCustomRejectsCorruptJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, CustomFilename), []byte("not json"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := LoadCustom(dir); err == nil {
		t.Error("LoadCustom on corrupt file should return an error (not silently discard)")
	}
}
