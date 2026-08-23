package remotecicli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDetectedRepositoryModuleIdentityOnlyChangesWithModuleFiles(t *testing.T) {
	root := newDetectedRepositoryFixture(t, "module-identity")
	writeDetectedModuleFile(t, root, "go.mod", "module example.invalid/module-identity\n\ngo 1.26.5\n")
	writeDetectedModuleFile(t, root, "go.sum", "example.invalid/dependency v1.0.0 h1:AAAA\n")
	commitDetectedFixture(t, root, "module identity")
	firstRepository, err := DetectRepository(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	first, err := deriveDetectedRepositoryModuleIdentity(context.Background(), firstRepository)
	if err != nil {
		t.Fatal(err)
	}
	writeDetectedModuleFile(t, root, "unrelated.txt", "source-only drift")
	commitDetectedFixture(t, root, "source drift")
	secondRepository, err := DetectRepository(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := deriveDetectedRepositoryModuleIdentity(context.Background(), secondRepository)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("module identity changed for source-only drift: %q != %q", first, second)
	}
	writeDetectedModuleFile(t, root, "go.mod", "module example.invalid/module-identity\n\ngo 1.26.5\n\nrequire example.invalid/dependency v1.0.0\n")
	commitDetectedFixture(t, root, "module drift")
	thirdRepository, err := DetectRepository(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	third, err := deriveDetectedRepositoryModuleIdentity(context.Background(), thirdRepository)
	if err != nil {
		t.Fatal(err)
	}
	if third == second {
		t.Fatal("module identity did not change with go.mod")
	}
}

func writeDetectedModuleFile(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
