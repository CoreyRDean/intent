// Package hf is a minimal, read-only client for the Hugging Face Hub
// API. intent uses it to probe user-supplied repos: verify the repo
// exists and is public, list the GGUF files it contains, and pick a
// sensible quant when the user didn't specify one.
//
// We intentionally do NOT pull in the huggingface_hub Go SDK — we
// only need three endpoints, and keeping the dependency surface small
// matters more than feature coverage.
package hf

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"
)

// endpoint is the base URL for the HF API. Overridable in tests.
var endpoint = "https://huggingface.co"

// RepoInfo is the subset of /api/models/{id} we care about. HF
// returns a lot more; we deliberately ignore the rest so schema
// drift never breaks us.
type RepoInfo struct {
	ID       string   `json:"id"`
	Author   string   `json:"author"`
	Private  bool     `json:"private"`
	Gated    any      `json:"gated"` // bool | string ("auto", "manual")
	Disabled bool     `json:"disabled"`
	Tags     []string `json:"tags"`
}

// File is one entry from the repo's file tree.
type File struct {
	// Type is "file" or "directory".
	Type string `json:"type"`
	// Path is the filename relative to repo root.
	Path string `json:"path"`
	// Size in bytes. Zero for directories.
	Size int64 `json:"size"`
	// OID is the git LFS pointer for downloadable blobs.
	OID string `json:"oid,omitempty"`
}

// Client holds the http.Client used for all calls. Zero value works
// for anonymous public-repo access.
type Client struct {
	HTTP  *http.Client
	Token string // optional; sent as Authorization: Bearer <token>
}

// New returns a client with a 15s timeout, suitable for CLI probes.
func New() *Client {
	return &Client{HTTP: &http.Client{Timeout: 15 * time.Second}}
}

// do runs an HTTP GET against the HF API with auth (if set) and JSON
// content type. Returns the decoded body or a descriptive error.
func (c *Client) do(ctx context.Context, urlPath string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+urlPath, nil)
	if err != nil {
		return err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	req.Header.Set("Accept", "application/json")
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	switch resp.StatusCode {
	case 200:
		if out == nil {
			return nil
		}
		return json.Unmarshal(body, out)
	case 401, 403:
		return fmt.Errorf("hf: access denied (%s). Is the repo gated or private? "+
			"Run `huggingface-cli login` or set HUGGING_FACE_HUB_TOKEN", resp.Status)
	case 404:
		return fmt.Errorf("hf: repo not found (%s)", resp.Status)
	default:
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 200 {
			snippet = snippet[:200] + "..."
		}
		return fmt.Errorf("hf: %s: %s", resp.Status, snippet)
	}
}

// GetRepo fetches repo metadata. Returns an error if the repo is
// missing or inaccessible.
func (c *Client) GetRepo(ctx context.Context, repoID string) (*RepoInfo, error) {
	info := &RepoInfo{}
	if err := c.do(ctx, "/api/models/"+repoID, info); err != nil {
		return nil, err
	}
	return info, nil
}

// ListFiles returns every file at the repo root. For deep trees HF
// returns only the top level; that's enough for our purposes (all
// known GGUF repos keep models at the root).
func (c *Client) ListFiles(ctx context.Context, repoID string) ([]File, error) {
	var files []File
	if err := c.do(ctx, "/api/models/"+repoID+"/tree/main", &files); err != nil {
		return nil, err
	}
	return files, nil
}

// FindGGUF filters a file list to .gguf entries only.
func FindGGUF(files []File) []File {
	out := make([]File, 0, len(files))
	for _, f := range files {
		if f.Type != "file" {
			continue
		}
		if strings.EqualFold(path.Ext(f.Path), ".gguf") {
			out = append(out, f)
		}
	}
	return out
}

// PickQuant scans a GGUF file list and returns the one matching the
// requested quant tag. Tag match is case-insensitive and matches any
// filename containing the tag surrounded by non-alphanumerics (so
// "Q4_K_M" matches "Model-Q4_K_M.gguf" but not "Model-Q4_K_M_X.gguf"
// if such a thing existed).
//
// If quantTag is empty, returns the first Q4_K_M we can find, else
// the first Q5_K_M, else the smallest file — in descending order of
// quality/size preference.
func PickQuant(files []File, quantTag string) (*File, error) {
	if len(files) == 0 {
		return nil, fmt.Errorf("no GGUF files in repo")
	}
	if quantTag != "" {
		for i := range files {
			if matchesQuant(files[i].Path, quantTag) {
				return &files[i], nil
			}
		}
		available := make([]string, 0, len(files))
		for _, f := range files {
			available = append(available, f.Path)
		}
		return nil, fmt.Errorf("quant %q not found; available: %v", quantTag, available)
	}
	for _, prefer := range []string{"Q4_K_M", "Q5_K_M", "Q6_K", "Q4_K_S", "Q4_0"} {
		for i := range files {
			if matchesQuant(files[i].Path, prefer) {
				return &files[i], nil
			}
		}
	}
	// Fall back to the smallest GGUF: usually a valid quant we just
	// didn't enumerate (e.g. IQ-series imatrix quants).
	smallest := 0
	for i := 1; i < len(files); i++ {
		if files[i].Size > 0 && (files[smallest].Size == 0 || files[i].Size < files[smallest].Size) {
			smallest = i
		}
	}
	return &files[smallest], nil
}

// matchesQuant reports whether a filename contains the given quant
// token as a distinct component. Case-insensitive. A "distinct
// component" means adjacent characters (if any) are non-alphanumeric
// or the boundary of the string — so "Q4_K_M" does not match inside
// "Q4_K_M_v2" as a different quant.
func matchesQuant(filename, tag string) bool {
	fn := strings.ToUpper(filename)
	t := strings.ToUpper(tag)
	idx := strings.Index(fn, t)
	if idx < 0 {
		return false
	}
	// Left boundary: start of string, or non-alnum.
	if idx > 0 && isAlnum(fn[idx-1]) {
		return false
	}
	// Right boundary: end of string, or non-alnum. We treat '_' as
	// part of the quant for patterns like Q4_K_M — so the boundary
	// is "character after the tag is a '.', '-', or end of string".
	end := idx + len(t)
	if end == len(fn) {
		return true
	}
	next := fn[end]
	return next == '.' || next == '-' || next == '_' && !isAlnum(nextAfterUnderscore(fn, end))
}

// nextAfterUnderscore finds the next non-underscore byte after index i.
// Used to distinguish "Q4_K_M." (ok) from "Q4_K_M_v2" (not ok).
func nextAfterUnderscore(s string, i int) byte {
	for i < len(s) && s[i] == '_' {
		i++
	}
	if i >= len(s) {
		return 0
	}
	return s[i]
}

func isAlnum(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
}

// ResolveURL is the canonical download URL for a file in a HF repo.
// Works for anonymous callers on public repos.
func ResolveURL(repoID, filename string) string {
	return fmt.Sprintf("%s/%s/resolve/main/%s?download=true", endpoint, repoID, filename)
}

// SetEndpoint overrides the HF API host. Tests use this to point
// at a local httptest server; production should never call it.
func SetEndpoint(url string) { endpoint = strings.TrimRight(url, "/") }
