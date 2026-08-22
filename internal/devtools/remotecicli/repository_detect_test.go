package remotecicli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteportable"
)

func TestDetectRepositoryAutomaticallyBindsCurrentGitIdentity(t *testing.T) {
	root := newDetectedRepositoryFixture(t, "repository-alpha")
	nested := filepath.Join(root, "nested", "work")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("create nested path: %v", err)
	}
	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	if err := os.Chdir(nested); err != nil {
		t.Fatalf("chdir nested: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	detected, err := DetectRepository(context.Background(), "")
	if err != nil {
		t.Fatalf("DetectRepository() error = %v", err)
	}
	assertDetectedRepositoryIdentity(t, detected, root, "repository-alpha")
}

func assertDetectedRepositoryIdentity(t *testing.T, detected DetectedRepository, root, repositoryID string) {
	t.Helper()
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if detected.Root != resolvedRoot || detected.RepositoryID != repositoryID || detected.ObjectFormat != "sha1" {
		t.Fatalf("detected identity root=%q want=%q id=%q want=%q format=%q", detected.Root, root, detected.RepositoryID, repositoryID, detected.ObjectFormat)
	}
	if len(detected.CommitSHA) != 40 || len(detected.TreeSHA) != 40 || detected.ManifestBlobSHA256 == "" || detected.ManifestABIDigest == "" {
		t.Fatalf("detected immutable fields = %#v", detected)
	}
}

func TestDetectRepositoryDistinguishesRepositoriesWithoutNameRules(t *testing.T) {
	first := newDetectedRepositoryFixture(t, "repository-one")
	second := newDetectedRepositoryFixture(t, "repository-two")
	firstDetected, err := DetectRepository(context.Background(), first)
	if err != nil {
		t.Fatalf("DetectRepository(first) error = %v", err)
	}
	secondDetected, err := DetectRepository(context.Background(), second)
	if err != nil {
		t.Fatalf("DetectRepository(second) error = %v", err)
	}
	if firstDetected.RepositoryID == secondDetected.RepositoryID || firstDetected.TreeSHA == secondDetected.TreeSHA {
		t.Fatalf("repository identities collapsed: first=%#v second=%#v", firstDetected, secondDetected)
	}
}

// TestDetectRepositoryReadsManifestFromExactTree 拒绝被 assume-unchanged 隐藏的工作树 manifest 影子。
func TestDetectRepositoryReadsManifestFromExactTree(t *testing.T) {
	root := newDetectedRepositoryFixture(t, "tree-owned")
	manifestPath := filepath.Join(root, filepath.FromSlash(portableWorkloadManifestRelativePath))
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := remoteportable.DecodePortableManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Target.RepositoryID = "worktree-shadow"
	manifest.Modules[0].ModulePath = "example.invalid/worktree-shadow"
	shadow, err := remoteportable.PortableManifestCanonicalEncode(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, shadow, 0o644); err != nil {
		t.Fatal(err)
	}
	runDetectedFixtureGit(t, root, "update-index", "--assume-unchanged", portableWorkloadManifestRelativePath)
	detected, err := DetectRepository(context.Background(), root)
	if err != nil {
		t.Fatalf("DetectRepository() error = %v", err)
	}
	if detected.RepositoryID != "tree-owned" {
		t.Fatalf("repository ID = %q, want exact-tree identity", detected.RepositoryID)
	}
}

func TestDetectRepositoryRejectsDirtyMissingAndSymlinkInputs(t *testing.T) {
	root := newDetectedRepositoryFixture(t, "repository-drift")
	if err := os.WriteFile(filepath.Join(root, "untracked.txt"), []byte("dirty"), 0o600); err != nil {
		t.Fatalf("write untracked file: %v", err)
	}
	if _, err := DetectRepository(context.Background(), root); err == nil || !strings.Contains(err.Error(), "clean") {
		t.Fatalf("dirty detection error = %v", err)
	}
	clean := newDetectedRepositoryFixture(t, "repository-missing")
	manifest := filepath.Join(clean, filepath.FromSlash(portableWorkloadManifestRelativePath))
	if err := os.Remove(manifest); err != nil {
		t.Fatalf("remove manifest: %v", err)
	}
	commitDetectedFixture(t, clean, "remove manifest")
	if _, err := DetectRepository(context.Background(), clean); err == nil || !strings.Contains(err.Error(), "manifest") {
		t.Fatalf("missing manifest error = %v", err)
	}
	link := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(root, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := DetectRepository(context.Background(), link); err == nil {
		t.Fatal("DetectRepository() accepted a symlink start path")
	}
}

func newDetectedRepositoryFixture(t *testing.T, repositoryID string) string {
	t.Helper()
	root := t.TempDir()
	runDetectedFixtureGit(t, root, "init", "--quiet")
	runDetectedFixtureGit(t, root, "config", "user.email", "fixture@example.invalid")
	runDetectedFixtureGit(t, root, "config", "user.name", "Fixture")
	digest := "sha256:" + strings.Repeat("a", 64)
	manifest := remoteportable.PortableManifest{
		SchemaVersion: remoteportable.PortableManifestSchemaVersion, ContractID: remoteportable.PortableManifestContractID,
		Engine:    remoteportable.PortableManifestEngine{RequiredProtocolABIDigest: digest, RequiredToolchainDigest: digest, RequiredCapabilities: []string{"go"}},
		Target:    remoteportable.PortableManifestTarget{RepositoryID: repositoryID, ObjectFormat: "sha1", WorktreeMount: "worktree"},
		Workloads: []remoteportable.PortableManifestWorkload{{UnitID: "go-test/example/TestFixture", CommandDigest: digest, TargetPlatform: string(remoteportable.RepositoryWorkloadPlatformLinuxAMD64), RuntimeCapability: string(remoteportable.RepositoryWorkloadRuntimeUbuntuContainer), CPUMilli: 1_000, MemoryBytes: 1 << 30, HistoricalEstimateMS: 1_000, ExecutionDigest: digest, Kind: remoteportable.RepositoryWorkloadKindGoTest, ModuleRoot: ".", Package: "./example", Name: "TestFixture", ExecutionPolicy: string(remoteportable.RepositoryWorkloadExecutionPolicyNormal)}, {UnitID: "go-test/windows/example/TestFixture", CommandDigest: digest, TargetPlatform: string(remoteportable.RepositoryWorkloadPlatformWindowsAMD64), RuntimeCapability: string(remoteportable.RepositoryWorkloadRuntimeWindowsNative), CPUMilli: 1_000, MemoryBytes: 1 << 30, HistoricalEstimateMS: 1_000, ExecutionDigest: digest, Kind: remoteportable.RepositoryWorkloadKindGoTest, ModuleRoot: ".", Package: "./example", Name: "TestFixture", ExecutionPolicy: string(remoteportable.RepositoryWorkloadExecutionPolicyNormal)}},
		Profile:   remoteportable.ProfileLocalFast, CapacityPolicyDigest: digest, RequiredNodeCapabilities: []string{"go1.26.5"},
		LogPolicy: remoteportable.PortableManifestLogPolicy{ResultSchema: "remote-ci-result/v1", EventSchema: "remote-ci-event/v1", MaxResultBytes: 1 << 20, MaxEventBytes: 64 << 10},
		Authority: remoteportable.PortableManifestAuthority{ContractID: remoteportable.PortableManifestContractID, PostgreSQLIdentity: "remote-ci-postgresql-primary/v1"},
		Modules:   []remoteportable.RepositoryWorkloadModule{{Root: ".", ModulePath: "example.invalid/" + repositoryID}}, ExcludedNestedModules: []string{},
	}
	encoded, err := remoteportable.PortableManifestCanonicalEncode(manifest)
	if err != nil {
		t.Fatalf("encode manifest: %v", err)
	}
	manifestPath := filepath.Join(root, filepath.FromSlash(portableWorkloadManifestRelativePath))
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatalf("create manifest directory: %v", err)
	}
	if err := os.WriteFile(manifestPath, encoded, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	marker := filepath.Join(root, "repository.txt")
	if err := os.WriteFile(marker, []byte(repositoryID), 0o644); err != nil {
		t.Fatalf("write repository marker: %v", err)
	}
	goMod := filepath.Join(root, "go.mod")
	if err := os.WriteFile(goMod, []byte("module example.invalid/"+repositoryID+"\n\ngo 1.24.0\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	sourceDir := filepath.Join(root, "example")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("create example package: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "example.go"), []byte("package example\n\nfunc Value() int { return 1 }\n"), 0o644); err != nil {
		t.Fatalf("write example source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "example_windows_test.go"), []byte("package example\n\nimport \"testing\"\n\nfunc TestWindowsValue(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatalf("write Windows example test: %v", err)
	}
	commitDetectedFixture(t, root, "initial")
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return resolvedRoot
}

func commitDetectedFixture(t *testing.T, root, message string) {
	t.Helper()
	runDetectedFixtureGit(t, root, "add", "-A")
	runDetectedFixtureGit(t, root, "commit", "--quiet", "-m", message)
}

func runDetectedFixtureGit(t *testing.T, root string, args ...string) {
	t.Helper()
	commandArgs := append([]string{"-C", root}, args...)
	command := exec.Command("git", commandArgs...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}
