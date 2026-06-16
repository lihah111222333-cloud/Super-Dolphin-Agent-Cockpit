package summarysuggest

import (
	"context"
	"errors"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// TestExecuteWithOptionsRetriesRetryableParseErrors verifies transient parser failures get one retry.
func TestExecuteWithOptionsRetriesRetryableParseErrors(t *testing.T) {
	t.Parallel()

	dream := &recordingDream{outputs: []string{"bad", "good"}}
	got, err := ExecuteWithOptions(context.Background(), dream, "prompt", contract.DreamOptions{}, func(raw string) (string, error) {
		if raw == "bad" {
			return "", errors.New("parse skill summary suggestion: bad JSON")
		}
		return raw, nil
	})
	if err != nil {
		t.Fatalf("ExecuteWithOptions() error = %v", err)
	}
	if got != "good" {
		t.Fatalf("ExecuteWithOptions() = %q, want good", got)
	}
	if dream.calls != 2 {
		t.Fatalf("dream calls = %d, want 2", dream.calls)
	}
}

// TestExecuteWithOptionsStopsOnNonRetryableParseError verifies validation failures are not retried.
func TestExecuteWithOptionsStopsOnNonRetryableParseError(t *testing.T) {
	t.Parallel()

	dream := &recordingDream{outputs: []string{"bad", "unused"}}
	_, err := ExecuteWithOptions(context.Background(), dream, "prompt", contract.DreamOptions{}, func(string) (string, error) {
		return "", errors.New("validation failed")
	})
	if err == nil || err.Error() != "validation failed" {
		t.Fatalf("ExecuteWithOptions() error = %v, want validation failed", err)
	}
	if dream.calls != 1 {
		t.Fatalf("dream calls = %d, want 1", dream.calls)
	}
}

// TestExecuteWithOptionsUsesDreamExecutorWithOptions verifies model options reach capable executors.
func TestExecuteWithOptionsUsesDreamExecutorWithOptions(t *testing.T) {
	t.Parallel()

	dream := &recordingDreamWithOptions{recordingDream: recordingDream{outputs: []string{"ok"}}}
	options := contract.DreamOptions{Provider: "codex", Model: "gpt"}
	got, err := ExecuteWithOptions(context.Background(), dream, "prompt", options, func(raw string) (string, error) {
		return raw, nil
	})
	if err != nil {
		t.Fatalf("ExecuteWithOptions() error = %v", err)
	}
	if got != "ok" {
		t.Fatalf("ExecuteWithOptions() = %q, want ok", got)
	}
	if dream.optionCalls != 1 || dream.calls != 0 {
		t.Fatalf("optionCalls=%d calls=%d, want option path only", dream.optionCalls, dream.calls)
	}
	if dream.lastOptions != options {
		t.Fatalf("options = %#v, want %#v", dream.lastOptions, options)
	}
}

type recordingDream struct {
	outputs []string
	calls   int
}

// ExecuteDream records a basic Dream execution and returns the next scripted output.
func (d *recordingDream) ExecuteDream(context.Context, string) (string, error) {
	out := d.outputs[d.calls]
	d.calls++
	return out, nil
}

type recordingDreamWithOptions struct {
	recordingDream
	optionCalls int
	lastOptions contract.DreamOptions
}

// ExecuteDreamWithOptions records options-aware Dream execution and returns the next scripted output.
func (d *recordingDreamWithOptions) ExecuteDreamWithOptions(_ context.Context, _ string, options contract.DreamOptions) (string, error) {
	out := d.outputs[d.optionCalls]
	d.optionCalls++
	d.lastOptions = options
	return out, nil
}
