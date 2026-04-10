package codexapp

import (
	"os"

	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// cleanPeerDiscoveryFiles removes HTTP discovery files for peer MCP processes.
// Called during ServerManager shutdown as a safety net — the peer processes
// themselves clean up in their HTTP runner, but if they crash, this ensures
// stale discovery files don't cause the next run to connect to dead endpoints.
func cleanPeerDiscoveryFiles() {
	myPID := os.Getpid()
	for _, binary := range []string{"mcp-orch", "mcp-lsp"} {
		if err := common.CleanupDiscoveryFile(binary, myPID); err != nil {
			if !os.IsNotExist(err) {
				pkglogger.Warn("peer discovery cleanup failed",
					"binary", binary, "pid", myPID, "error", err)
			}
		}
	}
}
