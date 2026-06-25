package insight

import (
	"log/slog"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// Module 将 insight subscriber、flusher、service 和 RPC handler 注入 Fx 树。
// subscriber 注入 BusModule 的 bus.subscribers 组；flusher 注入 runners 组由 platformrunner.RunGroup 驱动。
// 该模块不导入 turn/tracker，保证单向 observation 依赖关系不被破坏。
var Module = fx.Module("insight",
	fx.Provide(
		provideCollector,
		NewFlusher,
		NewService,
		NewInsightSubscribers,
	),
	fx.Provide(
		fx.Annotate(flusherAsRunner, fx.ResultTags(`group:"runners"`)),
	),
)

// provideCollector 用包默认容量创建 collector。
func provideCollector(logger *slog.Logger) *collector {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return newCollector(logger, defaultQueueCapacity)
}

// flusherAsRunner 将 *Flusher 收窄为 contract.Runner 接口，用于 `group:"runners"` 收集器。
func flusherAsRunner(f *Flusher) contract.Runner { return f }
