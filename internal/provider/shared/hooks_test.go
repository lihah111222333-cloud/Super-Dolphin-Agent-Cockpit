package shared

import (
	"errors"
	"testing"
)

func TestRuntimeHooksReturnErrorWhenNotConfigured(t *testing.T) {
	clearRuntimeHooksForTest(t)

	if _, err := CaptureToolResult(ToolResultMeta{ThreadID: "thread-1", TurnID: "turn-1"}, "result"); err == nil {
		t.Fatal("CaptureToolResult() error = nil, want configuration error")
	}
	if err := ResetToolResultScope("thread-1", "turn-1"); err == nil {
		t.Fatal("ResetToolResultScope() error = nil, want configuration error")
	}
}

func TestConfigureRuntimeHooksRejectsIncompleteBundle(t *testing.T) {
	clearRuntimeHooksForTest(t)

	if _, err := ConfigureRuntimeHooks(RuntimeHooks{}); err == nil {
		t.Fatal("ConfigureRuntimeHooks() error = nil, want capture dependency error")
	}
	if _, err := ConfigureRuntimeHooks(RuntimeHooks{
		CaptureToolResult: func(ToolResultMeta, string) (ToolResultRecord, error) {
			return ToolResultRecord{}, nil
		},
	}); err == nil {
		t.Fatal("ConfigureRuntimeHooks() error = nil, want reset dependency error")
	}
}

func TestConfiguredRuntimeHooksDelegateOperations(t *testing.T) {
	clearRuntimeHooksForTest(t)
	wantErr := errors.New("capture failed")
	resetCalled := false

	_, err := ConfigureRuntimeHooks(RuntimeHooks{
		CaptureToolResult: func(ToolResultMeta, string) (ToolResultRecord, error) {
			return ToolResultRecord{Preview: "preview"}, wantErr
		},
		ResetToolResultScope: func(string, string) error {
			resetCalled = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("ConfigureRuntimeHooks() error = %v", err)
	}

	result, err := CaptureToolResult(ToolResultMeta{}, "raw")
	if !errors.Is(err, wantErr) || result.Preview != "preview" {
		t.Fatalf("CaptureToolResult() = (%+v, %v), want preview and capture error", result, err)
	}
	if err := ResetToolResultScope("thread-1", "turn-1"); err != nil {
		t.Fatalf("ResetToolResultScope() error = %v", err)
	}
	if !resetCalled {
		t.Fatal("ResetToolResultScope() did not delegate")
	}
}

func clearRuntimeHooksForTest(t *testing.T) {
	t.Helper()
	oldHooks := runtimeHooks.Load()
	runtimeHooks.Store(nil)
	t.Cleanup(func() {
		runtimeHooks.Store(oldHooks)
	})
}
