package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseRoots(t *testing.T) {
	got := parseRoots(" internal/contract , cmd/mcp-orch/tools ,, ")
	want := []string{"internal/contract", "cmd/mcp-orch/tools"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("parseRoots() = %#v, want %#v", got, want)
	}
}

func TestBuildCapabilityManifestAndCheck(t *testing.T) {
	repoRoot := writeCapabilityFixture(t)
	out := "docs/doc/codemap/capability-contract/capability_manifest.json"
	manifest, data, err := buildCapabilityManifest(repoRoot, []string{"internal/contract"}, out)
	requireNoError(t, err, "buildCapabilityManifest")
	assertTotalFunctions(t, manifest.Summary.TotalFunctions, 1)
	outPath := writeCapabilityOutput(t, repoRoot, out, data)
	requireNoError(t, checkCapabilityManifest(outPath, data), "checkCapabilityManifest")
	assertStaleCapabilityManifest(t, outPath)
}

func writeCapabilityFixture(t *testing.T) string {
	t.Helper()
	repoRoot := t.TempDir()
	pkgDir := filepath.Join(repoRoot, "internal", "contract")
	requireNoError(t, os.MkdirAll(pkgDir, 0o755), "mkdir fixture")
	writeFixtureFile(t, filepath.Join(repoRoot, "go.mod"), "module fixture\n")
	writeFixtureFile(t, filepath.Join(repoRoot, "CLAUDE.md"), "# fixture\n")
	writeFixtureFile(t, filepath.Join(pkgDir, "contract.go"), "package contract\nfunc Exposed() error { return nil }\n")
	return repoRoot
}

func writeFixtureFile(t *testing.T, path, body string) {
	t.Helper()
	requireNoError(t, os.WriteFile(path, []byte(body), 0o644), "write "+filepath.Base(path))
}

func writeCapabilityOutput(t *testing.T, repoRoot, out string, data []byte) string {
	t.Helper()
	outPath := filepath.Join(repoRoot, filepath.FromSlash(out))
	requireNoError(t, os.MkdirAll(filepath.Dir(outPath), 0o755), "mkdir output")
	requireNoError(t, os.WriteFile(outPath, data, 0o644), "write output")
	return outPath
}

func assertTotalFunctions(t *testing.T, got, want int) {
	t.Helper()
	if got != want {
		t.Fatalf("TotalFunctions = %d, want %d", got, want)
	}
}

func assertStaleCapabilityManifest(t *testing.T, outPath string) {
	t.Helper()
	if err := checkCapabilityManifest(outPath, []byte("{}\n")); err == nil || !strings.Contains(err.Error(), "differs") {
		t.Fatalf("checkCapabilityManifest stale error = %v, want differs", err)
	}
}

func requireNoError(t *testing.T, err error, op string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s error = %v", op, err)
	}
}
