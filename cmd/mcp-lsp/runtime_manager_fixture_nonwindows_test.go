//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// configureRuntimeManagerLanguageServerFixtures 为非 Windows manager 测试发布 PATH fixture；
// Windows 使用锁定产品 cache 的独立实现，避免改变其他平台既有解析策略。
func configureRuntimeManagerLanguageServerFixtures(t *testing.T, _ string, languageIDs []string) {
	t.Helper()
	binDir := t.TempDir()
	written := make(map[string]struct{})
	for _, spec := range runtimeNPMInstallerSpecsForPlatform(runtime.GOOS) {
		if !runtimeTestSpecMatchesAnyLanguage(spec, languageIDs) {
			continue
		}
		if _, ok := written[spec.binaryName]; ok {
			continue
		}
		writeMcpLSPExecutable(t, binDir, spec.binaryName)
		written[spec.binaryName] = struct{}{}
	}
	if len(written) == 0 {
		t.Fatalf("no non-Windows runtime installer fixture matched languages %v", languageIDs)
	}
	t.Setenv("PATH", binDir+string(filepath.ListSeparator)+os.Getenv("PATH"))
}

func runtimeTestSpecMatchesAnyLanguage(spec runtimeInstallerSpec, languageIDs []string) bool {
	for _, registered := range spec.languages {
		for _, requested := range languageIDs {
			if registered == requested {
				return true
			}
		}
	}
	return false
}
