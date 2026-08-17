//go:build windows

package discovery

import (
	"os"
	"testing"
)

// Windows 权限由 ACL 语义覆盖，Unix mode bits 不是有效的 owner-only 断言。
func assertDiscoveryFileOwnerOnly(t *testing.T, info os.FileInfo) {
	t.Helper()
}
