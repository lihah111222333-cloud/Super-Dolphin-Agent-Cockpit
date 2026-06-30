package archtest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestArchGuardsFailClosedOnMalformedGoFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	domainFile := writeMalformedGoFile(t, root, "internal/domain/broken.go")
	adminFile := writeMalformedGoFile(t, root, "internal/admin/broken.go")
	domainInfo := statGuardFixture(t, domainFile)

	if _, err := onionArchitectureViolationsForFile(domainFile, "internal/domain/broken.go"); err == nil {
		t.Fatal("onion architecture guard swallowed malformed Go file")
	}
	if _, err := crossDomainFileViolations(adminFile, "internal/admin/broken.go"); err == nil {
		t.Fatal("cross-domain guard swallowed malformed Go file")
	}
	if err := deterministicTimeMalformedFixtureError(root, domainFile, domainInfo); err == nil {
		t.Fatal("deterministic time guard swallowed malformed Go file")
	}
	if _, err := countErrorStringMatchesInFile(domainFile); err == nil {
		t.Fatal("error string match guard swallowed malformed Go file")
	}
	if _, _, err := nakedGoroutineViolationForFile(root, domainFile, map[string]struct{}{}); err == nil {
		t.Fatal("naked goroutine guard swallowed malformed Go file")
	}
	if _, err := scatteredDecimalViolationsForFile(root, domainFile); err == nil {
		t.Fatal("scattered decimal guard swallowed malformed Go file")
	}
	if err := visitStatelessGuardPath(root, map[string]bool{}, &[]string{}, domainFile, domainInfo, nil); err == nil {
		t.Fatal("stateless guard swallowed malformed Go file")
	}
	if _, err := structuredLogPathViolations(root, domainFile, domainInfo, nil, map[string]bool{}); err == nil {
		t.Fatal("structured log guard swallowed malformed Go file")
	}
}

func deterministicTimeMalformedFixtureError(root, path string, info os.FileInfo) error {
	scan := deterministicTimeScan{
		root:        root,
		skipDirs:    map[string]bool{},
		allowedDirs: deterministicTimeAllowedDirs(),
		violations:  &[]string{},
	}
	return scan.visit(path, info, nil)
}

func writeMalformedGoFile(t *testing.T, root, rel string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir malformed fixture: %v", err)
	}
	if err := os.WriteFile(path, []byte("package broken\nimport (\n"), 0o644); err != nil {
		t.Fatalf("write malformed fixture: %v", err)
	}
	return path
}

func statGuardFixture(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat malformed fixture: %v", err)
	}
	return info
}
