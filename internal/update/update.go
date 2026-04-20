// Package update implements the GitHub-Releases-based update check used by
// `i update` and the daemon's periodic check.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

const releasesURL = "https://api.github.com/repos/CoreyRDean/intent/releases"

// Channel selects which releases are eligible.
type Channel string

const (
	ChannelStable  Channel = "stable"
	ChannelNightly Channel = "nightly"
	ChannelOff     Channel = "off"
)

// Release is the trimmed-down view of a GitHub release we care about.
type Release struct {
	TagName    string    `json:"tag_name"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	HTMLURL    string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
}

// CheckResult is what the caller wants.
type CheckResult struct {
	Current   string
	Latest    *Release
	Available bool
}

// Check queries GitHub for the latest release on the given channel.
// `current` is the running version (without leading v).
func Check(ctx context.Context, ch Channel, current string) (*CheckResult, error) {
	if ch == ChannelOff {
		return &CheckResult{Current: current}, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesURL+"?per_page=30", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "intent-update-check")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("github releases: %s", resp.Status)
	}
	var rels []Release
	if err := json.NewDecoder(resp.Body).Decode(&rels); err != nil {
		return nil, err
	}
	candidates := make([]Release, 0, len(rels))
	for _, r := range rels {
		if r.Draft {
			continue
		}
		isNightly := isNightlyTag(r.TagName)
		switch ch {
		case ChannelStable:
			if r.Prerelease || isNightly {
				continue
			}
		case ChannelNightly:
			// nightly channel accepts both nightly pre-releases and stable
		}
		candidates = append(candidates, r)
	}
	if len(candidates) == 0 {
		return &CheckResult{Current: current}, nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		return semverLess(strings.TrimPrefix(candidates[j].TagName, "v"),
			strings.TrimPrefix(candidates[i].TagName, "v"))
	})
	latest := candidates[0]
	avail := semverLess(current, strings.TrimPrefix(latest.TagName, "v"))
	return &CheckResult{Current: current, Latest: &latest, Available: avail}, nil
}

// isNightlyTag reports whether a tag is a nightly pre-release per D-005:
// vMAJOR.MINOR.PATCH-<unix-timestamp>.
func isNightlyTag(tag string) bool {
	tag = strings.TrimPrefix(tag, "v")
	dash := strings.Index(tag, "-")
	if dash < 0 {
		return false
	}
	suffix := tag[dash+1:]
	if _, err := strconv.ParseInt(suffix, 10, 64); err == nil {
		return true
	}
	return false
}

// semverLess is a lenient semver comparator. Returns a < b.
// Treats pre-release suffix (after `-`) as a numeric tail when possible
// (matches our nightly format) and as lexically-less-than-no-suffix otherwise.
func semverLess(a, b string) bool {
	aMain, aPre := splitPre(a)
	bMain, bPre := splitPre(b)
	ap := splitNums(aMain)
	bp := splitNums(bMain)
	for i := 0; i < 3; i++ {
		var av, bv int
		if i < len(ap) {
			av = ap[i]
		}
		if i < len(bp) {
			bv = bp[i]
		}
		if av != bv {
			return av < bv
		}
	}
	// equal main: pre-release sorts less than no-pre-release per semver.
	if aPre == "" && bPre == "" {
		return false
	}
	if aPre == "" {
		return false
	}
	if bPre == "" {
		return true
	}
	// Try numeric comparison (our nightly suffix).
	an, aerr := strconv.ParseInt(aPre, 10, 64)
	bn, berr := strconv.ParseInt(bPre, 10, 64)
	if aerr == nil && berr == nil {
		return an < bn
	}
	return aPre < bPre
}

func splitPre(v string) (main, pre string) {
	dash := strings.Index(v, "-")
	if dash < 0 {
		return v, ""
	}
	return v[:dash], v[dash+1:]
}

func splitNums(v string) []int {
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, _ := strconv.Atoi(p)
		out = append(out, n)
	}
	return out
}
