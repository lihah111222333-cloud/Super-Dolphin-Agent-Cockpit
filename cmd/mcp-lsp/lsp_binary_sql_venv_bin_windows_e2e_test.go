//go:build windows && e2e

package main

import "path/filepath"

// sqruffVenvBinDirForE2E 返回 Windows Python venv 的 Scripts 目录。
func sqruffVenvBinDirForE2E(venv string) string {
	return filepath.Join(venv, "Scripts")
}
