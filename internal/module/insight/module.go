package insight

import (
	"log/slog"

	"go.uber.org/fx"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// Module 将 insight subscriber、flusher、service 和 RPC handler 注入 Fx 树。
// subscriber 注入 BusModule 的 bus.subscribers 组；flusher 注入 runners 组由 platformrunner.RunGroup 驱动。
// 该模块不导入 turn 写入路径，保证 observation 到 insight 的单向依赖不被反向打破。
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
