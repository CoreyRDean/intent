package contextpack

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsNoiseName(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"":                true,
		".hidden":         true,
		"_internal":       true,
		"libfoo.so":       true,
		"libfoo.so.1":     true,
		"libfoo.dylib":    true,
		"libfoo.dylib.2":  true,
		"config.pc":       true,
		"thing.cmake":     true,
		"really-long-binary-name-" + repeat("x", 40): true,
		"git":         false,
		"kubectl":     false,
		"my-company-cli": false,
		"node":        false,
	}
	for name, want := range cases {
		if got := isNoiseName(name); got != want {
			t.Errorf("isNoiseName(%q) = %v, want %v", name, got, want)
		}
	}
}

func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}

// TestScanPATHDedupAndFilter builds a synthetic PATH with two directories,
// executable and non-executable files, a shadowed binary, and noise, and
// asserts scanPATH only returns the user-facing commands with the shell's
// first-wins semantics implicit in the set.
func TestScanPATHDedupAndFilter(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()

	writeExec(t, filepath.Join(dirA, "git"))
	writeExec(t, filepath.Join(dirA, "custom-tool"))
	writeExec(t, filepath.Join(dirA, "libthing.dylib"))
	writeNonExec(t, filepath.Join(dirA, "notes.txt"))
	writeExec(t, filepath.Join(dirA, ".hidden"))

	// Shadowed: dirB also has git, but since dirA is first we should
	// still list it exactly once.
	writeExec(t, filepath.Join(dirB, "git"))
	writeExec(t, filepath.Join(dirB, "other-tool"))

	t.Setenv("PATH", dirA+string(os.PathListSeparator)+dirB)
	names, dirs := scanPATH()

	mustHave := []string{"git", "custom-tool", "other-tool"}
	for _, m := range mustHave {
		if !containsString(names, m) {
			t.Errorf("scanPATH missing %q; got %v", m, names)
		}
	}
	mustNotHave := []string{"libthing.dylib", "notes.txt", ".hidden"}
	for _, m := range mustNotHave {
		if containsString(names, m) {
			t.Errorf("scanPATH returned noisy entry %q; got %v", m, names)
		}
	}
	gitCount := 0
	for _, n := range names {
		if n == "git" {
			gitCount++
		}
	}
	if gitCount != 1 {
		t.Errorf("expected git once after dedup; got %d", gitCount)
	}
	if len(dirs) != 2 {
		t.Errorf("expected 2 scanned dirs; got %v", dirs)
	}
}

// TestAssembleAvailableBinsPrioritizesCurated ensures that when the scan
// produces more than the budget, curated-and-present names always appear
// before other entries get truncated.
func TestAssembleAvailableBinsPrioritizesCurated(t *testing.T) {
	// Build a synthetic "on PATH" list: curated names + a large pile of
	// others. The budget should spare every curated entry and fill the
	// rest from the extras.
	var onPATH []string
	for _, c := range CuratedBinaries {
		onPATH = append(onPATH, c)
	}
	for i := 0; i < maxBinsInPrompt*2; i++ {
		onPATH = append(onPATH, filler(i))
	}

	got := assembleAvailableBins(onPATH)
	if len(got) != maxBinsInPrompt {
		t.Fatalf("expected output capped at %d; got %d", maxBinsInPrompt, len(got))
	}
	for _, c := range CuratedBinaries {
		if !containsString(got, c) {
			t.Errorf("curated %q dropped by truncation", c)
		}
	}
}

func writeExec(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatalf("writeExec %s: %v", path, err)
	}
}

func writeNonExec(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatalf("writeNonExec %s: %v", path, err)
	}
}

func containsString(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func filler(i int) string {
	// Prefix "zz-" so these sort after curated names alphabetically,
	// and use varying suffixes so there are no duplicates.
	return "zz-filler-" + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
