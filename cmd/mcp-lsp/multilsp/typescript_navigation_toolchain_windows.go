//go:build windows

package multilsp

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// platformTypeScriptNavigationModuleSearchPaths 返回 Windows PATH 中
// typescript-language-server 所属 node_modules 的父目录，供 Node 按 paths 语义解析配套 TypeScript。
func platformTypeScriptNavigationModuleSearchPaths() ([]string, error) {
	executable, err := exec.LookPath("typescript-language-server")
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("locate typescript-language-server: %w", err)
	}
	absolute, err := filepath.Abs(executable)
	if err != nil {
		return nil, fmt.Errorf("resolve typescript-language-server path: %w", err)
	}
	binDir := filepath.Dir(absolute)
	if !strings.EqualFold(filepath.Base(binDir), ".bin") {
		return nil, nil
	}
	return []string{filepath.Dir(filepath.Dir(binDir))}, nil
}
