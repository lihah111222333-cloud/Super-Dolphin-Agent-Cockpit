package observability

import (
	"strings"
	"testing"
)

func TestStackCaptureCompactLimitAndShape(t *testing.T) {
	cfg, err := ParseConfig(EnvMap{"OBS_TRACING_ENABLED": "1", "OBS_STACK_MAX_FRAMES": "4", "OBS_STACK_MAX_BYTES": "1024"})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	frames := stackTestHelper(cfg)
	if len(frames) == 0 {
		t.Fatalf("CaptureStack returned no frames")
	}
	if len(frames) > cfg.StackMaxFrames {
		t.Fatalf("len(frames) = %d, want <= %d", len(frames), cfg.StackMaxFrames)
	}
	encoded := string(mustStackJSON(t, frames))
	if len(encoded) > cfg.StackMaxBytes {
		t.Fatalf("stack bytes = %d, want <= %d", len(encoded), cfg.StackMaxBytes)
	}
	assertValidStackFrames(t, frames)
	if !hasStackTestFrame(frames) {
		t.Fatalf("expected application test frame in %+v", frames)
	}
}

func assertValidStackFrames(t *testing.T, frames []StackFrame) {
	t.Helper()
	for _, frame := range frames {
		if frame.File == "" || frame.Function == "" || frame.Line <= 0 {
			t.Fatalf("invalid frame shape: %+v", frame)
		}
	}
}

func hasStackTestFrame(frames []StackFrame) bool {
	for _, frame := range frames {
		if strings.HasSuffix(frame.File, "stack_test.go") {
			return true
		}
	}
	return false
}

func TestStackCaptureDisabledForNonConfiguredStatus(t *testing.T) {
	cfg, err := ParseConfig(EnvMap{"OBS_TRACING_ENABLED": "1", "OBS_TRACE_STACKS": "error"})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if got := CaptureStackForStatus(cfg, StatusSlow); len(got) != 0 {
		t.Fatalf("CaptureStackForStatus slow returned %d frames, want 0", len(got))
	}
	if got := CaptureStackForStatus(cfg, StatusError); len(got) == 0 {
		t.Fatalf("CaptureStackForStatus error returned no frames")
	}
}

func stackTestHelper(cfg Config) []StackFrame {
	return CaptureStack(cfg)
}

func mustStackJSON(t *testing.T, frames []StackFrame) []byte {
	t.Helper()
	data, err := MarshalStackJSON(frames)
	if err != nil {
		t.Fatalf("MarshalStackJSON: %v", err)
	}
	return data
}
