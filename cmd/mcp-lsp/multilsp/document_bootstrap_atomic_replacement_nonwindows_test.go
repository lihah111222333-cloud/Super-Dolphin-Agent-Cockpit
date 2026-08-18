//go:build !windows

package multilsp

import (
	"strings"
	"testing"
)

func assertAtomicReplacementOutcome(t *testing.T, renameErr error, _ documentSnapshot, err error) {
	t.Helper()
	if renameErr != nil {
		t.Fatalf("atomic replace target: %v", renameErr)
	}
	if err == nil || !strings.Contains(err.Error(), "path was replaced") {
		t.Fatalf("read snapshot error = %v, want atomic path replacement rejection", err)
	}
}
