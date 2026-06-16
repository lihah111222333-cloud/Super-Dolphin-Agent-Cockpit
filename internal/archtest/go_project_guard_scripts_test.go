package archtest

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestGoBoundaryGuardTreatsSidecarSubpackagesAsInternalLibraries runs a archtest operation.
func TestGoBoundaryGuardTreatsSidecarSubpackagesAsInternalLibraries(t *testing.T) {
	root := newGuardFixture(t)
	writeFixtureFile(t, root, "cmd/mcp-lsp/main.go", `package main

import _ "example.com/guardcase/internal/sidecar/lsp/tools"

func main() {}
`)
	writeFixtureFile(t, root, "internal/sidecar/lsp/tools/tools.go", `package tools

import _ "example.com/guardcase/internal/sidecar/lsp/manager"

// Ready is part of the archtest package API.
var Ready = true
`)
	writeFixtureFile(t, root, "internal/sidecar/lsp/manager/manager.go", `package manager

// Ready is part of the archtest package API.
var Ready = true
`)
	writeFixtureFile(t, root, "internal/sidecar/orch/orchestration/service.go", `package orchestration

import _ "example.com/guardcase/internal/platform/config"

// Ready is part of the archtest package API.
var Ready = true
`)
	writeFixtureFile(t, root, "internal/platform/config/config.go", `package config

// Ready is part of the archtest package API.
var Ready = true
`)

	out := runGuardScript(t, root, ".agents/skills/guarding-go-projects/scripts/check_go_boundaries.py")
	if out != "" && !strings.Contains(out, "passed") {
		t.Fatalf("boundary guard rejected valid sidecar fixture:\n%s", out)
	}
}

// TestGoBoundaryGuardRejectsLegacySidecarCommandSubpackages runs a archtest operation.
func TestGoBoundaryGuardRejectsLegacySidecarCommandSubpackages(t *testing.T) {
	root := newGuardFixture(t)
	writeFixtureFile(t, root, "cmd/mcp-lsp/main.go", `package main

func main() {}
`)
	writeFixtureFile(t, root, "cmd/mcp-lsp/tools/tools.go", `package tools

// Ready is part of the archtest package API.
var Ready = true
`)

	out, err := runGuardScriptWithEnvErr(t, root, ".agents/skills/guarding-go-projects/scripts/check_go_boundaries.py", nil)
	if err == nil {
		t.Fatalf("boundary guard allowed legacy sidecar command subpackage:\n%s", out)
	}
	if !strings.Contains(out, "cmd/mcp-lsp/tools") || !strings.Contains(out, "internal/sidecar/lsp") {
		t.Fatalf("boundary guard failed for the wrong reason:\n%s", out)
	}
}

// TestGoASTGuardAllowsLoggingImplementationAndCommandTools runs a archtest operation.
func TestGoASTGuardAllowsLoggingImplementationAndCommandTools(t *testing.T) {
	root := newGuardFixture(t)
	writeFixtureFile(t, root, "pkg/logger/logger.go", `package logger

import "log/slog"

// Default is part of the archtest package API.
var Default = slog.Default()
`)
	writeFixtureFile(t, root, "scripts/dev_tool.go", `//go:build ignore

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Println("ok")
	os.Exit(0)
}
`)
	writeFixtureFile(t, root, "docs/security/internal/check.go", `package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Println("ok")
	os.Exit(0)
}
`)

	out := runGuardScript(t, root, ".agents/skills/guarding-go-projects/scripts/check_go_ast_rules.py")
	if out != "" && !strings.Contains(out, "passed") {
		t.Fatalf("AST guard rejected logging implementation or command tools:\n%s", out)
	}
}

// TestGoSizeGuardBaselineSuppressesKnownDebtOnly runs a archtest operation.
func TestGoSizeGuardBaselineSuppressesKnownDebtOnly(t *testing.T) {
	root := newGuardFixture(t)
	writeFixtureFile(t, root, "internal/large/large.go", strings.Join([]string{
		"package large",
		strings.Repeat("var KnownDebt = true\n", 401),
	}, "\n"))
	baseline := filepath.Join(root, "guard-baseline.txt")
	if err := os.WriteFile(baseline, []byte("internal/large/large.go: 402 lines exceeds limit 400\n"), 0o644); err != nil {
		t.Fatalf("write baseline: %v", err)
	}

	runGuardScriptWithEnv(t, root, ".agents/skills/guarding-go-projects/scripts/check_go_size.py", []string{"GO_GUARD_BASELINE=" + baseline})

	writeFixtureFile(t, root, "internal/newlarge/newlarge.go", strings.Join([]string{
		"package newlarge",
		strings.Repeat("var NewDebt = true\n", 401),
	}, "\n"))
	out, err := runGuardScriptWithEnvErr(t, root, ".agents/skills/guarding-go-projects/scripts/check_go_size.py", []string{"GO_GUARD_BASELINE=" + baseline})
	if err == nil {
		t.Fatalf("size guard allowed a new violation outside the baseline:\n%s", out)
	}
	if !strings.Contains(out, "internal/newlarge/newlarge.go") {
		t.Fatalf("size guard failed for the wrong reason:\n%s", out)
	}
}

// TestMigrationGuardBaselineSuppressesKnownDebtOnly verifies migration baselines only suppress existing findings.
func TestMigrationGuardBaselineSuppressesKnownDebtOnly(t *testing.T) {
	root := newGuardFixture(t)
	writeFixtureFile(t, root, "migrations/0001_missing_markers.sql", "CREATE TABLE known_debt (id INTEGER);\n")
	baseline := filepath.Join(root, "migration-baseline.txt")
	if err := os.WriteFile(baseline, []byte(strings.Join([]string{
		"migrations/0001_missing_markers.sql: missing '-- +goose Up' marker",
		"migrations/0001_missing_markers.sql: missing '-- +goose Down' marker",
		"",
	}, "\n")), 0o644); err != nil {
		t.Fatalf("write baseline: %v", err)
	}

	runGuardScriptWithEnv(t, root, ".agents/skills/guarding-go-projects/scripts/check_migrations.py", []string{"GO_GUARD_BASELINE=" + baseline})

	writeFixtureFile(t, root, "migrations/0002_new_missing_markers.sql", "CREATE TABLE new_debt (id INTEGER);\n")
	out, err := runGuardScriptWithEnvErr(t, root, ".agents/skills/guarding-go-projects/scripts/check_migrations.py", []string{"GO_GUARD_BASELINE=" + baseline})
	if err == nil {
		t.Fatalf("migration guard allowed a new violation outside the baseline:\n%s", out)
	}
	if !strings.Contains(out, "migrations/0002_new_missing_markers.sql") {
		t.Fatalf("migration guard failed for the wrong reason:\n%s", out)
	}
}

func newGuardFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFixtureFile(t, root, "go.mod", "module example.com/guardcase\n\ngo 1.25\n")
	return root
}

func writeFixtureFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func runGuardScript(t *testing.T, fixtureRoot, scriptRel string) string {
	t.Helper()
	out, err := runGuardScriptWithEnvErr(t, fixtureRoot, scriptRel, nil)
	if err != nil {
		t.Fatalf("run %s: %v\n%s", scriptRel, err, out)
	}
	return out
}

func runGuardScriptWithEnv(t *testing.T, fixtureRoot, scriptRel string, env []string) string {
	t.Helper()
	out, err := runGuardScriptWithEnvErr(t, fixtureRoot, scriptRel, env)
	if err != nil {
		t.Fatalf("run %s: %v\n%s", scriptRel, err, out)
	}
	return out
}

func runGuardScriptWithEnvErr(t *testing.T, fixtureRoot, scriptRel string, env []string) (string, error) {
	t.Helper()
	scriptPath := filepath.Join(repoRoot(t), filepath.FromSlash(scriptRel))
	if _, err := os.Stat(scriptPath); err != nil {
		if os.IsNotExist(err) {
			t.Skipf("guard script is not available in this checkout: %s", scriptRel)
		}
		t.Fatalf("stat guard script %s: %v", scriptRel, err)
	}
	cmd := exec.Command("python3", scriptPath, fixtureRoot)
	cmd.Env = guardScriptTestEnv(env)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func guardScriptTestEnv(extra []string) []string {
	base := os.Environ()
	out := make([]string, 0, len(base)+len(extra))
	for _, item := range base {
		if strings.HasPrefix(item, "GO_GUARD_") {
			continue
		}
		out = append(out, item)
	}
	return append(out, extra...)
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
