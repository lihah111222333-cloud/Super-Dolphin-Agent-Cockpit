package main

import (
	"strings"
	"testing"
)

func TestCoordinatorOwnerCommandSanitizesInheritedGitRepositoryEnvironment(t *testing.T) {
	t.Setenv("GIT_DIR", "/untrusted/git-dir")
	t.Setenv("GIT_WORK_TREE", "/untrusted/work-tree")
	t.Setenv("GIT_COMMON_DIR", "/untrusted/common-dir")
	t.Setenv("GIT_INDEX_FILE", "/untrusted/alternate-index")
	t.Setenv("GIT_OBJECT_DIRECTORY", "/untrusted/objects")
	t.Setenv("PATH", "/untrusted/bin")

	command := newCoordinatorOwnerCommand("coordinator-owner")
	for _, name := range []string{
		"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY",
	} {
		if environmentContains(command.Env, name) {
			t.Fatalf("owner command inherited %s: %q", name, command.Env)
		}
	}
	if environmentContains(command.Env, "PATH") && !strings.Contains(strings.Join(command.Env, "\n"), "PATH="+coordinatorOwnerToolchainPath) {
		t.Fatalf("owner command did not replace caller PATH: %q", command.Env)
	}
	if strings.Contains(strings.Join(command.Env, "\n"), "/untrusted/bin") {
		t.Fatalf("owner command inherited caller PATH: %q", command.Env)
	}
}

func environmentContains(environment []string, name string) bool {
	for _, entry := range environment {
		key, _, found := strings.Cut(entry, "=")
		if found && key == name {
			return true
		}
	}
	return false
}
