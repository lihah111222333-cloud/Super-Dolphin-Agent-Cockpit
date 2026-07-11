package codexapp

import (
	"testing"

	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
)

// configureCaptureRuntimeHookForTest 临时替换结果捕获依赖，并在测试结束时恢复默认实现。
func configureCaptureRuntimeHookForTest(t *testing.T, capture providershared.CaptureToolResultFunc) {
	t.Helper()
	if err := configureRuntimeHooksForTest(capture); err != nil {
		t.Fatalf("configure runtime hooks: %v", err)
	}
	t.Cleanup(func() {
		if err := configureDefaultRuntimeHooksForTest(); err != nil {
			t.Errorf("restore default runtime hooks: %v", err)
		}
	})
}

func configureDefaultRuntimeHooksForTest() error {
	return configureRuntimeHooksForTest(func(_ providershared.ToolResultMeta, raw string) (providershared.ToolResultRecord, error) {
		return providershared.ToolResultRecord{Preview: raw, OriginalSize: len(raw)}, nil
	})
}

func configureRuntimeHooksForTest(capture providershared.CaptureToolResultFunc) error {
	_, err := providershared.ConfigureRuntimeHooks(providershared.RuntimeHooks{
		CaptureToolResult: capture,
		ResetToolResultScope: func(string, string) error {
			return nil
		},
	})
	return err
}
