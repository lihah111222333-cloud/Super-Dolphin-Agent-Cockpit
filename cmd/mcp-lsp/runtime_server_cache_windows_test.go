//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
)

// runtimeServerLockedNodeFixtureEnv 为 Windows 测试创建受控的绝对 Node fixture；
// 公共测试通过同名 helper 取得环境，避免把 PATH 或当前目录误当作产品资产来源。
func runtimeServerLockedNodeFixtureEnv(t *testing.T) []string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "node-v22.22.0-win-arm64", "node.exe")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create locked Node fixture directory: %v", err)
	}
	if err := os.WriteFile(path, []byte("locked Node fixture"), 0o600); err != nil {
		t.Fatalf("write locked Node fixture: %v", err)
	}
	return []string{runtimeServerWindowsNodeExecutableEnv + "=" + path}
}

// runtimeServerTestNodeVersionResolver 在 Windows 只接受上游传入的绝对 fixture，
// 再返回固定 catalog 版本；测试不得通过 PATH 或联网获得 Node 版本证据。
func runtimeServerTestNodeVersionResolver(nodeEnv []string) runtimeServerNodeVersionResolver {
	return func(_ []string) (string, bool, error) {
		path := runtimeServerEnvValue(nodeEnv, runtimeServerWindowsNodeExecutableEnv)
		if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
			return "", false, fmt.Errorf("Windows locked Node fixture path is not absolute: %q", path)
		}
		info, err := os.Stat(path)
		if err != nil {
			return "", false, fmt.Errorf("inspect Windows locked Node fixture %q: %w", path, err)
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return "", false, fmt.Errorf("Windows locked Node fixture is not a non-empty regular file: %q", path)
		}
		return "v22.22.0", false, nil
	}
}

// runtimeServerPortableNodeResolver 是公共 portable-cache 断言的 Windows 实现；
// 它复用同一受控 Node fixture，确保断言不会悄悄回退到 PATH。
func runtimeServerPortableNodeResolver(firstEnv map[string]string) runtimeServerNodeVersionResolver {
	nodeEnv := []string{runtimeServerWindowsNodeExecutableEnv + "=" + firstEnv[runtimeServerWindowsNodeExecutableEnv]}
	return runtimeServerTestNodeVersionResolver(nodeEnv)
}

// TestRuntimeServerWindowsTypeScriptLeaseRoundTrip 验证 Windows 选主锁与 lease 使用 DACL 而非 POSIX mode bits。
func TestRuntimeServerWindowsTypeScriptLeaseRoundTrip(t *testing.T) {
	root := runtimeServerSecureCacheRoot(t)
	const cohortID = "repo-windows-typescript"
	role, leasePath, err := runtimeServerAcquireResourceLease(root, cohortID)
	if err != nil {
		t.Fatalf("runtimeServerAcquireResourceLease() error = %v", err)
	}
	if role != multilsp.ResourceCohortRolePrimary {
		t.Fatalf("lease role = %q, want primary", role)
	}
	info, err := os.Lstat(leasePath)
	if err != nil {
		t.Fatalf("lstat lease: %v", err)
	}
	if info.Mode().Perm()&0o077 == 0 {
		t.Fatalf("Windows lease mode = %#o, want non-POSIX mode bits for regression coverage", info.Mode().Perm())
	}
	if _, err := runtimeServerReadResourceLease(leasePath); err != nil {
		t.Fatalf("runtimeServerReadResourceLease() error = %v", err)
	}
	env := []string{
		multilsp.ResourceRepositoryCohortIDEnv + "=" + cohortID,
		multilsp.ResourceCohortLeaseEnv + "=" + leasePath,
	}
	if err := multilsp.ReleaseResourceCohortLease(env); err != nil {
		t.Fatalf("ReleaseResourceCohortLease() error = %v", err)
	}
	if _, err := os.Lstat(leasePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("released lease still exists: %v", err)
	}
}
