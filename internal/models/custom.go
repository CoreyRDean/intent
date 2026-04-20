package models

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// CustomFilename is the JSON sidecar next to config.toml that holds
// user-added models. Kept separate from config.toml because our
// config file is intentionally flat and custom entries are richer.
const CustomFilename = "custom-models.json"

// customFile is the on-disk shape.
type customFile struct {
	Version int     `json:"version"`
	Models  []Model `json:"models"`
}

// LoadCustom reads the custom-models sidecar from the config dir.
// Missing file is not an error — returns an empty slice. Corrupt
// JSON IS an error so the user knows their state is unreadable.
func LoadCustom(configDir string) ([]Model, error) {
	path := filepath.Join(configDir, CustomFilename)
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var cf customFile
	if err := json.Unmarshal(b, &cf); err != nil {
		return nil, fmt.Errorf("parse %s: %w", CustomFilename, err)
	}
	return cf.Models, nil
}

// SaveCustom writes the custom-models sidecar atomically. Ensures the
// config dir exists first.
func SaveCustom(configDir string, list []Model) error {
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return err
	}
	cf := customFile{Version: 1, Models: list}
	b, err := json.MarshalIndent(cf, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(configDir, CustomFilename)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// AddCustom upserts a custom model into the on-disk list. Returns the
// updated list so callers can rebuild their Catalog without another
// read. Duplicate IDs replace rather than append, which is what the
// user wants when re-running `i model add foo` with different args.
func AddCustom(configDir string, m Model) ([]Model, error) {
	list, err := LoadCustom(configDir)
	if err != nil {
		return nil, err
	}
	replaced := false
	for i := range list {
		if list[i].ID == m.ID {
			list[i] = m
			replaced = true
			break
		}
	}
	if !replaced {
		list = append(list, m)
	}
	if err := SaveCustom(configDir, list); err != nil {
		return nil, err
	}
	return list, nil
}

// RemoveCustom deletes a custom entry by ID. Returns a helpful error
// if the ID belongs to a built-in catalog entry (user wanted to edit
// the catalog itself, which isn't supported).
func RemoveCustom(configDir, id string) ([]Model, error) {
	if isBuiltInID(id) {
		return nil, fmt.Errorf("%q is a built-in model; delete the file with `i model rm <id> --purge` instead", id)
	}
	list, err := LoadCustom(configDir)
	if err != nil {
		return nil, err
	}
	out := list[:0]
	removed := false
	for _, m := range list {
		if m.ID == id {
			removed = true
			continue
		}
		out = append(out, m)
	}
	if !removed {
		return list, fmt.Errorf("no custom model with ID %q", id)
	}
	if err := SaveCustom(configDir, out); err != nil {
		return nil, err
	}
	return out, nil
}

// isBuiltInID reports whether id matches a hardcoded catalog entry.
// Cheap linear scan; the catalog is small.
func isBuiltInID(id string) bool {
	for _, m := range builtIn {
		if m.ID == id {
			return true
		}
	}
	return false
}
