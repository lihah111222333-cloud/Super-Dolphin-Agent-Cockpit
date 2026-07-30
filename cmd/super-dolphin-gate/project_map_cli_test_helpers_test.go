package main

import (
	"bytes"
	"os"
	"testing"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func requireProjectMapCLICode(t *testing.T, want gatecontract.ExitCode, operation string, args ...string) {
	t.Helper()
	code, _, stderr := executeProjectMapCLI(args...)
	if code != int(want) {
		t.Fatalf("%s code=%d stderr=%q", operation, code, stderr)
	}
}

func assertProjectMapRefreshReplacesOnlyManagedOutputs(t *testing.T, mapPath string, stagedMap []byte, untrackedMapPath string, userPath string) {
	t.Helper()
	gotMap, err := os.ReadFile(mapPath)
	if err != nil {
		t.Fatalf("read refreshed project map: %v", err)
	}
	if !bytes.Equal(gotMap, stagedMap) {
		t.Fatal("refresh did not replace an unstaged project-map overlay from the exact tree")
	}
	if _, err := os.Lstat(untrackedMapPath); !os.IsNotExist(err) {
		t.Fatalf("untracked managed output survived refresh: %v", err)
	}
	userData, err := os.ReadFile(userPath)
	if err != nil || string(userData) != "user work\n" {
		t.Fatalf("refresh changed untracked user file: data=%q err=%v", userData, err)
	}
}
