package archtest

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// TestRemoteCIIncrementalSourceTransportContract locks the accepted-image Git
// baseline, deterministic thin bundle shape, strict manifest names and the
// upload-before-shard barrier to the cicontract owner.
var sourceMaterializerGuardFiles = []string{
	"source_materializer.go",
	"source_materializer_baseline.go",
	"source_materializer_helpers.go",
	"source_materializer_manifest.go",
	"source_worktree_materializer.go",
}

func TestRemoteCIIncrementalSourceTransportContract(t *testing.T) {
	root := findRepoRoot(t)
	validateSourceTransportContract(t)
	materializer := readSourceMaterializerGuardSources(t, root)
	assertRequiredSourceMaterializerMarkers(t, materializer)
	assertForbiddenSourceMaterializerMarkers(t, materializer)

	tree := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal", "devtools", "remoteci", "source_tree.go"))
	assertRequiredSourceBundleMarkers(t, tree)

	coordinator := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal", "devtools", "remoteci", "coordinator.go"))
	assertSourceAssetUploadBarrier(t, coordinator)
	assetsSource := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal", "devtools", "remoteci", "coordinator_assets.go"))
	assertRequiredSourceAssetMarkers(t, assetsSource)

	materialize := readRemoteCIContractGuardFile(t, filepath.Join(root, "cmd", "super-dolphin-gate", "remote_materialize.go"))
	assertRemoteMaterializerConsumesCanonicalSource(t, materialize)
	assertBaselinePublishedByImages(t, root)
}

// TestRemoteCIFuturePortableSourceBaselineArtifactContract freezes the future
// source-baseline artifact byte domain without claiming that runtime wiring
// already exists in this repository.
func TestRemoteCIFuturePortableSourceBaselineArtifactContract(t *testing.T) {
	root := findRepoRoot(t)
	contract := readRemoteCIContractGuardFile(t, filepath.Join(root, filepath.FromSlash(cicontract.DocumentPath)))
	for _, required := range []string{
		"source-baseline-git-bundle/v1",
		"标准自包含 Git bundle 单文件",
		"`refs/source/baseline`",
		"`DeterministicSourceBaselineCommitSHA`",
		"zero-parent commit",
		"0 prerequisites",
		"完整且无额外的 tree/blob closure",
		"artifact SHA/size 按实际 bundle bytes",
		"唯一 `aliyun-eci/v1` artifact builder",
		"builder/toolchain/repository_id/object_format/baseline commit/tree/artifact SHA/size",
		"`validate-only` resolver",
		"只读已有 artifact",
		"私有 `0700` 临时导入验证",
	} {
		if !strings.Contains(contract, required) {
			t.Errorf("accepted remote CI contract is missing future source-baseline artifact marker %q", required)
		}
	}
	for _, forbidden := range []string{
		"禁止 `tar bare ODB`",
		"禁止 `index-only`",
		"禁止 `worktree tar`",
		"禁止自动生成",
		"禁止自动修复",
		"禁止 fallback",
		"禁止 OSS 写",
		"禁止 SQLite 写",
		"禁止 PASS",
		"禁止 plan",
	} {
		if !strings.Contains(contract, forbidden) {
			t.Errorf("accepted remote CI contract is missing future source-baseline prohibition %q", forbidden)
		}
	}
}

func validateSourceTransportContract(t *testing.T) {
	t.Helper()
	if err := cicontract.ValidateSourceTransportContract(); err != nil {
		t.Fatal(err)
	}
}

func readSourceMaterializerGuardSources(t *testing.T, root string) string {
	t.Helper()
	base := filepath.Join(root, "internal", "devtools", "remoteci")
	contents := make([]string, len(sourceMaterializerGuardFiles))
	for i, name := range sourceMaterializerGuardFiles {
		contents[i] = readRemoteCIContractGuardFile(t, filepath.Join(base, name))
	}
	return strings.Join(contents, "\n")
}

func assertRequiredSourceMaterializerMarkers(t *testing.T, materializer string) {
	t.Helper()
	for _, required := range []string{
		`sourceBundleName            = "source.bundle"`,
		`sourceManifestName          = "source-manifest.json"`,
		`sourceManifestVersion       = cicontract.SourceManifestSchemaVersion`,
		`sourceTransportKind         = "git-bundle-thin"`,
		"SourceBaseline",
		"BuildSourceBaseline",
		"DeterministicSourceBaselineCommitSHA",
		"DeterministicSourceTransportCommitSHA",
		"DeterministicSourceSyntheticBaseCommitSHA",
		"SyntheticBaseTreeSHA",
		"SyntheticBaseCommitSHA",
		`json:"synthetic_base_tree_sha"`,
		`json:"synthetic_base_commit_sha"`,
		"TransportCommitSHA",
		"cicontract.SourceBaseRef",
		"MaterializeSource(ctx context.Context, repoRoot string, spec gate.SourceSpec, outputRoot string, baseline SourceBaseline)",
	} {
		if !strings.Contains(materializer, required) {
			t.Fatalf("source materializer is missing canonical incremental transport marker %q", required)
		}
	}
}

func assertForbiddenSourceMaterializerMarkers(t *testing.T, materializer string) {
	t.Helper()
	for _, forbidden := range []string{"MaterializedCommitSHA", "SyntheticCommitSHA", "source.patch", "source.manifest.json"} {
		if strings.Contains(materializer, forbidden) {
			t.Fatalf("source materializer retains retired transport marker %q", forbidden)
		}
	}
}

func assertRequiredSourceBundleMarkers(t *testing.T, tree string) {
	t.Helper()
	for _, required := range []string{
		"# v2 git bundle",
		"prerequisites: make([]string, 0, 1)",
		"len(header.prerequisites) != 1",
		`"^" + baseline.CommitSHA`,
		"verifyBundlePrerequisites",
		"DeterministicSourceTransportCommitSHA",
	} {
		if !strings.Contains(tree, required) {
			t.Fatalf("source bundle verifier is missing canonical marker %q", required)
		}
	}
}

func assertSourceAssetUploadBarrier(t *testing.T, coordinator string) {
	t.Helper()
	assets := strings.Index(coordinator, "uploadSourceAssets")
	groups := strings.Index(coordinator, "uploadAndCreateRemoteGroups")
	if assets < 0 || groups < 0 || assets >= groups {
		t.Fatalf("source bundle/manifest upload must complete before shard creation: upload=%d groups=%d", assets, groups)
	}
}

func assertRequiredSourceAssetMarkers(t *testing.T, assetsSource string) {
	t.Helper()
	for _, required := range []string{"BundlePath", "ManifestPath", "uploadSourceAssets"} {
		if !strings.Contains(assetsSource, required) {
			t.Fatalf("source asset uploader is missing %q", required)
		}
	}
}

func assertRemoteMaterializerConsumesCanonicalSource(t *testing.T, materialize string) {
	t.Helper()
	// Keep the path owned by cicontract rather than freezing its current value
	// into this guard.  The owner import and selector are the dynamic drift
	// boundary; cicontract.ValidateSourceTransportContract validates the value.
	if !strings.Contains(materialize, `"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"`) ||
		!strings.Contains(materialize, "cicontract.SourceBaselineRepositoryPath") ||
		!strings.Contains(materialize, cicontract.SourceBundleName) ||
		!strings.Contains(materialize, cicontract.SourceManifestName) {
		t.Fatal("remote materializer must consume the accepted baseline and canonical source artifacts")
	}
}

func assertBaselinePublishedByImages(t *testing.T, root string) {
	t.Helper()
	for _, path := range []string{"build/gate/Dockerfile", "build/gate/closure/closure.go"} {
		contents := readRemoteCIContractGuardFile(t, filepath.Join(root, filepath.FromSlash(path)))
		if !strings.Contains(contents, cicontract.SourceBaselineRepositoryPath) {
			t.Fatalf("%s must publish the accepted source baseline at %q", path, cicontract.SourceBaselineRepositoryPath)
		}
	}
}
