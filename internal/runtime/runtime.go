// Package runtime manages the local llamafile binary and model files.
// In v1 it can: report whether a runtime/model is present, and download
// either on demand with progress callbacks. Actually starting llamafile as a
// subprocess is wired into Phase 4 (daemon).
package runtime

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/CoreyRDean/intent/internal/models"
)

// LlamafileVersion is the runtime version we ship against.
const LlamafileVersion = "0.10.0"

// ModelInfo is the minimal shape the runtime package needs to
// download a model. It's a projection of models.Model kept here for
// backward compatibility; new code should pass models.Model around.
type ModelInfo struct {
	Name   string
	Repo   string // huggingface repo
	File   string // GGUF file name
	SizeMB int    // approximate, for progress UX
}

// FromCatalog projects a catalog entry into a ModelInfo for the
// runtime package. Caller must ensure m is non-nil.
func FromCatalog(m *models.Model) ModelInfo {
	return ModelInfo{
		Name:   m.ID,
		Repo:   m.Repo,
		File:   models.ModelFilename(m),
		SizeMB: m.SizeMB,
	}
}

// DefaultModel is the seeded "install this first" model. Source of
// truth is the models catalog; this variable stays for callers that
// already reference runtime.DefaultModel.
var DefaultModel = defaultModelInfo()

func defaultModelInfo() ModelInfo {
	cat := models.New(nil)
	m := cat.Default()
	if m == nil {
		return ModelInfo{}
	}
	return FromCatalog(m)
}

// ModelFileForName returns the GGUF filename for the given model
// config tag. Looks up the catalog (built-ins only, no disk I/O)
// and falls back to appending .gguf for unknown tags. For custom
// user models the caller should consult the Catalog directly since
// it has access to the full persisted entry.
func ModelFileForName(name string) string {
	if name == "" {
		return DefaultModel.File
	}
	cat := models.New(nil)
	if m := cat.Get(name); m != nil {
		return models.ModelFilename(m)
	}
	if filepath.Ext(name) == ".gguf" {
		return name
	}
	return name + ".gguf"
}

// Manager owns runtime/model artifacts on disk.
type Manager struct {
	CacheDir string
}

func New(cacheDir string) *Manager { return &Manager{CacheDir: cacheDir} }

// LlamafilePath returns the expected path of the llamafile binary.
func (m *Manager) LlamafilePath() string {
	return filepath.Join(m.CacheDir, "runtime", "llamafile-"+LlamafileVersion)
}

// ModelPath returns the expected path of the named model file.
func (m *Manager) ModelPath(file string) string {
	return filepath.Join(m.CacheDir, "models", file)
}

// HaveLlamafile reports whether the runtime exists and is executable.
func (m *Manager) HaveLlamafile() bool {
	info, err := os.Stat(m.LlamafilePath())
	if err != nil {
		return false
	}
	return info.Mode()&0o111 != 0
}

// HaveModel reports whether the named model file exists.
func (m *Manager) HaveModel(file string) bool {
	_, err := os.Stat(m.ModelPath(file))
	return err == nil
}

// Progress is a download progress callback.
type Progress func(downloaded, total int64)

// EnsureLlamafile downloads the runtime if missing.
func (m *Manager) EnsureLlamafile(ctx context.Context, progress Progress) error {
	if m.HaveLlamafile() {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(m.LlamafilePath()), 0o755); err != nil {
		return err
	}
	url := fmt.Sprintf("https://github.com/mozilla-ai/llamafile/releases/download/%s/llamafile-%s",
		LlamafileVersion, LlamafileVersion)
	if err := download(ctx, url, m.LlamafilePath(), progress); err != nil {
		return fmt.Errorf("download llamafile: %w", err)
	}
	return os.Chmod(m.LlamafilePath(), 0o755)
}

// EnsureModel downloads the model if missing.
func (m *Manager) EnsureModel(ctx context.Context, mi ModelInfo, progress Progress) error {
	dest := m.ModelPath(mi.File)
	if _, err := os.Stat(dest); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	url := fmt.Sprintf("https://huggingface.co/%s/resolve/main/%s?download=true", mi.Repo, mi.File)
	return download(ctx, url, dest, progress)
}

func download(ctx context.Context, url, dest string, progress Progress) error {
	tmp := dest + ".part"
	out, err := os.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	cli := &http.Client{Timeout: 0}
	resp, err := cli.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("http %s", resp.Status)
	}
	total := resp.ContentLength
	pr := &progressReader{r: resp.Body, total: total, cb: progress, last: time.Now()}
	if _, err := io.Copy(out, pr); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dest)
}

type progressReader struct {
	r     io.Reader
	read  int64
	total int64
	cb    Progress
	last  time.Time
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.read += int64(n)
	if p.cb != nil && time.Since(p.last) > 100*time.Millisecond {
		p.cb(p.read, p.total)
		p.last = time.Now()
	}
	return n, err
}
