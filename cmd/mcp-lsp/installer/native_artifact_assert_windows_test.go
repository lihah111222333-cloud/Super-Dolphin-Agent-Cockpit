//go:build windows

package installer

import "testing"

// assertExecutable 在 Windows 构建中不伪造 POSIX execute-bit 断言；Windows 可执行
// 格式与架构由对应 PE 校验测试负责。
func assertExecutable(t *testing.T, _ string) {
	t.Helper()
}
