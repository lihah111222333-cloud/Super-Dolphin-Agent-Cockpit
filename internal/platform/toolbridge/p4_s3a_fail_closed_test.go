package toolbridge

import (
	"context"
	"errors"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
)

// TestToolbridgePersistentSubagentRejectsMissingRuntime 锁定 spawn_agent 策略门禁的 fail-closed 行为。
// 当 thread 身份或 runtime 配置缺失时，routeToolCall 必须返回对应错误，不能借用
// cfg.Agent.PersistentSubagentDefault 继续创建持久子 agent。
//
// 该用例同时覆盖无 thread 身份、thread 存在但 runtime 行缺失两类入口。
// 测试会刻意启用 PersistentSubagentDefault，确保默认值只在 thread 与 runtime
// 都加载成功后才可用于策略判定。
func TestToolbridgePersistentSubagentRejectsMissingRuntime(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		handlerSetup func(h *Handler)
		req          ToolCallRequest
		wantErr      error
	}{
		{
			name: "missing thread identity",
			// 默认测试 Handler 未接入 bindingStore / threadStore，因此无法解析 thread 身份。
			handlerSetup: nil,
			req: ToolCallRequest{
				Name:      "spawn_agent",
				Arguments: mustRawJSON(t, map[string]any{"message": "create child agent"}),
			},
			wantErr: contract.ErrThreadRuntimeRequired,
		},
		{
			name: "missing runtime row",
			handlerSetup: func(h *Handler) {
				h.threadStore = &stubThreadStore{}
			},
			req: ToolCallRequest{
				Name:      "spawn_agent",
				Arguments: mustRawJSON(t, map[string]any{"message": "create child agent"}),
				ThreadID:  "thread-missing-runtime",
			},
			wantErr: contract.ErrPersistentSubagentRuntimeRequired,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h, _ := newHandlerForTest()
			// 故意启用 PersistentSubagentDefault；即便全局默认允许持久子 agent，
			// 缺少身份或 runtime 时也必须 fail-closed。
			h.cfg = &platformconfig.Config{
				Agent: platformconfig.AgentConfig{PersistentSubagentDefault: true},
			}
			if tt.handlerSetup != nil {
				tt.handlerSetup(h)
			}

			got, err := h.routeToolCall(context.Background(), tt.req)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("routeToolCall() error = %v, want %v (fail-closed even with PersistentSubagentDefault=true)", err, tt.wantErr)
			}
			if got != nil {
				t.Fatalf("routeToolCall() result = %#v, want nil (no block message should leak when identity is missing)", got)
			}
		})
	}
}
