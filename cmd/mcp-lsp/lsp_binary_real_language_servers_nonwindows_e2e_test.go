//go:build !windows && e2e

package main

import (
	"os/exec"
	"testing"
)

// requireRealLanguageServersForE2E 在非 Windows 上保持宿主 PATH 语言服务器契约。
// Windows 的产品缓存/installer 路径由同名 build-tag 实现独立处理。
func requireRealLanguageServersForE2E(t *testing.T, cases []realLSPDiagnosticsCase) {
	t.Helper()
	seen := map[string]struct{}{}
	for _, tc := range cases {
		for _, binary := range tc.binaries {
			if _, ok := seen[binary]; ok {
				continue
			}
			seen[binary] = struct{}{}
			if _, err := exec.LookPath(binary); err != nil {
				t.Fatalf("real system e2e requires %s in PATH: %v", binary, err)
			}
		}
	}
}
