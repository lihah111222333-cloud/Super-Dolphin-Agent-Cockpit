package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sqlcconfig"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/trustedgitpolicy"
)

func TestSQLCVerifyWorktreeMakefileContracts(t *testing.T) {
	makefile := readRepoFile(t, "../Makefile")

	assertScriptContains(t, makefile, "sqlc-generate sqlc-verify sqlc-verify-worktree")
	assertScriptContains(t, makefile, "\tbash scripts/sqlc_postprocess.sh\n")
	assertScriptContains(t, makefile, "\nsqlc-verify: sqlc-generate\n")
	assertScriptDoesNotContain(t, makefile, "$(MAKE) --no-print-directory sqlc-generate")
	assertScriptContains(t, makefile, "\nsqlc-verify-worktree:\n\tbash scripts/sqlc_verify_worktree.sh\n")
	assertScriptContains(t, makefile, "run ./scripts/sqlc_config_outputs --git-binary \"$(SQLC_GIT_CANONICAL)\" --preflight")
	assertScriptContains(t, makefile, "run ./scripts/sqlc_config_outputs --git-binary \"$(SQLC_GIT_CANONICAL)\" --verify")
	assertScriptContains(t, makefile, "override SQLC_VERSION := v1.30.0")
	assertScriptContains(t, makefile, "override SQLC := GOWORK=off $(SQLC_GO_CANONICAL) run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)")
	assertScriptDoesNotContain(t, makefile, "SQLC_VERSION ?=")
	assertScriptDoesNotContain(t, makefile, "ls-files -v -- \"$$output\"")
	assertScriptDoesNotContain(t, makefile, "git status --porcelain --untracked-files=all -- internal/store/sqlc cmd/mcp-orch/store/sqlc")
}

func TestSQLCPostprocessNormalizesEmptyInterfacesDeterministically(t *testing.T) {
	root := t.TempDir()
	writeSQLCFixtureFile(t, root, "scripts/sqlc_postprocess.sh", readScript(t, "sqlc_postprocess.sh"))
	writeSQLCDefaultConfigFixture(t, root)
	writeSQLCFixtureFile(t, root, "internal/store/sqlc/query.go", "package sqlc\n\ntype Row struct { Value interface{} }\n")
	writeSQLCFixtureFile(t, root, "cmd/mcp-orch/store/sqlc/query.go", "package sqlc\n\nfunc decode(v interface{}) interface{} { return v }\n")
	runSQLCFixtureCommand(t, root, "git", "init")

	first := runSQLCFixtureCommandOutput(t, root, "bash", "scripts/sqlc_postprocess.sh")
	if !strings.Contains(first, "normalized generated empty interfaces to any") {
		t.Fatalf("postprocess output missing success message:\n%s", first)
	}
	for _, rel := range []string{"internal/store/sqlc/query.go", "cmd/mcp-orch/store/sqlc/query.go"} {
		content, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if strings.Contains(string(content), "interface{}") || !strings.Contains(string(content), "any") {
			t.Fatalf("postprocessed %s = %q, want any without interface{}", rel, content)
		}
	}
	before := readFixtureFiles(t, root)
	runSQLCFixtureCommandOutput(t, root, "bash", "scripts/sqlc_postprocess.sh")
	after := readFixtureFiles(t, root)
	if before != after {
		t.Fatalf("second postprocess pass changed generated output")
	}
}

func TestSQLCPostprocessRunsAuthoritativePreflightBeforeMutation(t *testing.T) {
	root := t.TempDir()
	writeSQLCFixtureFile(t, root, "scripts/sqlc_postprocess.sh", readScript(t, "sqlc_postprocess.sh"))
	writeSQLCDefaultConfigFixture(t, root)
	sentinel := filepath.Join(root, "internal/store/sqlc/query.go")
	writeSQLCFixtureFile(t, root, "internal/store/sqlc/query.go", "package sqlc\n\ntype Row struct { Value interface{} }\n")
	writeSQLCFixtureFile(t, root, "cmd/mcp-orch/store/sqlc/query.go", "package sqlc\n")
	outside := filepath.Join(t.TempDir(), "outside.sql")
	writeSQLCFixtureFile(t, filepath.Dir(outside), filepath.Base(outside), "outside\n")
	if err := os.Symlink(outside, filepath.Join(root, "sql/queries/nested.sql")); err != nil {
		t.Fatal(err)
	}
	runSQLCFixtureCommand(t, root, "git", "init")
	before, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatal(err)
	}
	output, err := runSQLCPostprocessWithPath(t, root, filepath.Join(root, "missing-bin"))
	if err == nil || !strings.Contains(output, "symbolic link") {
		t.Fatalf("postprocess preflight = %v output=%q, want nested symlink rejection", err, output)
	}
	after, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("postprocess mutated sentinel before preflight failure: before=%q after=%q", before, after)
	}
}

func TestSQLCVerifyWorktreeScriptUsesSnapshotsNotHeadGate(t *testing.T) {
	script := readScript(t, "sqlc_verify_worktree.sh")

	for _, want := range []string{
		"GOWORK=off \"$go_binary\" run ./scripts/sqlc_config_outputs",
		"GOWORK=off \"$go_binary\" run ./scripts/sqlc_config_outputs --git-binary \"$git_binary\" --preflight >/dev/null",
		"generated_dirs=()",
		"tmpdir=\"$(mktemp -d",
		"snapshot_generated_dirs()",
		"cp -a \"$dir\" \"$before/$dir\"",
		"\"$make_binary\" sqlc-generate-worktree",
		"compare_generated_dirs()",
		"diff -ruN -- \"$before/$dir\" \"$dir\"",
		"trusted_git -C \"$root\" status --porcelain --untracked-files=all -- \"${generated_dirs[@]}\"",
		"GOWORK=off \"$go_binary\" run ./scripts/sqlc_config_outputs --git-binary \"$git_binary\" --verify-shape",
	} {
		assertScriptContains(t, script, want)
	}
	postprocess := readScript(t, "sqlc_postprocess.sh")
	trustedGitCommand := "env -i " + strings.Join(trustedgitpolicy.Environment(), " ") + " \"$git_binary\" --no-replace-objects \"$@\""
	for name, content := range map[string]string{"sqlc_verify_worktree.sh": script, "sqlc_postprocess.sh": postprocess} {
		t.Run(name+" closed Git environment", func(t *testing.T) {
			assertScriptContains(t, content, trustedGitCommand)
		})
	}
	for _, want := range []string{"file_list=", "\"$find_binary\" \"$dir\" -type f -name '*.go' -print0", "rg_status=$?", "rg failed while checking SQLC generated output"} {
		assertScriptContains(t, postprocess, want)
	}
	assertScriptContains(t, postprocess, "GOWORK=off \"$go_binary\" run ./scripts/sqlc_config_outputs --git-binary \"$git_binary\" --preflight >/dev/null")
	assertScriptContains(t, script, "\nsnapshot_generated_dirs\nMAKEFLAGS= MAKEOVERRIDES= \"$make_binary\" sqlc-generate-worktree\nGOWORK=off \"$go_binary\" run ./scripts/sqlc_config_outputs --git-binary \"$git_binary\" --verify-shape\ncompare_generated_dirs\n")
	assertScriptContains(t, script, "go_binary=\"$(resolve_external_tool go)\"\nmake_binary=\"$(resolve_external_tool make)\"")
	assertScriptDoesNotContain(t, script, "if [ -n \"$(git status")
	assertScriptDoesNotContain(t, script, "if [[ -n \"$(git status")
}

func TestSQLCPostprocessPropagatesFindFailure(t *testing.T) {
	root := newSQLCPostprocessFailureFixture(t)
	fakeBin := t.TempDir()
	writeSQLCFixtureFile(t, fakeBin, "find", "#!/bin/sh\nexit 23\n")
	if err := os.Chmod(filepath.Join(fakeBin, "find"), 0o700); err != nil {
		t.Fatal(err)
	}
	output, err := runSQLCPostprocessWithPath(t, root, fakeBin)
	if err == nil || !strings.Contains(output, "find failed") {
		t.Fatalf("postprocess find failure = %v output=%q", err, output)
	}
}

func TestSQLCPostprocessPropagatesRGFailure(t *testing.T) {
	root := newSQLCPostprocessFailureFixture(t)
	fakeBin := t.TempDir()
	writeSQLCFixtureFile(t, fakeBin, "rg", "#!/bin/sh\nexit 23\n")
	if err := os.Chmod(filepath.Join(fakeBin, "rg"), 0o700); err != nil {
		t.Fatal(err)
	}
	output, err := runSQLCPostprocessWithPath(t, root, fakeBin)
	if err == nil || !strings.Contains(output, "rg failed") {
		t.Fatalf("postprocess rg failure = %v output=%q", err, output)
	}
}

func TestSQLCPostprocessPreservesRegisteredManualExtensionExactly(t *testing.T) {
	root := newSQLCPostprocessFailureFixture(t)
	manual := filepath.Join(root, "cmd/mcp-orch/store/sqlc/types_ext.go")
	before := "// SQLC manual extension. DO NOT OVERWRITE.\npackage sqlc\n\nvar Manual interface{}\n"
	writeSQLCFixtureFile(t, root, "cmd/mcp-orch/store/sqlc/types_ext.go", before)
	output, err := runSQLCPostprocessWithPath(t, root, t.TempDir())
	if err != nil {
		t.Fatalf("postprocess manual extension: %v\n%s", err, output)
	}
	after, err := os.ReadFile(manual)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != before {
		t.Fatalf("registered manual extension changed:\n%s", after)
	}
}

func TestSQLCVerifyWorktreeScriptAcceptsDirtyStableGeneratedHead(t *testing.T) {
	root := newSQLCWorktreeVerifyFixture(t, "stable")
	writeSQLCFixtureFile(t, root, "internal/store/sqlc/query.go", "// Code generated by sqlc. DO NOT EDIT.\npackage sqlc\n\n// dirty but stable\n")

	output, err := runSQLCVerifyWorktreeScript(t, root)
	if err != nil || !strings.Contains(output, "generated output is stable") {
		t.Fatalf("sqlc worktree verifier rejected dirty stable generated file: %v\n%s", err, output)
	}
}

func TestSQLCVerifyWorktreeScriptRejectsRegenerationDrift(t *testing.T) {
	root := newSQLCWorktreeVerifyFixture(t, "drift")

	output, err := runSQLCVerifyWorktreeScript(t, root)
	if err == nil {
		t.Fatalf("sqlc worktree verifier accepted regeneration drift:\n%s", output)
	}
	assertOutputContainsAll(t, output, "generated SQLC directory changed", "requires generated output to be stable")
}

func TestSQLCVerifyWorktreeScriptRejectsIgnoredStableGeneratedFile(t *testing.T) {
	root := newSQLCWorktreeVerifyFixture(t, "stable")
	writeSQLCFixtureFile(t, root, ".gitignore", "query.go\n")
	output, err := runSQLCVerifyWorktreeScript(t, root)
	if err == nil || !strings.Contains(output, "Git-ignored") {
		t.Fatalf("sqlc worktree verifier accepted ignored stable generated file: %v\n%s", err, output)
	}
}

func TestSQLCVerifyWorktreeScriptRealMakeAcceptsDirtyStableCandidate(t *testing.T) {
	root, realGit, goBin := prepareSQLCVerifyMakeFixture(t)
	writeSQLCFixtureFile(t, root, "scripts/sqlc_verify_worktree.sh", readScript(t, "sqlc_verify_worktree.sh"))
	runPinnedSQLCGeneration(t, root, goBin)
	runSQLCFixtureCommandOutput(t, root, "bash", "scripts/sqlc_postprocess.sh")
	runSQLCFixtureCommand(t, root, realGit, "add", ".")
	runSQLCFixtureCommand(t, root, realGit, "commit", "-m", "fixture")

	query := "-- name: GetFixture :one\nSELECT id, name FROM fixture WHERE id = ? LIMIT 1;\n\n-- name: ListFixtures :many\nSELECT id, name FROM fixture ORDER BY id;\n"
	writeSQLCFixtureFile(t, root, "sql/queries/query.sql", query)
	runPinnedSQLCGeneration(t, root, goBin)
	runSQLCFixtureCommandOutput(t, root, "bash", "scripts/sqlc_postprocess.sh")
	output, err := runSQLCVerifyWorktreeScript(t, root)
	if err != nil || !strings.Contains(output, "generated output is stable") {
		t.Fatalf("real Makefile chain rejected dirty stable candidate: %v\n%s", err, output)
	}
}

func TestSQLCGenerateAllowsDirtyRegenerationWhileVerifyStaysStrict(t *testing.T) {
	root, realGit, goBin := prepareSQLCVerifyMakeFixture(t)
	runPinnedSQLCGeneration(t, root, goBin)
	runSQLCFixtureCommandOutput(t, root, "bash", "scripts/sqlc_postprocess.sh")
	runSQLCFixtureCommand(t, root, realGit, "add", ".")
	runSQLCFixtureCommand(t, root, realGit, "commit", "-m", "fixture")

	query := "-- name: GetFixture :one\nSELECT id, name FROM fixture WHERE id = ? LIMIT 1;\n\n-- name: ListFixtures :many\nSELECT id, name FROM fixture ORDER BY id;\n"
	writeSQLCFixtureFile(t, root, "sql/queries/query.sql", query)
	makeBin, err := exec.LookPath("make")
	if err != nil {
		t.Fatal("make is required")
	}
	generate := exec.Command(makeBin, "sqlc-generate")
	generate.Dir = root
	if output, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("make sqlc-generate rejected legitimate dirty regeneration: %v\n%s", err, output)
	}
	status := runSQLCFixtureCommandOutput(t, root, realGit, "status", "--porcelain=v1", "--", "internal/store/sqlc")
	if strings.TrimSpace(status) == "" {
		t.Fatal("make sqlc-generate did not leave regenerated SQLC output dirty against HEAD")
	}
	verify := exec.Command(makeBin, "sqlc-verify")
	verify.Dir = root
	if output, err := verify.CombinedOutput(); err == nil || !strings.Contains(string(output), "differs between") {
		t.Fatalf("make sqlc-verify accepted dirty generated output: %v\n%s", err, output)
	}
}

func TestSQLCVerificationModesStaySeparatedByWorkflow(t *testing.T) {
	aiMaintenance := readRepoFile(t, "ai_maintenance/main.go") +
		readRepoFile(t, "ai_maintenance/gate_execution.go")
	assertScriptContainsIgnoringWhitespace(t, aiMaintenance, `"sqlc:verify": {run: func() error { return runLocalSQLCDiagnostic(plan) }}`)
	if _, err := os.Stat(filepath.Join("ai_maintenance", "evidence.go")); !os.IsNotExist(err) {
		t.Fatalf("retired AI-maintenance evidence parser still exists: %v", err)
	}

	prePush := readRepoFile(t, "../.githooks/pre-push")
	assertScriptContains(t, prePush, `remote hook pre-push`)
	assertScriptContains(t, prePush, `--config "$remote_config"`)
	assertScriptContains(t, prePush, `--ledger "$remote_ledger"`)
	assertScriptContains(t, prePush, `--repository "$repo_root"`)
	assertScriptContains(t, prePush, `exec "$gate_bin" "${remote_args[@]}" "$1" "$2"`)
	assertScriptDoesNotContain(t, prePush, "run_ai_maintenance_push_gate")
	assertScriptDoesNotContain(t, prePush, "make sqlc-verify")
	assertScriptDoesNotContain(t, prePush, "make sqlc-verify-worktree")
}

func TestSQLCVerifyMakeUsesPinnedGenerationAndCannotBeSkippedByMAKEOverride(t *testing.T) {
	root, realGit, goBin := prepareSQLCVerifyMakeFixture(t)
	runPinnedSQLCGeneration(t, root, goBin)
	runSQLCFixtureCommandOutput(t, root, "bash", "scripts/sqlc_postprocess.sh")
	runSQLCFixtureCommand(t, root, realGit, "add", ".")
	runSQLCFixtureCommand(t, root, realGit, "commit", "-m", "fixture")
	generated := "internal/store/sqlc/query.sql.go"
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(generated))); err != nil {
		t.Fatal(err)
	}
	fakeBin, logPath := installLoggingGitFixture(t, realGit)
	makeBin, err := exec.LookPath("make")
	if err != nil {
		t.Fatal("make is required")
	}
	runPinnedMakeVerify(t, root, fakeBin, makeBin)
	assertGeneratedFixtureExists(t, root, generated)
	assertLoggedSQLCOutputArguments(t, logPath)
	assertMakeRejectsIndexFlags(t, root, fakeBin, makeBin, realGit, generated)
}

func prepareSQLCVerifyMakeFixture(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	writeSQLCParserFixture(t, root)
	writeSQLCDefaultConfigFixture(t, root)
	writeSQLCFixtureFile(t, root, "scripts/sqlc_postprocess.sh", readScript(t, "sqlc_postprocess.sh"))
	writeSQLCFixtureFile(t, root, "Makefile", readRepoFile(t, "../Makefile"))
	writeSQLCFixtureFile(t, root, "internal/platform/db/sqlite/migrations/001.sql", "CREATE TABLE fixture (id INTEGER PRIMARY KEY, name TEXT NOT NULL);\n")
	query := "-- name: GetFixture :one\nSELECT id, name FROM fixture WHERE id = ? LIMIT 1;\n"
	writeSQLCFixtureFile(t, root, "sql/queries/query.sql", query)
	writeSQLCFixtureFile(t, root, "cmd/mcp-orch/sql/queries/query.sql", query)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal("git is required")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatal("go is required")
	}
	runSQLCFixtureCommand(t, root, realGit, "init")
	runSQLCFixtureCommand(t, root, realGit, "config", "user.email", "test@example.invalid")
	runSQLCFixtureCommand(t, root, realGit, "config", "user.name", "SQLC Make Test")
	return root, realGit, goBin
}

func installLoggingGitFixture(t *testing.T, realGit string) (string, string) {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "git-args.log")
	fakeBin := t.TempDir()
	writeSQLCFixtureFile(t, fakeBin, "git", "#!/bin/sh\nprintf '%s\\n' \"$@\" >> "+shellQuoteForFixture(logPath)+"\nexec "+shellQuoteForFixture(realGit)+" \"$@\"\n")
	if err := os.Chmod(filepath.Join(fakeBin, "git"), 0o700); err != nil {
		t.Fatal(err)
	}
	return fakeBin, logPath
}

func runPinnedMakeVerify(t *testing.T, root, fakeBin, makeBin string) {
	t.Helper()
	cmd := exec.Command(makeBin, "MAKE=true", "SQLC=./untrusted-sqlc", "sqlc-verify")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PATH="+fakeBin+":"+os.Getenv("PATH"))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("make sqlc-verify failed: %v\n%s", err, output)
	}
}

func assertGeneratedFixtureExists(t *testing.T, root, relative string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(relative))); err != nil {
		t.Fatalf("MAKE=true skipped sqlc generation prerequisite: %v", err)
	}
}

func assertLoggedSQLCOutputArguments(t *testing.T, logPath string) {
	t.Helper()
	args, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	logged := string(args)
	for _, output := range []string{"internal/store/sqlc", "cmd/mcp-orch/store/sqlc"} {
		if !strings.Contains(logged, "--\n"+output+"\n") {
			t.Fatalf("git status did not receive one quoted output argument %q:\n%s", output, logged)
		}
	}
}

func assertMakeRejectsIndexFlags(t *testing.T, root, fakeBin, makeBin, realGit, generated string) {
	t.Helper()
	for _, flag := range []string{"--assume-unchanged", "--skip-worktree"} {
		runSQLCFixtureCommand(t, root, realGit, "update-index", flag, generated)
		flagged := exec.Command(makeBin, "sqlc-verify")
		flagged.Dir = root
		flagged.Env = append(os.Environ(), "PATH="+fakeBin+":"+os.Getenv("PATH"))
		output, err := flagged.CombinedOutput()
		if err == nil || !strings.Contains(string(output), "index flags") {
			t.Fatalf("make sqlc-verify accepted %s: %v\n%s", flag, err, output)
		}
		clearFlag := "--no-assume-unchanged"
		if flag == "--skip-worktree" {
			clearFlag = "--no-skip-worktree"
		}
		runSQLCFixtureCommand(t, root, realGit, "update-index", clearFlag, generated)
	}
}

func TestSQLCLocalDiagnosticsRejectCandidatePATHShims(t *testing.T) {
	t.Run("Make Go shim", func(t *testing.T) {
		root := t.TempDir()
		writeSQLCFixtureFile(t, root, "Makefile", readRepoFile(t, "../Makefile"))
		writeSQLCFixtureFile(t, root, "fake-bin/go", "#!/bin/sh\nexit 0\n")
		if err := os.Chmod(filepath.Join(root, "fake-bin/go"), 0o700); err != nil {
			t.Fatal(err)
		}
		makeBin, err := exec.LookPath("make")
		if err != nil {
			t.Fatal("make is required")
		}
		cmd := exec.Command(makeBin, "sqlc-generate")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "PATH="+filepath.Join(root, "fake-bin")+":"+os.Getenv("PATH"))
		output, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(output), "candidate-local Go executable is forbidden") {
			t.Fatalf("candidate-local Go shim error = %v\n%s", err, output)
		}
	})

	t.Run("worktree Make shim", func(t *testing.T) {
		root := t.TempDir()
		writeSQLCFixtureFile(t, root, "scripts/sqlc_verify_worktree.sh", readScript(t, "sqlc_verify_worktree.sh"))
		writeSQLCFixtureFile(t, root, "fake-bin/make", "#!/bin/sh\nexit 0\n")
		if err := os.Chmod(filepath.Join(root, "fake-bin/make"), 0o700); err != nil {
			t.Fatal(err)
		}
		runSQLCFixtureCommand(t, root, "git", "init")
		cmd := exec.Command("bash", "scripts/sqlc_verify_worktree.sh")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "PATH="+filepath.Join(root, "fake-bin")+":"+os.Getenv("PATH"))
		output, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(output), "candidate-local or non-absolute make is forbidden") {
			t.Fatalf("candidate-local Make shim error = %v\n%s", err, output)
		}
	})

	t.Run("inherited Make controls", func(t *testing.T) {
		root := t.TempDir()
		writeSQLCFixtureFile(t, root, "scripts/sqlc_verify_worktree.sh", readScript(t, "sqlc_verify_worktree.sh"))
		cmd := exec.Command("bash", "scripts/sqlc_verify_worktree.sh")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "MAKEFLAGS=-n")
		output, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(output), "MAKEFLAGS/MAKEOVERRIDES are forbidden") {
			t.Fatalf("inherited MAKEFLAGS error = %v\n%s", err, output)
		}
	})
}

func TestSQLCLocalDiagnosticsRejectSubdirectoryGitShims(t *testing.T) {
	for _, script := range []string{"sqlc_verify_worktree.sh", "sqlc_postprocess.sh"} {
		t.Run(script, func(t *testing.T) {
			root := t.TempDir()
			writeSQLCFixtureFile(t, root, "scripts/"+script, readScript(t, script))
			runSQLCFixtureCommand(t, root, "git", "init")
			realGit, err := exec.LookPath("git")
			if err != nil {
				t.Fatal("git is required")
			}
			sentinel := filepath.Join(root, "ambient-git-was-executed")
			shimDir := filepath.Join(root, "fake-bin")
			shim := "#!/bin/sh\nprintf used > " + shellQuoteForFixture(sentinel) + "\nexec " + shellQuoteForFixture(realGit) + " \"$@\"\n"
			writeSQLCFixtureFile(t, shimDir, "git", shim)
			if err := os.Chmod(filepath.Join(shimDir, "git"), 0o700); err != nil {
				t.Fatal(err)
			}
			nested := filepath.Join(root, "nested")
			if err := os.MkdirAll(nested, 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", filepath.Join(root, "scripts", script))
			cmd.Dir = nested
			cmd.Env = append(os.Environ(), "PATH="+shimDir+":"+os.Getenv("PATH"))
			_, _ = cmd.CombinedOutput()
			if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
				t.Fatalf("%s executed ambient PATH git shim from repository subdirectory: %v", script, err)
			}
		})
	}
}

func TestSQLCVerifyMakeRejectsIgnoredOutputBeforeGenerator(t *testing.T) {
	root := t.TempDir()
	writeSQLCParserFixture(t, root)
	writeSQLCDefaultConfigFixture(t, root)
	writeSQLCFixtureFile(t, root, "scripts/sqlc_postprocess.sh", readScript(t, "sqlc_postprocess.sh"))
	writeSQLCFixtureFile(t, root, "Makefile", readRepoFile(t, "../Makefile"))
	writeSQLCFixtureFile(t, root, ".gitignore", "internal/store/sqlc/\n")
	writeSQLCFixtureFile(t, root, "internal/store/sqlc/generated.go", "// Code generated by sqlc. DO NOT EDIT.\npackage sqlc\n")
	writeSQLCFixtureFile(t, root, "cmd/mcp-orch/store/sqlc/generated.go", "// Code generated by sqlc. DO NOT EDIT.\npackage sqlc\n")
	gitBinary, err := exec.LookPath("git")
	if err != nil {
		t.Fatal("git is required")
	}
	runSQLCFixtureCommand(t, root, gitBinary, "init")
	before, err := os.ReadFile(filepath.Join(root, "internal/store/sqlc/generated.go"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("make", "sqlc-verify")
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "Git-ignored") {
		t.Fatalf("make sqlc-verify ignored output = %v\n%s", err, output)
	}
	after, readErr := os.ReadFile(filepath.Join(root, "internal/store/sqlc/generated.go"))
	if readErr != nil || string(after) != string(before) {
		t.Fatalf("make generator mutated ignored output before preflight failure: error=%v before=%q after=%q", readErr, before, after)
	}
}

func TestSQLCVerifyMakeRejectsIgnoredGeneratedFileAfterGeneration(t *testing.T) {
	root := t.TempDir()
	writeSQLCParserFixture(t, root)
	writeSQLCDefaultConfigFixture(t, root)
	writeSQLCFixtureFile(t, root, "scripts/sqlc_postprocess.sh", readScript(t, "sqlc_postprocess.sh"))
	writeSQLCFixtureFile(t, root, "Makefile", readRepoFile(t, "../Makefile"))
	writeSQLCFixtureFile(t, root, ".gitignore", "*.sql.go\n")
	writeSQLCFixtureFile(t, root, "internal/store/sqlc/generated.go", "// Code generated by sqlc. DO NOT EDIT.\npackage sqlc\n")
	writeSQLCFixtureFile(t, root, "cmd/mcp-orch/store/sqlc/generated.go", "// Code generated by sqlc. DO NOT EDIT.\npackage sqlc\n")
	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Fatal("go is required")
	}
	gitBinary, err := exec.LookPath("git")
	if err != nil {
		t.Fatal("git is required")
	}
	configurePinnedSQLCProxy(t, realGo)
	writeSQLCFixtureFile(t, root, "internal/platform/db/sqlite/migrations/001.sql", "CREATE TABLE fixture (id INTEGER PRIMARY KEY, name TEXT NOT NULL);\n")
	query := "-- name: GetFixture :one\nSELECT id, name FROM fixture WHERE id = ? LIMIT 1;\n"
	writeSQLCFixtureFile(t, root, "sql/queries/query.sql", query)
	writeSQLCFixtureFile(t, root, "cmd/mcp-orch/sql/queries/query.sql", query)
	ignoredPath := filepath.Join(root, "internal/store/sqlc", "query.sql.go")
	runSQLCFixtureCommand(t, root, gitBinary, "init")
	runSQLCFixtureCommand(t, root, gitBinary, "config", "user.email", "test@example.invalid")
	runSQLCFixtureCommand(t, root, gitBinary, "config", "user.name", "SQLC Make Post-generation Test")
	runPinnedSQLCGeneration(t, root, realGo)
	runSQLCFixtureCommandOutput(t, root, "bash", "scripts/sqlc_postprocess.sh")
	if err := os.Remove(ignoredPath); err != nil {
		t.Fatal(err)
	}
	runSQLCFixtureCommand(t, root, gitBinary, "add", ".")
	runSQLCFixtureCommand(t, root, gitBinary, "commit", "-m", "fixture")
	cmd := exec.Command("make", "sqlc-verify")
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "Git-ignored") {
		t.Fatalf("make sqlc-verify post-generation ignored output = %v\n%s", err, output)
	}
	if _, statErr := os.Stat(ignoredPath); statErr != nil {
		t.Fatalf("pinned sqlc generator did not write ignored output: %v", statErr)
	}
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
	writeSQLCDefaultConfigFixture(t, root)
	writeSQLCFixtureFile(t, root, "internal/store/sqlc/query.go", "// Code generated by sqlc. DO NOT EDIT.\npackage sqlc\n\n// committed\n")
	writeSQLCFixtureFile(t, root, "cmd/mcp-orch/store/sqlc/query.go", "// Code generated by sqlc. DO NOT EDIT.\npackage sqlc\n\n// committed\n")
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
		return "sqlc-generate-worktree:\n\t@true\n"
	case "drift":
		return "sqlc-generate-worktree:\n\t@printf '%s\\n' '// Code generated by sqlc. DO NOT EDIT.' 'package sqlc' '' '// regenerated' > internal/store/sqlc/query.go\n"
	default:
		t.Fatalf("unknown sqlc fixture mode: %s", mode)
		return ""
	}
}

func writeSQLCDefaultConfigFixture(t *testing.T, root string) {
	t.Helper()
	writeSQLCParserFixture(t, root)
	writeSQLCFixtureFile(t, root, "sqlc.yaml", `version: "2"
sql:
  - engine: sqlite
    queries: sql/queries/
    schema: [internal/platform/db/sqlite/migrations/001.sql]
    strict_order_by: true
    gen:
      go:
        package: sqlc
        out: internal/store/sqlc
        emit_interface: true
        query_parameter_limit: 0
`)
	writeSQLCFixtureFile(t, root, "cmd/mcp-orch/sqlc.yaml", `version: "2"
sql:
  - engine: sqlite
    queries: sql/queries
    schema: [../../internal/platform/db/sqlite/migrations/001.sql]
    strict_order_by: true
    gen:
      go:
        package: sqlc
        out: store/sqlc
        query_parameter_limit: 0
`)
	for _, rel := range []string{
		"sql/queries/query.sql",
		"cmd/mcp-orch/sql/queries/query.sql",
		"internal/platform/db/sqlite/migrations/001.sql",
	} {
		writeSQLCFixtureFile(t, root, rel, "fixture\n")
	}
	writeSQLCManualExtensions(t, root)
}

func writeSQLCManualExtensions(t *testing.T, root string) {
	t.Helper()
	units := []sqlcconfig.Unit{
		{ConfigPath: sqlcconfig.RootConfigPath, Output: "internal/store/sqlc"},
		{ConfigPath: sqlcconfig.OrchestratorConfigPath, Output: "cmd/mcp-orch/store/sqlc"},
	}
	for _, relative := range sqlcconfig.ManualExtensionPaths(units) {
		marker := sqlcconfig.ManualExtensionMarker
		if strings.HasSuffix(relative, "_test.go") {
			marker = sqlcconfig.ManualTestExtensionMarker
		}
		writeSQLCFixtureFile(t, root, relative, marker+"\npackage sqlc\n")
	}
}

func newSQLCPostprocessFailureFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeSQLCFixtureFile(t, root, "scripts/sqlc_postprocess.sh", readScript(t, "sqlc_postprocess.sh"))
	writeSQLCDefaultConfigFixture(t, root)
	writeSQLCFixtureFile(t, root, "internal/store/sqlc/query.go", "package sqlc\n\nvar Value interface{}\n")
	writeSQLCFixtureFile(t, root, "cmd/mcp-orch/store/sqlc/query.go", "package sqlc\n")
	runSQLCFixtureCommand(t, root, "git", "init")
	return root
}

func writeSQLCParserFixture(t *testing.T, root string) {
	t.Helper()
	for _, rel := range []string{
		"go.mod",
		"go.sum",
		"internal/devtools/sqlcconfig/config.go",
		"internal/devtools/sqlcconfig/hardlink_unix.go",
		"internal/devtools/sqlcconfig/hardlink_windows.go",
		"internal/devtools/sqlcconfig/hardlink_other.go",
		"internal/devtools/sqlcconfig/git.go",
		"internal/devtools/sqlcconfig/git_process_unix.go",
		"internal/devtools/sqlcconfig/git_process_windows.go",
		"internal/devtools/sqlcconfig/manual_extensions.go",
		"internal/devtools/sqlcconfig/owner.go",
		"internal/devtools/sqlcconfig/verify_git.go",
		"internal/devtools/trustedgitpolicy/policy.go",
		"scripts/sqlc_config_outputs/main.go",
	} {
		source := filepath.Join("..", filepath.FromSlash(rel))
		raw, err := os.ReadFile(source)
		if err != nil {
			t.Fatalf("read SQLC parser fixture %s: %v", rel, err)
		}
		writeSQLCFixtureFile(t, root, rel, string(raw))
	}
}

func shellQuoteForFixture(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func writeSQLCFixtureFile(t *testing.T, root, rel, content string) {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
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

func runSQLCPostprocessWithPath(t *testing.T, root, binDir string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", "scripts/sqlc_postprocess.sh")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PATH="+binDir+":"+os.Getenv("PATH"))
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

func runSQLCFixtureCommandOutput(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, output)
	}
	return string(output)
}

func runPinnedSQLCGeneration(t *testing.T, root, goBinary string) {
	t.Helper()
	configurePinnedSQLCProxy(t, goBinary)
	commands := [][]string{
		{"run", "github.com/sqlc-dev/sqlc/cmd/sqlc@v1.30.0", "generate"},
		{"run", "github.com/sqlc-dev/sqlc/cmd/sqlc@v1.30.0", "generate", "-f", "cmd/mcp-orch/sqlc.yaml"},
	}
	environment := make([]string, 0, len(os.Environ())+1)
	for _, assignment := range os.Environ() {
		if key, _, ok := strings.Cut(assignment, "="); ok && strings.EqualFold(key, "GOWORK") {
			continue
		}
		environment = append(environment, assignment)
	}
	environment = append(environment, "GOWORK=off")
	for _, args := range commands {
		cmd := exec.Command(goBinary, args...)
		cmd.Dir = root
		cmd.Env = environment
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("pinned sqlc v1.30.0 generation failed: %v\n%s", err, output)
		}
	}
}

func configurePinnedSQLCProxy(t *testing.T, goBinary string) {
	t.Helper()
	goModCacheCommand := exec.Command(goBinary, "env", "GOMODCACHE")
	goModCache, err := goModCacheCommand.Output()
	if err != nil {
		t.Fatalf("resolve Go module cache: %v", err)
	}
	proxyRoot := filepath.Join(strings.TrimSpace(string(goModCache)), "cache", "download")
	moduleCache := filepath.Join(proxyRoot, "github.com", "sqlc-dev", "sqlc", "@v")
	for _, name := range []string{"v1.30.0.info", "v1.30.0.mod", "v1.30.0.zip"} {
		if _, err := os.Stat(filepath.Join(moduleCache, name)); err != nil {
			t.Skipf("pinned sqlc v1.30.0 cache is unavailable; release generation remains N/V: %v", err)
		}
	}
	t.Setenv("GOPROXY", "file://"+proxyRoot)
}

func readFixtureFiles(t *testing.T, root string) string {
	t.Helper()
	var combined strings.Builder
	for _, rel := range []string{"internal/store/sqlc/query.go", "cmd/mcp-orch/store/sqlc/query.go"} {
		content, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		combined.Write(content)
	}
	return combined.String()
}
