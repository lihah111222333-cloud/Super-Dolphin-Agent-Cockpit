//go:build e2e && !windows

package main

import "path/filepath"

// diagnosticsPayloadPathEqual 在非 Windows E2E 中按宿主大小写敏感路径语义比较诊断目标。
func diagnosticsPayloadPathEqual(left, right string) bool {
	return filepath.Clean(left) == filepath.Clean(right)
}
