package thread

import (
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/module/prompt/classifier"
)

// TestClassifierReady_NilClassifierFailsClosed asserts that when fx hasn't
// wired a classifier at all, classifierReady() returns false so the router
// degrades to match_when / default fallback instead of dereferencing nil.
func TestClassifierReady_NilClassifierFailsClosed(t *testing.T) {
	t.Parallel()
	s := &service{}
	if s.classifierReady() {
		t.Fatal("nil classifier must report not-ready")
	}
}

// TestClassifierReady_NoopClassifierFailsClosed asserts that when the
// classifier backend auto-degraded to NoopClassifier (because `claude` CLI
// was not on PATH, or because DISABLE_PROMPT_CLASSIFIER=true), the router
// still fail-closes. This is the real "backend is the disable gate" path
// the P2 fix preserves.
func TestClassifierReady_NoopClassifierFailsClosed(t *testing.T) {
	t.Parallel()
	s := &service{classifier: classifier.NoopClassifier{}}
	if s.classifierReady() {
		t.Fatal("NoopClassifier must report not-ready (Enabled=false)")
	}
}

// TestClassifierReady_EnabledClassifierIsReady asserts that any non-nil
// classifier whose Enabled() returns true is treated as ready. After the
// P2 fix this is the *only* runtime gate: there is no ENABLE_PROMPT_CLASSIFIER
// env var blocking the UI toggle from taking effect.
func TestClassifierReady_EnabledClassifierIsReady(t *testing.T) {
	t.Parallel()
	s := &service{classifier: &fakeClassifier{}}
	if !s.classifierReady() {
		t.Fatal("Enabled classifier must report ready")
	}
}

// TestClassifierBackendDisabledHint_DescribesRealCauses pins down the
// diagnostic message that fires when the user opted-in via the UI toggle
// but the backend is not ready. After the P2 fix the hint must NOT name
// the long-gone ENABLE_PROMPT_CLASSIFIER env var (which never existed in
// the actual code path — service.go reads DISABLE_PROMPT_CLASSIFIER instead),
// and must instead point at the two real causes: claude CLI missing from
// PATH, or DISABLE_PROMPT_CLASSIFIER=true.
//
// This test is the contract for SystemPromptPage support tickets: when a
// user reports "I turned the toggle on but classifier isn't running", the
// log line they grep for must tell them which real condition to check.
func TestClassifierBackendDisabledHint_DescribesRealCauses(t *testing.T) {
	t.Parallel()
	hint := classifierBackendDisabledHint
	if strings.Contains(hint, "ENABLE_PROMPT_CLASSIFIER") {
		t.Fatalf("hint must not reference the non-existent ENABLE_PROMPT_CLASSIFIER env var, got: %q", hint)
	}
	if !strings.Contains(hint, "claude") {
		t.Fatalf("hint must mention `claude` CLI dependency, got: %q", hint)
	}
	if !strings.Contains(hint, "PATH") {
		t.Fatalf("hint must mention PATH so users know what to fix, got: %q", hint)
	}
	if !strings.Contains(hint, "DISABLE_PROMPT_CLASSIFIER") {
		t.Fatalf("hint must mention DISABLE_PROMPT_CLASSIFIER as the real opt-out, got: %q", hint)
	}
}
