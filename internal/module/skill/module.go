package skill

import (
	"strings"

	"github.com/kelindar/event"
	"go.uber.org/fx"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

// TODO(P7): 接入事件驱动 auto-match。当前仅 skills/match/preview RPC 触发，
// 无运行时自动触发（如 thread 启动时自动匹配）。需要订阅 thread.Started 事件
// 并在回调中执行 auto-match + 绑定到 session。
var Module = fx.Module("skill",
	fx.Provide(newService),
	fx.Provide(NewSkillHandlers),
)

func newService(cfg *platformconfig.Config, dispatcher *event.Dispatcher) Service {
	projectRoot := ""
	if cfg != nil {
		projectRoot = strings.TrimSpace(cfg.ProjectRoot)
	}
	svc := NewService(projectRoot)
	if impl, ok := svc.(*service); ok {
		impl.bindDispatcher(dispatcher)
	}
	return svc
}
