package claudecli

import (
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// discoverPeerAddrs probes for running peer MCP processes that expose
// HTTP endpoints. When found, BuildManifest() generates {"type":"http","url":"..."}
// instead of {"command":"..."}, so Claude connects to the shared peer instead
// of spawning a per-agent sidecar.
func discoverPeerAddrs() map[dto.ToolFamily]string {
	addrs := make(map[dto.ToolFamily]string)
	for _, pair := range []struct {
		family dto.ToolFamily
		binary string
	}{
		{dto.FamilyOrch, "mcp-orch"},
		{dto.FamilyLSP, "mcp-lsp"},
	} {
		addr, err := common.DiscoverPeerHTTPAddr(pair.binary)
		if err != nil {
			continue
		}
		if !common.IsValidHTTPAddr(addr) {
			pkglogger.Warn("claudecli: invalid peer discovery addr",
				"binary", pair.binary, "addr", addr)
			continue
		}
		addrs[pair.family] = addr
		pkglogger.Info("claudecli: using shared peer HTTP endpoint",
			"binary", pair.binary, "addr", addr)
	}
	return addrs
}
