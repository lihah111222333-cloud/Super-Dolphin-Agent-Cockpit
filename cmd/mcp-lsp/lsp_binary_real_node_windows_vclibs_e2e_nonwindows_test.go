//go:build e2e && !windows

package main

import "testing"

// realNodeProvisionWindowsVCLibsDesktopAppLocal 在非 Windows E2E 中保持无副作用，现有平台继续使用各自原生运行时策略。
func realNodeProvisionWindowsVCLibsDesktopAppLocal(t *testing.T) {
	t.Helper()
}
