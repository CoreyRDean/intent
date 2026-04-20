// Package model defines the JSON contract every backend must satisfy
// and the Backend interface that wraps any LLM source.
package model

import (
	"context"
	"encoding/json"
	"fmt"
)

// Approach is the discriminator field on Response.
type Approach string

const (
	ApproachCommand  Approach = "command"
	ApproachScript   Approach = "script"
	ApproachToolCall Approach = "tool_call"
	ApproachClarify  Approach = "clarify"
	ApproachRefuse   Approach = "refuse"
	ApproachInform   Approach = "inform"
)

// Risk levels, ordered.
type Risk string

const (
	RiskSafe        Risk = "safe"
	RiskNetwork     Risk = "network"
	RiskMutates     Risk = "mutates"
	RiskDestructive Risk = "destructive"
	RiskSudo        Risk = "sudo"
)

// Rank returns a comparable severity for risk levels (higher = more dangerous).
func (r Risk) Rank() int {
	switch r {
	case RiskSafe:
		return 0
	case RiskNetwork:
		return 1
	case RiskMutates:
		return 2
	case RiskDestructive:
		return 3
	case RiskSudo:
		return 4
	default:
		return -1
	}
}

// AutoRunEligible reports whether `--yes` may auto-confirm at this risk level.
func (r Risk) AutoRunEligible() bool {
	return r == RiskSafe || r == RiskNetwork
}

// Runtime is a coarse expected-runtime estimate.
type Runtime string

const (
	RuntimeInstant Runtime = "instant"
	RuntimeSeconds Runtime = "seconds"
	RuntimeMinutes Runtime = "minutes"
	RuntimeLong    Runtime = "long"
)

// Confidence is the model's self-rated confidence.
type Confidence string

const (
	ConfidenceLow    Confidence = "low"
	ConfidenceMedium Confidence = "medium"
	ConfidenceHigh   Confidence = "high"
)

// Script is the body of an approach=script response.
type Script struct {
	Interpreter string `json:"interpreter"`
	Body        string `json:"body"`
}

// ToolCall is a request from the model to read context.
type ToolCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// Alternative is a sibling approach the model offers.
type Alternative struct {
	Command     string `json:"command"`
	Description string `json:"description"`
	Risk        Risk   `json:"risk"`
}

// Response is the full schema-conformant response from the model.
// See docs/SPEC.md §2 for the binding contract.
type Response struct {
	IntentSummary      string        `json:"intent_summary"`
	Approach           Approach      `json:"approach"`
	Command            string        `json:"command,omitempty"`
	Script             *Script       `json:"script,omitempty"`
	ToolCall           *ToolCall     `json:"tool_call,omitempty"`
	ClarifyingQuestion string        `json:"clarifying_question,omitempty"`
	RefusalReason      string        `json:"refusal_reason,omitempty"`
	StdoutToUser       string        `json:"stdout_to_user,omitempty"`
	Description        string        `json:"description,omitempty"`
	Risk               Risk          `json:"risk,omitempty"`
	NeedsSudo          bool          `json:"needs_sudo,omitempty"`
	ExpectedRuntime    Runtime       `json:"expected_runtime,omitempty"`
	Alternatives       []Alternative `json:"alternatives,omitempty"`
	Confidence         Confidence    `json:"confidence,omitempty"`
}

// Validate enforces the per-approach required-field rules from SPEC §2.1.
func (r *Response) Validate() error {
	if r == nil {
		return fmt.Errorf("response is nil")
	}
	switch r.Approach {
	case ApproachCommand:
		if r.Command == "" {
			return fmt.Errorf("approach=command requires command")
		}
		if r.Description == "" {
			return fmt.Errorf("approach=command requires description")
		}
		if r.Risk == "" {
			return fmt.Errorf("approach=command requires risk")
		}
	case ApproachScript:
		if r.Script == nil || r.Script.Body == "" {
			return fmt.Errorf("approach=script requires script.body")
		}
		if r.Description == "" {
			return fmt.Errorf("approach=script requires description")
		}
		if r.Risk == "" {
			return fmt.Errorf("approach=script requires risk")
		}
	case ApproachToolCall:
		if r.ToolCall == nil || r.ToolCall.Name == "" {
			return fmt.Errorf("approach=tool_call requires tool_call.name")
		}
	case ApproachClarify:
		if r.ClarifyingQuestion == "" {
			return fmt.Errorf("approach=clarify requires clarifying_question")
		}
	case ApproachRefuse:
		if r.RefusalReason == "" {
			return fmt.Errorf("approach=refuse requires refusal_reason")
		}
	case ApproachInform:
		if r.StdoutToUser == "" {
			return fmt.Errorf("approach=inform requires stdout_to_user")
		}
	default:
		return fmt.Errorf("unknown approach: %q", r.Approach)
	}
	return nil
}

// Message is a single conversation turn fed to a backend.
type Message struct {
	Role    string `json:"role"` // system | user | assistant | tool
	Name    string `json:"name,omitempty"`
	Content string `json:"content"`
}

// CompleteRequest is the input to Backend.Complete.
type CompleteRequest struct {
	Messages    []Message
	SchemaJSON  []byte // JSON schema for response_format
	GrammarGBNF string // optional GBNF for llama.cpp
	Temperature float64
	MaxTokens   int
	Seed        *int64
}

// StreamEvent is one event from a streaming backend.
type StreamEvent struct {
	Type  string // "token" | "response" | "error" | "final"
	Token string
	Final *Response
	Err   error
}

// Backend is implemented by every LLM source.
type Backend interface {
	Name() string
	Available(ctx context.Context) error
	Complete(ctx context.Context, req CompleteRequest) (*Response, error)
}

// StreamingBackend is an optional capability.
type StreamingBackend interface {
	Backend
	Stream(ctx context.Context, req CompleteRequest) (<-chan StreamEvent, error)
}

// StructuredRequest is a bare chat request for producing arbitrary
// JSON conforming to a caller-supplied schema, bypassing the standard
// Response envelope. Used by tools like `i report` that need a
// task-specific structured output rather than an approach/risk/command
// proposal.
type StructuredRequest struct {
	Messages    []Message
	SchemaJSON  []byte // REQUIRED. The schema the backend must enforce.
	Temperature float64
	MaxTokens   int
	Seed        *int64
}

// StructuredBackend is an optional capability for backends that can
// enforce an arbitrary JSON schema on output (llama.cpp / llamafile
// via response_format.schema, OpenAI via response_format.json_schema).
// Returns the raw JSON bytes the model produced, already schema-valid.
// Backends that don't support this return ErrStructuredUnsupported so
// callers can fall back cleanly.
type StructuredBackend interface {
	Backend
	CompleteStructured(ctx context.Context, req StructuredRequest) ([]byte, error)
}

// ErrStructuredUnsupported is returned by a Backend.CompleteStructured
// implementation to signal that the caller should use the envelope
// path plus best-effort parsing instead.
var ErrStructuredUnsupported = fmt.Errorf("backend does not support structured output")
