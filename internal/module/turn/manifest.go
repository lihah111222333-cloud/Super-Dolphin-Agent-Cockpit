package turn

import (
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/manifestbuilder"
)

type manifestBuilder struct {
	binaryDir string
}

func newManifestBuilder(binaryDir string) *manifestBuilder {
	return &manifestBuilder{binaryDir: strings.TrimSpace(binaryDir)}
}

func (b *manifestBuilder) Build(input PrepareInput) dto.MCPManifest {
	return manifestbuilder.BuildManifest(dto.ManifestContext{
		AgentID:       input.AgentID,
		CWD:           input.CWD,
		ThreadCaps:    input.ThreadCaps,
		BinaryDir:     b.binaryDirFor(input.BinaryDir),
		PeerHTTPAddrs: discoverPeers(),
	})
}

func (b *manifestBuilder) binaryDirFor(binaryDir string) string {
	if binaryDir = strings.TrimSpace(binaryDir); binaryDir != "" {
		return binaryDir
	}
	return strings.TrimSpace(b.binaryDir)
}

// discoverPeers probes for running peer HTTP endpoints. Returns nil if none.
func discoverPeers() map[dto.ToolFamily]string {
	addrs := make(map[dto.ToolFamily]string)
	for _, fam := range []dto.ToolFamily{dto.FamilyLSP, dto.FamilyOrch} {
		addr, err := common.DiscoverPeerHTTPAddr("mcp-" + string(fam))
		if err != nil || addr == "" {
			continue
		}
		addrs[fam] = addr
	}
	if len(addrs) == 0 {
		return nil
	}
	return addrs
}
