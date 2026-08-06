package shared

import (
	"errors"
	"testing"
)

func TestRuntimeHooksReturnErrorWhenNotConfigured(t *testing.T) {
	hooks := RuntimeHooks{}

	if _, err := hooks.CaptureToolResult(ToolResultMeta{ThreadID: "thread-1", TurnID: "turn-1"}, "result"); err == nil {
		t.Fatal("RuntimeHooks.CaptureToolResult() error = nil, want configuration error")
	}
	if err := hooks.ResetToolResultScope("thread-1", "turn-1"); err == nil {
		t.Fatal("RuntimeHooks.ResetToolResultScope() error = nil, want configuration error")
	}
}

func TestConfigureRuntimeHooksRejectsIncompleteBundle(t *testing.T) {
	if _, err := ConfigureRuntimeHooks(RuntimeHooks{}); err == nil {
		t.Fatal("ConfigureRuntimeHooks() error = nil, want capture dependency error")
	}
	if _, err := ConfigureRuntimeHooks(RuntimeHooks{
		Capture: func(ToolResultMeta, string) (ToolResultRecord, error) {
			return ToolResultRecord{}, nil
		},
	}); err == nil {
		t.Fatal("ConfigureRuntimeHooks() error = nil, want reset dependency error")
	}
}

func TestConfiguredRuntimeHooksDelegateOperations(t *testing.T) {
	wantErr := errors.New("capture failed")
	resetCalled := false

	hooks, err := ConfigureRuntimeHooks(RuntimeHooks{
		Capture: func(ToolResultMeta, string) (ToolResultRecord, error) {
			return ToolResultRecord{Preview: "preview"}, wantErr
		},
		Reset: func(string, string) error {
			resetCalled = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("ConfigureRuntimeHooks() error = %v", err)
	}

	result, err := hooks.CaptureToolResult(ToolResultMeta{}, "raw")
	if !errors.Is(err, wantErr) || result.Preview != "preview" {
		t.Fatalf("RuntimeHooks.CaptureToolResult() = (%+v, %v), want preview and capture error", result, err)
	}
	if err := hooks.ResetToolResultScope("thread-1", "turn-1"); err != nil {
		t.Fatalf("RuntimeHooks.ResetToolResultScope() error = %v", err)
	}
	if !resetCalled {
		t.Fatal("ResetToolResultScope() did not delegate")
	}
}

func TestRuntimeHooksOwnersDoNotOverrideEachOther(t *testing.T) {
	first, err := ConfigureRuntimeHooks(RuntimeHooks{
		Capture: func(ToolResultMeta, string) (ToolResultRecord, error) { return ToolResultRecord{Preview: "first"}, nil },
		Reset:   func(string, string) error { return errors.New("first reset") },
	})
	if err != nil {
		t.Fatalf("ConfigureRuntimeHooks(first): %v", err)
	}
	second, err := ConfigureRuntimeHooks(RuntimeHooks{
		Capture: func(ToolResultMeta, string) (ToolResultRecord, error) {
			return ToolResultRecord{Preview: "second"}, nil
		},
		Reset: func(string, string) error { return errors.New("second reset") },
	})
	if err != nil {
		t.Fatalf("ConfigureRuntimeHooks(second): %v", err)
	}
	if result, err := first.CaptureToolResult(ToolResultMeta{}, ""); err != nil || result.Preview != "first" {
		t.Fatalf("first CaptureToolResult() = (%+v, %v), want first owner", result, err)
	}
	if err := second.ResetToolResultScope("thread", "turn"); err == nil || err.Error() != "second reset" {
		t.Fatalf("second ResetToolResultScope() error = %v, want second owner", err)
	}
}
