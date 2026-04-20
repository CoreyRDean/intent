// Package mock provides a deterministic Backend for tests and developer
// hacking. It accepts the same CompleteRequest as a real backend but produces
// a canned response based on simple keyword rules. Useful when you don't want
// to wait on a model download.
package mock

import (
	"context"
	"strings"

	"github.com/CoreyRDean/intent/internal/model"
)

type Backend struct{}

func New() *Backend { return &Backend{} }

func (b *Backend) Name() string                            { return "mock" }
func (b *Backend) Available(ctx context.Context) error     { return nil }

// Complete returns a hard-coded response keyed off the last user message.
// This is intentionally dumb. It exists so the rest of the pipeline can be
// exercised end-to-end without a real model.
func (b *Backend) Complete(ctx context.Context, req model.CompleteRequest) (*model.Response, error) {
	prompt := lastUser(req.Messages)
	p := strings.ToLower(prompt)

	switch {
	case strings.Contains(p, "ping") && strings.Contains(p, "google"):
		return &model.Response{
			IntentSummary:   "Check whether Google's public DNS responds to a single ICMP echo.",
			Approach:        model.ApproachCommand,
			Command:         "ping -c 1 -W 1 8.8.8.8",
			Description:     "Send one ICMP echo to 8.8.8.8 and report whether it responded.",
			Risk:            model.RiskNetwork,
			ExpectedRuntime: model.RuntimeInstant,
			Confidence:      model.ConfidenceHigh,
		}, nil
	case strings.Contains(p, "list") && strings.Contains(p, "files"):
		return &model.Response{
			IntentSummary:   "List files in the current directory.",
			Approach:        model.ApproachCommand,
			Command:         "ls -la",
			Description:     "List files and directories in the current working directory, including hidden entries and metadata.",
			Risk:            model.RiskSafe,
			ExpectedRuntime: model.RuntimeInstant,
			Confidence:      model.ConfidenceHigh,
		}, nil
	case strings.Contains(p, "what is my ip"), strings.Contains(p, "whats my ip"), strings.Contains(p, "what's my ip"):
		return &model.Response{
			IntentSummary:   "Report the user's current public IP.",
			Approach:        model.ApproachCommand,
			Command:         "curl -s https://api.ipify.org",
			Description:     "Fetch your current public IP address from ipify.org.",
			Risk:            model.RiskNetwork,
			ExpectedRuntime: model.RuntimeInstant,
			Confidence:      model.ConfidenceHigh,
		}, nil
	case strings.Contains(p, "hello"), prompt == "":
		return &model.Response{
			IntentSummary: "Print a friendly greeting.",
			Approach:      model.ApproachInform,
			StdoutToUser:  "hello, intent. (mock backend — no model installed yet.)\n",
			Confidence:    model.ConfidenceHigh,
		}, nil
	default:
		return &model.Response{
			IntentSummary:   "Print the user's request, since the mock backend cannot interpret it.",
			Approach:        model.ApproachCommand,
			Command:         "echo " + shellQuote(prompt),
			Description:     "Echo the user's request. The mock backend doesn't recognize this intent; install a real model to get a useful answer.",
			Risk:            model.RiskSafe,
			ExpectedRuntime: model.RuntimeInstant,
			Confidence:      model.ConfidenceLow,
		}, nil
	}
}

func lastUser(msgs []model.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			return msgs[i].Content
		}
	}
	return ""
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
