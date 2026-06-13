package manifestbuilder

import (
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

// BuildManifest returns declarative MCP binary metadata for external executors.
// BuildManifest 构建manifest。
func BuildManifest(ctx dto.ManifestContext) dto.MCPManifest {
	return contract.BuildManifest(ctx)
}
