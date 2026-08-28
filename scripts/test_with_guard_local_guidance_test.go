package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type localGuidanceFixture struct {
	repository    string
	candidatePath string
	nestedPath    string
	tree          string
}

func TestHostTestLocalGateGuidanceRequiresCleanExactCandidate(t *testing.T) {
	fixture := newLocalGuidanceFixture(t)
	writeLocalGuidanceFile(t, fixture.candidatePath, "staged candidate\nunstaged drift\n")
	assertDirtyLocalGuidanceRejected(t, fixture, "unstaged tracked worktree")
	localGuidanceGit(t, fixture.repository, "checkout", "--", "candidate.txt")

	untrackedPath := filepath.Join(fixture.repository, "untracked_candidate.go")
	writeLocalGuidanceFile(t, untrackedPath, "package candidate\n")
	assertDirtyLocalGuidanceRejected(t, fixture, "untracked source worktree")
	removeLocalGuidanceFile(t, untrackedPath)

	writeLocalGuidanceFile(t, fixture.nestedPath, "nested drift\n")
	assertDirtyLocalGuidanceRejected(t, fixture, "submodule worktree drift")
	localGuidanceGit(t, filepath.Dir(fixture.nestedPath), "checkout", "--", "nested.txt")

	staged := requireHostTestLocalGuidance(t, fixture.repository, "staged")
	assertLocalGateGuidance(t, "staged", staged)
	localGuidanceGit(t, fixture.repository, "commit", "-qm", "candidate")
	clean := requireHostTestLocalGuidance(t, fixture.repository, "clean")
	assertLocalGateGuidance(t, "clean", clean)
}

func newLocalGuidanceFixture(t *testing.T) localGuidanceFixture {
	t.Helper()
	repository := t.TempDir()
	initializeLocalGuidanceRepository(t, repository)
	submoduleSource := t.TempDir()
	initializeLocalGuidanceRepository(t, submoduleSource)
	writeLocalGuidanceFile(t, filepath.Join(submoduleSource, "nested.txt"), "nested base\n")
	localGuidanceGit(t, submoduleSource, "add", "nested.txt")
	localGuidanceGit(t, submoduleSource, "commit", "-qm", "nested base")
	localGuidanceGit(t, repository, "-c", "protocol.file.allow=always", "submodule", "add", "-q", submoduleSource, "nested")
	candidatePath := filepath.Join(repository, "candidate.txt")
	writeLocalGuidanceFile(t, candidatePath, "base\n")
	localGuidanceGit(t, repository, "add", "candidate.txt")
	localGuidanceGit(t, repository, "commit", "-qm", "base")
	writeLocalGuidanceFile(t, candidatePath, "staged candidate\n")
	localGuidanceGit(t, repository, "add", "candidate.txt")
	return localGuidanceFixture{
		repository: repository, candidatePath: candidatePath,
		nestedPath: filepath.Join(repository, "nested", "nested.txt"),
		tree:       localGuidanceGitOutput(t, repository, "write-tree"),
	}
}

func initializeLocalGuidanceRepository(t *testing.T, repository string) {
	t.Helper()
	localGuidanceGit(t, repository, "init", "-q")
	localGuidanceGit(t, repository, "config", "user.name", "Local Guidance Test")
	localGuidanceGit(t, repository, "config", "user.email", "local-guidance@example.invalid")
}

func assertDirtyLocalGuidanceRejected(t *testing.T, fixture localGuidanceFixture, name string) {
	t.Helper()
	output, err := runHostTestLocalGuidance(t, fixture.repository)
	if err == nil {
		t.Fatalf("%s local guidance was admitted: %s", name, output)
	}
	assertLocalGuidanceContains(t, name, output, "stage or clean the candidate")
	assertLocalGuidanceOmits(t, name, output,
		"remote CI exact test:", "trusted_gate_launcher", "--tree ", "--parent ", "--commit ", fixture.tree)
}

func assertLocalGateGuidance(t *testing.T, name, output string) {
	t.Helper()
	assertLocalGuidanceContains(t, name, output,
		"[test-with-guard] local exact test: ./scripts -run TestBoundary -count 1",
		"trusted local Gate project-map and closure checks remain required")
	assertLocalGuidanceOmits(t, name, output,
		"remote CI exact test:", "trusted_gate_launcher", "--tree ", "--parent ", "--commit ",
		"--agent-token", "SUPER_DOLPHIN_CI_AGENT_TOKEN")
}

func assertLocalGuidanceContains(t *testing.T, name, output string, fragments ...string) {
	t.Helper()
	for _, fragment := range fragments {
		if !strings.Contains(output, fragment) {
			t.Fatalf("%s local guidance omitted %q: %s", name, fragment, output)
		}
	}
}

func assertLocalGuidanceOmits(t *testing.T, name, output string, fragments ...string) {
	t.Helper()
	for _, fragment := range fragments {
		if strings.Contains(output, fragment) {
			t.Fatalf("%s local guidance contains forbidden %q: %s", name, fragment, output)
		}
	}
}

func requireHostTestLocalGuidance(t *testing.T, repository, name string) string {
	t.Helper()
	output, err := runHostTestLocalGuidance(t, repository)
	if err != nil {
		t.Fatalf("%s local guidance failed: %v\n%s", name, err, output)
	}
	return output
}

func runHostTestLocalGuidance(t *testing.T, repository string) (string, error) {
	t.Helper()
	repositoryRoot := gitRevParseRequired(t, "--show-toplevel")
	scriptPath := filepath.Join(repositoryRoot, "scripts", "test_with_guard.sh")
	command := exec.Command("bash", "-c", `
set -euo pipefail
source "$1"
cd "$2"
host_test_local_gate_guidance ./scripts -run '^TestBoundary$'
`, "bash", bashAbsolutePath(scriptPath), bashAbsolutePath(repository))
	output, err := command.CombinedOutput()
	return string(output), err
}

func localGuidanceGit(t *testing.T, repository string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repository}, arguments...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
}

func localGuidanceGitOutput(t *testing.T, repository string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repository}, arguments...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func writeLocalGuidanceFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func removeLocalGuidanceFile(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}
