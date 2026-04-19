package claudecli

import (
	"log/slog"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	promptpkg "github.com/anthropic-ai/super-agent-v3/internal/module/prompt"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/pidregistry"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
)

func NewDriverFactory(logger *slog.Logger, dispatcher *unified.EventDispatcher, reporter contract.RuntimeReporter, reg *pidregistry.Registry, proxyAddrFn func() string) contract.DriverFactory {
	return contract.DriverFactory{
		Name: "claude",
		Create: func() contract.Driver {
			return newDriver(logger, dispatcher, reporter, reg, proxyAddrFn)
		},
	}
}

var Module = fx.Module("provider.claudecli",
	fx.Provide(
		fx.Annotate(NewDriverFactory, fx.ResultTags(`group:"drivers"`)),
		fx.Annotate(provideDreamExecutorProvider, fx.ResultTags(`group:"dream_executors"`)),
		// P20.1 Phase 10: 注册 Claude CLI 原生 skill detector 到 prompt 模块的聚合 group。
		fx.Annotate(NewSkillInjectionPort, fx.ResultTags(promptpkg.SkillInjectionPortGroupTag)),
	),
	fx.Invoke(RegisterTranslators),
)
