package difftracker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestIsSkippedBinaryExtensionRecognizesOnlyBinaryExtensions(t *testing.T) {
	for _, extension := range []string{
		".7z", ".a", ".avi", ".bmp", ".class", ".dll", ".dylib", ".exe", ".gif", ".gz",
		".ico", ".jar", ".jpeg", ".jpg", ".mov", ".mp3", ".mp4", ".pdf", ".png", ".so",
		".tar", ".tgz", ".wasm", ".webm", ".webp", ".zip",
	} {
		if !isSkippedBinaryExtension(extension) {
			t.Fatalf("isSkippedBinaryExtension(%q) = false, want true", extension)
		}
	}
	if !isSkippedBinaryExtension(".PNG") {
		t.Fatal("isSkippedBinaryExtension(.PNG) = false, want case-insensitive true")
	}
	for _, extension := range []string{".go", ".json", ".txt", ""} {
		if isSkippedBinaryExtension(extension) {
			t.Fatalf("isSkippedBinaryExtension(%q) = true, want false", extension)
		}
	}
}

func TestFindGitRoot_InGitRepo(t *testing.T) {
	repo := initGitRepo(t)
	nested := filepath.Join(repo, "sub", "dir")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	root, err := findGitRoot(context.Background(), nested)
	if err != nil {
		t.Fatalf("FindGitRoot() error = %v", err)
	}
	if resolveSymlinks(t, root) != resolveSymlinks(t, repo) {
		t.Fatalf("root = %q, want %q", root, repo)
	}
}

func TestFindGitRoot_NotGitRepo(t *testing.T) {
	if _, err := findGitRoot(context.Background(), t.TempDir()); err == nil {
		t.Fatal("FindGitRoot() error = nil, want non-nil")
	}
}

func TestListDirtyFiles(t *testing.T) {
	repo := initGitRepo(t)
	writeFile(t, filepath.Join(repo, "tracked.txt"), "v1\n")
	runGitCommand(t, repo, "add", "tracked.txt")
	runGitCommand(t, repo, "commit", "-m", "init")

	writeFile(t, filepath.Join(repo, "tracked.txt"), "v2\n")
	writeFile(t, filepath.Join(repo, "new.txt"), "new\n")

	got, err := listDirtyFiles(context.Background(), repo)
	if err != nil {
		t.Fatalf("ListDirtyFiles() error = %v", err)
	}
	want := []string{"new.txt", "tracked.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dirty files = %#v, want %#v", got, want)
	}
}

func initGitRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGitCommand(t, repo, "init")
	runGitCommand(t, repo, "config", "user.email", "test@example.com")
	runGitCommand(t, repo, "config", "user.name", "Diff Tracker")
	return repo
}

func runGitCommand(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, output)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}

func resolveSymlinks(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q) error = %v", path, err)
	}
	return resolved
}
