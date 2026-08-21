package remotecicli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
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
	if detected.Root != root || detected.RepositoryID != repositoryID || detected.ObjectFormat != "sha1" {
		t.Fatalf("detected identity = %#v", detected)
	}
	if len(detected.CommitSHA) != 40 || len(detected.TreeSHA) != 40 || detected.ManifestSHA256 == "" || detected.ManifestABIDigest == "" {
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
	manifest, err := remoteci.DecodeRepositoryWorkloadManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	manifest.RepositoryID = "worktree-shadow"
	manifest.Modules[0].ModulePath = "example.invalid/worktree-shadow"
	shadow, err := manifest.CanonicalEncode()
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
	manifest := remoteci.RepositoryWorkloadManifest{
		SchemaVersion: remoteci.PortableWorkloadManifestSchemaVersion, RepositoryID: repositoryID,
		Modules:               []remoteci.RepositoryWorkloadModule{{Root: ".", ModulePath: "example.invalid/" + repositoryID}},
		ExcludedNestedModules: []string{},
		Workloads: []remoteci.RepositoryWorkload{{
			ID: "go-test/example/TestFixture", Kind: remoteci.RepositoryWorkloadKindGoTest,
			ModuleRoot: ".", Package: "./example", Name: "TestFixture",
			Platform: remoteci.RepositoryWorkloadPlatformLinuxAMD64, Runtime: remoteci.RepositoryWorkloadRuntimeUbuntuContainer,
			BootstrapEstimateMS: 1_000, CPUMilli: 1_000, MemoryBytes: 1 << 30,
			ExecutionPolicy: remoteci.RepositoryWorkloadExecutionPolicyNormal,
		}},
	}
	encoded, err := manifest.CanonicalEncode()
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
	commitDetectedFixture(t, root, "initial")
	return root
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
