//go:build darwin

// super-dolphin-ci: platform=darwin

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
