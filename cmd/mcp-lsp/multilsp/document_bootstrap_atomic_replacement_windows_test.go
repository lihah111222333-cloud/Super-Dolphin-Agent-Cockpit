//go:build windows

package multilsp

import (
	"os"
	"strings"
	"testing"
)

func assertAtomicReplacementOutcome(t *testing.T, renameErr error, snapshot documentSnapshot, err error) {
	t.Helper()
	if renameErr != nil {
		if !os.IsPermission(renameErr) {
			t.Fatalf("Windows atomic replacement error = %v, want access denied while target is open", renameErr)
		}
		if err != nil || snapshot.text != "function OldAtomic() {}\n" {
			t.Fatalf("snapshot after OS-blocked replacement = text %q err %v, want original clean snapshot", snapshot.text, err)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), "path was replaced") {
		t.Fatalf("read snapshot error = %v, want atomic path replacement rejection", err)
	}
}
