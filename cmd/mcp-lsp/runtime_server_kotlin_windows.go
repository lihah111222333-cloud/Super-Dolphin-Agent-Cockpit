//go:build windows

package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
)

// runtimeServerWindowsKotlinProcessBinary 只为产品根内、已锁定的 Kotlin server
// 建立物理短布局；外部同名 binary 原样返回，其他语言和非 Windows 不进入此分支。
func runtimeServerWindowsKotlinProcessBinary(serverBinary string) (string, bool, error) {
	if !strings.EqualFold(filepath.Base(filepath.Clean(serverBinary)), "intellij-server.exe") {
		return serverBinary, false, nil
	}
	productRoot, owned, err := runtimeServerWindowsOwnedProductRoot(serverBinary)
	if err != nil {
		return "", true, err
	}
	if !owned {
		return serverBinary, false, nil
	}
	shortBinary, err := installer.MaterializeWindowsKotlinProcessRoot(productRoot, serverBinary)
	if err != nil {
		return "", true, fmt.Errorf("materialize product-owned Kotlin short process root: %w", err)
	}
	return shortBinary, true, nil
}
