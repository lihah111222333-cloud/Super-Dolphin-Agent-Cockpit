package multilsp

import (
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
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
