package contract

import (
	"os"
	"path/filepath"
	"testing"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

const testInternalSQLitePathEnvKey = "SUPER_DOLPHIN_INTERNAL_SQLITE_PATH"

func TestBuildManifestPassesModelRegistryEnvFromProcessToStdioBinaries(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_MODEL_REGISTRY", "/bundle/models.yaml")

	manifest := BuildManifest(dto.ManifestContext{
		BinaryDir:     "/bundle/bin",
		TransportMode: dto.ManifestTransportStdioOnly,
	})

	if len(manifest.Binaries) == 0 {
		t.Fatal("BuildManifest() returned no binaries")
	}
	for _, bin := range manifest.Binaries {
		if got := bin.Env["SUPER_DOLPHIN_MODEL_REGISTRY"]; got != "/bundle/models.yaml" {
			t.Fatalf("binary %s SUPER_DOLPHIN_MODEL_REGISTRY = %q, want /bundle/models.yaml", bin.Name, got)
		}
	}
}

func TestBuildManifestPassesSidecarRuntimeContractToStdioBinaries(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "dev")
	t.Setenv("SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR", "/work/repo")

	manifest := BuildManifest(dto.ManifestContext{
		BinaryDir:     "/bundle/bin",
		TransportMode: dto.ManifestTransportStdioOnly,
	})

	if len(manifest.Binaries) == 0 {
		t.Fatal("BuildManifest() returned no binaries")
	}
	for _, bin := range manifest.Binaries {
		if got := bin.Env["SUPER_DOLPHIN_RUNTIME_MODE"]; got != "dev" {
			t.Fatalf("binary %s SUPER_DOLPHIN_RUNTIME_MODE = %q, want dev", bin.Name, got)
		}
		if got := bin.Env["SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR"]; got != "/work/repo" {
			t.Fatalf("binary %s SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR = %q, want /work/repo", bin.Name, got)
		}
	}
}

func TestBuildManifestForcesProductionDependencyProfileForCoreStdioBinaries(t *testing.T) {
	manifest := BuildManifest(dto.ManifestContext{
		BinaryDir: "/bundle/bin",
		Env: map[string]string{
			"SUPER_DOLPHIN_DEPENDENCY_PROFILE": "desktop_host",
		},
		TransportMode: dto.ManifestTransportStdioOnly,
	})

	if len(manifest.Binaries) == 0 {
		t.Fatal("BuildManifest() returned no binaries")
	}
	for _, bin := range manifest.Binaries {
		if got := bin.Env["SUPER_DOLPHIN_DEPENDENCY_PROFILE"]; got != string(DependencyProfileProduction) {
			t.Fatalf("binary %s SUPER_DOLPHIN_DEPENDENCY_PROFILE = %q, want %q", bin.Name, got, DependencyProfileProduction)
		}
	}
}

func TestBuildManifestStripsDatabaseEnvironmentFromStdioBinaries(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://process@127.0.0.1:5432/super_dolphin?sslmode=disable")
	t.Setenv("POSTGRES_CONNECTION_STRING", "postgres://compat@127.0.0.1:5432/super_dolphin?sslmode=disable")
	t.Setenv("SUPER_DOLPHIN_SQLITE_PATH", "/Users/alice/private/super-dolphin.db")
	t.Setenv(testInternalSQLitePathEnvKey, "/Users/alice/private/internal.db")

	manifest := BuildManifest(dto.ManifestContext{
		Env: map[string]string{
			"DATABASE_URL":               "postgres://ctx@127.0.0.1:5432/super_dolphin?sslmode=disable",
			"POSTGRES_CONNECTION_STRING": "postgres://ctx-compat@127.0.0.1:5432/super_dolphin?sslmode=disable",
			"SUPER_DOLPHIN_SQLITE_PATH":  "/Users/alice/private/ctx.db",
			testInternalSQLitePathEnvKey: "/Users/alice/private/ctx-internal.db",
			"FOO":                        "bar",
		},
		TransportMode: dto.ManifestTransportStdioOnly,
	})

	if len(manifest.Binaries) == 0 {
		t.Fatal("BuildManifest() returned no binaries")
	}
	for _, bin := range manifest.Binaries {
		for _, key := range []string{"DATABASE_URL", "POSTGRES_CONNECTION_STRING", "SUPER_DOLPHIN_SQLITE_PATH", testInternalSQLitePathEnvKey} {
			if _, ok := bin.Env[key]; ok {
				t.Fatalf("binary %s leaked %s in env %#v", bin.Name, key, bin.Env)
			}
		}
		if got := bin.Env["FOO"]; got != "bar" {
			t.Fatalf("binary %s FOO = %q, want bar", bin.Name, got)
		}
	}
}

func TestHasManifestMigrationsDirUsesSQLiteLayout(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "platform", "db", "sqlite", "migrations"), 0o755); err != nil {
		t.Fatal(err)
	}

	if !hasManifestMigrationsDir(root) {
		t.Fatal("hasManifestMigrationsDir() = false, want true for SQLite migrations layout")
	}
}

func TestHasManifestMigrationsDirRejectsLegacyTopLevelMigrations(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "migrations"), 0o755); err != nil {
		t.Fatal(err)
	}

	if hasManifestMigrationsDir(root) {
		t.Fatal("hasManifestMigrationsDir() = true, want false for legacy top-level migrations")
	}
}

func TestInferManifestProjectRootFromBinaryDirUsesSQLiteLayout(t *testing.T) {
	root := t.TempDir()
	binaryDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binaryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "internal", "platform", "db", "sqlite", "migrations"), 0o755); err != nil {
		t.Fatal(err)
	}

	if got := inferManifestProjectRootFromBinaryDir(binaryDir); got != root {
		t.Fatalf("inferManifestProjectRootFromBinaryDir() = %q, want %q", got, root)
	}
}

func TestInferManifestProjectRootFromBinaryDirRejectsLegacyTopLevelMigrations(t *testing.T) {
	root := t.TempDir()
	binaryDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binaryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "migrations"), 0o755); err != nil {
		t.Fatal(err)
	}

	if got := inferManifestProjectRootFromBinaryDir(binaryDir); got != "" {
		t.Fatalf("inferManifestProjectRootFromBinaryDir() = %q, want empty for legacy top-level migrations", got)
	}
}
