// Package nested 管理 CLAUDE.md 及相关规则文件的发现、加载与注入。
// 负责从 managed/user/project/addDir 四类来源解析候选项，过滤后提供给 prompt 构建流程。
package nested

import (
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"go.uber.org/fx"
)

// Module 是 memory.nested 的 fx 装配模块，注册 NestedRuntime 和 ClaudeMdSourcesProvider。
var Module = fx.Module("memory.nested",
	fx.Provide(
		NewNestedRuntime,
		fx.Annotate(
			NewClaudeMdSourcesProvider,
			fx.As(new(contract.ClaudeMdSourceProvider)),
		),
	),
)
