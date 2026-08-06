package claudecli

import (
	"testing"

	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
)

// configureCaptureRuntimeHookForTest 为单个测试创建结果捕获 owner，禁止写入进程级状态。
func configureCaptureRuntimeHookForTest(t *testing.T, capture providershared.CaptureToolResultFunc) providershared.RuntimeHooks {
	t.Helper()
	hooks, err := configureRuntimeHooksForTest(capture)
	if err != nil {
		t.Fatalf("configure runtime hooks: %v", err)
	}
	return hooks
}

func testRuntimeHooks(t *testing.T) providershared.RuntimeHooks {
	t.Helper()
	hooks, err := configureDefaultRuntimeHooksForTest()
	if err != nil {
		t.Fatalf("configure default runtime hooks: %v", err)
	}
	return hooks
}

func configureDefaultRuntimeHooksForTest() (providershared.RuntimeHooks, error) {
	return configureRuntimeHooksForTest(func(providershared.ToolResultMeta, string) (providershared.ToolResultRecord, error) {
		return providershared.ToolResultRecord{}, nil
	})
}

func configureRuntimeHooksForTest(capture providershared.CaptureToolResultFunc) (providershared.RuntimeHooks, error) {
	return providershared.ConfigureRuntimeHooks(providershared.RuntimeHooks{
		Capture: capture,
		Reset: func(string, string) error {
			return nil
		},
	})
}
