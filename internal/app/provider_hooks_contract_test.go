package app

import (
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/turn"
	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
)

// TestProviderRuntimeHooksAreWiredThroughAppAssembly 验证根图适配器能发布完整依赖。
func TestProviderRuntimeHooksAreWiredThroughAppAssembly(t *testing.T) {
	hooks, err := provideProviderRuntimeHooks(turn.NewToolResultRuntime())
	if err != nil {
		t.Fatalf("provideProviderRuntimeHooks() error = %v", err)
	}

	result, err := hooks.CaptureToolResult(providershared.ToolResultMeta{
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		CallID:   "call-1",
		ToolName: "Read",
	}, "result")
	if err != nil {
		t.Fatalf("CaptureToolResult() error = %v", err)
	}
	if result.Preview != "result" {
		t.Fatalf("CaptureToolResult().Preview = %q, want result", result.Preview)
	}
	if err := hooks.ResetToolResultScope("thread-1", "turn-1"); err != nil {
		t.Fatalf("ResetToolResultScope() error = %v", err)
	}
}

func TestProviderRuntimeHooksRequireToolResultRuntime(t *testing.T) {
	if _, err := provideProviderRuntimeHooks(nil); err == nil {
		t.Fatal("provideProviderRuntimeHooks(nil) error = nil, want required runtime error")
	}
}
