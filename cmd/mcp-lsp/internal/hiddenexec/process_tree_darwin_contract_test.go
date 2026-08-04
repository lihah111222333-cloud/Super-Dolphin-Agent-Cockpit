//go:build darwin

package hiddenexec

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func TestDarwinIdentityUsesHighResolutionNativeStartToken(t *testing.T) {
	cmd := Command("/bin/sleep", "30")
	tree, err := StartProcessTree(cmd)
	if err != nil {
		t.Fatalf("StartProcessTree() error = %v", err)
	}
	identity, err := tree.Identity()
	if err != nil {
		t.Fatalf("Identity() error = %v", err)
	}
	if !strings.Contains(identity.StartToken, ".") {
		t.Fatalf("StartToken = %q, want native second.microsecond token", identity.StartToken)
	}
	if err := tree.Force(context.Background()); err != nil {
		t.Fatalf("Force() error = %v", err)
	}
	_ = cmd.Wait()
}

func TestDarwinStartupPIDReuseDoesNotSignal(t *testing.T) {
	expected := &ProcessIdentity{PID: 5454, StartToken: "boot/start-a"}
	signals := 0
	err, pending := startupAbortAction(&exec.Cmd{}, expected.PID, expected, true, nil, startupAbortHooks{
		captureIdentity: func(pid int) (ProcessIdentity, error) {
			return ProcessIdentity{PID: pid, StartToken: "boot/start-b"}, nil
		},
		killGroup: func(int) error {
			signals++
			return nil
		},
		killProcess: func(*exec.Cmd) error {
			signals++
			return nil
		},
	})
	if !pending || !errors.Is(err, ErrProcessTreeIdentityMismatch) {
		t.Fatalf("Darwin startup PID reuse result = (%v, pending=%v), want fail-closed identity mismatch", err, pending)
	}
	if signals != 0 {
		t.Fatalf("Darwin startup PID reuse signal attempts = %d, want zero", signals)
	}
}
