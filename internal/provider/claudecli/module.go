package claudecli

import (
	"log/slog"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/fbsd"
	"github.com/anthropic-ai/super-agent-v3/internal/module/nativefilter"
	"github.com/anthropic-ai/super-agent-v3/internal/module/skilllibrary"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/pidregistry"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
)

// driverFactoryParams collects the fx dependencies for NewDriverFactory.
// skilllibrary.Config is marked optional so test fixtures that do not provide
// it still compile and wire correctly.
type driverFactoryParams struct {
	fx.In

	Logger         *slog.Logger
	Dispatcher     *unified.EventDispatcher
	Reporter       contract.RuntimeReporter
	Reg            *pidregistry.Registry
	ProxyAddrFn    func() string
	SkillLibConfig skilllibrary.Config  `optional:"true"`
	NativeFilter   *nativefilter.Filter `optional:"true"`
	Tracker        *fbsd.Tracker        `optional:"true"`
}

func NewDriverFactory(p driverFactoryParams) contract.DriverFactory {
	// P6: install FBSD recorder hook so Claude tool_use parser can打点
	// when the model issues Read(.claude/skills/<n>/references/...) calls.
	// nil tracker / disabled tracker keeps the hook nil-safe.
	if p.Tracker != nil {
		SetFBSDRecorder(p.Tracker.Record)
	}
	return contract.DriverFactory{
		Name: "claude",
		Create: func() contract.Driver {
			return newDriver(p.Logger, p.Dispatcher, p.Reporter, p.Reg, p.ProxyAddrFn, p.SkillLibConfig.CacheDir, p.NativeFilter)
		},
	}
}

var Module = fx.Module("provider.claudecli",
	fx.Provide(
		fx.Annotate(NewDriverFactory, fx.ResultTags(`group:"drivers"`)),
		fx.Annotate(provideDreamExecutorProvider, fx.ResultTags(`group:"dream_executors"`)),
	),
	fx.Invoke(RegisterTranslators),
)
