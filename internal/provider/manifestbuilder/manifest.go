package manifestbuilder

import (
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

// BuildManifest 为外部 executor 构建 MCP 二进制 manifest。
// 当前实现委托 contract 层，provider 包保留该入口用于隔离调用方依赖。
func BuildManifest(ctx dto.ManifestContext) dto.MCPManifest {
	return contract.BuildManifest(ctx)
}
