package claudecli

import (
	"fmt"
	"os"
	"testing"

	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
)

// TestMain 为独立 provider 测试进程装配与生产同形的 runtime hooks。
func TestMain(m *testing.M) {
	if err := configureDefaultRuntimeHooksForTest(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "configure claudecli runtime hooks: %v\n", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

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
	return configureRuntimeHooksForTest(func(providershared.ToolResultMeta, string) (providershared.ToolResultRecord, error) {
		return providershared.ToolResultRecord{}, nil
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
