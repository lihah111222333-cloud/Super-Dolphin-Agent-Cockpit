//go:build windows

package skill

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveOwnerIdentityCreatesWindowsACLBackedSalt(t *testing.T) {
	home := t.TempDir()
	identity, err := resolveOwnerIdentity(home, "501", "default")
	if err != nil {
		t.Fatalf("resolveOwnerIdentity: %v", err)
	}
	if _, err := readOwnerIdentitySalt(identity.SaltPath); err != nil {
		t.Fatalf("readOwnerIdentitySalt(%s): %v", identity.SaltPath, err)
	}
}

func TestReadOwnerIdentitySaltRejectsBroadWindowsACL(t *testing.T) {
	home := t.TempDir()
	identity, err := resolveOwnerIdentity(home, "501", "default")
	if err != nil {
		t.Fatalf("resolveOwnerIdentity: %v", err)
	}
	if err := runTestICACLS(identity.SaltPath, "/grant", "*S-1-5-11:(R)"); err != nil {
		t.Fatalf("grant broad salt ACL: %v", err)
	}

	_, err = readOwnerIdentitySalt(filepath.Clean(identity.SaltPath))
	if err == nil || !strings.Contains(err.Error(), "broad principal") {
		t.Fatalf("readOwnerIdentitySalt() error = %v, want broad principal ACL rejection", err)
	}
}

func runTestICACLS(path string, args ...string) error {
	cmdArgs := append([]string{path}, args...)
	cmd := exec.Command("icacls", cmdArgs...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return errWithOutput{err: err, output: strings.TrimSpace(string(output))}
	}
	return nil
}

type errWithOutput struct {
	err    error
	output string
}

func (e errWithOutput) Error() string {
	if e.output == "" {
		return e.err.Error()
	}
	return e.err.Error() + ": " + e.output
}
