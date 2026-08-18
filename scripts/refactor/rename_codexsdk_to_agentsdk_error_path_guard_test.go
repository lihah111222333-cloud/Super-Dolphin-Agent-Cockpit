package main

import "testing"

func TestRenameScriptErrorPathGuard_FatalfAndRenameFailures(t *testing.T) {
	t.Parallel()

	runRenameHarnessGoTest(t, "rename_codexsdk_to_agentsdk_error_harness_test.go", renameErrorPathFatalHarnessSource)
}

const renameErrorPathFatalHarnessSource = `package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFatalfExitSemantics(t *testing.T) {
	if os.Getenv("RENAME_FATALF_HELPER") == "1" {
		fatalf("fatal helper %d", 7)
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestFatalfExitSemantics$")
	cmd.Env = append(os.Environ(), "RENAME_FATALF_HELPER=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatal("fatalf helper unexpectedly succeeded")
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("error type = %T, want *exec.ExitError", err)
	}
	if exitErr.ExitCode() != 1 {
		t.Fatalf("exit code = %d, want 1", exitErr.ExitCode())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if got := stderr.String(); got != "fatal helper 7\n" {
		t.Fatalf("stderr = %q, want %q", got, "fatal helper 7\n")
	}
}

func TestRenameWalkDirSkipVsAbortBoundary(t *testing.T) {
	root := t.TempDir()
	txtPath := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(txtPath, []byte("unchanged"), 0o644); err != nil {
		t.Fatalf("write notes.txt: %v", err)
	}
	vendorDir := filepath.Join(root, "vendor")
	if err := os.MkdirAll(vendorDir, 0o755); err != nil {
		t.Fatalf("mkdir vendor: %v", err)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("readdir root: %v", err)
	}
	var txtEntry, vendorEntry os.DirEntry
	for _, entry := range entries {
		switch entry.Name() {
		case "notes.txt":
			txtEntry = entry
		case "vendor":
			vendorEntry = entry
		}
	}
	if txtEntry == nil || vendorEntry == nil {
		t.Fatalf("entries missing: txt=%v vendor=%v", txtEntry, vendorEntry)
	}

	calledCollect := false
	calledApply := false
	reports := []fileReport{}
	total := 0
	walk := renameWalkDir(root, false,
		func(path string, src []byte) ([]edit, []replacement, error) {
			calledCollect = true
			return nil, nil, nil
		},
		func(src []byte, edits []edit) []byte {
			calledApply = true
			return src
		},
		&reports,
		&total,
	)

	if err := walk(txtPath, txtEntry, nil); err != nil {
		t.Fatalf("non-go walk err = %v, want nil", err)
	}
	if calledCollect || calledApply {
		t.Fatalf("non-go file should skip collector/applier, collect=%v apply=%v", calledCollect, calledApply)
	}
	if err := walk(vendorDir, vendorEntry, nil); err != filepath.SkipDir {
		t.Fatalf("vendor walk err = %v, want filepath.SkipDir", err)
	}

	missingPath := filepath.Join(root, "missing.go")
	err = processRenameFile(root, missingPath, false,
		func(path string, src []byte) ([]edit, []replacement, error) {
			calledCollect = true
			return nil, nil, nil
		},
		func(src []byte, edits []edit) []byte {
			calledApply = true
			return src
		},
		&reports,
		&total,
	)
	if err == nil {
		t.Fatal("missing go file should abort with read error")
	}
	if !strings.Contains(err.Error(), "read missing.go:") {
		t.Fatalf("read error = %q, want wrapped read missing.go error", err.Error())
	}
	if calledCollect || calledApply {
		t.Fatalf("read failure should abort before collector/applier, collect=%v apply=%v", calledCollect, calledApply)
	}
	if len(reports) != 0 || total != 0 {
		t.Fatalf("read failure should not record reports, reports=%#v total=%d", reports, total)
	}
}

func TestProcessRenameFileWrapsCollectEditsParseErrors(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "broken.go")
	if err := os.WriteFile(target, []byte("package demo\nimport (\n"), 0o644); err != nil {
		t.Fatalf("write broken.go: %v", err)
	}

	reports := []fileReport{}
	total := 0
	err := processRenameFile(root, target, false, collectEdits, applyEdits, &reports, &total)
	if err == nil {
		t.Fatal("invalid go file should return wrapped collect error")
	}
	if !strings.Contains(err.Error(), "collect edits for broken.go:") {
		t.Fatalf("error = %q, want collect wrapper", err.Error())
	}
	if !strings.Contains(err.Error(), "expected") {
		t.Fatalf("error = %q, want parse detail", err.Error())
	}
	if len(reports) != 0 || total != 0 {
		t.Fatalf("parse failure should not record reports, reports=%#v total=%d", reports, total)
	}
}
`

func TestRenameScriptErrorPathGuard_ProcessFileFailures(t *testing.T) {
	t.Parallel()

	runRenameHarnessGoTest(t, "rename_codexsdk_to_agentsdk_error_process_harness_test.go", renameErrorPathProcessHarnessSource)
}

const renameErrorPathProcessHarnessSource = `package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProcessRenameFileNoopAndReadErrorContracts(t *testing.T) {
	root := t.TempDir()
	noop := filepath.Join(root, "noop.go")
	if err := os.WriteFile(noop, []byte("package demo\n\nimport _ \"fmt\"\n"), 0o640); err != nil {
		t.Fatalf("write noop.go: %v", err)
	}

	reports := []fileReport{}
	total := 0
	if err := processRenameFile(root, noop, false, collectEdits, applyEdits, &reports, &total); err != nil {
		t.Fatalf("processRenameFile(noop) err = %v, want nil", err)
	}
	if len(reports) != 0 || total != 0 {
		t.Fatalf("noop should not record reports, reports=%#v total=%d", reports, total)
	}

	err := processRenameFile(root, filepath.Join(root, "missing.go"), false, collectEdits, applyEdits, &reports, &total)
	if err == nil {
		t.Fatal("missing file should return wrapped read error")
	}
	if !strings.Contains(err.Error(), "read missing.go:") {
		t.Fatalf("missing file err = %q, want wrapped read error", err.Error())
	}
}

func TestProcessRenameFileApplyWriteFailureIncludesContext(t *testing.T) {
	root := t.TempDir()
	blocked := filepath.Join(root, "blocked.go")
	if err := os.WriteFile(blocked, []byte("package demo\n"), 0o400); err != nil {
		t.Fatalf("write blocked.go: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o600) })
	if err := os.WriteFile(blocked, []byte("probe"), 0o600); err == nil {
		t.Skip("environment ignores 0400 file permissions (e.g. running as root)")
	}

	reports := []fileReport{}
	total := 0
	err := processRenameFile(root, blocked, true,
		func(path string, src []byte) ([]edit, []replacement, error) {
			return []edit{{Start: 0, End: 0, NewLit: "package demo\n", Line: 1}},
				[]replacement{{Old: oldImportRoot, New: newImportRoot, Line: 1}}, nil
		},
		applyEdits,
		&reports,
		&total,
	)
	if err == nil {
		t.Fatal("processRenameFile() should surface write failure")
	}
	if !strings.Contains(err.Error(), "write blocked.go:") {
		t.Fatalf("processRenameFile() err = %q, want blocked.go write failure", err.Error())
	}
}
`
