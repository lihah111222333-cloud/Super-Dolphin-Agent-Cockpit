//go:build windows

package observability

import "os"

// chmodOwnerOnly 保留 Windows 既有语义：Unix mode bit 不能表达 Windows ACL，
// 因此本函数不把 os.FileMode 误写成 ACL 保证，也不改变现有 trace 目录策略。
func chmodOwnerOnly(_ string, _ os.FileMode) error {
	return nil
}
