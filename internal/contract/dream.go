package contract

import (
	"context"
	"errors"
)

var ErrDreamExecutorNotConfigured = errors.New("dream executor is not configured")

type DreamExecutor interface {
	ExecuteDream(ctx context.Context, prompt string) (string, error)
}

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

type DreamExecutorWithOptions interface {
	ExecuteDreamWithOptions(ctx context.Context, prompt string, options DreamOptions) (string, error)
}

type DreamExecutorProvider struct {
	Name     string
	Executor DreamExecutor
}
