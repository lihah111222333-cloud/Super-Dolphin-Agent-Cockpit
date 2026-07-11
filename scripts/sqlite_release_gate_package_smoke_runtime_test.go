package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

type sqliteReleaseGateUnsignedPackage struct {
	root        string
	entrypoint  string
	envFiles    []string
	launchers   []string
	binaryNames []string
}

func TestSQLiteReleaseGatePackageSmokeRuntime(t *testing.T) {
	stage := writeSQLiteReleaseGateUnsignedPackage(t)
	verifySQLiteReleaseGateUnsignedPackage(t, stage)
	home, oldPGData := prepareSQLiteReleaseGatePackageSmokeHome(t)
	output := runSQLiteReleaseGatePackageSmokeRuntime(t, stage, home, oldPGData)

	if !strings.Contains(output, "sqlite package runtime smoke passed") {
		t.Fatalf("package smoke output missing success evidence:\n%s", output)
	}
	t.Logf("package smoke runtime output:\n%s", output)
	assertSQLiteReleaseGatePackageSmokeState(t, filepath.Join(home, "super-dolphin.db"), oldPGData)
}

func TestSQLiteReleaseGatePackageSmokeUsesRealRuntimeEntrypoint(t *testing.T) {
	stage := writeSQLiteReleaseGateUnsignedPackage(t)

	raw, err := os.ReadFile(stage.entrypoint)
	if err != nil {
		t.Fatalf("read package smoke runtime entrypoint: %v", err)
	}
	body := strings.ToLower(string(raw))
	for _, forbidden := range []string{strings.Join([]string{"place", "holder"}, ""), strings.Join([]string{"exit", " 0"}, "")} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("package smoke runtime entrypoint %s is a fake no-op containing %q", stage.entrypoint, forbidden)
		}
	}
}

func TestSQLiteReleaseGateLinuxVerifierRejectsPackageRootHomeResolution(t *testing.T) {
	stage := writeMinimalPackagedLinuxStage(t)
	writeRuntimeManifest(t, stage, map[string]string{
		"bundled_codex_path":  "bin/codex",
		"bundled_gopls_path":  "bin/gopls",
		"lsp_bundle_path":     "lsp",
		"lsp_manifest_path":   "lsp/lsp-manifest.json",
		"model_registry_path": "models.yaml",
	})
	writeFile(t, filepath.Join(stage, "run.sh"), `#!/usr/bin/env bash
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export PROJECT_ROOT="$here"
export SUPER_DOLPHIN_PACKAGE_ROOT="$here"
export SUPER_DOLPHIN_RUNTIME_MODE=packaged
export SUPER_DOLPHIN_PACKAGED_LAUNCHER=1
export SUPER_DOLPHIN_HOME="$here"
exec "$here/bin/agent-terminal" "$@"
`, 0o755)

	output, err := runVerifyPackagedAppLinux(t, stage)
	if err == nil {
		t.Fatalf("expected Linux verifier to reject SQLite home under package root, got success:\n%s", output)
	}
	if !strings.Contains(output, "Linux launcher must not resolve SQLite home under package root") {
		t.Fatalf("expected package-root SQLite home rejection, got:\n%s", output)
	}
}

func prepareSQLiteReleaseGatePackageSmokeHome(t *testing.T) (string, string) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "super-dolphin-home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("create clean SUPER_DOLPHIN_HOME: %v", err)
	}
	if err := securefs.RestrictOwnerOnly(home, 0o700); err != nil {
		t.Fatalf("restrict clean SUPER_DOLPHIN_HOME: %v", err)
	}
	oldPGData := filepath.Join(home, "postgres", "data")
	if err := os.MkdirAll(oldPGData, 0o700); err != nil {
		t.Fatalf("create old PostgreSQL data dir: %v", err)
	}
	writeFile(t, filepath.Join(oldPGData, "PG_VERSION"), "16\n", 0o600)
	return home, oldPGData
}

func runSQLiteReleaseGatePackageSmokeRuntime(t *testing.T, stage sqliteReleaseGateUnsignedPackage, home, oldPGData string) string {
	t.Helper()

	cmd := sqliteReleaseGatePackageSmokeCommand(t, stage)
	cmd.Env = sqliteReleaseGatePackageSmokeEnv(stage, home, oldPGData)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run unsigned package SQLite smoke through package entrypoint: %v\n%s", err, output)
	}
	return string(output)
}

func sqliteReleaseGatePackageSmokeCommand(t *testing.T, stage sqliteReleaseGateUnsignedPackage) *exec.Cmd {
	t.Helper()
	switch runtime.GOOS {
	case "windows":
		cmd := exec.Command("cmd", "/c", filepath.Base(stage.launchers[0]))
		cmd.Dir = stage.root
		return cmd
	case "darwin":
		cmd := exec.Command(stage.entrypoint)
		cmd.Dir = stage.root
		return cmd
	default:
		cmd := exec.Command(stage.launchers[0])
		cmd.Dir = stage.root
		return cmd
	}
}

func sqliteReleaseGatePackageSmokeEnv(stage sqliteReleaseGateUnsignedPackage, home, oldPGData string) []string {
	skip := map[string]bool{
		contract.SQLitePathEnvKey:            true,
		contract.InternalSQLitePathEnvKey:    true,
		"PROJECT_ROOT":                       true,
		"SUPER_DOLPHIN_PACKAGE_ROOT":         true,
		"SUPER_DOLPHIN_RUNTIME_MODE":         true,
		"SUPER_DOLPHIN_PACKAGED_LAUNCHER":    true,
		"DATABASE_URL":                       true,
		"POSTGRES_CONNECTION_STRING":         true,
		"RPC_ADDR":                           true,
		"GO_AGENT_CTL_RPC_ADDR":              true,
		"SUPER_DOLPHIN_DEPENDENCY_BOOTSTRAP": true,
		"SUPER_DOLPHIN_DEPENDENCY_PROFILE":   true,
	}
	env := make([]string, 0, len(os.Environ())+8)
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if !skip[key] {
			env = append(env, kv)
		}
	}
	env = append(env,
		"SUPER_DOLPHIN_HOME="+home,
		"SUPER_DOLPHIN_PACKAGE_SMOKE_OLD_PG_DATA="+oldPGData,
		"SUPER_DOLPHIN_DEPENDENCY_BOOTSTRAP=production",
		"SUPER_DOLPHIN_DEPENDENCY_PROFILE=production",
		"DATABASE_URL=postgres://127.0.0.1:1/super_dolphin?sslmode=disable",
		"POSTGRES_CONNECTION_STRING=postgres://127.0.0.1:1/super_dolphin?sslmode=disable",
	)
	if len(stage.launchers) == 0 {
		env = append(env,
			"PROJECT_ROOT="+stage.root,
			"SUPER_DOLPHIN_RUNTIME_MODE=packaged",
			"SUPER_DOLPHIN_PACKAGED_LAUNCHER=1",
		)
	}
	return env
}

func assertSQLiteReleaseGatePackageSmokeState(t *testing.T, sqlitePath, oldPGData string) {
	t.Helper()
	if _, err := os.Stat(sqlitePath); err != nil {
		t.Fatalf("packaged runtime did not create SQLite DB at clean home path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(oldPGData, "PG_VERSION")); err != nil {
		t.Fatalf("old PostgreSQL data dir was not preserved for smoke assertion: %v", err)
	}
}

func writeSQLiteReleaseGateUnsignedPackage(t *testing.T) sqliteReleaseGateUnsignedPackage {
	t.Helper()

	stageDir := t.TempDir()
	pkg := sqliteReleaseGateUnsignedPackage{}
	switch runtime.GOOS {
	case "darwin":
		app := filepath.Join(stageDir, "Super Dolphin.app")
		pkg.root = filepath.Join(app, "Contents", "Resources")
		pkg.entrypoint = filepath.Join(app, "Contents", "MacOS", "agent-terminal")
		pkg.binaryNames = []string{"mcp-orch", "mcp-lsp", "mcp-ida", "codex", "gopls"}
	case "windows":
		pkg.root = filepath.Join(stageDir, fmt.Sprintf("super-dolphin-0.1.0-windows-%s", runtime.GOARCH))
		pkg.entrypoint = filepath.Join(pkg.root, "bin", "agent-terminal.exe")
		pkg.launchers = []string{filepath.Join(pkg.root, "run.cmd"), filepath.Join(pkg.root, "run.ps1")}
		pkg.binaryNames = []string{"agent-terminal.exe", "mcp-orch.exe", "mcp-lsp.exe", "mcp-ida.exe", "codex.exe", "gopls.exe"}
	default:
		pkg.root = filepath.Join(stageDir, fmt.Sprintf("super-dolphin-0.1.0-%s-%s", runtime.GOOS, runtime.GOARCH))
		pkg.entrypoint = filepath.Join(pkg.root, "bin", "agent-terminal")
		pkg.launchers = []string{filepath.Join(pkg.root, "run.sh")}
		pkg.binaryNames = []string{"agent-terminal", "mcp-orch", "mcp-lsp", "mcp-ida", "codex", "gopls"}
	}
	pkg.envFiles = []string{filepath.Join(pkg.root, ".env"), filepath.Join(pkg.root, "runtime-manifest.json")}

	writeFile(t, filepath.Join(pkg.root, ".env"), "# sqlite release gate smoke\n", 0o600)
	writeFile(t, filepath.Join(pkg.root, "models.yaml"), "models: []\n", 0o644)
	copySQLiteReleaseGateMigrations(t, filepath.Join(pkg.root, "internal", "platform", "db", "sqlite", "migrations"))
	writeSQLiteReleaseGateRuntimeManifest(t, pkg.root, runtime.GOOS == "windows")
	for _, name := range pkg.binaryNames {
		path := filepath.Join(pkg.root, "bin", name)
		if samePackageSmokePath(path, pkg.entrypoint) {
			buildSQLiteReleaseGatePackageSmokeEntrypoint(t, path)
			continue
		}
		writeFile(t, path, unusedPackagePeerBody(runtime.GOOS, name), 0o755)
	}
	if runtime.GOOS == "darwin" {
		buildSQLiteReleaseGatePackageSmokeEntrypoint(t, pkg.entrypoint)
	}
	for _, launcher := range pkg.launchers {
		writeFile(t, launcher, packageSmokeLauncherBody(runtime.GOOS), 0o755)
	}
	return pkg
}

func buildSQLiteReleaseGatePackageSmokeEntrypoint(t *testing.T, dest string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatalf("create package smoke entrypoint dir: %v", err)
	}
	cmd := exec.Command("go", "build", "-o", dest, "./internal/devtools/sqlitepackagesmoke")
	cmd.Dir = scriptRepoRoot(t)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build package smoke runtime entrypoint: %v\n%s", err, output)
	}
}

func samePackageSmokePath(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func copySQLiteReleaseGateMigrations(t *testing.T, dest string) {
	t.Helper()

	src := filepath.Join(scriptRepoRoot(t), "internal", "platform", "db", "sqlite", "migrations")
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("read SQLite migrations source: %v", err)
	}
	var copied int
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(src, entry.Name()))
		if err != nil {
			t.Fatalf("read SQLite migration %s: %v", entry.Name(), err)
		}
		writeFile(t, filepath.Join(dest, entry.Name()), string(raw), 0o644)
		copied++
	}
	if copied == 0 {
		t.Fatal("copied zero SQLite migrations into unsigned package fixture")
	}
}

func writeSQLiteReleaseGateRuntimeManifest(t *testing.T, root string, windows bool) {
	t.Helper()

	codexPath := "bin/codex"
	goplsPath := "bin/gopls"
	if windows {
		codexPath += ".exe"
		goplsPath += ".exe"
	}
	raw, err := json.MarshalIndent(map[string]string{
		"bundled_codex_path":  codexPath,
		"bundled_gopls_path":  goplsPath,
		"lsp_bundle_path":     "lsp",
		"lsp_manifest_path":   "lsp/lsp-manifest.json",
		"model_registry_path": "models.yaml",
	}, "", "  ")
	if err != nil {
		t.Fatalf("marshal runtime manifest: %v", err)
	}
	writeFile(t, filepath.Join(root, "runtime-manifest.json"), string(raw)+"\n", 0o644)
}

func verifySQLiteReleaseGateUnsignedPackage(t *testing.T, stage sqliteReleaseGateUnsignedPackage) {
	t.Helper()

	for _, path := range append(append([]string{}, stage.envFiles...), stage.launchers...) {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("unsigned package fixture missing %s: %v", path, err)
		}
	}
	if _, err := os.Stat(stage.entrypoint); err != nil {
		t.Fatalf("unsigned package fixture missing runtime entrypoint %s: %v", stage.entrypoint, err)
	}
	for _, name := range stage.binaryNames {
		if _, err := os.Stat(filepath.Join(stage.root, "bin", name)); err != nil {
			t.Fatalf("unsigned package fixture missing binary %s: %v", name, err)
		}
	}
	assertSQLitePackageHasMigrations(t, stage.root)
	assertSQLitePackageHasNoPostgresRuntime(t, stage)
}

func assertSQLitePackageHasMigrations(t *testing.T, root string) {
	t.Helper()

	entries, err := os.ReadDir(filepath.Join(root, "internal", "platform", "db", "sqlite", "migrations"))
	if err != nil {
		t.Fatalf("read package SQLite migrations: %v", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() && entry.Name() == "001_baseline.sql" {
			return
		}
	}
	t.Fatal("package SQLite migrations missing 001_baseline.sql")
}

func assertSQLitePackageHasNoPostgresRuntime(t *testing.T, stage sqliteReleaseGateUnsignedPackage) {
	t.Helper()

	err := filepath.WalkDir(stage.root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		name := strings.ToLower(entry.Name())
		for _, forbidden := range []string{"postgres", "pg_ctl", "initdb", "postgres.bki"} {
			if strings.Contains(name, forbidden) {
				return fmt.Errorf("packaged PostgreSQL runtime artifact %q found at %s", forbidden, path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range append(append([]string{}, stage.envFiles...), stage.launchers...) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read package env/launcher %s: %v", path, err)
		}
		body := strings.ToUpper(string(raw))
		for _, forbidden := range []string{"POSTGRES", "EMBEDDED_POSTGRES_RESOURCE_PATH", "DATABASE_URL", "PG_CTL", "INITDB"} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("package env/launcher %s still references PostgreSQL runtime/env %q", path, forbidden)
			}
		}
	}
}

func unusedPackagePeerBody(goos, name string) string {
	if goos == "windows" {
		return "@echo off\r\necho sqlite release gate smoke unused peer " + name + "\r\nexit /b %SUPER_DOLPHIN_UNUSED_PEER_STATUS%\r\n"
	}
	return "#!/bin/sh\necho sqlite release gate smoke unused peer " + name + "\nexit ${SUPER_DOLPHIN_UNUSED_PEER_STATUS:-0}\n"
}

func packageSmokeLauncherBody(goos string) string {
	if goos == "windows" {
		return "@echo off\r\nsetlocal\r\nset \"here=%~dp0\"\r\nfor %%I in (\"%here%.\") do set \"here=%%~fI\"\r\nset \"SUPER_DOLPHIN_PACKAGE_ROOT=%here%\"\r\nset \"PROJECT_ROOT=%here%\"\r\nset \"PATH=%here%\\bin;%PATH%\"\r\nset \"SUPER_DOLPHIN_RUNTIME_MODE=packaged\"\r\nset \"SUPER_DOLPHIN_PACKAGED_LAUNCHER=1\"\r\n\"%here%\\bin\\agent-terminal.exe\" %*\r\nexit /b %ERRORLEVEL%\r\n"
	}
	return "#!/usr/bin/env bash\nset -euo pipefail\nhere=\"$(cd \"$(dirname \"${BASH_SOURCE[0]}\")\" && pwd)\"\nexport SUPER_DOLPHIN_PACKAGE_ROOT=\"$here\"\nexport PROJECT_ROOT=\"$here\"\nexport PATH=\"$here/bin:${PATH:-}\"\nexport SUPER_DOLPHIN_RUNTIME_MODE=packaged\nexport SUPER_DOLPHIN_PACKAGED_LAUNCHER=1\nexec \"$here/bin/agent-terminal\" \"$@\"\n"
}
