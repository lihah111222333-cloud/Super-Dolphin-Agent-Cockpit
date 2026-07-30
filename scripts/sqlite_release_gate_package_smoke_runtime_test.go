package main

import (
	"bytes"
	"database/sql"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
	"golang.org/x/sync/errgroup"
	_ "modernc.org/sqlite"
)

type sqliteReleaseGateUnsignedPackage struct {
	root        string
	entrypoint  string
	envFiles    []string
	launchers   []string
	binaryNames []string
}

type sqlitePackageRuntimeEvidence struct {
	schemaVersion int
	journalMode   string
	writeRows     int
}

type sqlitePackageCommandWaiter struct {
	group    errgroup.Group
	result   chan error
	done     chan struct{}
	joinOnce sync.Once
}

func TestSQLiteReleaseGatePackageSmokeRuntime(t *testing.T) {
	requireExplicitSQLitePackageSmokeSelection(t)
	stage := writeSQLiteReleaseGateUnsignedPackage(t)
	verifySQLiteReleaseGateUnsignedPackage(t, stage)
	assertSQLiteReleaseGateRealRuntimeEntrypoint(t, stage.entrypoint)
	home, oldPGData := prepareSQLiteReleaseGatePackageSmokeHome(t)
	output, evidence := runSQLiteReleaseGatePackageSmokeRuntime(t, stage, home, oldPGData)

	t.Logf(
		"package smoke runtime evidence: schema_version=%d journal_mode=%s write_rows=%d\n%s",
		evidence.schemaVersion,
		evidence.journalMode,
		evidence.writeRows,
		output,
	)
	assertSQLiteReleaseGatePackageSmokeState(t, filepath.Join(home, "super-dolphin.db"), oldPGData)
}

func requireExplicitSQLitePackageSmokeSelection(t *testing.T) {
	t.Helper()
	testRun := flag.Lookup("test.run")
	if testRun == nil || strings.TrimSpace(testRun.Value.String()) == "" {
		t.Skip("real packaged desktop smoke runs only when explicitly selected by G12")
	}
}

func assertSQLiteReleaseGateRealRuntimeEntrypoint(t *testing.T, entrypoint string) {
	t.Helper()
	info, err := buildinfo.ReadFile(entrypoint)
	if err != nil {
		t.Fatalf("read package smoke runtime entrypoint build identity: %v", err)
	}
	const want = "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/agent-terminal"
	if info.Path != want {
		t.Fatalf("package smoke entrypoint build path = %q, want real product entrypoint %q", info.Path, want)
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

func runSQLiteReleaseGatePackageSmokeRuntime(t *testing.T, stage sqliteReleaseGateUnsignedPackage, home, oldPGData string) (string, sqlitePackageRuntimeEvidence) {
	t.Helper()

	cmd := sqliteReleaseGatePackageSmokeCommand(t, stage)
	cmd.Env = sqliteReleaseGatePackageSmokeEnv(t, stage, home, oldPGData)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatalf("start unsigned package SQLite smoke through real product entrypoint: %v", err)
	}
	waiter := startSQLitePackageCommandWaiter(cmd)
	t.Cleanup(func() {
		waiter.cleanup(cmd.Process)
	})
	sqlitePath := filepath.Join(home, "super-dolphin.db")
	evidence := waitForSQLitePackageRuntimeEvidence(t, cmd, waiter, stage, sqlitePath, &output)
	terminateSQLitePackageRuntime(t, cmd, waiter, &output)
	return output.String(), evidence
}

func startSQLitePackageCommandWaiter(cmd *exec.Cmd) *sqlitePackageCommandWaiter {
	waiter := &sqlitePackageCommandWaiter{
		result: make(chan error, 1),
		done:   make(chan struct{}),
	}
	waiter.group.Go(func() error {
		waiter.result <- cmd.Wait()
		close(waiter.done)
		return nil
	})
	return waiter
}

func (waiter *sqlitePackageCommandWaiter) cleanup(process *os.Process) {
	select {
	case <-waiter.done:
	default:
		_ = process.Kill()
	}
	waiter.join()
}

func (waiter *sqlitePackageCommandWaiter) join() {
	waiter.joinOnce.Do(func() {
		_ = waiter.group.Wait()
	})
}

func (waiter *sqlitePackageCommandWaiter) await(timeout time.Duration) (error, bool) {
	select {
	case err := <-waiter.result:
		waiter.join()
		return err, true
	case <-time.After(timeout):
		return nil, false
	}
}

func waitForSQLitePackageRuntimeEvidence(
	t *testing.T,
	cmd *exec.Cmd,
	waiter *sqlitePackageCommandWaiter,
	stage sqliteReleaseGateUnsignedPackage,
	sqlitePath string,
	output *bytes.Buffer,
) sqlitePackageRuntimeEvidence {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		evidence, probeErr := inspectSQLitePackageRuntime(stage, sqlitePath)
		if probeErr == nil {
			return evidence
		}
		select {
		case err := <-waiter.result:
			waiter.join()
			t.Fatalf("real packaged entrypoint exited before healthy SQLite evidence: %v; last probe: %v\n%s", err, probeErr, output.String())
		default:
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			if _, ok := waiter.await(10 * time.Second); !ok {
				t.Fatal("real packaged entrypoint did not terminate after SQLite evidence timeout")
			}
			t.Fatalf("real packaged entrypoint did not produce healthy SQLite evidence before timeout: %v\n%s", probeErr, output.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func terminateSQLitePackageRuntime(
	t *testing.T,
	cmd *exec.Cmd,
	waiter *sqlitePackageCommandWaiter,
	output *bytes.Buffer,
) {
	t.Helper()
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("real packaged entrypoint exited before test-initiated termination: %v\n%s", err, output.String())
	}
	err, ok := waiter.await(10 * time.Second)
	if !ok {
		t.Fatal("real packaged entrypoint did not terminate after Process.Kill")
	}
	var exitErr *exec.ExitError
	if err == nil || !errors.As(err, &exitErr) {
		t.Fatalf("test-initiated package runtime termination returned unexpected wait result: %v", err)
	}
}

func inspectSQLitePackageRuntime(stage sqliteReleaseGateUnsignedPackage, sqlitePath string) (sqlitePackageRuntimeEvidence, error) {
	if info, err := os.Stat(sqlitePath); err != nil {
		return sqlitePackageRuntimeEvidence{}, fmt.Errorf("wait for packaged runtime SQLite creation: %w", err)
	} else if !info.Mode().IsRegular() {
		return sqlitePackageRuntimeEvidence{}, fmt.Errorf("packaged runtime SQLite path is not a regular file: %s", sqlitePath)
	}
	expectedVersion, err := latestPackagedSQLiteMigrationVersion(stage.root)
	if err != nil {
		return sqlitePackageRuntimeEvidence{}, err
	}
	db, err := sql.Open("sqlite", sqlitePath)
	if err != nil {
		return sqlitePackageRuntimeEvidence{}, fmt.Errorf("open packaged runtime SQLite: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		return sqlitePackageRuntimeEvidence{}, fmt.Errorf("configure packaged runtime SQLite probe: %w", err)
	}
	return readSQLitePackageRuntimeHealth(db, expectedVersion)
}

func readSQLitePackageRuntimeHealth(db *sql.DB, expectedVersion int) (sqlitePackageRuntimeEvidence, error) {
	var evidence sqlitePackageRuntimeEvidence
	if err := db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&evidence.schemaVersion); err != nil {
		return sqlitePackageRuntimeEvidence{}, fmt.Errorf("read packaged runtime schema version: %w", err)
	}
	if evidence.schemaVersion < expectedVersion {
		return sqlitePackageRuntimeEvidence{}, fmt.Errorf("packaged runtime schema version = %d, want >= %d", evidence.schemaVersion, expectedVersion)
	}
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&evidence.journalMode); err != nil {
		return sqlitePackageRuntimeEvidence{}, fmt.Errorf("read packaged runtime journal_mode: %w", err)
	}
	if !strings.EqualFold(evidence.journalMode, "wal") {
		return sqlitePackageRuntimeEvidence{}, fmt.Errorf("packaged runtime journal_mode = %q, want WAL", evidence.journalMode)
	}
	if err := writeSQLitePackageRuntimeProbe(db, &evidence); err != nil {
		return sqlitePackageRuntimeEvidence{}, err
	}
	return evidence, nil
}

func writeSQLitePackageRuntimeProbe(db *sql.DB, evidence *sqlitePackageRuntimeEvidence) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin packaged runtime write probe: %w", err)
	}
	defer tx.Rollback()
	for _, statement := range []string{
		"CREATE TABLE IF NOT EXISTS package_runtime_smoke_probe (id INTEGER PRIMARY KEY, evidence TEXT NOT NULL)",
		"DELETE FROM package_runtime_smoke_probe",
		"INSERT INTO package_runtime_smoke_probe(id, evidence) VALUES (1, 'real-entrypoint')",
	} {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("execute packaged runtime write probe: %w", err)
		}
	}
	if err := tx.QueryRow("SELECT COUNT(*) FROM package_runtime_smoke_probe WHERE evidence = 'real-entrypoint'").Scan(&evidence.writeRows); err != nil {
		return fmt.Errorf("read packaged runtime write probe: %w", err)
	}
	if evidence.writeRows != 1 {
		return fmt.Errorf("packaged runtime write probe rows = %d, want 1", evidence.writeRows)
	}
	if _, err := tx.Exec("DROP TABLE package_runtime_smoke_probe"); err != nil {
		return fmt.Errorf("remove packaged runtime write probe: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit packaged runtime write probe: %w", err)
	}
	return nil
}

func latestPackagedSQLiteMigrationVersion(root string) (int, error) {
	dir := filepath.Join(root, "internal", "platform", "db", "sqlite", "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("read packaged SQLite migrations: %w", err)
	}
	latest := 0
	for _, entry := range entries {
		extension := filepath.Ext(entry.Name())
		if entry.IsDir() || extension != ".sql" {
			continue
		}
		prefix := strings.TrimSuffix(entry.Name(), extension)
		versionText, _, cut := strings.Cut(prefix, "_")
		if !cut {
			return 0, fmt.Errorf("packaged SQLite migration %q has no numeric prefix separator", entry.Name())
		}
		version, err := strconv.Atoi(versionText)
		if err != nil {
			return 0, fmt.Errorf("parse packaged SQLite migration %q: %w", entry.Name(), err)
		}
		if version > latest {
			latest = version
		}
	}
	if latest == 0 {
		return 0, errors.New("packaged SQLite migrations contain no versioned SQL files")
	}
	return latest, nil
}

func sqliteReleaseGatePackageSmokeCommand(t *testing.T, stage sqliteReleaseGateUnsignedPackage) *exec.Cmd {
	t.Helper()
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		command = exec.Command("cmd", "/c", filepath.Base(stage.launchers[0]))
	case "darwin":
		command = exec.Command(stage.entrypoint)
	default:
		command = exec.Command(stage.launchers[0])
	}
	command.Dir = stage.root
	if os.Getenv("SUPER_DOLPHIN_TEST_BACKEND") != "remote-worker" || runtime.GOOS == "windows" {
		return command
	}
	xvfbRun := os.Getenv("SUPER_DOLPHIN_GATE_XVFB_RUN")
	if xvfbRun == "" || !filepath.IsAbs(xvfbRun) {
		t.Fatalf("remote worker package smoke requires absolute SUPER_DOLPHIN_GATE_XVFB_RUN")
	}
	if info, err := os.Stat(xvfbRun); err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		t.Fatalf("remote worker package smoke requires executable SUPER_DOLPHIN_GATE_XVFB_RUN %q: %v", xvfbRun, err)
	}
	arguments := append([]string{"--auto-servernum", "--server-args=-screen 0 1280x1024x24", command.Path}, command.Args[1:]...)
	wrapped := exec.Command(xvfbRun, arguments...)
	wrapped.Dir = command.Dir
	return wrapped
}

func TestSQLiteReleaseGatePackageSmokeCommandUsesWorkerXvfb(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows package smoke uses cmd launcher")
	}
	fixture := newSQLitePackageSmokeCommandFixture(t)
	assertSQLitePackageSmokeCommand(t, fixture)
	assertSQLitePackageSmokeRuntimePath(t, fixture.gitPath, fixture.xvfbRun)
	assertSQLitePackageSmokeMemoryOverride(t, fixture.stage)
}

type sqlitePackageSmokeCommandFixture struct {
	stage   sqliteReleaseGateUnsignedPackage
	command *exec.Cmd
	gitPath string
	xvfbRun string
}

func newSQLitePackageSmokeCommandFixture(t *testing.T) sqlitePackageSmokeCommandFixture {
	t.Helper()
	xvfbRun := filepath.Join(t.TempDir(), "xvfb-run")
	if err := os.WriteFile(xvfbRun, []byte("#!/bin/sh\n:\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	gitPath := filepath.Join(t.TempDir(), "git")
	if err := os.WriteFile(gitPath, []byte("#!/bin/sh\n:\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUPER_DOLPHIN_TEST_BACKEND", "remote-worker")
	t.Setenv("SUPER_DOLPHIN_GATE_GIT", gitPath)
	t.Setenv("SUPER_DOLPHIN_GATE_XVFB_RUN", xvfbRun)
	stage := sqliteReleaseGateUnsignedPackage{
		root:       t.TempDir(),
		entrypoint: "/stage/bin/agent-terminal",
		launchers:  []string{"/stage/run.sh"},
	}
	return sqlitePackageSmokeCommandFixture{
		stage:   stage,
		command: sqliteReleaseGatePackageSmokeCommand(t, stage),
		gitPath: gitPath,
		xvfbRun: xvfbRun,
	}

}

func assertSQLitePackageSmokeCommand(t *testing.T, fixture sqlitePackageSmokeCommandFixture) {
	t.Helper()
	if fixture.command.Path != fixture.xvfbRun {
		t.Fatalf("remote worker smoke command = %q, want fixed Xvfb runner %q", fixture.command.Path, fixture.xvfbRun)
	}
	target := fixture.stage.launchers[0]
	if runtime.GOOS == "darwin" {
		target = fixture.stage.entrypoint
	}
	if !slices.Equal(fixture.command.Args[1:], []string{"--auto-servernum", "--server-args=-screen 0 1280x1024x24", target}) {
		t.Fatalf("remote worker Xvfb arguments = %q", fixture.command.Args[1:])
	}
	if fixture.command.Dir != fixture.stage.root {
		t.Fatalf("remote worker Xvfb directory = %q, want %q", fixture.command.Dir, fixture.stage.root)
	}
}

func assertSQLitePackageSmokeRuntimePath(t *testing.T, gitPath, xvfbRun string) {
	t.Helper()
	path := sqlitePackageSmokeRuntimePath(t)
	for _, directory := range []string{filepath.Dir(gitPath), filepath.Dir(xvfbRun)} {
		if !slices.Contains(filepath.SplitList(path), directory) {
			t.Fatalf("remote worker runtime PATH = %q, missing %q", path, directory)
		}
	}
}

func assertSQLitePackageSmokeMemoryOverride(t *testing.T, stage sqliteReleaseGateUnsignedPackage) {
	t.Helper()
	home := t.TempDir()
	inheritedOverride := filepath.Join(t.TempDir(), "inherited-memory")
	t.Setenv("MULTI_AGENT_MEMORY_PATH_OVERRIDE", inheritedOverride)
	env := sqliteReleaseGatePackageSmokeEnv(t, stage, home, t.TempDir())
	wantOverride := "MULTI_AGENT_MEMORY_PATH_OVERRIDE=" + filepath.Join(home, "memory")
	if !slices.Contains(env, wantOverride) {
		t.Fatalf("remote worker package smoke env missing isolated memory override %q", wantOverride)
	}
	if slices.Contains(env, "MULTI_AGENT_MEMORY_PATH_OVERRIDE="+inheritedOverride) {
		t.Fatalf("remote worker package smoke env retained inherited memory override %q", inheritedOverride)
	}
}

func sqliteReleaseGatePackageSmokeEnv(t *testing.T, stage sqliteReleaseGateUnsignedPackage, home, oldPGData string) []string {
	t.Helper()
	skip := map[string]bool{
		contract.SQLitePathEnvKey:                   true,
		contract.InternalSQLitePathEnvKey:           true,
		"PROJECT_ROOT":                              true,
		"SUPER_DOLPHIN_PACKAGE_ROOT":                true,
		"SUPER_DOLPHIN_RUNTIME_MODE":                true,
		"SUPER_DOLPHIN_PACKAGED_LAUNCHER":           true,
		"DATABASE_URL":                              true,
		"POSTGRES_CONNECTION_STRING":                true,
		"RPC_ADDR":                                  true,
		"GO_AGENT_CTL_RPC_ADDR":                     true,
		"SUPER_DOLPHIN_DEPENDENCY_BOOTSTRAP":        true,
		"SUPER_DOLPHIN_DEPENDENCY_PROFILE":          true,
		"SUPER_DOLPHIN_CODEX_RELAY_BASE_URL":        true,
		"SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN": true,
		"SUPER_DOLPHIN_CODEX_RELAY_API_KEY":         true,
		"CODEX_HOME":                                true,
		"MULTI_AGENT_MEMORY_PATH_OVERRIDE":          true,
		"CLAUDE_COWORK_MEMORY_PATH_OVERRIDE":        true,
		"PATH":                                      true,
	}
	env := make([]string, 0, len(os.Environ())+8)
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if !skip[key] {
			env = append(env, kv)
		}
	}
	env = append(env,
		"PATH="+sqlitePackageSmokeRuntimePath(t),
		"SUPER_DOLPHIN_HOME="+home,
		contract.SQLitePathEnvKey+"="+filepath.Join(home, "super-dolphin.db"),
		"CODEX_HOME="+filepath.Join(home, "providers", "codex"),
		"MULTI_AGENT_MEMORY_PATH_OVERRIDE="+filepath.Join(home, "memory"),
		"SUPER_DOLPHIN_CODEX_RELAY_BASE_URL=http://127.0.0.1:1/v1",
		"SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN=package-smoke-bootstrap",
		"SUPER_DOLPHIN_PACKAGE_SMOKE_OLD_PG_DATA="+oldPGData,
		"SUPER_DOLPHIN_DEPENDENCY_BOOTSTRAP=desktop_host",
		"SUPER_DOLPHIN_DEPENDENCY_PROFILE=desktop_host",
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

func sqlitePackageSmokeRuntimePath(t *testing.T) string {
	t.Helper()
	path := os.Getenv("PATH")
	if strings.TrimSpace(path) == "" {
		t.Fatal("package smoke runtime PATH is empty")
	}
	if os.Getenv("SUPER_DOLPHIN_TEST_BACKEND") != "remote-worker" {
		return path
	}
	directories := make([]string, 0, 2)
	for _, key := range []string{"SUPER_DOLPHIN_GATE_GIT", "SUPER_DOLPHIN_GATE_XVFB_RUN"} {
		executable := os.Getenv(key)
		if !filepath.IsAbs(executable) {
			t.Fatalf("remote package smoke requires absolute %s", key)
		}
		info, err := os.Stat(executable)
		if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
			t.Fatalf("remote package smoke requires executable %s %q: %v", key, executable, err)
		}
		directories = append(directories, filepath.Dir(executable))
	}
	directories = append(directories, path)
	return strings.Join(directories, string(os.PathListSeparator))
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
	writeFile(t, filepath.Join(pkg.root, "lsp", "bin", executableForPackageSmoke("gopls")), unusedPackagePeerBody(runtime.GOOS, "gopls"), 0o755)
	writeFile(t, filepath.Join(pkg.root, "lsp", "lsp-manifest.json"), fmt.Sprintf(
		"{\"servers\":{\"gopls\":{\"path\":\"bin/%s\",\"languages\":[\"go\"]}}}\n",
		executableForPackageSmoke("gopls"),
	), 0o644)
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

	embeddedIndex := filepath.Join(scriptRepoRoot(t), "cmd", "agent-terminal", "web-dist", "index.html")
	if info, err := os.Stat(embeddedIndex); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("real product entrypoint requires generated frontend assets at %s; run npm ci and npm run build in frontend-app first", embeddedIndex)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatalf("create package smoke entrypoint dir: %v", err)
	}
	cmd := exec.Command("go", "build", "-o", dest, "./cmd/agent-terminal")
	cmd.Dir = scriptRepoRoot(t)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build package smoke runtime entrypoint: %v\n%s", err, output)
	}
}

func executableForPackageSmoke(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
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
