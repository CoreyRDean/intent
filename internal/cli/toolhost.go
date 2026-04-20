package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/CoreyRDean/intent/internal/tui"
)

// cliToolHost implements tools.Host for the interactive CLI. It
// bridges the agentic loop's ask_user tool to the user's real TTY,
// pausing any active spinner while the question is on screen so the
// user actually sees it.
//
// If stderr (for the prompt) or stdin (for the answer) isn't a TTY,
// AskUser returns an error so the model can fall back to `clarify`
// or a best-effort default. Intent running in a pipe (e.g. the README
// example `i A | i B`) trips this path by design: there's no human to
// interrupt, so the agent must commit to a final answer instead.
type cliToolHost struct {
	sp *tui.Spinner
}

func (h *cliToolHost) AskUser(ctx context.Context, question string, choices []string) (string, error) {
	if !tui.IsTTY(os.Stderr) || !tui.IsTTY(os.Stdin) {
		return "", fmt.Errorf("no interactive TTY")
	}

	// The question ends up on the same stderr the spinner is painting
	// on, so we must tear the animation down before we print. Resume
	// puts it back so the next tool call / backend step is still
	// narrated.
	var resume func()
	if h.sp != nil {
		resume = h.sp.Suspend()
	}
	defer func() {
		if resume != nil {
			resume()
		}
	}()

	style := tui.DefaultStyle()
	fmt.Fprintln(style.Stderr)
	fmt.Fprintf(style.Stderr, "  %s %s\n", style.Dim("?"), question)
	if len(choices) > 0 {
		// Render choices on one line, comma-separated, so the prompt
		// stays compact even when the spinner is still paused.
		fmt.Fprintf(style.Stderr, "    options: %s\n", strings.Join(choices, ", "))
	}
	fmt.Fprint(style.Stderr, "  > ")

	// Read one line. We share os.Stdin with the rest of the CLI; the
	// confirm prompt uses a bufio.NewReader(os.Stdin) as well, so
	// there's precedent -- any buffered-but-unread bytes between
	// rounds are vanishingly unlikely in the interactive path.
	r := bufio.NewReader(os.Stdin)

	doneCh := make(chan struct{})
	var (
		line    string
		readErr error
	)
	go func() {
		defer close(doneCh)
		line, readErr = r.ReadString('\n')
	}()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-doneCh:
	}
	if readErr != nil {
		return "", readErr
	}
	return strings.TrimRight(line, "\r\n"), nil
}
