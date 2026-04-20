// Package audit writes the append-only JSONL audit log.
package audit

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
	"crypto/rand"
	"crypto/sha256"

	"github.com/CoreyRDean/intent/internal/model"
	"github.com/CoreyRDean/intent/internal/safety"
)

// Entry is one audit row. Field order follows SPEC §3.5.
type Entry struct {
	TS              string             `json:"ts"`
	ID              string             `json:"id"`
	Version         string             `json:"version"`
	Backend         string             `json:"backend"`
	Model           string             `json:"model"`
	Prompt          string             `json:"prompt"`
	Context         map[string]any     `json:"context,omitempty"`
	ModelResponse   *model.Response    `json:"model_response,omitempty"`
	GuardActions    []safety.Action    `json:"guard_actions,omitempty"`
	UserDecision    string             `json:"user_decision"`
	ExecutedCommand string             `json:"executed_command,omitempty"`
	ExitCode        *int               `json:"exit_code,omitempty"`
	StdoutHash      string             `json:"stdout_hash,omitempty"`
	StderrHash      string             `json:"stderr_hash,omitempty"`
	DurationMS      int64              `json:"duration_ms,omitempty"`
}

// Logger appends entries to a single audit file under a process-wide lock.
type Logger struct {
	path string
	mu   sync.Mutex
}

func New(path string) (*Logger, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return &Logger{path: path}, nil
}

// Append writes one entry. The entry's Prompt is redacted in-place.
func (l *Logger) Append(e Entry) error {
	if e.TS == "" {
		e.TS = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if e.ID == "" {
		e.ID = newID()
	}
	if redacted, did := safety.RedactSecrets(e.Prompt); did {
		e.Prompt = redacted
	}
	if e.ExecutedCommand != "" {
		if redacted, did := safety.RedactSecrets(e.ExecutedCommand); did {
			e.ExecutedCommand = redacted
		}
	}
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}

func newID() string {
	var b [16]byte
	_, _ = io.ReadFull(rand.Reader, b[:])
	return hex.EncodeToString(b[:])
}

// HashOutput returns a sha256 of arbitrary bytes for storage in entries.
func HashOutput(b []byte) string {
	h := sha256.Sum256(b)
	return fmt.Sprintf("sha256:%x", h)
}
