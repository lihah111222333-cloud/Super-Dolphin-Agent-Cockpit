package gateprivate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestTrustedGitCommandIgnoresReplaceAndAmbientRepositoryRouting uses a real
// replacement ref: the exact tree OID must still read its original content.
func TestTrustedGitCommandIgnoresReplaceAndAmbientRepositoryRouting(t *testing.T) {
	repository := t.TempDir()
	trustedGitFixtureRun(t, repository, "init", "--quiet")
	trustedGitFixtureRun(t, repository, "config", "user.name", "trusted git")
	trustedGitFixtureRun(t, repository, "config", "user.email", "trusted-git@example.invalid")
	trustedGitFixtureWrite(t, repository, "original\n")
	trustedGitFixtureRun(t, repository, "add", ".")
	trustedGitFixtureRun(t, repository, "commit", "--quiet", "-m", "原始")
	originalTree := trustedGitFixtureOutput(t, repository, "rev-parse", "HEAD^{tree}")
	trustedGitFixtureWrite(t, repository, "replacement\n")
	trustedGitFixtureRun(t, repository, "add", ".")
	trustedGitFixtureRun(t, repository, "commit", "--quiet", "-m", "替代")
	replacementTree := trustedGitFixtureOutput(t, repository, "rev-parse", "HEAD^{tree}")
	trustedGitFixtureRun(t, repository, "replace", originalTree, replacementTree)
	ambient := t.TempDir()
	trustedGitFixtureRun(t, ambient, "init", "--quiet")
	t.Setenv("GIT_DIR", filepath.Join(ambient, ".git"))
	if got := trustedGitOutput(t, repository, "show", originalTree+":identity.txt"); got != "original\n" {
		t.Fatalf("trusted content = %q, want original object", got)
	}
	if err := os.Unsetenv("GIT_DIR"); err != nil {
		t.Fatal(err)
	}
	trustedGitFixtureRun(t, repository, "replace", "-d", originalTree)
	if got := trustedGitOutput(t, repository, "show", originalTree+":identity.txt"); got != "original\n" {
		t.Fatalf("content after replace deletion = %q, want original object", got)
	}
}

// TestCandidateObjectAuthorityRejectsPartialAndDriftedAmbientRouting keeps the
// only permitted candidate ODB capture fail-closed. No later trusted Git read
// may consult ambient Git routing.
func TestCandidateObjectAuthorityRejectsPartialAndDriftedAmbientRouting(t *testing.T) {
	t.Run("alternate without primary", func(t *testing.T) {
		t.Setenv("GIT_ALTERNATE_OBJECT_DIRECTORIES", t.TempDir())
		if _, err := CaptureCandidateObjectAuthority(); err == nil {
			t.Fatal("partial ambient candidate object routing unexpectedly passed")
		}
	})
	t.Run("relative primary", func(t *testing.T) {
		t.Setenv("GIT_OBJECT_DIRECTORY", "relative-objects")
		if _, err := CaptureCandidateObjectAuthority(); err == nil {
			t.Fatal("relative ambient candidate object routing unexpectedly passed")
		}
	})
	t.Run("path drift", func(t *testing.T) {
		objects := filepath.Join(t.TempDir(), "objects")
		if err := os.MkdirAll(objects, 0o700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GIT_OBJECT_DIRECTORY", objects)
		authority, err := CaptureCandidateObjectAuthority()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(objects); err != nil {
			t.Fatal(err)
		}
		if _, err := TrustedGitCommandWithCandidateObjectAuthority(context.Background(), t.TempDir(), authority, "rev-parse", "HEAD"); err == nil {
			t.Fatal("drifted candidate object authority unexpectedly produced a trusted Git command")
		}
	})
}

func trustedGitOutput(t *testing.T, repository string, args ...string) string {
	t.Helper()
	command, err := TrustedGitCommand(context.Background(), repository, args...)
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.Output()
	if err != nil {
		t.Fatalf("trusted git %v: %v", args, err)
	}
	return string(output)
}

func trustedGitFixtureWrite(t *testing.T, repository, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repository, "identity.txt"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func trustedGitFixtureRun(t *testing.T, repository string, args ...string) {
	t.Helper()
	if output, err := exec.Command("/usr/bin/git", append([]string{"-C", repository}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("fixture git %v: %v: %s", args, err, output)
	}
}

func trustedGitFixtureOutput(t *testing.T, repository string, args ...string) string {
	t.Helper()
	output, err := exec.Command("/usr/bin/git", append([]string{"-C", repository}, args...)...).Output()
	if err != nil {
		t.Fatalf("fixture git %v: %v", args, err)
	}
	return strings.TrimSpace(string(output))
}
