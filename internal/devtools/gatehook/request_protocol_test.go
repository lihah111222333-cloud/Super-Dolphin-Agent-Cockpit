package gatehook

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRequestProtocolRejectsRetiredKinds(t *testing.T) {
	for _, kind := range []RequestKind{"status", "wait"} {
		t.Run(string(kind), func(t *testing.T) {
			err := (Request{Kind: kind, Submit: &SubmitRequest{}}).Validate()
			if err == nil || !strings.Contains(err.Error(), "unsupported request kind") {
				t.Fatalf("Validate(%q) error = %v, want unsupported request kind", kind, err)
			}
		})
	}
}

func TestRequestProtocolSourceGuardContainsOnlySubmitBranch(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	source, err := os.ReadFile(filepath.Join(filepath.Dir(currentFile), "types.go"))
	if err != nil {
		t.Fatalf("read request protocol source: %v", err)
	}
	for _, forbidden := range []string{
		"Status" + "Request",
		"Wait" + "Request",
		"RequestKind" + "Status",
		"RequestKind" + "Wait",
		"Status *",
		"Wait *",
		"Status(context.Context",
		"Wait(context.Context",
	} {
		if strings.Contains(string(source), forbidden) {
			t.Fatalf("types.go retains retired request surface %q", forbidden)
		}
	}
}
