package manifestbuilder

import (
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

// BuildManifest 为外部 executor 构建 MCP 二进制 manifest。
// 当前实现委托 contract 层，provider 包保留该入口用于隔离调用方依赖。
func BuildManifest(ctx dto.ManifestContext) dto.MCPManifest {
	return contract.BuildManifest(ctx)
}
