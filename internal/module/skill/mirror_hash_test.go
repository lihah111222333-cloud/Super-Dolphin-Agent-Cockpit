package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func skipWindowsShortMirrorIntegration(t *testing.T) {
	t.Helper()
	if testing.Short() && runtime.GOOS == "windows" {
		t.Skip("skipping Windows short-mode mirror integration; covered by full test matrix")
	}
}

func TestMirrorHashStableIncludesRelativePathModeAndBytes(t *testing.T) {
	first := writeMirrorHashFixture(t)
	second := writeMirrorHashFixture(t)

	firstHash, err := stableMirrorDirectoryHash(first)
	if err != nil {
		t.Fatalf("stableMirrorDirectoryHash(first): %v", err)
	}
	secondHash, err := stableMirrorDirectoryHash(second)
	if err != nil {
		t.Fatalf("stableMirrorDirectoryHash(second): %v", err)
	}
	if firstHash == "" || firstHash != secondHash {
		t.Fatalf("hash must be stable across roots: %q vs %q", firstHash, secondHash)
	}

	writeFileMode(t, first, "references/api.md", 0o644, []byte("different bytes\n"))
	changedBytesHash, err := stableMirrorDirectoryHash(first)
	if err != nil {
		t.Fatalf("stableMirrorDirectoryHash(changed bytes): %v", err)
	}
	if changedBytesHash == firstHash {
		t.Fatalf("hash did not change after file bytes changed")
	}

	if runtime.GOOS != "windows" {
		writeFileMode(t, second, "scripts/run.sh", 0o644, []byte("#!/bin/sh\necho hi\n"))
		changedModeHash, err := stableMirrorDirectoryHash(second)
		if err != nil {
			t.Fatalf("stableMirrorDirectoryHash(changed mode): %v", err)
		}
		if changedModeHash == firstHash {
			t.Fatalf("hash did not change after mode bits changed")
		}
	}
}

func TestMirrorHashIgnoresMirrorManifest(t *testing.T) {
	root := writeMirrorHashFixture(t)
	before, err := stableMirrorDirectoryHash(root)
	if err != nil {
		t.Fatalf("stableMirrorDirectoryHash: %v", err)
	}

	writeFileMode(t, root, skillMirrorManifestFile, 0o644, []byte(`{"mirror_hash":"changed"}`))
	after, err := stableMirrorDirectoryHash(root)
	if err != nil {
		t.Fatalf("stableMirrorDirectoryHash after manifest write: %v", err)
	}
	if after != before {
		t.Fatalf("manifest file must be ignored: before %q after %q", before, after)
	}
}

func TestMirrorHashIncludesRelativePath(t *testing.T) {
	left := t.TempDir()
	right := t.TempDir()
	writeFileMode(t, left, "references/api.md", 0o644, []byte("same\n"))
	writeFileMode(t, right, "templates/api.md", 0o644, []byte("same\n"))

	leftHash, err := stableMirrorDirectoryHash(left)
	if err != nil {
		t.Fatalf("stableMirrorDirectoryHash(left): %v", err)
	}
	rightHash, err := stableMirrorDirectoryHash(right)
	if err != nil {
		t.Fatalf("stableMirrorDirectoryHash(right): %v", err)
	}
	if leftHash == rightHash {
		t.Fatalf("hash did not change for different relative paths")
	}
}

func TestMirrorHashRejectsUnsafePaths(t *testing.T) {
	root := writeMirrorHashFixture(t)
	if err := os.Symlink(filepath.Join(t.TempDir(), "outside.md"), filepath.Join(root, "outside.md")); err != nil {
		skipIfSymlinkPrivilegeNotHeld(t, err)
		t.Fatalf("Symlink: %v", err)
	}
	if _, err := stableMirrorDirectoryHash(root); err == nil {
		t.Fatalf("stableMirrorDirectoryHash error = nil, want unsafe path rejection")
	}
}

func TestSkillDirContentHashRejectsOversizedSupportFile(t *testing.T) {
	root := t.TempDir()
	writeFileMode(t, root, skillMainFile, 0o644, []byte("# Build\n"))
	writeFileMode(t, root, "references/huge.bin", 0o644, make([]byte, maxSkillFileBytes+1))

	if _, err := skillDirContentHash(root); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("skillDirContentHash oversized file error = %v, want too large", err)
	}
}

func TestSkillDirContentHashRejectsTotalByteLimit(t *testing.T) {
	root := t.TempDir()
	writeFileMode(t, root, skillMainFile, 0o644, []byte("# Build\n"))
	for i := 0; i < 33; i++ {
		writeFileMode(t, root, fmt.Sprintf("references/part-%02d.bin", i), 0o644, make([]byte, maxSkillFileBytes))
	}

	if _, err := skillDirContentHash(root); err == nil || !strings.Contains(err.Error(), "total") {
		t.Fatalf("skillDirContentHash total limit error = %v, want total limit", err)
	}
}

func TestSkillDirContentHashRejectsFileCountLimit(t *testing.T) {
	root := t.TempDir()
	writeFileMode(t, root, skillMainFile, 0o644, []byte("# Build\n"))
	for i := 0; i < 513; i++ {
		writeFileMode(t, root, fmt.Sprintf("references/file-%03d.md", i), 0o644, []byte("x\n"))
	}

	if _, err := skillDirContentHash(root); err == nil || !strings.Contains(err.Error(), "too many files") {
		t.Fatalf("skillDirContentHash file count error = %v, want too many files", err)
	}
}

func writeMirrorHashFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFileMode(t, root, "SKILL.md", 0o644, []byte("# Build\n"))
	writeFileMode(t, root, "references/api.md", 0o644, []byte("api\n"))
	writeFileMode(t, root, "scripts/run.sh", 0o755, []byte("#!/bin/sh\necho hi\n"))
	writeFileMode(t, root, skillMirrorManifestFile, 0o644, []byte(`{"ignored":true}`))
	return root
}

func writeFileMode(t *testing.T, root, rel string, mode os.FileMode, data []byte) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("Chmod(%s): %v", path, err)
	}
}
