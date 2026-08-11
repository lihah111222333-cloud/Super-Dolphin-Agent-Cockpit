package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestRootGateBinaryIsIgnoredByExactCandidateIndex keeps an accidental local
// gate build out of the isolated index used for an exact candidate snapshot.
func TestRootGateBinaryIsIgnoredByExactCandidateIndex(t *testing.T) {
	ignore, err := os.ReadFile(filepath.Join("..", ".gitignore"))
	if err != nil {
		t.Fatalf("read repository gitignore: %v", err)
	}

	root := t.TempDir()
	writeRootBinaryIgnoreFixture(t, root, ".gitignore", string(ignore))
	writeRootBinaryIgnoreFixture(t, root, "super-dolphin-gate", "local Mach-O build artifact\n")
	writeRootBinaryIgnoreFixture(t, root, "cmd/super-dolphin-gate/main.go", "package main\n\nfunc main() {}\n")
	runRootBinaryIgnoreGit(t, root, nil, "init", "--quiet")

	if err := exec.Command("git", "-C", root, "check-ignore", "-q", "super-dolphin-gate").Run(); err != nil {
		t.Fatalf("root gate binary must be ignored: %v", err)
	}
	if err := exec.Command("git", "-C", root, "check-ignore", "-q", "cmd/super-dolphin-gate/main.go").Run(); err == nil {
		t.Fatal("gate command source directory must not be ignored")
	}

	index := filepath.Join(t.TempDir(), "exact-candidate.index")
	env := append(os.Environ(), "GIT_INDEX_FILE="+index)
	runRootBinaryIgnoreGit(t, root, env, "add", "--all")
	tree := runRootBinaryIgnoreGit(t, root, env, "write-tree")
	paths := strings.Fields(runRootBinaryIgnoreGit(t, root, env, "ls-tree", "-r", "--name-only", tree))
	for _, want := range []string{".gitignore", "cmd/super-dolphin-gate/main.go"} {
		if !containsRootBinaryIgnorePath(paths, want) {
			t.Fatalf("exact candidate paths = %v, missing %q", paths, want)
		}
	}
	if containsRootBinaryIgnorePath(paths, "super-dolphin-gate") {
		t.Fatalf("exact candidate paths must not include root gate binary: %v", paths)
	}
}

func writeRootBinaryIgnoreFixture(t *testing.T, root, path, content string) {
	t.Helper()
	fullPath := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("create fixture parent for %s: %v", path, err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture %s: %v", path, err)
	}
}

func runRootBinaryIgnoreGit(t *testing.T, root string, env []string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	if env != nil {
		command.Env = env
	}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func containsRootBinaryIgnorePath(paths []string, want string) bool {
	return slices.Contains(paths, want)
}
