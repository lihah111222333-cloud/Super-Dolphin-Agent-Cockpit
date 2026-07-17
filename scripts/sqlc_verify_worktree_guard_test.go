package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSQLCVerifyWorktreeMakefileContracts(t *testing.T) {
	makefile := readRepoFile(t, "../Makefile")

	assertScriptContains(t, makefile, "sqlc-generate sqlc-verify sqlc-verify-worktree")
	assertScriptContains(t, makefile, "\nsqlc-verify-worktree:\n\tbash scripts/sqlc_verify_worktree.sh\n")
	assertScriptContains(t, makefile, "git status --porcelain --untracked-files=all -- internal/store/sqlc cmd/mcp-orch/store/sqlc")
}

func TestSQLCVerifyWorktreeScriptUsesSnapshotsNotHeadGate(t *testing.T) {
	script := readScript(t, "sqlc_verify_worktree.sh")

	for _, want := range []string{
		"generated_dirs=(",
		"internal/store/sqlc",
		"cmd/mcp-orch/store/sqlc",
		"tmpdir=\"$(mktemp -d",
		"snapshot_generated_dirs()",
		"cp -a \"$dir\" \"$before/$dir\"",
		"make sqlc-generate",
		"compare_generated_dirs()",
		"diff -ruN \"$before/$dir\" \"$dir\"",
		"git status --porcelain --untracked-files=all -- \"${generated_dirs[@]}\"",
	} {
		assertScriptContains(t, script, want)
	}
	assertScriptContains(t, script, "\nsnapshot_generated_dirs\nmake sqlc-generate\ncompare_generated_dirs\n")
	assertScriptDoesNotContain(t, script, "if [ -n \"$(git status")
	assertScriptDoesNotContain(t, script, "if [[ -n \"$(git status")
}

func TestSQLCVerifyWorktreeScriptAllowsDirtyGeneratedHeadWhenStable(t *testing.T) {
	root := newSQLCWorktreeVerifyFixture(t, "stable")
	writeSQLCFixtureFile(t, root, "internal/store/sqlc/query.go", "package sqlc\n\n// dirty but stable\n")

	output, err := runSQLCVerifyWorktreeScript(t, root)
	if err != nil {
		t.Fatalf("sqlc worktree verifier rejected dirty-but-stable generated files: %v\n%s", err, output)
	}
	if !strings.Contains(output, "generated output is stable before/after regeneration") {
		t.Fatalf("sqlc worktree verifier success output missing stability message:\n%s", output)
	}
}

func TestSQLCVerifyWorktreeScriptRejectsRegenerationDrift(t *testing.T) {
	root := newSQLCWorktreeVerifyFixture(t, "drift")

	output, err := runSQLCVerifyWorktreeScript(t, root)
	if err == nil {
		t.Fatalf("sqlc worktree verifier accepted regeneration drift:\n%s", output)
	}
	assertOutputContainsAll(t, output,
		"generated SQLC directory changed after make sqlc-generate",
		"sqlc-verify-worktree requires generated output to be stable",
		"Diagnostic git status for generated directories",
	)
}

func TestSQLCVerificationModesStaySeparatedByWorkflow(t *testing.T) {
	aiMaintenance := readRepoFile(t, "ai_maintenance/main.go") + readRepoFile(t, "ai_maintenance/gate_execution.go")
	assertScriptContainsIgnoringWhitespace(t, aiMaintenance, `"sqlc:verify": cacheable(func() error { return runCommand("", "make", "sqlc-verify-worktree") })`)
	assertScriptContainsIgnoringWhitespace(t, aiMaintenance, `"sqlc:verify": {"make sqlc-verify-worktree", "make sqlc-verify"}`)

	prePush := readRepoFile(t, "../.githooks/pre-push")
	assertScriptContains(t, prePush, `exec "$gate_bin" hook pre-push "$1" "$2"`)
	assertScriptDoesNotContain(t, prePush, "run_ai_maintenance_push_gate")
	assertScriptDoesNotContain(t, prePush, "make sqlc-verify")
	assertScriptDoesNotContain(t, prePush, "make sqlc-verify-worktree")
}

func assertScriptContainsIgnoringWhitespace(t *testing.T, content, want string) {
	t.Helper()
	normalizedContent := strings.Join(strings.Fields(content), " ")
	normalizedWant := strings.Join(strings.Fields(want), " ")
	if !strings.Contains(normalizedContent, normalizedWant) {
		t.Fatalf("script missing %q", want)
	}
}

func newSQLCWorktreeVerifyFixture(t *testing.T, mode string) string {
	t.Helper()

	root := t.TempDir()
	writeSQLCFixtureFile(t, root, "scripts/sqlc_verify_worktree.sh", readScript(t, "sqlc_verify_worktree.sh"))
	writeSQLCFixtureFile(t, root, "internal/store/sqlc/query.go", "package sqlc\n\n// committed\n")
	writeSQLCFixtureFile(t, root, "cmd/mcp-orch/store/sqlc/query.go", "package sqlc\n\n// committed\n")
	writeSQLCFixtureFile(t, root, "Makefile", sqlcFixtureMakefile(t, mode))

	runSQLCFixtureCommand(t, root, "git", "init")
	runSQLCFixtureCommand(t, root, "git", "config", "user.email", "test@example.invalid")
	runSQLCFixtureCommand(t, root, "git", "config", "user.name", "SQLC Worktree Test")
	runSQLCFixtureCommand(t, root, "git", "add", ".")
	runSQLCFixtureCommand(t, root, "git", "commit", "-m", "chore: fixture")

	return root
}

func sqlcFixtureMakefile(t *testing.T, mode string) string {
	t.Helper()

	switch mode {
	case "stable":
		return "sqlc-generate:\n\t@true\n"
	case "drift":
		return "sqlc-generate:\n\t@printf '%s\\n' 'package sqlc' '' '// regenerated' > internal/store/sqlc/query.go\n"
	default:
		t.Fatalf("unknown sqlc fixture mode: %s", mode)
		return ""
	}
}

func writeSQLCFixtureFile(t *testing.T, root, rel, content string) {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func runSQLCVerifyWorktreeScript(t *testing.T, root string) (string, error) {
	t.Helper()

	cmd := exec.Command("bash", "scripts/sqlc_verify_worktree.sh")
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func runSQLCFixtureCommand(t *testing.T, dir, name string, args ...string) {
	t.Helper()

	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, output)
	}
}
