package orchestration

import (
	"encoding/json"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
)

func TestResolveRetryPolicy_DAGMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		metadata string
		want     RetryPolicy
	}{
		{
			name:     "empty metadata defaults to single attempt no fail-fast",
			metadata: ``,
			want:     RetryPolicy{MaxAttempts: 1, FailFast: false},
		},
		{
			name:     "default_retry zero means single attempt",
			metadata: `{"schedule":{"default_retry":0}}`,
			want:     RetryPolicy{MaxAttempts: 1, FailFast: false},
		},
		{
			name:     "default_retry two means three total attempts",
			metadata: `{"schedule":{"default_retry":2}}`,
			want:     RetryPolicy{MaxAttempts: 3, FailFast: false},
		},
		{
			name:     "fail_fast propagates",
			metadata: `{"schedule":{"default_retry":1,"fail_fast":true}}`,
			want:     RetryPolicy{MaxAttempts: 2, FailFast: true},
		},
		{
			name:     "negative default_retry clamped to single attempt",
			metadata: `{"schedule":{"default_retry":-5}}`,
			want:     RetryPolicy{MaxAttempts: 1, FailFast: false},
		},
		{
			name:     "malformed json silently degrades to defaults",
			metadata: `{"schedule": broken}`,
			want:     RetryPolicy{MaxAttempts: 1, FailFast: false},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ResolveRetryPolicy(json.RawMessage(tt.metadata), nil)
			if got != tt.want {
				t.Fatalf("ResolveRetryPolicy(%q) = %+v, want %+v", tt.metadata, got, tt.want)
			}
		})
	}
}

func TestResolveRetryPolicy_NodeConfigOverridesDAGRetry(t *testing.T) {
	t.Parallel()

	dagMetadata := json.RawMessage(`{"schedule":{"default_retry":5,"fail_fast":true}}`)

	tests := []struct {
		name   string
		config string
		want   RetryPolicy
	}{
		{
			name:   "node without execution falls back to DAG default",
			config: `{}`,
			want:   RetryPolicy{MaxAttempts: 6, FailFast: true},
		},
		{
			name:   "node execution.retry zero overrides DAG default to single attempt",
			config: `{"execution":{"retry":0}}`,
			want:   RetryPolicy{MaxAttempts: 1, FailFast: true},
		},
		{
			name:   "node execution.retry two overrides DAG default",
			config: `{"execution":{"retry":2}}`,
			want:   RetryPolicy{MaxAttempts: 3, FailFast: true},
		},
		{
			name:   "fail_fast comes from DAG even if node overrides retry",
			config: `{"execution":{"retry":1}}`,
			want:   RetryPolicy{MaxAttempts: 2, FailFast: true},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ResolveRetryPolicy(dagMetadata, json.RawMessage(tt.config))
			if got != tt.want {
				t.Fatalf("ResolveRetryPolicy(node=%q) = %+v, want %+v", tt.config, got, tt.want)
			}
		})
	}
}

func TestFailureClassPermanent_F14BasicRetryClasses(t *testing.T) {
	t.Parallel()

	retryable := []nodeexec.FailureClass{
		nodeexec.FailureClassTransient,
		nodeexec.FailureClassQuota,
		nodeexec.FailureClassValidation,
	}
	for _, class := range retryable {
		class := class
		t.Run(string(class)+"_uses_bounded_retry", func(t *testing.T) {
			t.Parallel()
			if failureClassPermanent(class) {
				t.Fatalf("failureClassPermanent(%q) = true, want false", class)
			}
		})
	}

	permanent := []nodeexec.FailureClass{
		nodeexec.FailureClassHard,
		nodeexec.FailureClassNeedsHuman,
	}
	for _, class := range permanent {
		class := class
		t.Run(string(class)+"_bypasses_retry", func(t *testing.T) {
			t.Parallel()
			if !failureClassPermanent(class) {
				t.Fatalf("failureClassPermanent(%q) = false, want true", class)
			}
		})
	}
}

func TestFailureOutcomePermanent_ValidationLaunchOnlyRetries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		outcome nodeexec.NodeOutcome
		want    bool
	}{
		{
			name: "launch_validation_uses_bounded_retry",
			outcome: nodeexec.NodeOutcome{
				Status:       nodeexec.NodeStatusFailed,
				FailureClass: nodeexec.FailureClassValidation,
				ErrorSummary: "launch agent: 401 unauthorized",
			},
			want: false,
		},
		{
			name: "config_validation_bypasses_retry",
			outcome: nodeexec.NodeOutcome{
				Status:       nodeexec.NodeStatusFailed,
				FailureClass: nodeexec.FailureClassValidation,
				ErrorSummary: "decode agent config: invalid json",
			},
			want: true,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := failureOutcomePermanent(tt.outcome); got != tt.want {
				t.Fatalf("failureOutcomePermanent() = %v, want %v", got, tt.want)
			}
		})
	}
}
