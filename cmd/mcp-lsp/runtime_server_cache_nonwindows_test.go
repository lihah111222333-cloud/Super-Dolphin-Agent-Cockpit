//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// runtimeServerLockedNodeFixtureEnv 在非 Windows 保持空环境；
// 公共测试仍调用同名 helper，但 POSIX Node 证据由 adapter PATH 测试覆盖。
func runtimeServerLockedNodeFixtureEnv(t *testing.T) []string {
	t.Helper()
	return nil
}

// runtimeServerTestNodeVersionResolver 在非 Windows 保持生产的 PATH resolver，
// 使公共缓存测试不携带 Windows 专用资产假设。
func runtimeServerTestNodeVersionResolver(_ []string) runtimeServerNodeVersionResolver {
	return runtimeServerNodeVersion
}

// runtimeServerPortableNodeResolver 是公共 portable-cache 断言的非 Windows 实现；
// 不注入 Windows 环境变量，也不改变生产 resolver 的行为。
func runtimeServerPortableNodeResolver(_ map[string]string) runtimeServerNodeVersionResolver {
	return runtimeServerNodeVersion
}

func TestRuntimeServerNodeVersionUsesAdapterPATH(t *testing.T) {
	dir := t.TempDir()
	nodePath := filepath.Join(dir, "node")
	if err := os.WriteFile(nodePath, []byte("#!/bin/sh\nprintf 'v24.11.9\\n'\n"), 0o755); err != nil {
		t.Fatalf("write fake Node runtime: %v", err)
	}
	version, portable, err := runtimeServerNodeVersion([]string{"PATH=" + dir})
	if err != nil {
		t.Fatalf("runtimeServerNodeVersion(adapter PATH) error = %v", err)
	}
	if version != "v24.11.9" || portable {
		t.Fatalf("adapter Node result = (%q, %v), want (v24.11.9, false)", version, portable)
	}
}
