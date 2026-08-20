//go:build windows

package installer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
	"golang.org/x/sys/windows"
)

func TestKotlinWindowsPathErrorPreservesAuthorizationCodes(t *testing.T) {
	for _, code := range []syscall.Errno{5, 1314} {
		err := wrapKotlinWindowsPathError(code, `C:\private\kotlin\stage`)
		var permissionErr *securefs.WindowsPermissionError
		if !errors.As(err, &permissionErr) || permissionErr == nil || permissionErr.Win32Code() != uint32(code) {
			t.Fatalf("wrapped code=%d error=%v permission=%#v", code, err, permissionErr)
		}
	}
}

func TestMaterializeWindowsKotlinProcessRootRejectsInvalidIdentity(t *testing.T) {
	root := t.TempDir()
	if _, err := MaterializeWindowsKotlinProcessRoot(root, filepath.Join(root, "ready", "bin", "other.exe")); err == nil {
		t.Fatal("wrong Kotlin basename was accepted")
	}
	if _, err := MaterializeWindowsKotlinProcessRoot(root, filepath.Join(root, "..", "ready", "bin", "intellij-server.exe")); err == nil {
		t.Fatal("server outside product root was accepted")
	}
}

func TestValidateKotlinWindowsProcessTargetPathFailsBeforePublish(t *testing.T) {
	target := filepath.Join(`C:\product`, strings.Repeat("deep-", 60), "bin", "intellij-server.exe")
	if err := validateKotlinWindowsProcessTargetPath(target); err == nil {
		t.Fatal("overlong Kotlin target path was accepted")
	}
	if _, err := os.Stat(filepath.Dir(target)); !os.IsNotExist(err) {
		t.Fatalf("path guard created publish side effects: stat=%v", err)
	}
}

func TestMaterializeWindowsKotlinProcessRootPublishesDigestIsolatedFlatTree(t *testing.T) {
	root := t.TempDir()
	ready := filepath.Join(root, "cache", "ready", "bin")
	if err := os.MkdirAll(ready, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := securefs.RestrictOwnerOnly(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := securefs.RestrictOwnerOnly(filepath.Join(root, "cache"), 0o700); err != nil {
		t.Fatal(err)
	}
	server := filepath.Join(ready, "intellij-server.exe")
	if err := os.WriteFile(server, []byte("locked-arm64-kotlin"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ready, "librocksdbjni-win-arm64.dll"), []byte("jni"), 0o600); err != nil {
		t.Fatal(err)
	}
	flat, err := MaterializeWindowsKotlinProcessRoot(root, server)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(filepath.Base(filepath.Dir(filepath.Dir(flat))), "kotlin-process-") {
		t.Fatalf("flat root=%q is not digest isolated", flat)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(flat), "librocksdbjni-win-arm64.dll")); err != nil {
		t.Fatalf("flat JNI payload missing: %v", err)
	}
	flatAgain, err := MaterializeWindowsKotlinProcessRoot(root, server)
	if err != nil || flatAgain != flat {
		t.Fatalf("flat reuse=(%q,%v), want same identity=%q", flatAgain, err, flat)
	}
	if err := os.WriteFile(flat, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := MaterializeWindowsKotlinProcessRoot(root, server); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("tampered Kotlin process tree accepted: %v", err)
	}
}

func TestMaterializeWindowsKotlinProcessRootConvergesReadyRootACL(t *testing.T) {
	root := t.TempDir()
	readyRoot := filepath.Join(root, "cache", "ready")
	serverDir := filepath.Join(readyRoot, "bin")
	if err := os.MkdirAll(serverDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := securefs.RestrictOwnerOnly(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := securefs.RestrictOwnerOnly(filepath.Join(root, "cache"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := setBroadKotlinReadyRootACL(readyRoot); err != nil {
		t.Fatalf("set broad ready root ACL: %v", err)
	}
	server := filepath.Join(serverDir, "intellij-server.exe")
	if err := os.WriteFile(server, []byte("acl-convergence"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := MaterializeWindowsKotlinProcessRoot(root, server); err != nil {
		t.Fatalf("MaterializeWindowsKotlinProcessRoot() did not converge a current-user-writable ACL: %v", err)
	}
	if err := securefs.CheckExistingOwnerOnly(readyRoot, nil); err != nil {
		t.Fatalf("ready root ACL remains broad after materialization: %v", err)
	}
}

func setBroadKotlinReadyRootACL(path string) error {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return err
	}
	userSID, err := windows.StringToSid(user.User.Sid.String())
	if err != nil {
		return err
	}
	sddl := "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;" + userSID.String() + ")(A;OICI;FA;;;BU)"
	descriptor, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	)
}

func TestMaterializeWindowsKotlinProcessRootConcurrentSameDigest(t *testing.T) {
	root := t.TempDir()
	ready := filepath.Join(root, "ready", "bin")
	if err := os.MkdirAll(ready, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := securefs.RestrictOwnerOnly(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := securefs.RestrictOwnerOnly(filepath.Join(root, "ready"), 0o700); err != nil {
		t.Fatal(err)
	}
	server := filepath.Join(ready, "intellij-server.exe")
	if err := os.WriteFile(server, []byte("same-digest"), 0o600); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan string, 4)
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			path, err := MaterializeWindowsKotlinProcessRoot(root, server)
			if err != nil {
				errs <- err
				return
			}
			results <- path
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var first string
	for path := range results {
		if first == "" {
			first = path
		} else if path != first {
			t.Fatalf("concurrent same digest produced paths %q and %q", first, path)
		}
	}
}

func TestMaterializeWindowsKotlinProcessRootRejectsReparseSource(t *testing.T) {
	root := t.TempDir()
	ready := filepath.Join(root, "ready", "bin")
	if err := os.MkdirAll(ready, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := securefs.RestrictOwnerOnly(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := securefs.RestrictOwnerOnly(filepath.Join(root, "ready"), 0o700); err != nil {
		t.Fatal(err)
	}
	server := filepath.Join(ready, "intellij-server.exe")
	if err := os.WriteFile(server, []byte("server"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	link := filepath.Join(ready, "linked.dll")
	if err := os.Symlink(filepath.Join(outside, "missing.dll"), link); err != nil {
		t.Skipf("symlink/reparse fixture unavailable: %v", err)
	}
	if _, err := MaterializeWindowsKotlinProcessRoot(root, server); err == nil || !strings.Contains(strings.ToLower(err.Error()), "reparse") {
		t.Fatalf("reparse source accepted: %v", err)
	}
}

func TestMaterializeWindowsKotlinProcessRootRejectsReparseTarget(t *testing.T) {
	root := t.TempDir()
	ready := filepath.Join(root, "ready", "bin")
	if err := os.MkdirAll(ready, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := securefs.RestrictOwnerOnly(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := securefs.RestrictOwnerOnly(filepath.Join(root, "ready"), 0o700); err != nil {
		t.Fatal(err)
	}
	server := filepath.Join(ready, "intellij-server.exe")
	if err := os.WriteFile(server, []byte("server"), 0o600); err != nil {
		t.Fatal(err)
	}
	flat, err := MaterializeWindowsKotlinProcessRoot(root, server)
	if err != nil {
		t.Fatal(err)
	}
	targetRoot := filepath.Dir(filepath.Dir(flat))
	outside := t.TempDir()
	if err := os.RemoveAll(targetRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, targetRoot); err != nil {
		t.Skipf("symlink/reparse fixture unavailable: %v", err)
	}
	if _, err := MaterializeWindowsKotlinProcessRoot(root, server); err == nil || !strings.Contains(strings.ToLower(err.Error()), "reparse") {
		t.Fatalf("reparse target accepted: %v", err)
	}
}
