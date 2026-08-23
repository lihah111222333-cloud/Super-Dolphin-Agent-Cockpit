package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type remoteGuidanceFixture struct {
	repository    string
	candidatePath string
	nestedPath    string
	parent        string
	tree          string
}

func TestHostTestRemoteGuidanceBindsExactGitCandidate(t *testing.T) {
	fixture := newRemoteGuidanceFixture(t)
	writeRemoteGuidanceFile(t, fixture.candidatePath, "staged candidate\nunstaged drift\n")
	assertDirtyRemoteGuidanceRejected(t, fixture, "unstaged tracked worktree")
	remoteGuidanceGit(t, fixture.repository, "checkout", "--", "candidate.txt")

	untrackedPath := filepath.Join(fixture.repository, "untracked_candidate.go")
	writeRemoteGuidanceFile(t, untrackedPath, "package candidate\n")
	assertDirtyRemoteGuidanceRejected(t, fixture, "untracked source worktree")
	removeRemoteGuidanceFile(t, untrackedPath)

	writeRemoteGuidanceFile(t, fixture.nestedPath, "nested drift\n")
	assertDirtyRemoteGuidanceRejected(t, fixture, "submodule worktree drift")
	remoteGuidanceGit(t, filepath.Dir(fixture.nestedPath), "checkout", "--", "nested.txt")

	staged := requireHostTestRemoteGuidance(t, fixture.repository, "staged")
	assertStagedRemoteGuidance(t, fixture, staged)
	assertRemoteGuidanceShellSyntax(t, "staged", staged)
	remoteGuidanceGit(t, fixture.repository, "commit", "-qm", "candidate")
	commit := remoteGuidanceGitOutput(t, fixture.repository, "rev-parse", "HEAD")
	clean := requireHostTestRemoteGuidance(t, fixture.repository, "clean")
	assertCleanRemoteGuidance(t, fixture.tree, commit, clean)
	assertRemoteGuidanceShellSyntax(t, "clean", clean)
}

func newRemoteGuidanceFixture(t *testing.T) remoteGuidanceFixture {
	t.Helper()
	repository := t.TempDir()
	initializeRemoteGuidanceRepository(t, repository)
	submoduleSource := t.TempDir()
	initializeRemoteGuidanceRepository(t, submoduleSource)
	writeRemoteGuidanceFile(t, filepath.Join(submoduleSource, "nested.txt"), "nested base\n")
	remoteGuidanceGit(t, submoduleSource, "add", "nested.txt")
	remoteGuidanceGit(t, submoduleSource, "commit", "-qm", "nested base")
	remoteGuidanceGit(t, repository, "-c", "protocol.file.allow=always", "submodule", "add", "-q", submoduleSource, "nested")
	candidatePath := filepath.Join(repository, "candidate.txt")
	writeRemoteGuidanceFile(t, candidatePath, "base\n")
	remoteGuidanceGit(t, repository, "add", "candidate.txt")
	remoteGuidanceGit(t, repository, "commit", "-qm", "base")
	writeRemoteGuidanceFile(t, candidatePath, "staged candidate\n")
	remoteGuidanceGit(t, repository, "add", "candidate.txt")
	return remoteGuidanceFixture{
		repository: repository, candidatePath: candidatePath,
		nestedPath: filepath.Join(repository, "nested", "nested.txt"),
		parent:     remoteGuidanceGitOutput(t, repository, "rev-parse", "HEAD"),
		tree:       remoteGuidanceGitOutput(t, repository, "write-tree"),
	}
}

func initializeRemoteGuidanceRepository(t *testing.T, repository string) {
	t.Helper()
	remoteGuidanceGit(t, repository, "init", "-q")
	remoteGuidanceGit(t, repository, "config", "user.name", "Remote Guidance Test")
	remoteGuidanceGit(t, repository, "config", "user.email", "remote-guidance@example.invalid")
}

func assertDirtyRemoteGuidanceRejected(t *testing.T, fixture remoteGuidanceFixture, name string) {
	t.Helper()
	output, err := runHostTestRemoteGuidance(t, fixture.repository)
	if err == nil {
		t.Fatalf("%s remote guidance was admitted: %s", name, output)
	}
	assertRemoteGuidanceContains(t, name, output, "stage or clean the candidate")
	assertRemoteGuidanceOmits(t, name, output,
		"remote CI exact test:", "trusted_gate_launcher", "--tree ", "--parent ", "--commit ", fixture.tree)
}

func assertStagedRemoteGuidance(t *testing.T, fixture remoteGuidanceFixture, output string) {
	t.Helper()
	assertRemoteGuidanceContains(t, "staged", output,
		`trusted_gate_launcher_for_tree "$(git rev-parse --show-toplevel)" `+fixture.tree,
		"--tree "+fixture.tree, "--parent "+fixture.parent)
	assertRemoteGuidanceOmits(t, "staged", output, "--commit ", "--agent-token", "SUPER_DOLPHIN_CI_AGENT_TOKEN")
}

func assertCleanRemoteGuidance(t *testing.T, tree, commit, output string) {
	t.Helper()
	assertRemoteGuidanceContains(t, "clean", output,
		`trusted_gate_launcher_for_tree "$(git rev-parse --show-toplevel)" `+tree, "--commit "+commit)
	assertRemoteGuidanceOmits(t, "clean", output, "--tree ", "--parent ", "--agent-token", "SUPER_DOLPHIN_CI_AGENT_TOKEN")
}

func assertRemoteGuidanceContains(t *testing.T, name, output string, fragments ...string) {
	t.Helper()
	for _, fragment := range fragments {
		if !strings.Contains(output, fragment) {
			t.Fatalf("%s remote guidance omitted %q: %s", name, fragment, output)
		}
	}
}

func assertRemoteGuidanceOmits(t *testing.T, name, output string, fragments ...string) {
	t.Helper()
	for _, fragment := range fragments {
		if strings.Contains(output, fragment) {
			t.Fatalf("%s remote guidance contains forbidden %q: %s", name, fragment, output)
		}
	}
}

func assertRemoteGuidanceShellSyntax(t *testing.T, name, guidance string) {
	t.Helper()
	line := strings.SplitN(guidance, "\n", 2)[0]
	commandLine, ok := strings.CutPrefix(line, "[test-with-guard] remote CI exact test: ")
	if !ok {
		t.Fatalf("%s remote guidance omitted command prefix: %s", name, guidance)
	}
	if output, err := exec.Command("bash", "-n", "-c", commandLine).CombinedOutput(); err != nil {
		t.Fatalf("%s remote guidance is not valid shell syntax: %v\n%s\n%s", name, err, output, commandLine)
	}
}

func requireHostTestRemoteGuidance(t *testing.T, repository, name string) string {
	t.Helper()
	output, err := runHostTestRemoteGuidance(t, repository)
	if err != nil {
		t.Fatalf("%s remote guidance failed: %v\n%s", name, err, output)
	}
	return output
}

func runHostTestRemoteGuidance(t *testing.T, repository string) (string, error) {
	t.Helper()
	repositoryRoot := gitRevParseRequired(t, "--show-toplevel")
	scriptPath := filepath.Join(repositoryRoot, "scripts", "test_with_guard.sh")
	command := exec.Command("bash", "-c", `
set -euo pipefail
source "$1"
cd "$2"
host_test_remote_guidance ./scripts -run '^TestBoundary$'
`, "bash", bashAbsolutePath(scriptPath), bashAbsolutePath(repository))
	output, err := command.CombinedOutput()
	return string(output), err
}

func remoteGuidanceGit(t *testing.T, repository string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repository}, arguments...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
}

func remoteGuidanceGitOutput(t *testing.T, repository string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repository}, arguments...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func writeRemoteGuidanceFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func removeRemoteGuidanceFile(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}
