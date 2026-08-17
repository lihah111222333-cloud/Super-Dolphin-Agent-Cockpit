//go:build !windows && e2e

package main

import "path/filepath"

// sqruffVenvBinDirForE2E 返回非 Windows Python venv 的 bin 目录。
func sqruffVenvBinDirForE2E(venv string) string {
	return filepath.Join(venv, "bin")
}
