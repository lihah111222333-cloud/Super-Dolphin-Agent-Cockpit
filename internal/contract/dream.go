package contract

import (
	"context"
	"errors"
)

var ErrDreamExecutorNotConfigured = errors.New("dream executor is not configured")

// DreamExecutor 是 dream 任务调用 provider 的最小执行契约。
// 实现方必须在内部应用最小权限策略，调用方只关心 prompt 到文本结果。
type DreamExecutor interface {
	ExecuteDream(ctx context.Context, prompt string) (string, error)
}

// DreamOptions 描述一次 dream 调用可选择的 provider、模型和运行时策略。
// 空字段表示沿用调用方或聚合器的显式配置，不能被解释为静默降级。
type DreamOptions struct {
	Provider      string             `json:"provider,omitempty"`
	Model         string             `json:"model,omitempty"`
	ModelProvider string             `json:"model_provider,omitempty"`
	RuntimePolicy DreamRuntimePolicy `json:"runtime_policy,omitempty"`
}

// DreamRuntimePolicy 描述 dream provider 子进程的安全边界。
// dream 只用于整理/决策，不能打开工具、写沙箱或继承完整父进程环境。
type DreamRuntimePolicy struct {
	ToolsDisabled   bool `json:"tools_disabled,omitempty"`
	ReadOnlySandbox bool `json:"read_only_sandbox,omitempty"`
	MinEnv          bool `json:"min_env,omitempty"`
}

// StrictDreamRuntimePolicy 返回所有 dream 调用都必须满足的最小权限策略。
func StrictDreamRuntimePolicy() DreamRuntimePolicy {
	return DreamRuntimePolicy{
		ToolsDisabled:   true,
		ReadOnlySandbox: true,
		MinEnv:          true,
	}
}

// WithStrictDefaults 把缺失或放宽的 dream policy 收紧为最小权限策略。
func (p DreamRuntimePolicy) WithStrictDefaults() DreamRuntimePolicy {
	strict := StrictDreamRuntimePolicy()
	p.ToolsDisabled = p.ToolsDisabled || strict.ToolsDisabled
	p.ReadOnlySandbox = p.ReadOnlySandbox || strict.ReadOnlySandbox
	p.MinEnv = p.MinEnv || strict.MinEnv
	return p
}

// DreamExecutorWithOptions 扩展 dream 执行契约，允许调用方传入模型和安全策略。
// 支持该接口的 provider 需要自行合并 StrictDreamRuntimePolicy。
type DreamExecutorWithOptions interface {
	ExecuteDreamWithOptions(ctx context.Context, prompt string, options DreamOptions) (string, error)
}

// DreamExecutorProvider 把 provider 名称和对应 executor 绑定在一起。
// 聚合器用它按名称路由 dream 调用，而不是把 provider 具体类型暴露给上层。
type DreamExecutorProvider struct {
	Name     string
	Executor DreamExecutor
}
