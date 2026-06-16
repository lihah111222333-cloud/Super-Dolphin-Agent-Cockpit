package prompt

import (
	"github.com/kelindar/event"
	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/kernel/builtinprompts"
	pkglogger "github.com/anthropic-ai/super-agent-v3/internal/platform/logging"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

// Module 把 prompt 的注册表、组装器和 RPC 接起来。
// prompt 只负责组 start/turn 内容；memory 写入、skill mirror 和 provider 启动在别处做。
var Module = fx.Module("prompt",
	fx.Provide(
		NewConfig,
		NewServiceFx,
		builtinprompts.NewDefaultRegistry,
		AsPromptRegistry,
		AsPromptAssemblyService,
		AsDynamicSectionRegistrar,
		AsSectionInvalidator,
		registerPromptHandlers,
		newMatchWhenEvaluator,
		newEnableWhenEvaluator,
	),
)

func newMatchWhenEvaluator() contract.MatchWhenEvaluator {
	return EvaluateMatchWhen
}

func newEnableWhenEvaluator() contract.EnableWhenEvaluator {
	return EvaluateEnableWhen
}

type promptHandlersParams struct {
	fx.In

	Store      contract.PromptStore
	Builtin    contract.BuiltinPromptRegistry `optional:"true"`
	Dream      contract.DreamExecutor         `optional:"true"`
	Sections   contract.SectionInvalidator    `optional:"true"`
	Dispatcher *event.Dispatcher              `optional:"true"`
}

func registerPromptHandlers(params promptHandlersParams) platformrpc.HandlerMapResult {
	return buildPromptHandlersWithService(
		newPromptServiceWithBuiltin(params.Store, params.Builtin, params.Sections),
		params.Store,
		params.Builtin,
		params.Sections,
		params.Dream,
		params.Dispatcher,
	)
}

// ServiceFxParams resolves optional dependencies needed to surface the
// user-configurable LSP prompt hint in the start system prompt.
type ServiceFxParams struct {
	fx.In
	Cfg             *Config
	Logger          *pkglogger.Logger          `optional:"true"`
	Prefs           contract.UIPreferenceStore `optional:"true"`
	SharedFiles     contract.SharedFileReader  `optional:"true"`
	DisabledToolsFn DisabledBuiltinToolsFn     `optional:"true"`
}

// NewServiceFx is the fx-facing constructor that wires the preference store,
// shared-file reader, and disabled-tools function into the prompt Service.
// NewServiceFx 为 fx 创建 prompt service。
func NewServiceFx(p ServiceFxParams) Service {
	opts := []ServiceOption{
		WithPromptHintSources(p.Prefs, p.SharedFiles),
	}
	if p.DisabledToolsFn != nil {
		opts = append(opts, WithDisabledBuiltinToolsFn(p.DisabledToolsFn))
	}
	return NewService(p.Cfg, p.Logger, opts...)
}
