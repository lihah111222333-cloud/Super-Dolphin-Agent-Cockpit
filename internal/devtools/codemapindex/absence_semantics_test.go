package codemapindex

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateCodemapAbsenceIgnoresOnlyGitIgnoredBuildOutput(t *testing.T) {
	root := t.TempDir()
	command := exec.Command("git", "init", "--quiet", root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("initialize Git fixture: %v: %s", err, output)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("dist/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "dist"), 0o700); err != nil {
		t.Fatal(err)
	}
	if problem := validateCodemapAbsence(root, "map.md", 1, "dist"); problem != "" {
		t.Fatalf("ignored build output violated absence: %s", problem)
	}
	if err := os.MkdirAll(filepath.Join(root, "source"), 0o700); err != nil {
		t.Fatal(err)
	}
	problem := validateCodemapAbsence(root, "map.md", 2, "source")
	if !strings.Contains(problem, "codemap absence violated") {
		t.Fatalf("tracked candidate path problem = %q", problem)
	}
}
