//go:build windows && e2e

package processobserve_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/processobserve"
	"golang.org/x/sys/windows"
)

// TestDurableStoreWindowsDACLFileLockAtomicPublishE2E 验证 Windows 持久化根、跨句柄锁与原子发布闭环。
func TestDurableStoreWindowsDACLFileLockAtomicPublishE2E(t *testing.T) {
	root := filepath.Join(t.TempDir(), "durable")
	store, err := processobserve.OpenDurableStore(root, processobserve.DurableOptions{TestOnly: true})
	if err != nil {
		t.Fatalf("OpenDurableStore() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	assertWindowsPrivateDirectory(t, root)

	decision, err := store.RecordGhost(context.Background(), probeMustFail(t, 9_999_994))
	if err != nil {
		t.Fatalf("RecordGhost() error = %v", err)
	}
	if decision.Status() != processobserve.DecisionPersisted {
		t.Fatalf("decision status = %q, want %q", decision.Status(), processobserve.DecisionPersisted)
	}
	assertWindowsAtomicIncident(t, root)
	assertWindowsStoreLockHonorsContext(t, store, filepath.Join(root, ".store.lock"))
}

// TestDurableStoreWindowsRejectsReparseRootE2E 验证持久化根被目录联接替换后必须失败关闭。
func TestDurableStoreWindowsRejectsReparseRootE2E(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "durable")
	store, err := processobserve.OpenDurableStore(root, processobserve.DurableOptions{TestOnly: true})
	if err != nil {
		t.Fatalf("OpenDurableStore() initial error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	moved := filepath.Join(base, "moved")
	if err := os.Rename(root, moved); err != nil {
		t.Fatalf("rename durable root: %v", err)
	}
	output, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", root, moved).CombinedOutput()
	if err != nil {
		t.Fatalf("create directory junction: %v: %s", err, output)
	}
	if _, err := processobserve.OpenDurableStore(root, processobserve.DurableOptions{TestOnly: true}); err == nil {
		t.Fatal("OpenDurableStore() accepted reparse-point root")
	}
}

func assertWindowsPrivateDirectory(t *testing.T, path string) {
	t.Helper()
	descriptor := mustWindowsDirectorySecurityDescriptor(t, path)
	assertWindowsDirectoryDACLProtected(t, path, descriptor)
	userSID := mustWindowsCurrentUserSID(t)
	assertWindowsDirectoryOwner(t, descriptor, userSID)
	systemSID := mustWindowsLocalSystemSID(t)
	dacl := mustWindowsDirectoryDACL(t, descriptor)
	assertWindowsDirectoryDACL(t, dacl, userSID, systemSID)
}

func mustWindowsDirectorySecurityDescriptor(t *testing.T, path string) *windows.SECURITY_DESCRIPTOR {
	t.Helper()
	descriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo(%q): %v", path, err)
	}
	return descriptor
}

func assertWindowsDirectoryDACLProtected(t *testing.T, path string, descriptor *windows.SECURITY_DESCRIPTOR) {
	t.Helper()
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatalf("read directory security control: %v", err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("directory DACL is not protected: %q", path)
	}
}

func mustWindowsCurrentUserSID(t *testing.T) *windows.SID {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatalf("GetTokenUser(): %v", err)
	}
	return user.User.Sid
}

func assertWindowsDirectoryOwner(t *testing.T, descriptor *windows.SECURITY_DESCRIPTOR, userSID *windows.SID) {
	t.Helper()
	owner, _, err := descriptor.Owner()
	if err != nil {
		t.Fatalf("read directory owner: %v", err)
	}
	if owner == nil || !owner.Equals(userSID) {
		t.Fatalf("directory owner = %v, want current user %s", owner, userSID)
	}
}

func mustWindowsLocalSystemSID(t *testing.T) *windows.SID {
	t.Helper()
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatalf("CreateWellKnownSid(LocalSystem): %v", err)
	}
	return system
}

func mustWindowsDirectoryDACL(t *testing.T, descriptor *windows.SECURITY_DESCRIPTOR) *windows.ACL {
	t.Helper()
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		t.Fatalf("read directory DACL: %v", err)
	}
	return dacl
}

func assertWindowsDirectoryDACL(t *testing.T, dacl *windows.ACL, userSID, systemSID *windows.SID) {
	t.Helper()
	foundUser, foundSystem := false, false
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		sid := mustWindowsAllowedACESID(t, dacl, index)
		switch {
		case sid.Equals(userSID):
			foundUser = true
		case sid.Equals(systemSID):
			foundSystem = true
		default:
			t.Fatalf("directory DACL grants unexpected SID %s", sid.String())
		}
	}
	if !foundUser || !foundSystem {
		t.Fatalf("directory DACL entries user=%v system=%v, want both", foundUser, foundSystem)
	}
}

func mustWindowsAllowedACESID(t *testing.T, dacl *windows.ACL, index uint32) *windows.SID {
	t.Helper()
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, index, &ace); err != nil {
		t.Fatalf("read directory ACE %d: %v", index, err)
	}
	if ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Mask == 0 {
		t.Fatalf("directory ACE %d is unsupported", index)
	}
	return (*windows.SID)(unsafe.Pointer(&ace.SidStart))
}

func assertWindowsAtomicIncident(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", root, err)
	}
	incidentCount := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".incident-") && strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("temporary incident remained after publish: %q", entry.Name())
		}
		if strings.HasSuffix(entry.Name(), ".incident") {
			incidentCount++
		}
	}
	if incidentCount != 1 {
		t.Fatalf("incident file count = %d, want 1", incidentCount)
	}
}

func assertWindowsStoreLockHonorsContext(t *testing.T, store *processobserve.Store, lockPath string) {
	t.Helper()
	lockFile, err := os.OpenFile(lockPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open durable store lock: %v", err)
	}
	defer lockFile.Close()
	var overlapped windows.Overlapped
	if err := windows.LockFileEx(windows.Handle(lockFile.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &overlapped); err != nil {
		t.Fatalf("LockFileEx(): %v", err)
	}
	defer windows.UnlockFileEx(windows.Handle(lockFile.Fd()), 0, 1, 0, &overlapped)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = store.ListDecisions(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ListDecisions() error = %v, want context deadline while lock is held", err)
	}
}
