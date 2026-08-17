//go:build linux && e2e

package main

import (
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// Linux 的 proto/lua/terraform/sql/csharp、Dart/Rust 与 clangd 由固定 managed-artifact
// manifest 供应；它们不应进入 POSIX fake installer E2E，避免用 fake 成功冒充生产路由。

func binaryAutoInstallLanguageCases(t *testing.T) []binaryAutoInstallLanguageCase {
	t.Helper()
	all := allBinaryAutoInstallLanguageCases(t)
	filtered := make([]binaryAutoInstallLanguageCase, 0, len(all))
	for _, tc := range all {
		if isLinuxManagedArtifactLanguage(tc.languageID) {
			continue
		}
		filtered = append(filtered, tc)
	}
	return filtered
}

func isLinuxManagedArtifactLanguage(languageID string) bool {
	switch languageID {
	case "proto", "lua", "terraform", "sql", "csharp", contract.LSPServiceDart, contract.LSPServiceRust:
		return true
	}
	for _, clangdLanguageID := range contract.ClangdLanguageIDs() {
		if languageID == clangdLanguageID {
			return true
		}
	}
	return false
}
