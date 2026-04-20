// Package cache implements the deterministic skill cache. v1 uses a simple
// JSON-on-disk store; we'll move to bolt or sqlite when the working set grows.
//
// See docs/SPEC.md §6.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/CoreyRDean/intent/internal/model"
)

// KeyInputs is everything that goes into the cache key.
type KeyInputs struct {
	Prompt                string
	CwdFingerprint        string
	OS                    string
	BinariesFingerprint   string
	ModelName             string
	PromptTemplateVersion string
}

// Key computes the deterministic cache key.
func Key(in KeyInputs) string {
	parts := []string{
		normalizePrompt(in.Prompt),
		in.CwdFingerprint,
		in.OS,
		in.BinariesFingerprint,
		in.ModelName,
		in.PromptTemplateVersion,
	}
	h := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return hex.EncodeToString(h[:])
}

func normalizePrompt(s string) string {
	s = strings.ToLower(s)
	s = strings.Join(strings.Fields(s), " ")
	stop := map[string]struct{}{
		"please": {}, "could": {}, "would": {}, "you": {}, "the": {}, "a": {}, "an": {},
		"i": {}, "me": {}, "my": {}, "to": {}, "for": {}, "of": {}, "is": {}, "are": {},
	}
	out := make([]string, 0, 16)
	for _, w := range strings.Fields(s) {
		if _, drop := stop[w]; drop {
			continue
		}
		out = append(out, w)
	}
	return strings.Join(out, " ")
}

// CwdFingerprint returns a hash of (basename(cwd), is_git_repo, git_remote).
func CwdFingerprint(cwd, gitRemote string) string {
	base := filepath.Base(cwd)
	is := "0"
	if gitRemote != "" {
		is = "1"
	}
	h := sha256.Sum256([]byte(base + "\x1f" + is + "\x1f" + gitRemote))
	return hex.EncodeToString(h[:8])
}

// BinariesFingerprint returns a hash of the curated binary availability set.
func BinariesFingerprint(available []string) string {
	cp := make([]string, len(available))
	copy(cp, available)
	sort.Strings(cp)
	h := sha256.Sum256([]byte(strings.Join(cp, ",")))
	return hex.EncodeToString(h[:8])
}

// Entry is one cached response.
type Entry struct {
	Key         string          `json:"key"`
	Prompt      string          `json:"prompt"`
	Response    *model.Response `json:"response"`
	CreatedAt   time.Time       `json:"created_at"`
	LastUsedAt  time.Time       `json:"last_used_at"`
	UseCount    int             `json:"use_count"`
	Pinned      bool            `json:"pinned"`
	PinnedName  string          `json:"pinned_name,omitempty"`
}

// Store is a tiny key→Entry persistent map.
type Store struct {
	path string
	mu   sync.Mutex
	data map[string]*Entry
}

// Open loads or initializes the store at path.
func Open(path string) (*Store, error) {
	s := &Store{path: path, data: map[string]*Entry{}}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	_ = json.Unmarshal(b, &s.data)
	return s, nil
}

// Get returns the entry for key, or nil.
func (s *Store) Get(key string) *Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.data[key]
	if !ok {
		return nil
	}
	e.LastUsedAt = time.Now().UTC()
	e.UseCount++
	return e
}

// Put stores an entry.
func (s *Store) Put(e *Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	e.LastUsedAt = time.Now().UTC()
	s.data[e.Key] = e
	return s.flushLocked()
}

// Forget removes an entry by key. Returns true if removed.
func (s *Store) Forget(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data[key]; !ok {
		return false
	}
	delete(s.data, key)
	_ = s.flushLocked()
	return true
}

// Pin marks an entry as a named, never-evictable skill.
func (s *Store) Pin(key, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.data[key]
	if !ok {
		return os.ErrNotExist
	}
	e.Pinned = true
	e.PinnedName = name
	return s.flushLocked()
}

// All returns a snapshot of all entries.
func (s *Store) All() []*Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Entry, 0, len(s.data))
	for _, e := range s.data {
		out = append(out, e)
	}
	return out
}

func (s *Store) flushLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
