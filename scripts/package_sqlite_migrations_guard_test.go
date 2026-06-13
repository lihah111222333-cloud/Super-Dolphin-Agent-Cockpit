package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const sqliteMigrationsRelPath = "internal/platform/db/sqlite/migrations"

func sqliteMigrationsPath(root string, elems ...string) string {
	parts := append([]string{root, filepath.FromSlash(sqliteMigrationsRelPath)}, elems...)
	return filepath.Join(parts...)
}

func assertPackageScriptCopySQLiteMigrationsFailsFast(t *testing.T, scriptPath, goos string) {
	t.Helper()

	root := t.TempDir()
	stage := t.TempDir()
	output, err := runPackageCopySQLiteMigrations(t, scriptPath, goos, root, stage)
	if err == nil {
		t.Fatalf("%s copy_sqlite_migrations succeeded with missing source, want failure:\n%s", scriptPath, output)
	}
	if !strings.Contains(output, "missing SQLite migrations directory") {
		t.Fatalf("%s missing-source output = %q, want SQLite migrations directory failure", scriptPath, output)
	}

	root = t.TempDir()
	stage = t.TempDir()
	if err := os.MkdirAll(sqliteMigrationsPath(root), 0o755); err != nil {
		t.Fatalf("mkdir empty sqlite migrations dir: %v", err)
	}
	output, err = runPackageCopySQLiteMigrations(t, scriptPath, goos, root, stage)
	if err == nil {
		t.Fatalf("%s copy_sqlite_migrations succeeded with empty source, want failure:\n%s", scriptPath, output)
	}
	if !strings.Contains(output, "missing SQLite migration files under") {
		t.Fatalf("%s empty-source output = %q, want SQLite migration files failure", scriptPath, output)
	}

	root = t.TempDir()
	stage = t.TempDir()
	writeFile(t, sqliteMigrationsPath(root, "001_baseline.sql"), "select 1;\n", 0o644)
	output, err = runPackageCopySQLiteMigrations(t, scriptPath, goos, root, stage)
	if err != nil {
		t.Fatalf("%s copy_sqlite_migrations failed with populated source: %v\n%s", scriptPath, err, output)
	}
	if _, err := os.Stat(sqliteMigrationsPath(stage, "001_baseline.sql")); err != nil {
		t.Fatalf("%s did not stage SQLite migration under runtime path: %v", scriptPath, err)
	}
	if _, err := os.Stat(filepath.Join(stage, "migrations", "001_baseline.sql")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s staged legacy top-level migrations file, stat err = %v", scriptPath, err)
	}
}

func runPackageCopySQLiteMigrations(t *testing.T, scriptPath, goos, root, bundleRoot string) (string, error) {
	t.Helper()

	script := readScript(t, scriptPath)
	harness := scriptPrefixThroughFunction(t, script, "copy_sqlite_migrations") + "\nroot=" + bashQuote(bashArg("", root)) + "\ncopy_sqlite_migrations " + bashQuote(bashArg("", bundleRoot)) + "\n"
	harnessPath := filepath.Join(t.TempDir(), filepath.Base(scriptPath))
	if err := os.WriteFile(harnessPath, []byte(harness), 0o700); err != nil {
		t.Fatalf("write %s copy_sqlite_migrations harness: %v", scriptPath, err)
	}
	cmd := exec.Command("bash", bashArg("", harnessPath))
	cmd.Dir = "."
	cmd.Env = packageScriptValidationEnv(t, goos, nil)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func TestPackageMacOSScriptCopiesSQLiteRuntimeMigrations(t *testing.T) {
	script := readScript(t, "package_macos.sh")

	assertScriptContains(t, script, "copy_sqlite_migrations()")
	assertScriptContains(t, script, "local src=\"$root/internal/platform/db/sqlite/migrations\"")
	assertScriptContains(t, script, "local dest=\"$bundle_root/internal/platform/db/sqlite/migrations\"")
	assertScriptContains(t, script, "missing SQLite migrations directory: $src")
	assertScriptContains(t, script, "missing SQLite migration files under $src")
	assertScriptContains(t, script, "copy_sqlite_migrations \"$resources\"")
	assertScriptDoesNotContain(t, script, "cp -R \"$root/migrations\" \"$resources/migrations\"")
	assertScriptOrder(t, script, "copy_sqlite_migrations \"$resources\"", "write_runtime_manifest \"$resources\" \"$platform\"")
	assertPackageScriptCopySQLiteMigrationsFailsFast(t, "package_macos.sh", "darwin")
}

func TestVerifyPackagedAppMacOSRejectsOnlyLegacyTopLevelMigrations(t *testing.T) {
	app := writeMinimalPackagedMacOSApp(t)
	resources := filepath.Join(app, "Contents", "Resources")
	writeDefaultMacOSRuntimeManifest(t, resources)
	if err := os.RemoveAll(sqliteMigrationsPath(resources)); err != nil {
		t.Fatalf("remove SQLite migrations: %v", err)
	}
	writeFile(t, filepath.Join(resources, "migrations", "0001.sql"), "select 1;\n", 0o644)

	output, err := runVerifyPackagedAppMacOS(t, app)
	if err == nil {
		t.Fatalf("expected packaged app verifier to reject legacy-only migrations, got success:\n%s", output)
	}
	if !strings.Contains(output, "missing SQLite migration files under") {
		t.Fatalf("expected missing SQLite migrations error, got:\n%s", output)
	}
}
