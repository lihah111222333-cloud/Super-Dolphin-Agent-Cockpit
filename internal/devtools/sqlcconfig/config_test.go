package sqlcconfig

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestGitRunnerOutputDeadlineKillsPipeHoldingDescendant(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell fixture")
	}
	root := t.TempDir()
	git := filepath.Join(root, "git")
	writeFixtureFile(t, git, "#!/bin/sh\n(sleep 2) &\nexit 0\n")
	if err := os.Chmod(git, 0o755); err != nil {
		t.Fatal(err)
	}
	runner, err := NewGitRunner(git)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = runner.Output(ctx, root, "status")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Output() error = %v, want deadline cause", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Output() elapsed = %v, pipe-holding child outlived deadline", elapsed)
	}
}

func TestPreflightWithGitPreservesDeadlineCause(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell fixture")
	}
	root := newConfigFixture(t, "internal/store/out/product", "store/out/orch")
	git := filepath.Join(t.TempDir(), "git")
	writeFixtureFile(t, git, "#!/bin/sh\nsleep 2\n")
	if err := os.Chmod(git, 0o755); err != nil {
		t.Fatal(err)
	}
	runner, err := NewGitRunner(git)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = PreflightWithGit(ctx, root, runner)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("PreflightWithGit() error = %v, want deadline cause", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("PreflightWithGit() elapsed = %v, Git outlived deadline", elapsed)
	}
}

func TestGitRunnerPreservesCustomContextCause(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell fixture")
	}
	root := t.TempDir()
	git := filepath.Join(root, "git")
	writeFixtureFile(t, git, "#!/bin/sh\nsleep 2\n")
	if err := os.Chmod(git, 0o755); err != nil {
		t.Fatal(err)
	}
	runner, err := NewGitRunner(git)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("cancel cause", func(t *testing.T) {
		cause := errors.New("remote worker terminated")
		ctx, cancel := context.WithCancelCause(context.Background())
		cancel(cause)
		_, err := runner.Output(ctx, root, "status")
		if !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
			t.Fatalf("Output() error = %v, want canceled and custom cause", err)
		}
	})
	t.Run("timeout cause", func(t *testing.T) {
		cause := errors.New("remote SQLC deadline")
		ctx, cancel := context.WithTimeoutCause(context.Background(), 30*time.Millisecond, cause)
		defer cancel()
		_, err := runner.Output(ctx, root, "status")
		if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, cause) {
			t.Fatalf("Output() error = %v, want deadline and custom cause", err)
		}
	})
}

func TestGitRunnerUsesClosedGateTrustedPolicy(t *testing.T) {
	gitBinary, err := exec.LookPath("git")
	if err != nil {
		t.Fatal("git is required")
	}
	runner, err := NewGitRunner(gitBinary)
	if err != nil {
		t.Fatal(err)
	}
	command, err := runner.command(context.Background(), t.TempDir(), "status")
	if err != nil {
		t.Fatal(err)
	}
	if len(command.Args) < 2 || command.Args[1] != "--no-replace-objects" {
		t.Fatalf("Git argv = %v, want --no-replace-objects", command.Args)
	}
	for _, assignment := range []string{
		"GIT_NO_REPLACE_OBJECTS=1",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=/bin/false",
		"GIT_OPTIONAL_LOCKS=0",
	} {
		if !slices.Contains(command.Env, assignment) {
			t.Fatalf("trusted Git environment missing %q: %v", assignment, command.Env)
		}
	}
}

func TestGitRunnerIgnoresCandidateReplaceRefs(t *testing.T) {
	root := t.TempDir()
	runConfigFixtureCommand(t, root, "git", "init", "-q")
	runConfigFixtureCommand(t, root, "git", "config", "user.email", "sqlc@example.invalid")
	runConfigFixtureCommand(t, root, "git", "config", "user.name", "SQLC Fixture")
	writeFixtureFile(t, filepath.Join(root, "value.txt"), "original\n")
	runConfigFixtureCommand(t, root, "git", "add", "value.txt")
	runConfigFixtureCommand(t, root, "git", "commit", "-qm", "original")
	original := strings.TrimSpace(runConfigFixtureCommandOutput(t, root, "git", "rev-parse", "HEAD"))
	writeFixtureFile(t, filepath.Join(root, "value.txt"), "replacement\n")
	runConfigFixtureCommand(t, root, "git", "commit", "-qam", "replacement")
	replacement := strings.TrimSpace(runConfigFixtureCommandOutput(t, root, "git", "rev-parse", "HEAD"))
	runConfigFixtureCommand(t, root, "git", "replace", original, replacement)
	gitBinary, err := exec.LookPath("git")
	if err != nil {
		t.Fatal("git is required")
	}
	runner, err := NewGitRunner(gitBinary)
	if err != nil {
		t.Fatal(err)
	}
	output, err := runner.Output(context.Background(), root, "show", original+":value.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != "original\n" {
		t.Fatalf("trusted Git followed candidate replace ref: %q", output)
	}
}

func TestPreflightAcceptsDocumentedTrailingSlashAndMissingOutput(t *testing.T) {
	root := newConfigFixture(t, "internal/store/out/product", "store/out/orch")
	if err := os.RemoveAll(filepath.Join(root, "internal/store/out")); err != nil {
		t.Fatal(err)
	}
	units, err := Preflight(root)
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	if len(units) != 2 || units[0].Queries != "sql/queries" || units[0].Output != "internal/store/out/product" {
		t.Fatalf("units = %#v, want canonical paths", units)
	}
}

func TestPreflightRejectsNestedSymlinkBeforeGeneration(t *testing.T) {
	root := newConfigFixture(t, "internal/store/out/product", "store/out/orch")
	outside := filepath.Join(t.TempDir(), "outside.sql")
	writeFixtureFile(t, outside, "outside\n")
	if err := os.Symlink(outside, filepath.Join(root, "sql", "queries", "nested.sql")); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(root, "internal", "store", "out", "product", "sentinel.go")
	writeFixtureFile(t, sentinel, "package sentinel\n")
	before, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Preflight(root); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("Preflight() error = %v, want nested symlink rejection", err)
	}
	after, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("preflight changed sentinel: before=%q after=%q", before, after)
	}
}

func TestPreflightRejectsDuplicateOutput(t *testing.T) {
	root := newConfigFixture(t, "out/product", "../../out/product")
	if _, err := LoadDefault(root); err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("LoadDefault() error = %v, want duplicate output rejection", err)
	}
}

func TestLoadDefaultRejectsCaseOnlyMissingOutputAlias(t *testing.T) {
	root := newConfigFixture(t, "out/Foo", "../../out/foo")
	if err := os.RemoveAll(filepath.Join(root, "out")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDefault(root); err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("LoadDefault() error = %v, want case-only missing-output alias rejection", err)
	}
}

func TestLoadDefaultRejectsStaleGeneratedOutputAfterConfiguredMove(t *testing.T) {
	for _, test := range []struct {
		name, config, old, replacement, newOutput string
	}{
		{"root", "sqlc.yaml", `out: "internal/store/sqlc"`, `out: "internal/store/sqlc-alt"`, "internal/store/sqlc-alt/generated.go"},
		{"orchestrator", "cmd/mcp-orch/sqlc.yaml", `out: "store/sqlc"`, `out: "store/sqlc-alt"`, "cmd/mcp-orch/store/sqlc-alt/generated.go"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := newConfigFixture(t, "internal/store/sqlc", "store/sqlc")
			configPath := filepath.Join(root, filepath.FromSlash(test.config))
			raw, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			writeFixtureFile(t, configPath, strings.Replace(string(raw), test.old, test.replacement, 1))
			writeFixtureFile(t, filepath.Join(root, filepath.FromSlash(test.newOutput)), "// Code generated by sqlc. DO NOT EDIT.\npackage generated\n")
			if _, err := LoadDefault(root); err == nil || !strings.Contains(err.Error(), "stale SQLC output") {
				t.Fatalf("LoadDefault() error = %v, want stale output rejection", err)
			}
		})
	}
}

func TestLoadDefaultRejectsStaleManualTestExtensionAfterConfiguredMove(t *testing.T) {
	root := newConfigFixture(t, "internal/store/sqlc", "store/sqlc")
	configPath := filepath.Join(root, "sqlc.yaml")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, configPath, strings.Replace(string(raw), `out: "internal/store/sqlc"`, `out: "internal/store/sqlc-alt"`, 1))
	if err := os.Remove(filepath.Join(root, "internal/store/sqlc/generated.go")); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(root, "internal/store/sqlc/stale_test.go"), ManualTestExtensionMarker+"\npackage sqlc\n")
	if _, err := LoadDefault(root); err == nil || !strings.Contains(err.Error(), "stale SQLC output") {
		t.Fatalf("LoadDefault() error = %v, want stale manual test extension rejection", err)
	}
}

func TestLoadDefaultRejectsSymlinkedStaleOutputAfterConfiguredMove(t *testing.T) {
	root := newConfigFixture(t, "internal/store/sqlc", "store/sqlc")
	configPath := filepath.Join(root, "sqlc.yaml")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, configPath, strings.Replace(string(raw), `out: "internal/store/sqlc"`, `out: "internal/store/sqlc-alt"`, 1))
	if err := os.RemoveAll(filepath.Join(root, "internal/store/sqlc")); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	writeFixtureFile(t, filepath.Join(outside, "stale.go"), GeneratedGoMarker+"\npackage sqlc\n")
	if err := os.Symlink(outside, filepath.Join(root, "internal/store/sqlc")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDefault(root); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("LoadDefault() error = %v, want stale symlink rejection", err)
	}
}

func TestHardLinkCountRejectsUnavailableMetadata(t *testing.T) {
	if _, err := hardLinkCount("", nil); err == nil {
		t.Fatal("hardLinkCount() accepted unavailable metadata")
	}
}

func TestPreflightRejectsNestedOutputSymlink(t *testing.T) {
	root := newConfigFixture(t, "internal/store/out/product", "store/out/orch")
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "internal", "store", "out", "product", "nested")); err != nil {
		t.Fatal(err)
	}
	if _, err := Preflight(root); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("Preflight() error = %v, want output symlink rejection", err)
	}
}

func TestPreflightRejectsAncestorAndDescendantOutputs(t *testing.T) {
	root := newConfigFixture(t, "out", "out/orch")
	configPath := filepath.Join(root, "cmd/mcp-orch", "sqlc.yaml")
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	config = []byte(strings.Replace(string(config), `out: "out/orch"`, `out: "../../out/orch"`, 1))
	if err := os.WriteFile(configPath, config, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "out", "orch"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDefault(root); err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("LoadDefault() error = %v, want output overlap rejection", err)
	}
}

func TestPreflightRejectsNestedConfigParentEscapeBeforeSentinel(t *testing.T) {
	root := newConfigFixture(t, "internal/store/out/product", "store/out/orch")
	configPath := filepath.Join(root, "cmd/mcp-orch", "sqlc.yaml")
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	config = []byte(strings.Replace(string(config), `out: "store/out/orch"`, `out: "../../.."`, 1))
	if err := os.WriteFile(configPath, config, 0o644); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(filepath.Dir(root), "sqlc-parent-escape-sentinel.go")
	writeFixtureFile(t, sentinel, "package sentinel\n")
	before, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Preflight(root); err == nil || !strings.Contains(err.Error(), "escapes repository") {
		t.Fatalf("Preflight() error = %v, want nested config parent escape rejection", err)
	}
	after, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("preflight changed parent-escape sentinel: before=%q after=%q", before, after)
	}
}

func TestPreflightRejectsNestedSchemaSymlink(t *testing.T) {
	root := newConfigFixture(t, "internal/store/out/product", "store/out/orch")
	outside := filepath.Join(t.TempDir(), "outside.sql")
	writeFixtureFile(t, outside, "CREATE TABLE outside (id INTEGER);\n")
	if err := os.Symlink(outside, filepath.Join(root, "schema", "nested.sql")); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(root, "sqlc.yaml")
	data, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), `schema: ["schema/main.sql"]`, `schema: ["schema/nested.sql"]`, 1))
	if err := os.WriteFile(config, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Preflight(root); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("Preflight() error = %v, want nested schema symlink rejection", err)
	}
}

func TestPreflightRejectsOutputHardlink(t *testing.T) {
	root := newConfigFixture(t, "internal/store/out/product", "store/out/orch")
	outside := filepath.Join(t.TempDir(), "outside.go")
	writeFixtureFile(t, outside, "package outside\n")
	hardlink := filepath.Join(root, "internal", "store", "out", "product", "hardlink.go")
	if err := os.Link(outside, hardlink); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Preflight(root); err == nil || !strings.Contains(err.Error(), "hard link") {
		t.Fatalf("Preflight() error = %v, want output hard-link rejection", err)
	}
	after, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("preflight changed hard-link sentinel: before=%q after=%q", before, after)
	}
}

func TestLoadDefaultRejectsConfigSymlinkAndQueryNonRegular(t *testing.T) {
	t.Run("config symlink", func(t *testing.T) {
		root := newConfigFixture(t, "internal/store/out/product", "store/out/orch")
		config := filepath.Join(root, "sqlc.yaml")
		copyPath := filepath.Join(root, "sqlc.real.yaml")
		data, err := os.ReadFile(config)
		if err != nil {
			t.Fatal(err)
		}
		writeFixtureFile(t, copyPath, string(data))
		if err := os.Remove(config); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(copyPath, config); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadDefault(root); err == nil || !strings.Contains(err.Error(), "symbolic link") {
			t.Fatalf("LoadDefault() error = %v, want config symlink rejection", err)
		}
	})
	t.Run("schema directory", func(t *testing.T) {
		root := newConfigFixture(t, "internal/store/out/product", "store/out/orch")
		schema := filepath.Join(root, "schema", "main.sql")
		if err := os.Remove(schema); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(schema, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadDefault(root); err == nil || !strings.Contains(err.Error(), "unexpected directory type") {
			t.Fatalf("LoadDefault() error = %v, want schema regular-file rejection", err)
		}
	})
}

func TestConfiguredPathRejectsShellAmbiguity(t *testing.T) {
	for _, configured := range []string{
		"..", "custom/*", "custom/[", "custom/with space", "custom/with\nnewline",
		"-generated", "custom/-generated", "C:/generated", "C:generated", "custom:generated",
	} {
		t.Run(strings.ReplaceAll(configured, "\n", "newline"), func(t *testing.T) {
			if _, err := canonicalConfiguredPath(configured); err == nil {
				t.Fatalf("canonicalConfiguredPath(%q) unexpectedly succeeded", configured)
			}
		})
	}
}

func TestLoadDefaultRejectsIgnoredMissingOutput(t *testing.T) {
	root := newConfigFixture(t, "ignored/product", "out/orch")
	writeFixtureFile(t, filepath.Join(root, ".gitignore"), "ignored/\n")
	if err := os.RemoveAll(filepath.Join(root, "ignored")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDefault(root); err == nil || !strings.Contains(err.Error(), "Git-ignored") {
		t.Fatalf("LoadDefault() error = %v, want ignored missing output rejection", err)
	}
}

func TestLoadDefaultRejectsGeneratedFilesIgnoredByPattern(t *testing.T) {
	root := newConfigFixture(t, "generated/product", "out/orch")
	writeFixtureFile(t, filepath.Join(root, ".gitignore"), "*.go\n")
	if err := os.RemoveAll(filepath.Join(root, "generated")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDefault(root); err == nil || !strings.Contains(err.Error(), "Git-ignored") {
		t.Fatalf("LoadDefault() error = %v, want generated-file ignore rejection", err)
	}
}

func TestLoadDefaultRejectsGitMetadataOutput(t *testing.T) {
	root := newConfigFixture(t, ".git/generated", "out/orch")
	if _, err := LoadDefault(root); err == nil || !strings.Contains(err.Error(), ".git metadata") {
		t.Fatalf("LoadDefault() error = %v, want Git metadata output rejection", err)
	}
}

func TestValidateOutputOwnersRequiresStrictConfigParents(t *testing.T) {
	tests := []struct {
		name    string
		unit    Unit
		wantErr string
	}{
		{name: "root safe", unit: Unit{ConfigPath: "sqlc.yaml", Output: "internal/store/sqlc"}},
		{name: "root parent", unit: Unit{ConfigPath: "sqlc.yaml", Output: "internal/store"}, wantErr: "strict descendant"},
		{name: "root outside", unit: Unit{ConfigPath: "sqlc.yaml", Output: "generated/sqlc"}, wantErr: "strict descendant"},
		{name: "orchestrator safe", unit: Unit{ConfigPath: "cmd/mcp-orch/sqlc.yaml", Output: "cmd/mcp-orch/store/sqlc"}},
		{name: "orchestrator parent", unit: Unit{ConfigPath: "cmd/mcp-orch/sqlc.yaml", Output: "cmd/mcp-orch/store"}, wantErr: "strict descendant"},
		{name: "unknown owner", unit: Unit{ConfigPath: "other/sqlc.yaml", Output: "internal/store/sqlc"}, wantErr: "no owner parent"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateOutputOwners([]Unit{test.unit})
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateOutputOwners() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("ValidateOutputOwners() error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestLoadDefaultEnforcesOutputOwnerBeforeReturningUnits(t *testing.T) {
	root := newConfigFixture(t, "generated/sqlc", "store/sqlc")
	if _, err := LoadDefault(root); err == nil || !strings.Contains(err.Error(), "strict descendant") {
		t.Fatalf("LoadDefault() error = %v, want owner rejection", err)
	}
}

func TestVerifyGeneratedOutputsRejectsIgnoredGeneratedFiles(t *testing.T) {
	for _, pattern := range []string{"*.sql.go", "*_sql.go"} {
		t.Run(pattern, func(t *testing.T) {
			root := newConfigFixture(t, "internal/store/out/product", "store/out/orch")
			writeFixtureFile(t, filepath.Join(root, "cmd/mcp-orch", "store/out/orch", "generated.go"), "// Code generated by sqlc. DO NOT EDIT.\npackage generated\n")
			writeFixtureFile(t, filepath.Join(root, ".gitignore"), pattern+"\n")
			name := "ignored.sql.go"
			if pattern == "*_sql.go" {
				name = "ignored_sql.go"
			}
			writeFixtureFile(t, filepath.Join(root, "internal/store/out/product", name), GeneratedGoMarker+"package ignored\n")
			if _, err := os.Stat(filepath.Join(root, "internal/store/out/product", name)); err != nil {
				t.Fatal(err)
			}
			if err := VerifyGeneratedOutputs(root); err == nil || !strings.Contains(err.Error(), "Git-ignored") {
				t.Fatalf("VerifyGeneratedOutputs() error = %v, want ignored generated file rejection", err)
			}
		})
	}
}

func TestVerifyGeneratedOutputsRejectsVisibleButUntrackedGeneratedFile(t *testing.T) {
	root := newConfigFixture(t, "internal/store/out/product", "store/out/orch")
	writeFixtureFile(t, filepath.Join(root, "cmd/mcp-orch", "store/out/orch", "generated.go"), "// Code generated by sqlc. DO NOT EDIT.\npackage generated\n")
	newFile := filepath.Join(root, "internal/store/out/product", "new.sql.go")
	writeFixtureFile(t, newFile, "// Code generated by sqlc. DO NOT EDIT.\npackage generated\n")
	if err := VerifyGeneratedOutputs(root); err == nil || !strings.Contains(err.Error(), "not tracked") {
		t.Fatalf("VerifyGeneratedOutputs() error = %v, want visible untracked rejection", err)
	}
	git, err := ResolveExternalGit(root)
	if err != nil {
		t.Fatal(err)
	}
	visible, err := gitPathStatus(context.Background(), root, "internal/store/out/product/new.sql.go", git)
	if err != nil {
		t.Fatal(err)
	}
	if !visible {
		t.Fatal("new nonignored generated file was not visible to git status")
	}
}

func TestVerifyGeneratedOutputsRejectsEmptyOutput(t *testing.T) {
	root := newConfigFixture(t, "internal/store/out/product", "store/out/orch")
	writeFixtureFile(t, filepath.Join(root, "cmd/mcp-orch", "store/out/orch", "generated.go"), "package generated\n")
	for _, relative := range []string{
		"internal/store/out/product/generated.go",
		"cmd/mcp-orch/store/out/orch/generated.go",
	} {
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(relative))); err != nil {
			t.Fatal(err)
		}
	}
	if err := VerifyGeneratedOutputs(root); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("VerifyGeneratedOutputs() error = %v, want empty output rejection", err)
	}
}

func TestVerifyGeneratedOutputsRequiresCanonicalGeneratedGoMarker(t *testing.T) {
	root := newConfigFixture(t, "internal/store/out/product", "store/out/orch")
	writeFixtureFile(t, filepath.Join(root, "cmd/mcp-orch/store/out/orch/generated.go"), "// Code generated by sqlc. DO NOT EDIT.\npackage generated\n")
	writeFixtureFile(t, filepath.Join(root, "internal/store/out/product/generated.go"), "package generated\n")
	if err := VerifyGeneratedOutputs(root); err == nil || !strings.Contains(err.Error(), "canonical sqlc marker") {
		t.Fatalf("VerifyGeneratedOutputs() error = %v, want generated marker rejection", err)
	}
}

func TestVerifyGeneratedOutputsRejectsUnregisteredManualMarker(t *testing.T) {
	root := newConfigFixture(t, "internal/store/out/product", "store/out/orch")
	writeFixtureFile(t, filepath.Join(root, "cmd/mcp-orch/store/out/orch/generated.go"), GeneratedGoMarker+"package generated\n")
	writeFixtureFile(t, filepath.Join(root, "internal/store/out/product/evil.go"), ManualExtensionMarker+"package generated\n")
	if err := VerifyGeneratedOutputs(root); err == nil || !strings.Contains(err.Error(), "lacks canonical sqlc marker") {
		t.Fatalf("VerifyGeneratedOutputs() error = %v, want unregistered manual marker rejection", err)
	}
}

func TestVerifyGeneratedOutputsRejectsMarkerlessTestFile(t *testing.T) {
	root := newConfigFixture(t, "internal/store/out/product", "store/out/orch")
	writeFixtureFile(t, filepath.Join(root, "cmd/mcp-orch/store/out/orch/generated.go"), GeneratedGoMarker+"package generated\n")
	writeFixtureFile(t, filepath.Join(root, "internal/store/out/product/evil_test.go"), "package generated\n")
	if err := VerifyGeneratedOutputs(root); err == nil || !strings.Contains(err.Error(), "test file") {
		t.Fatalf("VerifyGeneratedOutputs() error = %v, want SQLC output test-file rejection", err)
	}
}

func TestVerifyGeneratedOutputsRejectsTrackedIndexFlags(t *testing.T) {
	for _, flag := range []string{"--assume-unchanged", "--skip-worktree"} {
		t.Run(flag, func(t *testing.T) {
			root := newConfigFixture(t, "internal/store/out/product", "store/out/orch")
			writeFixtureFile(t, filepath.Join(root, "cmd/mcp-orch/store/out/orch/generated.go"), GeneratedGoMarker+"package generated\n")
			relative := "internal/store/out/product/generated.go"
			runConfigFixtureCommand(t, root, "git", "update-index", flag, relative)
			if err := VerifyGeneratedOutputs(root); err == nil || !strings.Contains(err.Error(), "index flags") {
				t.Fatalf("VerifyGeneratedOutputs() accepted %s: %v", flag, err)
			}
		})
	}
}

func TestPreflightDoesNotExecuteAmbientPATHGitShim(t *testing.T) {
	root := newConfigFixture(t, "internal/store/out/product", "store/out/orch")
	shimDir := t.TempDir()
	shim := filepath.Join(shimDir, "git")
	writeFixtureFile(t, shim, "#!/bin/sh\nexit 97\n")
	if err := os.Chmod(shim, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := Preflight(root); err != nil {
		t.Fatalf("Preflight() executed ambient PATH git shim: %v", err)
	}
}

func TestVerifyGeneratedOutputsRejectsImpureOrExecutableOutput(t *testing.T) {
	for _, test := range []struct {
		name string
		path string
		mode os.FileMode
	}{
		{name: "non Go", path: "unexpected.txt", mode: 0o644},
		{name: "executable Go", path: "unexpected.go", mode: 0o755},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := newConfigFixture(t, "internal/store/out/product", "store/out/orch")
			file := filepath.Join(root, "internal/store/out/product", test.path)
			writeFixtureFile(t, file, GeneratedGoMarker+"package generated\n")
			if err := os.Chmod(file, test.mode); err != nil {
				t.Fatal(err)
			}
			if err := VerifyGeneratedOutputs(root); err == nil || (!strings.Contains(err.Error(), "non-Go") && !strings.Contains(err.Error(), "permissions")) {
				t.Fatalf("VerifyGeneratedOutputs() error = %v, want purity/permission rejection", err)
			}
		})
	}
}

func TestVerifyGeneratedOutputsRejectsOutputWithoutGeneratedGoFile(t *testing.T) {
	root := newConfigFixture(t, "internal/store/out/product", "store/out/orch")
	writeFixtureFile(t, filepath.Join(root, "cmd/mcp-orch/store/out/orch/generated.go"), "// Code generated by sqlc. DO NOT EDIT.\npackage generated\n")
	if err := os.Remove(filepath.Join(root, "internal/store/out/product/generated.go")); err != nil {
		t.Fatal(err)
	}
	if err := VerifyGeneratedOutputs(root); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("VerifyGeneratedOutputs() error = %v, want generated Go file rejection", err)
	}
}

func TestLoadDefaultRejectsOutputQueriesAndSchemaOverlap(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{name: "queries", output: "sql/queries", want: "queries tree"},
		{name: "schema", output: "schema", want: "schema tree"},
		{name: "nested unit queries", output: "cmd/mcp-orch/sql/queries", want: "queries tree"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := newConfigFixture(t, test.output, "out/orch")
			if _, err := LoadDefault(root); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("LoadDefault() error = %v, want output/%s overlap rejection", err, test.want)
			}
		})
	}
}

func TestGitRunnerEnvironmentIsClosedAgainstRepositoryRouting(t *testing.T) {
	hostile := map[string]string{
		"GIT_DIR":                             filepath.Join(t.TempDir(), "hostile.git"),
		"GIT_WORK_TREE":                       t.TempDir(),
		"GIT_INDEX_FILE":                      filepath.Join(t.TempDir(), "index"),
		"GIT_OBJECT_DIRECTORY":                filepath.Join(t.TempDir(), "objects"),
		"GIT_ALTERNATE_OBJECT_DIRECTORIES":    filepath.Join(t.TempDir(), "alternates"),
		"GIT_REPLACE_REF_BASE":                "refs/replace/hostile",
		"GIT_CONFIG_PARAMETERS":               "'sqlc.hostile=true'",
		"GIT_CONFIG_COUNT":                    "1",
		"GIT_CONFIG_KEY_0":                    "sqlc.hostile",
		"GIT_CONFIG_VALUE_0":                  "true",
		"GIT_CONFIG_GLOBAL":                   filepath.Join(t.TempDir(), "global-config"),
		"GIT_CONFIG_SYSTEM":                   filepath.Join(t.TempDir(), "system-config"),
		"SQLC_UNRELATED_AMBIENT_ENV_SENTINEL": "must-not-propagate",
	}
	for key, value := range hostile {
		t.Setenv(key, value)
	}
	for _, assignment := range trustedGitEnvironment() {
		key, _, ok := strings.Cut(assignment, "=")
		if !ok {
			t.Fatalf("malformed trusted Git environment assignment %q", assignment)
		}
		if hostileValue, found := hostile[key]; found && assignment == key+"="+hostileValue {
			t.Fatalf("trusted Git environment inherited hostile assignment %q", assignment)
		}
	}
}

func TestLoadDefaultRequiresCanonicalGenerationSafetyOptions(t *testing.T) {
	sharedMutations := []struct {
		name, old, replacement, want string
	}{
		{name: "missing strict_order_by", old: "    strict_order_by: true\n", want: "strict_order_by"},
		{name: "disabled strict_order_by", old: "    strict_order_by: true\n", replacement: "    strict_order_by: false\n", want: "strict_order_by"},
		{name: "missing query_parameter_limit", old: "        query_parameter_limit: 0\n", want: "query_parameter_limit"},
		{name: "nonzero query_parameter_limit", old: "        query_parameter_limit: 0\n", replacement: "        query_parameter_limit: 1\n", want: "query_parameter_limit"},
	}
	for _, configPath := range []string{RootConfigPath, OrchestratorConfigPath} {
		mutations := slices.Clone(sharedMutations)
		if configPath == RootConfigPath {
			mutations = append(mutations,
				struct{ name, old, replacement, want string }{name: "missing emit_interface", old: "        emit_interface: true\n", want: "emit_interface"},
				struct{ name, old, replacement, want string }{name: "disabled emit_interface", old: "        emit_interface: true\n", replacement: "        emit_interface: false\n", want: "emit_interface"},
			)
		}
		for _, mutation := range mutations {
			t.Run(configPath+"/"+mutation.name, func(t *testing.T) {
				root := newConfigFixture(t, "internal/store/out/product", "store/out/orch")
				absolute := filepath.Join(root, filepath.FromSlash(configPath))
				raw, err := os.ReadFile(absolute)
				if err != nil {
					t.Fatal(err)
				}
				raw = []byte(strings.Replace(string(raw), mutation.old, mutation.replacement, 1))
				if err := os.WriteFile(absolute, raw, 0o644); err != nil {
					t.Fatal(err)
				}
				if _, err := LoadDefault(root); err == nil || !strings.Contains(err.Error(), mutation.want) {
					t.Fatalf("LoadDefault() error = %v, want %s rejection", err, mutation.want)
				}
			})
		}
	}
}

func newConfigFixture(t *testing.T, output, orchOutput string) string {
	t.Helper()
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "sqlc.yaml"), `version: "2"
sql:
  - engine: "sqlite"
    queries: "sql/queries/"
    schema: ["schema/main.sql"]
    strict_order_by: true
    gen:
      go:
        package: "sqlc"
        out: "`+output+`"
        emit_interface: true
        query_parameter_limit: 0
`)
	writeFixtureFile(t, filepath.Join(root, "cmd/mcp-orch", "sqlc.yaml"), `version: "2"
sql:
  - engine: "sqlite"
    queries: "sql/queries"
    schema: ["../../schema/orch.sql"]
    strict_order_by: true
    gen:
      go:
        package: "sqlc"
        out: "`+orchOutput+`"
        query_parameter_limit: 0
`)
	writeFixtureFile(t, filepath.Join(root, "sql", "queries", "query.sql"), "-- query\n")
	writeFixtureFile(t, filepath.Join(root, "cmd/mcp-orch", "sql", "queries", "query.sql"), "-- query\n")
	writeFixtureFile(t, filepath.Join(root, "schema", "main.sql"), "CREATE TABLE main (id INTEGER);\n")
	writeFixtureFile(t, filepath.Join(root, "schema", "orch.sql"), "CREATE TABLE orch (id INTEGER);\n")
	writeFixtureFile(t, filepath.Join(root, output, "generated.go"), "// Code generated by sqlc. DO NOT EDIT.\npackage generated\n")
	writeFixtureFile(t, filepath.Join(root, "cmd/mcp-orch", orchOutput, "generated.go"), "// Code generated by sqlc. DO NOT EDIT.\npackage generated\n")
	writeRequiredManualExtensions(t, root, []Unit{
		{ConfigPath: RootConfigPath, Output: filepath.ToSlash(output)},
		{ConfigPath: OrchestratorConfigPath, Output: path.Join("cmd/mcp-orch", filepath.ToSlash(orchOutput))},
	})
	runConfigFixtureCommand(t, root, "git", "init", "-q")
	runConfigFixtureCommand(t, root, "git", "config", "user.email", "sqlc@example.invalid")
	runConfigFixtureCommand(t, root, "git", "config", "user.name", "SQLC Fixture")
	runConfigFixtureCommand(t, root, "git", "add", ".")
	runConfigFixtureCommand(t, root, "git", "commit", "-qm", "fixture")
	return root
}

func writeRequiredManualExtensions(t *testing.T, root string, units []Unit) {
	t.Helper()
	for _, relative := range ManualExtensionPaths(units) {
		marker := ManualExtensionMarker
		if strings.HasSuffix(relative, "_test.go") {
			marker = ManualTestExtensionMarker
		}
		writeFixtureFile(t, filepath.Join(root, filepath.FromSlash(relative)), marker+"\npackage sqlc\n")
	}
}

func runConfigFixtureCommand(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	command := exec.Command(name, args...)
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, output)
	}
}

func runConfigFixtureCommandOutput(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	command := exec.Command(name, args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, output)
	}
	return string(output)
}

func writeFixtureFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
