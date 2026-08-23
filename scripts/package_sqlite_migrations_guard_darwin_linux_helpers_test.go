//go:build darwin || linux

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func assertPackageScriptCopySQLiteMigrationsFailsFast(t *testing.T, scriptPath, goos string) {
	t.Helper()
	root, stage := t.TempDir(), t.TempDir()
	output, err := runPackageCopySQLiteMigrations(t, scriptPath, goos, root, stage)
	if err == nil || !strings.Contains(output, "missing SQLite migrations directory") {
		t.Fatalf("%s missing-source result err=%v output=%q", scriptPath, err, output)
	}

	root, stage = t.TempDir(), t.TempDir()
	if err := os.MkdirAll(sqliteMigrationsPath(root), 0o755); err != nil {
		t.Fatalf("mkdir empty sqlite migrations dir: %v", err)
	}
	output, err = runPackageCopySQLiteMigrations(t, scriptPath, goos, root, stage)
	if err == nil || !strings.Contains(output, "missing SQLite migration files under") {
		t.Fatalf("%s empty-source result err=%v output=%q", scriptPath, err, output)
	}

	root, stage = t.TempDir(), t.TempDir()
	writeFile(t, sqliteMigrationsPath(root, "001_baseline.sql"), "select 1;\n", 0o644)
	output, err = runPackageCopySQLiteMigrations(t, scriptPath, goos, root, stage)
	if err != nil {
		t.Fatalf("%s populated-source failure: %v\n%s", scriptPath, err, output)
	}
	if _, err := os.Stat(sqliteMigrationsPath(stage, "001_baseline.sql")); err != nil {
		t.Fatalf("%s did not stage SQLite migration: %v", scriptPath, err)
	}
	if _, err := os.Stat(filepath.Join(stage, "migrations", "001_baseline.sql")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s staged legacy top-level migration, stat err=%v", scriptPath, err)
	}
}

func runPackageCopySQLiteMigrations(t *testing.T, scriptPath, goos, root, bundleRoot string) (string, error) {
	t.Helper()
	script := readScript(t, scriptPath)
	harness := scriptPrefixThroughFunction(t, script, "copy_sqlite_migrations") + "\nroot=" + bashQuote(bashArg("", root)) + "\ncopy_sqlite_migrations " + bashQuote(bashArg("", bundleRoot)) + "\n"
	harnessPath := filepath.Join(t.TempDir(), filepath.Base(scriptPath))
	if err := os.WriteFile(harnessPath, []byte(harness), 0o700); err != nil {
		t.Fatalf("write %s migration harness: %v", scriptPath, err)
	}
	cmd := exec.Command("bash", bashArg("", harnessPath))
	cmd.Dir = "."
	cmd.Env = packageScriptValidationEnv(t, goos, nil)
	output, err := cmd.CombinedOutput()
	return string(output), err
}
