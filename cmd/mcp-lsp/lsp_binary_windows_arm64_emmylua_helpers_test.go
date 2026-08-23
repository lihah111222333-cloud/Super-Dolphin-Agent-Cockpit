//go:build windows && arm64

package main

import (
	"errors"
	"os"
	"testing"
)

// emmyLuaPathExists 返回受控 EmmyLua 缓存路径是否存在，并屏蔽绝对路径错误。
func emmyLuaPathExists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	if err != nil {
		t.Fatalf("inspect EmmyLua cache entry: %s", redactEmmyLuaWindowsARM64Text(err.Error(), path))
	}
	return true
}
