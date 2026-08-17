//go:build !windows

package lspplatform

import (
	"fmt"
	"os"
	"slices"
	"syscall"
	"time"
)

// GoplsServerArgs 为支持命名 auto endpoint 的平台配置共享 daemon。
func GoplsServerArgs(idleTimeout time.Duration) ([]string, error) {
	if err := ValidateGoplsIdleTimeout(idleTimeout); err != nil {
		return nil, err
	}
	return []string{
		GoplsRemoteAutoCohortArg,
		"-remote.listen.timeout=" + idleTimeout.String(),
	}, nil
}

// NormalizeGoplsForwarderArgs 保持非 Windows 的命名 auto endpoint 参数。
func NormalizeGoplsForwarderArgs(args []string) []string {
	return slices.Clone(args)
}

// GoplsUsesSharedDaemon 表示非 Windows 平台继续使用 gopls 原生命名 auto daemon。
func GoplsUsesSharedDaemon() bool {
	return true
}

func stableDirectoryIdentity(_ string, info os.FileInfo) (string, error) {
	if info == nil {
		return "", fmt.Errorf("canonical root filesystem identity is unavailable")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return "", fmt.Errorf("canonical root filesystem identity is unsupported on this platform")
	}
	return fmt.Sprintf("dev:%d:ino:%d", stat.Dev, stat.Ino), nil
}
