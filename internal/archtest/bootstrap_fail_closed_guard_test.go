package archtest

import (
	"os"
	"strings"
	"testing"
)

// TestBootstrapHandleCallbackFailsClosedOnUnknownMethod 验证 bootstrap client 不会默认 ACK 未知方法。
// 该守卫检查源码形状，确保未知方法继续返回 method_not_found，wire 行为变化时 diff 可直接暴露。
func TestBootstrapHandleCallbackFailsClosedOnUnknownMethod(t *testing.T) {
	const path = "../../internal/mcpserver/common/bootstrap/lifecycle.go"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	text := string(data)
	required := []string{
		"errBootstrapUnknownMethod(",
		"dispatchLifecycleRequest(",
		"contract.CodeMethodNotFound",
	}
	for _, tok := range required {
		if !strings.Contains(text, tok) {
			t.Errorf("%s: expected %q to be present (fail-closed unknown method must stay wired)", path, tok)
		}
	}
	// 旧的默认成功响应形状不得重新出现。
	forbidden := []string{
		"return map[string]bool{\"ok\": true}, nil\n}\n\nfunc (c *Client) dispatchRequest",
	}
	for _, tok := range forbidden {
		if strings.Contains(text, tok) {
			t.Errorf("%s: forbidden default-ACK shape reappeared near %q", path, tok)
		}
	}
}

// TestBootstrapPendingHooksDropsBootAgentIDFallback 验证 PendingHooks 只使用 cfg.AgentID 作为身份来源。
// 守卫禁止重新引入 boot.AgentID fallback，避免订阅恢复路径在多身份场景下串到错误 agent。
func TestBootstrapPendingHooksDropsBootAgentIDFallback(t *testing.T) {
	const path = "../../internal/mcpserver/common/bootstrap/hooks.go"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	text := string(data)

	forbidden := []string{
		"FirstNonEmpty(c.cfg.AgentID, c.boot.AgentID)",
	}
	for _, tok := range forbidden {
		if strings.Contains(text, tok) {
			t.Errorf("%s: forbidden FirstNonEmpty fallback present (cfg.AgentID must be the sole identity source)", path)
		}
	}

	required := []string{
		"agentID := strings.TrimSpace(c.cfg.AgentID)",
		"errHookPendingAgentIDRequired()",
		"mcp.HookPendingRequest{AgentID: agentID}",
	}
	for _, tok := range required {
		if !strings.Contains(text, tok) {
			t.Errorf("%s: expected %q to be present (pending hooks must use cfg.AgentID only)", path, tok)
		}
	}
}
