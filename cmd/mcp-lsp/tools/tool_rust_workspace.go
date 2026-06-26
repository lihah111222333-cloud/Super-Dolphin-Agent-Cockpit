package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/format"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

// rustDetachedWorkspaceMessage 为脱离 Cargo workspace 的 Rust 文件补充 LSP 能力提示。
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

// rustDetachedWorkspaceMessageForURIs 扫描一组 URI，命中脱离 workspace 的 Rust 文件就返回增强提示。
func rustDetachedWorkspaceMessageForURIs(uris []string, capability, base string) string {
	for _, uri := range uris {
		message := rustDetachedWorkspaceMessage(format.URIToPath(uri), capability, base)
		if message != base {
			return message
		}
	}
	return base
}

// rustDetachedWorkspaceError 把 detached Rust 造成的 LSP 失败包装成可重试 coded error。
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

// isDetachedRustFile 判断 .rs 文件是否缺少上级 Cargo.toml。
func isDetachedRustFile(filePath string) bool {
	if strings.ToLower(filepath.Ext(strings.TrimSpace(filePath))) != ".rs" {
		return false
	}
	return !hasCargoManifestAncestor(filePath)
}

// hasCargoManifestAncestor 从文件所在目录向上查找 Cargo.toml。
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
