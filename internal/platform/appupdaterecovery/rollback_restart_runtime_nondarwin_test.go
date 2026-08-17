//go:build !darwin

package appupdaterecovery

import "testing"

// 只有 Darwin 提供本测试所需的 cooperative Unix endpoint；其他平台保持显式跳过。
func newRefusedRollbackRestartEndpoint(t *testing.T) string {
	t.Helper()
	t.Skip("cooperative rollback endpoint requires Darwin")
	return ""
}
