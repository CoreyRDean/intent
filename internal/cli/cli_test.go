package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
)

// captureStdout replaces os.Stdout, runs f, and returns what was written.
func captureStdout(f func()) string {
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w

	f()

	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}

func TestVersionFlags(t *testing.T) {
	cases := []struct {
		args []string
	}{
		{[]string{"--version"}},
		{[]string{"-V"}},
		{[]string{"version"}},
	}
	for _, c := range cases {
		t.Run(strings.Join(c.args, " "), func(t *testing.T) {
			var code int
			out := captureStdout(func() {
				code = Run(context.Background(), c.args)
			})
			if code != 0 {
				t.Errorf("args %v: exit code %d, want 0", c.args, code)
			}
			if strings.TrimSpace(out) == "" {
				t.Errorf("args %v: version output is empty", c.args)
			}
		})
	}
}
