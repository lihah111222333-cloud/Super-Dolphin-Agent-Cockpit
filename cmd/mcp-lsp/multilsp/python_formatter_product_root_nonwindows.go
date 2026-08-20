//go:build !windows

package multilsp

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// resolvePythonFormatterProductRootPlatform 保留非 Windows 的显式产品根契约；
// workspace 自动派生只属于 Windows sidecar 的缺失环境修复。
func resolvePythonFormatterProductRootPlatform(_ *manager, _ context.Context) (string, error) {
	productRoot := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_HOME"))
	if productRoot == "" {
		return "", fmt.Errorf("Ruff formatter requires SUPER_DOLPHIN_HOME")
	}
	return productRoot, nil
}
