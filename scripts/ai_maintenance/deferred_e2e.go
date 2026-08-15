package main

import (
	"fmt"
	"os"
	"strings"
)

// excludeDeferredE2EGoPackages 从快速门禁范围中移除显式延后到 make test/CI 的 provider E2E 包。
func excludeDeferredE2EGoPackages(packages []string, manifestPath string) ([]string, error) {
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read deferred E2E package manifest %s: %w", manifestPath, err)
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
