//go:build windows

package observability

import "testing"

// TestChmodOwnerOnlyWindowsPreservesNoopContract 锁定 Windows 不用 Unix mode bit 冒充 ACL。
func TestChmodOwnerOnlyWindowsPreservesNoopContract(t *testing.T) {
	if err := chmodOwnerOnly(t.TempDir(), traceDirPerm); err != nil {
		t.Fatalf("chmodOwnerOnly() Windows contract error = %v", err)
	}
}
