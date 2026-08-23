//go:build e2e && windows

package main

import (
	"path/filepath"
	"strings"
)

// diagnosticsPayloadPathEqual 在 Windows E2E 中忽略盘符与路径大小写差异。
func diagnosticsPayloadPathEqual(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}
