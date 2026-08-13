//go:build windows

package multilsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestResourceCohortMemberWindowsRoundTripUsesDACL 验证成员报告在 Windows mode bits 非私有时仍按严格 DACL 读取。
func TestResourceCohortMemberWindowsRoundTripUsesDACL(t *testing.T) {
	dir := resourceCohortPrivateTestDir(t)
	member := resourceCohortTestMember(os.Getpid(), os.Getpid(), 1, time.Now(), 0)
	if err := writeResourceCohortMember(dir, member); err != nil {
		t.Fatalf("writeResourceCohortMember() error = %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read cohort directory: %v", err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatalf("lstat cohort member: %v", err)
		}
		if info.Mode().Perm()&0o077 == 0 {
			t.Fatalf("Windows member mode = %#o, want non-POSIX mode bits for regression coverage", info.Mode().Perm())
		}
		got, err := readResourceCohortMember(path)
		if err != nil {
			t.Fatalf("readResourceCohortMember() error = %v", err)
		}
		if got != member {
			t.Fatalf("round-trip member = %#v, want %#v", got, member)
		}
		return
	}
	t.Fatal("resource cohort member report was not published")
}
