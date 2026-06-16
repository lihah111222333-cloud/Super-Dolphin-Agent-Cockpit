package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/format"
	common "github.com/anthropic-ai/super-agent-v3/internal/mcpserver/runtime"
)

func rustDetachedWorkspaceMessage(filePath, capability, base string) string {
	if !isDetachedRustFile(filePath) {
		return base
	}
	message := fmt.Sprintf("Rust file is outside a Cargo workspace; rust-analyzer may not provide %s for detached files.", capability)
	if strings.TrimSpace(base) == "" {
		return message
	}
	return strings.TrimSpace(base) + "; " + message
}

func rustDetachedWorkspaceMessageForURIs(uris []string, capability, base string) string {
	for _, uri := range uris {
		message := rustDetachedWorkspaceMessage(format.URIToPath(uri), capability, base)
		if message != base {
			return message
		}
	}
	return base
}

func rustDetachedWorkspaceError(uris []string, capability string, err error) error {
	for _, uri := range uris {
		path := format.URIToPath(uri)
		if !isDetachedRustFile(path) {
			continue
		}
		hint := rustDetachedWorkspaceMessage(path, capability, "Open the file inside a Cargo workspace or add a Cargo.toml before retrying.")
		return common.NewCodedToolError("lsp_unavailable", err, true, hint)
	}
	return err
}

func isDetachedRustFile(filePath string) bool {
	if strings.ToLower(filepath.Ext(strings.TrimSpace(filePath))) != ".rs" {
		return false
	}
	return !hasCargoManifestAncestor(filePath)
}

func hasCargoManifestAncestor(filePath string) bool {
	dir := filepath.Dir(filepath.Clean(filePath))
	for {
		if _, err := os.Stat(filepath.Join(dir, "Cargo.toml")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}
