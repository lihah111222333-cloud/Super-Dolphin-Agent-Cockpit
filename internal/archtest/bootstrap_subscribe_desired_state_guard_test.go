package archtest

import (
	"os"
	"strings"
	"testing"
)

// TestBootstrapSubscribeHooksPersistsDesiredStateOnLiveFailure 确认 SubscribeHooks 的 live-call
// 失败分支会先持久化期望订阅状态再返回错误，保证重连路径可重放订阅。
//
// 该 archtest 有意使用源码形状检查：hooks.go 必须同时保留 `c.hooks.store(` 和
// `c.hooks.markReplayPending(`，未来重构如果删掉持久化调用会直接失败。
func TestBootstrapSubscribeHooksPersistsDesiredStateOnLiveFailure(t *testing.T) {
	const path = "../../internal/mcpserver/common/bootstrap/hooks.go"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	text := string(data)

	required := []string{
		"c.hooks.store(",             // 必须持久化期望状态
		"c.hooks.markReplayPending(", // 并标记为待重放
	}
	for _, tok := range required {
		if !strings.Contains(text, tok) {
			t.Errorf("%s: expected %q to be present (live-call failure must persist desired state for replay)", path, tok)
		}
	}
}
