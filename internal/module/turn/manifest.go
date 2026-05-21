package turn

import (
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common/bootstrap"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/discovery"
)

type manifestBuilder struct {
	binaryDir string
	buildFn   contract.ManifestBuildFunc
}

func newManifestBuilder(binaryDir string, buildFn contract.ManifestBuildFunc) *manifestBuilder {
	return &manifestBuilder{
		binaryDir: strings.TrimSpace(binaryDir),
		buildFn:   buildFn,
	}
}

func (b *manifestBuilder) Build(input PrepareInput, threadID string) dto.MCPManifest {
	peerAddrs, peerTokens := discoverPeers()
	return b.buildFn(dto.ManifestContext{
		AgentID:                      input.AgentID,
		ThreadID:                     threadID,
		CWD:                          input.CWD,
		AdditionalWorkingDirectories: append([]string(nil), input.AdditionalWorkingDirectories...),
		ThreadCaps:                   input.ThreadCaps,
		BinaryDir:                    b.binaryDirFor(input.BinaryDir),
		PeerHTTPAddrs:                peerAddrs,
		PeerHTTPTokens:               peerTokens,
		TransportMode:                dto.ManifestTransportStdioOnly,
	})
}

func (b *manifestBuilder) binaryDirFor(binaryDir string) string {
	if binaryDir = strings.TrimSpace(binaryDir); binaryDir != "" {
		return binaryDir
	}
	return strings.TrimSpace(b.binaryDir)
}

// discoverPeers probes for running peer HTTP endpoints. Returns nil maps if none.
func discoverPeers() (map[dto.ToolFamily]string, map[dto.ToolFamily]string) {
	token := bootstrap.SessionTokenFromEnv()
	addrs := make(map[dto.ToolFamily]string)
	tokens := make(map[dto.ToolFamily]string)
	for _, fam := range []dto.ToolFamily{dto.FamilyLSP, dto.FamilyOrch} {
		addr, err := discovery.DiscoverPeerHTTPAddrWithToken("mcp-"+string(fam), token)
		if err != nil || addr == "" {
			continue
		}
		addrs[fam] = addr
		if token != "" {
			tokens[fam] = token
		}
	}
	if len(addrs) == 0 {
		return nil, nil
	}
	if len(tokens) == 0 {
		return addrs, nil
	}
	return addrs, tokens
}
