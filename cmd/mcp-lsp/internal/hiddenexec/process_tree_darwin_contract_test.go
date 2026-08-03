//go:build darwin

package hiddenexec

import (
	"context"
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
