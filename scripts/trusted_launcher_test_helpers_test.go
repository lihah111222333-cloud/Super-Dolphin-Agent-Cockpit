package main

import (
	"os"
	"path/filepath"
	"testing"
)

// secureTrustedLauncherTestRoot 在当前执行环境的 HOME 下创建与生产同构的私有 launcher 根。
//
// ECI worker 使用固定的非 root UID，但其最小镜像可能没有 passwd 条目；
// 因此测试 fixture 不能依赖 os/user.LookupId，而应复用 executor 显式提供的
// HOME（该目录本身及其祖先由 worker workspace contract 保持为私有、安全路径）。
func secureTrustedLauncherTestRoot(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("resolve HOME for launcher fixture: %v", err)
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatalf("resolve HOME for launcher fixture: %v", err)
	}
	root, err := os.MkdirTemp(home, ".super-dolphin-gate-launcher-test-")
	if err != nil {
		t.Fatalf("create private launcher fixture root: %v", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		_ = os.RemoveAll(root)
		t.Fatalf("restrict private launcher fixture root: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove private launcher fixture root: %v", err)
		}
	})
	return root
}
