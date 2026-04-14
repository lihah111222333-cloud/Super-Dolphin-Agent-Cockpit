package memory

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestTeamSanitizePathKeyRejectsTraversalAttacks(t *testing.T) {
	cases := []string{
		"../secret.md",
		"notes/%2e%2e/secret.md",
		"%252e%252e%252fsecret.md",
		`..\\secret.md`,
		"．．／secret.md",
		"/etc/passwd",
		`C:\\Windows\\win.ini`,
		"notes\x00secret.md",
	}
	for _, raw := range cases {
		if _, err := sanitizePathKey(raw); !errors.Is(err, ErrInvalidTeamMemKey) {
			t.Fatalf("sanitizePathKey(%q) error = %v, want %v", raw, err, ErrInvalidTeamMemKey)
		}
	}
}

func TestTeamSanitizePathKeyNormalizesEncodedSeparators(t *testing.T) {
	got, err := sanitizePathKey(`notes\\Roadmap%20v1.md`)
	if err != nil {
		t.Fatalf("sanitizePathKey() error = %v", err)
	}
	if want := "notes/Roadmap v1.md"; got != want {
		t.Fatalf("sanitizePathKey() = %q, want %q", got, want)
	}
}

func TestTeamValidateTeamMemKeyNormalizesAndValidatesWithinRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo", teamMemoryRootDirName)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", root, err)
	}
	got, err := validateTeamMemKey(root, `notes\\Roadmap%20v1.md`)
	if err != nil {
		t.Fatalf("validateTeamMemKey() error = %v", err)
	}
	if want := "notes/Roadmap v1.md"; got != want {
		t.Fatalf("validateTeamMemKey() = %q, want %q", got, want)
	}
}

func TestTeamValidateTeamMemWritePathRejectsSymlinkEscape(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo", teamMemoryRootDirName)
	outside := filepath.Join(t.TempDir(), "outside")
	for _, dir := range []string{root, outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", dir, err)
		}
	}
	linkPath := filepath.Join(root, "escaped")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Skipf("Symlink unsupported: %v", err)
	}
	if _, err := validateTeamMemWritePath(root, filepath.Join("escaped", "secret.md")); !errors.Is(err, ErrInvalidTeamMemWritePath) {
		t.Fatalf("validateTeamMemWritePath(symlink escape) error = %v, want %v", err, ErrInvalidTeamMemWritePath)
	}
}

func TestTeamValidateTeamMemWritePathRejectsDanglingSymlink(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo", teamMemoryRootDirName)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", root, err)
	}
	linkPath := filepath.Join(root, "dangling")
	if err := os.Symlink(filepath.Join(root, "missing-target"), linkPath); err != nil {
		t.Skipf("Symlink unsupported: %v", err)
	}
	if _, err := validateTeamMemWritePath(root, filepath.Join("dangling", "secret.md")); !errors.Is(err, ErrInvalidTeamMemWritePath) {
		t.Fatalf("validateTeamMemWritePath(dangling symlink) error = %v, want %v", err, ErrInvalidTeamMemWritePath)
	}
}

func TestTeamValidateTeamMemWritePathRejectsSymlinkLoop(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo", teamMemoryRootDirName)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", root, err)
	}
	loopPath := filepath.Join(root, "loop")
	if err := os.Symlink(loopPath, loopPath); err != nil {
		t.Skipf("Symlink unsupported: %v", err)
	}
	if _, err := validateTeamMemWritePath(root, filepath.Join("loop", "secret.md")); !errors.Is(err, ErrInvalidTeamMemWritePath) {
		t.Fatalf("validateTeamMemWritePath(symlink loop) error = %v, want %v", err, ErrInvalidTeamMemWritePath)
	}
}

func TestTeamValidateTeamMemWritePathAcceptsNormalizedRelativePath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo", teamMemoryRootDirName)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", root, err)
	}
	got, err := validateTeamMemWritePath(root, `notes\\Roadmap%20v1.md`)
	if err != nil {
		t.Fatalf("validateTeamMemWritePath() error = %v", err)
	}
	want := filepath.Join(root, "notes", "Roadmap v1.md")
	if got != want {
		t.Fatalf("validateTeamMemWritePath() = %q, want %q", got, want)
	}
}
