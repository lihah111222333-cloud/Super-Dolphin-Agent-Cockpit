package prompt

import (
	"log/slog"

	"go.uber.org/fx"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared/builtinprompts"
)

// Module 把 prompt 的注册表和组装器接入应用图。
// prompt 只负责组 start/turn 内容；memory 写入、provider-native skill discovery 和 provider 启动在别处做。
var Module = fx.Module("prompt",
	fx.Provide(
		NewConfig,
		NewServiceFx,
		builtinprompts.NewDefaultRegistry,
		AsPromptRegistry,
		AsPromptAssemblyService,
		AsDynamicSectionRegistrar,
		AsSectionInvalidator,
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

// ServiceFxParams 收集 prompt Service 的 fx 依赖，包含可选偏好存储和共享文件读取器。
type ServiceFxParams struct {
	fx.In
	Cfg             *Config
	Logger          *slog.Logger           `optional:"true"`
	Prefs           PreferenceReader       `optional:"true"`
	SharedFiles     SharedFileReader       `optional:"true"`
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
