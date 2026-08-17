package main

// 本文件是公共跨平台的 E2E 清单策略，不执行 E2E，也不按宿主平台选源；
// 普通维护门禁必须在所有平台读取同一份延后清单，因此故意不加 e2e build tag。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/capcontract"
)

// excludeDeferredE2EGoPackages 从快速门禁范围中移除显式延后到 make test/CI 的 provider E2E 包。
func excludeDeferredE2EGoPackages(packages []string, manifestPath string) ([]string, error) {
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		repoRoot, rootErr := capcontract.FindRepoRoot(".")
		if rootErr != nil {
			return nil, fmt.Errorf("read deferred E2E package manifest %s: %w; resolve repository root: %v", manifestPath, err, rootErr)
		}
		resolvedPath := filepath.Join(repoRoot, filepath.FromSlash(manifestPath))
		manifest, err = os.ReadFile(resolvedPath)
		if err != nil {
			return nil, fmt.Errorf("read deferred E2E package manifest %s: %w", resolvedPath, err)
		}
	}
	deferred := map[string]bool{}
	for packageName := range strings.FieldsSeq(string(manifest)) {
		deferred[packageName] = true
	}
	filtered := make([]string, 0, len(packages))
	for _, packageName := range packages {
		if !deferred[packageName] {
			filtered = append(filtered, packageName)
		}
	}
	return filtered, nil
}
