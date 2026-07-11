package multilsp

import (
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

func effectiveLSPLogMessageType(params protocol.LogMessageParams) protocol.LogMessageType {
	if params.Type == protocol.LogMessageError && lspErrorMessageIsWarning(params.Message) {
		return protocol.LogMessageWarning
	}
	return params.Type
}

func lspErrorMessageIsWarning(message string) bool {
	normalized := strings.ToLower(strings.TrimSpace(message))
	return strings.HasPrefix(normalized, "warning:") ||
		strings.Contains(normalized, "warning: while diagnosing orphaned files: session is shut down")
}
