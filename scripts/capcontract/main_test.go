package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	capcontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/capcontract"
)

func TestParseRoots(t *testing.T) {
	got := parseRoots(" internal/contract , cmd/mcp-orch/tools ,, ")
	want := []string{"internal/contract", "cmd/mcp-orch/tools"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("parseRoots() = %#v, want %#v", got, want)
	}
}

func TestCapabilityRootsFlagDefaultsAndAllowsExplicitOverride(t *testing.T) {
	flags := flag.NewFlagSet("capcontract", flag.ContinueOnError)
	rootsFlag := newCapabilityRootsFlag(flags)
	defaultRoots := "internal/contract,internal/provider,cmd/mcp-orch/orchestration,cmd/mcp-orch/tools"
	if got := *rootsFlag; got != defaultRoots {
		t.Fatalf("--roots default = %q, want %q", got, defaultRoots)
	}
	pathRules := capcontract.PathRules{DefaultRoots: []string{"internal/derived-default"}}
	if got := selectedCapabilityRoots(pathRules, *rootsFlag, flagWasSet(flags, "roots")); !sameRoots(got, pathRules.DefaultRoots) {
		t.Fatalf("selected default roots = %#v, want %#v", got, pathRules.DefaultRoots)
	}

	const explicitRoots = "internal/contract,cmd/mcp-orch/tools"
	requireNoError(t, flags.Parse([]string{"--roots", explicitRoots}), "parse explicit --roots")
	if got := *rootsFlag; got != explicitRoots {
		t.Fatalf("--roots explicit override = %q, want %q", got, explicitRoots)
	}
	wantExplicitRoots := []string{"internal/contract", "cmd/mcp-orch/tools"}
	if got := selectedCapabilityRoots(pathRules, *rootsFlag, flagWasSet(flags, "roots")); !sameRoots(got, wantExplicitRoots) {
		t.Fatalf("selected explicit roots = %#v, want %#v", got, wantExplicitRoots)
	}
}

func TestCapabilityGeneratedAtUsesUTC(t *testing.T) {
	got := capabilityGeneratedAt(time.Date(2026, time.August, 1, 0, 30, 0, 0, time.FixedZone("UTC+14", 14*60*60)))
	if got != "2026-07-31" {
		t.Fatalf("capabilityGeneratedAt() = %q, want UTC date", got)
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

func sameRoots(got, want []string) bool {
	return strings.Join(got, ",") == strings.Join(want, ",")
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
