package lspplatform

import (
	"fmt"
	"os"
	"time"
)

const (
	// GoplsRemoteAutoArg 是不带平台 cohort 标识的 gopls 自动 daemon 地址。
	GoplsRemoteAutoArg = "-remote=auto"
	// GoplsRemoteAutoCohortArg 是支持命名 auto endpoint 平台的基础参数。
	GoplsRemoteAutoCohortArg = "-remote=auto;sdmcp2"
)

// ValidateGoplsIdleTimeout 拒绝无法形成明确 daemon 生命周期的非正超时。
func ValidateGoplsIdleTimeout(idleTimeout time.Duration) error {
	if idleTimeout <= 0 {
		return fmt.Errorf("LSP idle timeout must be positive: %s", idleTimeout)
	}
	return nil
}

// StableDirectoryIdentity 返回平台稳定的目录身份；路径替换后身份必须变化。
func StableDirectoryIdentity(path string, info os.FileInfo) (string, error) {
	return stableDirectoryIdentity(path, info)
}
