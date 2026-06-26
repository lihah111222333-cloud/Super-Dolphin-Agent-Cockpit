package prompt

import (
	"log/slog"

	"github.com/kelindar/event"
	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared/builtinprompts"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
	"github.com/anthropic-ai/super-agent-v3/internal/store/uipreference"
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

// newMatchWhenEvaluator 暴露模板 auto-route 条件评估器给跨模块 contract。
func newMatchWhenEvaluator() contract.MatchWhenEvaluator {
	return EvaluateMatchWhen
}

// newEnableWhenEvaluator 暴露 section enable_when 条件评估器给跨模块 contract。
func newEnableWhenEvaluator() contract.EnableWhenEvaluator {
	return EvaluateEnableWhen
}

// promptHandlersParams 汇总 prompt RPC handler 装配需要的 store 和可选协作者。
type promptHandlersParams struct {
	fx.In

	Store      promptstore.Store
	Builtin    contract.BuiltinPromptRegistry `optional:"true"`
	Dream      contract.DreamExecutor         `optional:"true"`
	Sections   contract.SectionInvalidator    `optional:"true"`
	Dispatcher *event.Dispatcher              `optional:"true"`
}

// registerPromptHandlers 用 fx 参数创建 prompt RPC handler map。
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

// ServiceFxParams 收集 prompt Service 的 fx 依赖，包含可选偏好存储和共享文件读取器。
type ServiceFxParams struct {
	fx.In
	Cfg             *Config
	Logger          *slog.Logger           `optional:"true"`
	Prefs           uipreference.Store     `optional:"true"`
	SharedFiles     sharedfilestore.Reader `optional:"true"`
	DisabledToolsFn DisabledBuiltinToolsFn `optional:"true"`
}

// NewServiceFx 是 fx 使用的 prompt Service 构造函数，负责注入可选配置来源。
func NewServiceFx(p ServiceFxParams) Service {
	opts := []ServiceOption{
		WithPromptHintSources(p.Prefs, p.SharedFiles),
	}
	if p.DisabledToolsFn != nil {
		opts = append(opts, WithDisabledBuiltinToolsFn(p.DisabledToolsFn))
	}
	return NewService(p.Cfg, p.Logger, opts...)
}
