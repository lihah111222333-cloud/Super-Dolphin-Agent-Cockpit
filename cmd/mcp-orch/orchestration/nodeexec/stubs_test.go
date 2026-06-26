package nodeexec

import (
	"context"
	"testing"
)

// stubExecutorCases 集中表驱动测试三种 stub 的统一形状。
func stubExecutorCases() []struct {
	name     string
	exec     NodeExecutor
	nodeType string
} {
	return []struct {
		name     string
		exec     NodeExecutor
		nodeType string
	}{
		// AgentExecutor / AutomationExecutor 已有真实实现。
		// 本表只覆盖仍保持占位行为的 Hybrid。
		{"HybridExecutor", HybridExecutor{}, "hybrid"},
	}
}

func TestExecutorStubs_ImplementNodeExecutor(t *testing.T) {
	t.Parallel()
	// 编译期检查
	var _ NodeExecutor = HybridExecutor{}
}

func TestExecutorStubs_ReturnDone(t *testing.T) {
	t.Parallel()
	for _, tc := range stubExecutorCases() {
		t.Run(tc.name, func(t *testing.T) {
			out, err := tc.exec.Execute(context.Background(), Node{NodeType: tc.nodeType}, RunContext{})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if out.Status != NodeStatusDone {
				t.Fatalf("Status = %q, want %q", out.Status, NodeStatusDone)
			}
		})
	}
}

func TestExecutorStubs_HooksNil(t *testing.T) {
	t.Parallel()
	for _, tc := range stubExecutorCases() {
		t.Run(tc.name, func(t *testing.T) {
			if hooks := tc.exec.Hooks(); hooks != nil {
				t.Fatalf("Hooks() should return nil in skeleton stub")
			}
		})
	}
}
