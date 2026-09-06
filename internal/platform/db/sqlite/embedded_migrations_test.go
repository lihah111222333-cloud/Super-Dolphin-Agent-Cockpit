package sqlite

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"testing"
)

func TestRunEmbeddedMigrationsFreshDatabaseIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db := openMigrationTestDB(t)

	if err := RunEmbeddedMigrations(ctx, db); err != nil {
		t.Fatalf("RunEmbeddedMigrations(fresh) error = %v", err)
	}
	assertMaxMigrationVersion(t, db, 136)
	assertMigrationMarkerCount(t, db, canonicalProviderRecovery123, 1)

	if err := RunEmbeddedMigrations(ctx, db); err != nil {
		t.Fatalf("RunEmbeddedMigrations(repeat) error = %v", err)
	}
	assertMaxMigrationVersion(t, db, 136)
	assertMigrationMarkerCount(t, db, canonicalProviderRecovery123, 1)
}

func TestRunEmbeddedMigrationsSupportsLegacy120Markers(t *testing.T) {
	for _, kind := range []legacy120FixtureKind{managedGenerationFixture, providerRecoveryFixture} {
		for _, targetExists := range []bool{false, true} {
			t.Run(string(kind)+"/target="+boolLabel(targetExists), func(t *testing.T) {
				t.Parallel()
				fixture := setupLegacy120CompatibilityFixture(t, kind, targetExists)
				if err := RunEmbeddedMigrations(fixture.ctx, fixture.db); err != nil {
					t.Fatalf("RunEmbeddedMigrations() error = %v", err)
				}
				if err := RunEmbeddedMigrations(fixture.ctx, fixture.db); err != nil {
					t.Fatalf("RunEmbeddedMigrations(repeat) error = %v", err)
				}
				assertMaxMigrationVersion(t, fixture.db, 136)
				assertLegacy120CompatibilityApplied(t, fixture, targetExists)
			})
		}
	}
}

func TestEmbeddedMigrationsExactlyMatchDiskSetAndContents(t *testing.T) {
	diskFS := os.DirFS("migrations")
	diskEntries, err := fs.ReadDir(diskFS, ".")
	if err != nil {
		t.Fatalf("read disk migrations: %v", err)
	}
	embeddedEntries, err := fs.ReadDir(embeddedMigrations, "migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}

	diskNames := migrationFSNames(diskEntries)
	embeddedNames := migrationFSNames(embeddedEntries)
	if !slices.Equal(diskNames, embeddedNames) {
		t.Fatalf("embedded migration set = %v, want disk set %v", embeddedNames, diskNames)
	}
	for _, name := range diskNames {
		diskBody, err := fs.ReadFile(diskFS, name)
		if err != nil {
			t.Fatalf("read disk migration %s: %v", name, err)
		}
		embeddedBody, err := fs.ReadFile(embeddedMigrations, path.Join("migrations", name))
		if err != nil {
			t.Fatalf("read embedded migration %s: %v", name, err)
		}
		if !bytes.Equal(diskBody, embeddedBody) {
			t.Fatalf("embedded migration %s differs from disk source", name)
		}
	}
}

func migrationFSNames(entries []fs.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		names = append(names, entry.Name())
	}
	return names
}
