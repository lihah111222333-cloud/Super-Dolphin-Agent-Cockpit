package turn

import (
	"sort"
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

// Build 构建turn。
func (b *manifestBuilder) Build(input PrepareInput, threadID string) dto.MCPManifest {
	peerAddrs, peerTokens := discoverPeers()
	return b.buildFn(dto.ManifestContext{
		AgentID:                      input.AgentID,
		ThreadID:                     threadID,
		CWD:                          input.CWD,
		AdditionalWorkingDirectories: append([]string(nil), input.AdditionalWorkingDirectories...),
		ThreadCaps:                   input.ThreadCaps,
		BinaryDir:                    b.binaryDirFor(input.BinaryDir),
		ExtraBinaries:                mcpServerConfigBinaries(input.MCPSnapshot.ServerConfigs),
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

func mcpServerConfigBinaries(configs map[string]contract.MCPServerConfig) []dto.MCPBinary {
	if len(configs) == 0 {
		return nil
	}
	names := turnMCPServerConfigNames(configs)
	sort.Strings(names)
	binaries := make([]dto.MCPBinary, 0, len(names))
	for _, name := range names {
		config := configs[name]
		binaries = append(binaries, dto.MCPBinary{
			Name:    name,
			Type:    "http",
			URL:     strings.TrimSpace(config.URL),
			Headers: cloneMCPServerHeaders(config.Headers),
		})
	}
	return binaries
}

func cloneMCPServerHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for key, value := range headers {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// discoverPeers probes for running peer HTTP endpoints. Returns nil maps if none.
// discoverPeers 处理discoverpeers。
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
