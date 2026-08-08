package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/capcontract"
	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestCapabilityContractCLIRefreshesAndChecksExactTree(t *testing.T) {
	repository := newCapabilityContractCLIRepository(t)
	enterProjectMapRepository(t, repository)

	initialTree := projectMapTestGit(t, repository, "write-tree")
	requireCapabilityContractCLICode(t, gatecontract.ExitOK, "initial refresh", "refresh", "--tree", initialTree)
	projectMapTestGit(t, repository, "add", "docs/doc/codemap/capability-contract/capability_manifest.json")
	refreshedTree := projectMapTestGit(t, repository, "write-tree")
	requireCapabilityContractCLICode(t, gatecontract.ExitOK, "refreshed check", "check", "--tree", refreshedTree)

	// Unstaged source and a candidate generator marker must not affect an exact-tree check.
	contractPath := filepath.Join(repository, "internal", "contract", "contract.go")
	appendCapabilityFile(t, contractPath, "\nfunc UnstagedOnly() error { return nil }\n")
	markerPath := filepath.Join(repository, "candidate-capcontract-generator-executed")
	appendCapabilityFile(t, filepath.Join(repository, "scripts", "capcontract", "main.go"), fmt.Sprintf("\nfunc init() { _ = os.WriteFile(%q, []byte(\"executed\"), 0o600) }\n", markerPath))
	requireCapabilityContractCLICode(t, gatecontract.ExitOK, "exact-tree check with unstaged source", "check", "--tree", refreshedTree)
	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Fatalf("candidate capability generator was executed: %v", err)
	}

	appendCapabilityFile(t, contractPath, "\nfunc StagedOnly() error { return nil }\n")
	projectMapTestGit(t, repository, "add", "internal/contract/contract.go")
	driftedTree := projectMapTestGit(t, repository, "write-tree")
	requireCapabilityContractCLICode(t, gatecontract.ExitGateViolation, "drifted check", "check", "--tree", driftedTree)
	requireCapabilityContractCLICode(t, gatecontract.ExitOK, "drifted refresh", "refresh", "--tree", driftedTree)
	projectMapTestGit(t, repository, "add", "docs/doc/codemap/capability-contract/capability_manifest.json")
	postRefreshTree := projectMapTestGit(t, repository, "write-tree")
	requireCapabilityContractCLICode(t, gatecontract.ExitOK, "post-refresh check", "check", "--tree", postRefreshTree)
	if postRefreshTree == driftedTree {
		t.Fatal("capability-contract refresh did not produce a new staged tree")
	}
}

func TestCapabilityContractCLIDoesNotCreateRepositoryVarWithAbsoluteTMPDIR(t *testing.T) {
	repositoryRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("get package repository root: %v", err)
	}
	repository := newCapabilityContractCLIRepository(t)
	temporaryRoot := filepath.Join(repository, "tmpdir")
	if err := os.MkdirAll(temporaryRoot, 0o700); err != nil {
		t.Fatalf("create absolute TMPDIR fixture: %v", err)
	}
	t.Setenv("TMPDIR", temporaryRoot)
	enterProjectMapRepository(t, repository)
	tree := projectMapTestGit(t, repository, "write-tree")
	requireCapabilityContractCLICode(t, gatecontract.ExitOK, "absolute TMPDIR refresh", "refresh", "--tree", tree)
	if _, err := os.Stat(filepath.Join(repositoryRoot, "var")); !os.IsNotExist(err) {
		t.Fatalf("capability-contract CLI leaked relative TMPDIR into repository: %v", err)
	}
}

func TestCapabilityContractCLIRefreshUsesWorktreeGeneratedAtWhenExactTreeOmitsManifest(t *testing.T) {
	repository := newCapabilityContractCLIRepository(t)
	enterProjectMapRepository(t, repository)
	manifestPath := "docs/doc/codemap/capability-contract/capability_manifest.json"
	projectMapTestGit(t, repository, "update-index", "--force-remove", manifestPath)
	tree := projectMapTestGit(t, repository, "write-tree")
	requireCapabilityContractCLICode(t, gatecontract.ExitOK, "refresh tree without manifest", "refresh", "--tree", tree)
	manifest, err := capcontract.LoadManifest(filepath.Join(repository, filepath.FromSlash(manifestPath)))
	if err != nil {
		t.Fatalf("load refreshed manifest: %v", err)
	}
	if manifest.GeneratedAt != "2026-08-07" {
		t.Fatalf("refreshed manifest generated_at = %q, want worktree date", manifest.GeneratedAt)
	}
}

func TestCapabilityContractCLIRefreshReplacesOnlyManagedOutput(t *testing.T) {
	repository := newCapabilityContractCLIRepository(t)
	enterProjectMapRepository(t, repository)
	initialTree := projectMapTestGit(t, repository, "write-tree")
	requireCapabilityContractCLICode(t, gatecontract.ExitOK, "initial refresh", "refresh", "--tree", initialTree)
	manifestPath := filepath.Join(repository, "docs", "doc", "codemap", "capability-contract", "capability_manifest.json")
	projectMapTestGit(t, repository, "add", "docs/doc/codemap/capability-contract/capability_manifest.json")
	exactTree := projectMapTestGit(t, repository, "write-tree")
	stagedManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read staged manifest: %v", err)
	}
	if err := os.WriteFile(manifestPath, append(bytes.Clone(stagedManifest), []byte("unstaged overlay\n")...), 0o644); err != nil {
		t.Fatalf("write unstaged manifest overlay: %v", err)
	}
	untracked := filepath.Join(repository, "untracked-user.txt")
	if err := os.WriteFile(untracked, []byte("user work\n"), 0o644); err != nil {
		t.Fatalf("write untracked user file: %v", err)
	}
	requireCapabilityContractCLICode(t, gatecontract.ExitOK, "overlay refresh", "refresh", "--tree", exactTree)
	gotManifest, err := os.ReadFile(manifestPath)
	if err != nil || !bytes.Equal(gotManifest, stagedManifest) {
		t.Fatalf("refresh did not restore exact-tree manifest: equal=%v err=%v", bytes.Equal(gotManifest, stagedManifest), err)
	}
	gotUserWork, err := os.ReadFile(untracked)
	if err != nil || string(gotUserWork) != "user work\n" {
		t.Fatalf("refresh changed untracked user file: data=%q err=%v", gotUserWork, err)
	}
}

func TestCapabilityContractCLIRejectsMutableTreeReference(t *testing.T) {
	repository := newCapabilityContractCLIRepository(t)
	enterProjectMapRepository(t, repository)
	code, _, stderr := executeCapabilityContractCLI("check", "--tree", "HEAD")
	if code != int(gatecontract.ExitSourceMismatch) || !strings.Contains(stderr, "40- or 64-hex") {
		t.Fatalf("mutable tree code=%d stderr=%q", code, stderr)
	}
}

func executeCapabilityContractCLI(args ...string) (int, string, string) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	command := append([]string{"capability-contract"}, args...)
	code := runCLI(command, stdout, stderr)
	return code, stdout.String(), stderr.String()
}

func requireCapabilityContractCLICode(t *testing.T, want gatecontract.ExitCode, operation string, args ...string) {
	t.Helper()
	code, _, stderr := executeCapabilityContractCLI(args...)
	if code != int(want) {
		t.Fatalf("%s code=%d stderr=%q", operation, code, stderr)
	}
}

func newCapabilityContractCLIRepository(t *testing.T) string {
	t.Helper()
	repository := t.TempDir()
	projectMapTestGit(t, repository, "init", "-q")
	projectMapTestGit(t, repository, "config", "user.name", "Capability Contract CLI Test")
	projectMapTestGit(t, repository, "config", "user.email", "capcontract-cli@example.invalid")
	files := map[string]string{
		"CLAUDE.md":                     "Capability contract CLI fixture.\n",
		"go.mod":                        "module example.invalid/capability-contract-cli\n\ngo 1.26.5\n",
		"scripts/capcontract/main.go":   "package main\n\nfunc defaultCapabilityRoots() []string { return []string{\"internal/contract\"} }\n",
		"internal/contract/contract.go": "package contract\n\nfunc Exposed() error { return nil }\n",
	}
	for relative, content := range files {
		path := filepath.Join(repository, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create fixture parent %s: %v", relative, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write fixture file %s: %v", relative, err)
		}
	}
	manifest, err := capcontract.Scan(capcontract.ScanOptions{
		RepoRoot:    repository,
		Roots:       []string{"internal/contract"},
		GeneratedAt: "2026-08-07",
	})
	if err != nil {
		t.Fatalf("scan capability fixture: %v", err)
	}
	data, err := capcontract.MarshalManifest(manifest)
	if err != nil {
		t.Fatalf("marshal capability fixture: %v", err)
	}
	manifestPath := filepath.Join(repository, filepath.FromSlash("docs/doc/codemap/capability-contract/capability_manifest.json"))
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatalf("create manifest parent: %v", err)
	}
	if err := os.WriteFile(manifestPath, data, 0o644); err != nil {
		t.Fatalf("write capability fixture manifest: %v", err)
	}
	projectMapTestGit(t, repository, "add", "-A")
	return repository
}

func appendCapabilityFile(t *testing.T, path, suffix string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open %s for append: %v", path, err)
	}
	if _, err := file.WriteString(suffix); err != nil {
		_ = file.Close()
		t.Fatalf("append %s: %v", path, err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close %s: %v", path, err)
	}
}
