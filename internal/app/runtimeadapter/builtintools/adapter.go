// Package builtintoolsadapter 将运行时工具注册表和偏好适配为内置工具端口。
package builtintoolsadapter

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/prompt"
	"github.com/anthropic-ai/super-agent-v3/internal/module/uistate"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
	"github.com/anthropic-ai/super-agent-v3/internal/store/uipreference"
	"go.uber.org/fx"
)

// Module 提供原生工具描述和基于 UI 偏好的软过滤解析器。
var Module = fx.Module("builtintoolsadapter",
	fx.Provide(
		provideNativeToolDescriptors,
		provideDisabledBuiltinToolsFn,
	),
)

// provideNativeToolDescriptors 从 unified registry 暴露原生工具描述。
func provideNativeToolDescriptors(registry *unified.Registry) []contract.NativeToolDescriptor {
	if registry == nil {
		return nil
	}
	return registry.NativeTools()
}

// provideDisabledBuiltinToolsFn 将 UI 偏好的内置工具软过滤接入 prompt。
// 这里做成函数桥接，避免 prompt 包直接依赖 uistate 包形成反向导入。
func provideDisabledBuiltinToolsFn(prefs uipreference.Store, tools []contract.NativeToolDescriptor) prompt.DisabledBuiltinToolsFn {
	index := make(map[string]contract.NativeToolDescriptor, len(tools))
	for _, tool := range tools {
		index[tool.ID] = tool
	}
	return func(ctx context.Context, cwd, provider string) ([]string, error) {
		return uistate.ResolveExplicitSoftFilteredBuiltinTools(ctx, prefs, cwd, tools, index, provider)
	}
}
