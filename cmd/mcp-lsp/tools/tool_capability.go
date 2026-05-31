package tools

import (
	"errors"
	"fmt"

	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
)

func isUnsupportedCapability(err error) bool {
	return errors.Is(err, lspmanager.ErrUnsupportedCapability)
}

func unsupportedCapabilityEmptyResult(capability string) emptyListEnvelope {
	return emptyListEnvelope{
		Success: true,
		Data:    []any{},
		Meta: resultMeta{
			Count:   0,
			Message: fmt.Sprintf("%s unsupported by current language server", capability),
		},
	}
}
