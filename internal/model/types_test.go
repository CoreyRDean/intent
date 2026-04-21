package model

import "testing"

func TestResponseValidateCommandRequiresRuntimeAndConfidence(t *testing.T) {
	base := &Response{
		Approach:    ApproachCommand,
		Command:     "ls -la",
		Description: "List files.",
		Risk:        RiskSafe,
	}

	if err := base.Validate(); err == nil || err.Error() != "approach=command requires expected_runtime" {
		t.Fatalf("expected missing-runtime error, got: %v", err)
	}

	base.ExpectedRuntime = RuntimeInstant
	if err := base.Validate(); err == nil || err.Error() != "approach=command requires confidence" {
		t.Fatalf("expected missing-confidence error, got: %v", err)
	}

	base.Confidence = ConfidenceHigh
	if err := base.Validate(); err != nil {
		t.Fatalf("expected command response to validate, got: %v", err)
	}
}

func TestResponseValidateScriptRequiresRuntimeAndConfidence(t *testing.T) {
	base := &Response{
		Approach:    ApproachScript,
		Script:      &Script{Interpreter: "bash", Body: "echo hi"},
		Description: "Print text.",
		Risk:        RiskSafe,
	}

	if err := base.Validate(); err == nil || err.Error() != "approach=script requires expected_runtime" {
		t.Fatalf("expected missing-runtime error, got: %v", err)
	}

	base.ExpectedRuntime = RuntimeInstant
	if err := base.Validate(); err == nil || err.Error() != "approach=script requires confidence" {
		t.Fatalf("expected missing-confidence error, got: %v", err)
	}

	base.Confidence = ConfidenceMedium
	if err := base.Validate(); err != nil {
		t.Fatalf("expected script response to validate, got: %v", err)
	}
}
