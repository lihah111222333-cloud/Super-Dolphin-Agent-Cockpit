package gatehook

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func newTestRepository(t *testing.T) string {
	t.Helper()
	return newTestRepositoryWithObjectFormat(t, "sha1")
}

func newTestRepositoryWithObjectFormat(t *testing.T, objectFormat string) string {
	t.Helper()
	repository := filepath.Join(t.TempDir(), "repo")
	if err := os.Mkdir(repository, 0o755); err != nil {
		t.Fatalf("mkdir test repository: %v", err)
	}
	runTestGit(t, repository, "init", "--object-format="+objectFormat, "-b", "main")
	runTestGit(t, repository, "config", "user.name", "Gate Hook Test")
	runTestGit(t, repository, "config", "user.email", "gatehook@example.invalid")
	writeTestFile(t, repository, "tracked.txt", "initial\n")
	runTestGit(t, repository, "add", "tracked.txt")
	runTestGit(t, repository, "commit", "-m", "初始提交")
	return repository
}

func commitTestFile(t *testing.T, repository, contents, message string) string {
	t.Helper()
	writeTestFile(t, repository, "tracked.txt", contents)
	runTestGit(t, repository, "add", "tracked.txt")
	runTestGit(t, repository, "commit", "-m", message)
	return strings.TrimSpace(runTestGit(t, repository, "rev-parse", "HEAD"))
}

func writeTestFile(t *testing.T, root, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
}

func runTestGit(t *testing.T, cwd string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", cwd}, arguments...)...)
	command.Env = sanitizedGitEnvironment(nil)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
	return string(output)
}

func loadFixture(t *testing.T, path string) string {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join("testdata", filepath.FromSlash(path)))
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return string(payload)
}
