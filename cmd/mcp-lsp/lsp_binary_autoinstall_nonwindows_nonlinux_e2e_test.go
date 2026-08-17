//go:build !windows && !linux && e2e

package main

// Darwin/FreeBSD 等非 Windows、非 Linux 平台使用完整 POSIX fake installer case 表；
// 平台选择由本文件的显式 build constraint 完成，不依赖 runtime.GOOS 或运行时 Skip。

import "testing"

func binaryAutoInstallLanguageCases(t *testing.T) []binaryAutoInstallLanguageCase {
	t.Helper()
	return allBinaryAutoInstallLanguageCases(t)
}
