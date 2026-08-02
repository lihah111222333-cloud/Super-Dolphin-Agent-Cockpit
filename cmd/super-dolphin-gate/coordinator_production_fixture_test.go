package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

func writeProductionBuildInputs(t *testing.T, repository string) {
	t.Helper()
	sourceRoot := productionGitLine(t, ".", "rev-parse", "--show-toplevel")
	mainCommit := productionGitLine(t, sourceRoot, "rev-parse", "--verify", "origin/main^{commit}")
	runProductionGit(t, "", "clone", "-q", "--no-checkout", "--", sourceRoot, repository)
	runProductionGit(t, repository, "checkout", "-q", "-B", "main", mainCommit)
}

func TestProductionRuntimeDepsLockUsesNodeLocalSchema(t *testing.T) {
	fixture := newProductionTestFixture(t)
	data, err := os.ReadFile(filepath.Join(fixture.sourceRepo, "build/gate/runtime-deps.lock"))
	if err != nil {
		t.Fatal(err)
	}
	var lock map[string]json.RawMessage
	if err := json.Unmarshal(data, &lock); err != nil {
		t.Fatal(err)
	}
	want := map[string]struct{}{
		"schema_version": {}, "build_mode": {}, "cache_scope": {}, "inputs": {}, "recipe_inputs": {}, "paths": {},
	}
	if len(lock) != len(want) {
		t.Fatalf("runtime dependency lock fields = %v, want only %v", sortedProductionFixtureKeys(lock), sortedProductionFixtureKeys(want))
	}
	for field := range want {
		if _, exists := lock[field]; !exists {
			t.Fatalf("runtime dependency lock missing %q", field)
		}
	}
	schemaVersion := productionFixtureString(t, lock, "schema_version")
	buildMode := productionFixtureString(t, lock, "build_mode")
	cacheScope := productionFixtureString(t, lock, "cache_scope")
	if schemaVersion != remoteci.RuntimeDependencySchemaVersion || buildMode != "node-local" || cacheScope != "node" {
		t.Fatalf("runtime dependency lock header = (%q, %q, %q)", schemaVersion, buildMode, cacheScope)
	}
}

func TestProductionRuntimeDepsLockRejectsRemovedSeedField(t *testing.T) {
	fixture := newProductionTestFixture(t)
	lockPath := filepath.Join(fixture.sourceRepo, "build/gate/runtime-deps.lock")
	lockData, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(lockData, &document); err != nil {
		t.Fatal(err)
	}
	var inputs map[string]string
	if err := json.Unmarshal(document["inputs"], &inputs); err != nil {
		t.Fatal(err)
	}
	inputs["runtime_seed_script_browser_sha256"] = productionFixtureDigest("removed seed fixture\n")
	encodedInputs, err := json.Marshal(inputs)
	if err != nil {
		t.Fatal(err)
	}
	document["inputs"] = encodedInputs
	updated, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, append(updated, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	runProductionGit(t, fixture.sourceRepo, "add", "--", "build/gate/runtime-deps.lock")
	runProductionGit(t, fixture.sourceRepo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "legacy seed field")
	source := productionSourceSpec(fixture)
	source.Commit.SHA = productionGitLine(t, fixture.sourceRepo, "rev-parse", "HEAD^{commit}")
	source.SourceTreeSHA = productionGitLine(t, fixture.sourceRepo, "rev-parse", "HEAD^{tree}")
	tree, err := localci.LoadReadOnlyGitTree(context.Background(), fixture.sourceRepo, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := localci.ResolveGateImageInputs(tree, productionDigest("1"), fixture.config.Platform); err == nil ||
		!strings.Contains(err.Error(), "unknown field \"runtime_seed_script_browser_sha256\"") {
		t.Fatalf("ResolveGateImageInputs() error = %v, want strict rejection of removed seed field", err)
	}
}

func productionFixtureString(t *testing.T, fields map[string]json.RawMessage, name string) string {
	t.Helper()
	var value string
	if err := json.Unmarshal(fields[name], &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func sortedProductionFixtureKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func changeProductionBuildInput(t *testing.T, repository string, path string, content string) {
	t.Helper()
	absolute := filepath.Join(repository, filepath.FromSlash(path))
	previous, err := os.ReadFile(absolute)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolute, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(repository, "build/gate/runtime-deps.lock")
	lockData, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(lockData, &document); err != nil {
		t.Fatal(err)
	}
	var schemaVersion string
	if err := json.Unmarshal(document["schema_version"], &schemaVersion); err != nil {
		t.Fatal(err)
	}
	if schemaVersion != remoteci.RuntimeDependencySchemaVersion {
		t.Fatalf("runtime dependency lock schema = %q, want %q", schemaVersion, remoteci.RuntimeDependencySchemaVersion)
	}
	var inputs map[string]string
	if err := json.Unmarshal(document["inputs"], &inputs); err != nil {
		t.Fatal(err)
	}
	previousDigest := productionFixtureDigest(string(previous))
	field := ""
	for name, digest := range inputs {
		if digest != previousDigest {
			continue
		}
		if field != "" {
			t.Fatalf("runtime dependency lock has ambiguous input digest for %q: %q and %q", path, field, name)
		}
		field = name
	}
	if field == "" {
		t.Fatalf("runtime dependency lock does not bind changed input %q", path)
	}
	inputs[field] = productionFixtureDigest(content)
	encodedInputs, err := json.Marshal(inputs)
	if err != nil {
		t.Fatal(err)
	}
	document["inputs"] = encodedInputs
	updated, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, append(updated, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func productionFixtureDigest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func TestProductionSourceMaterializerUsesGitObjectSnapshot(t *testing.T) {
	fixture := newProductionTestFixture(t)
	candidate := productionGitLine(
		t, fixture.sourceRepo,
		"-c", "user.name=Production Materializer", "-c", "user.email=materializer@example.invalid",
		"commit-tree", fixture.tree, "-p", fixture.commit, "-m", "candidate",
	)
	if err := os.WriteFile(filepath.Join(fixture.sourceRepo, "trusted.txt"), []byte("tampered checkout\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	materializer, result, outputRoot := materializeProductionCandidateSource(t, fixture, candidate)
	assertProductionCandidateSource(t, materializer, result, fixture)
	if err := result.Cleanup(); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
	if _, err := os.Stat(outputRoot); !os.IsNotExist(err) {
		t.Fatalf("output root still exists: %v", err)
	}
}

func materializeProductionCandidateSource(
	t *testing.T,
	fixture productionTestFixture,
	candidate string,
) (*productionSourceMaterializer, materializedJobSource, string) {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	outputParent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outputRoot := filepath.Join(outputParent, "job")
	materializer := &productionSourceMaterializer{gitPath: gitPath}
	source := productionSourceSpec(fixture)
	source.Commit.SHA = candidate
	result, err := materializer.Materialize(context.Background(), sourceMaterializeRequest{
		RepositoryRoot: fixture.sourceRepo, OutputRoot: outputRoot, Source: source,
	})
	if err != nil {
		t.Fatalf("Materialize() error = %v", err)
	}
	return materializer, result, outputRoot
}

func assertProductionCandidateSource(
	t *testing.T,
	materializer *productionSourceMaterializer,
	result materializedJobSource,
	fixture productionTestFixture,
) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(result.SnapshotDir, "trusted.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "trusted object content\n" {
		t.Fatalf("snapshot content = %q", got)
	}
	if result.SourceTreeSHA != fixture.tree {
		t.Fatalf("SourceTreeSHA = %s, want %s", result.SourceTreeSHA, fixture.tree)
	}
	base, err := materializer.gitLine(
		context.Background(), result.SnapshotDir,
		"rev-parse", "--verify", "--end-of-options", "refs/source/base^{commit}",
	)
	if err != nil {
		t.Fatalf("resolve canonical source base ref: %v", err)
	}
	if base != fixture.commit {
		t.Fatalf("canonical source base = %s, want candidate parent %s", base, fixture.commit)
	}
}

// newProductionTestFixture 组装 production 测试所需的仓库、密钥与权威配置。
func newProductionTestFixture(t *testing.T) productionTestFixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	chmodPrivate(t, root)
	repository := prepareProductionRepository(t, root)
	authority := prepareProductionAuthority(t, root)
	bootstrapRootFile := filepath.Join(root, "bootstrap-root.json")
	bootstrapControllerFile := writeProductionTestBootstrapController(t, root)
	bootstrapControllerKeyFile := filepath.Join(root, "bootstrap-controller-key.json")
	config := productionCoordinatorConfig{
		AcceptedImageRoot:  makePrivateDirectory(t, root, "accepted"),
		CandidateStateRoot: makePrivateDirectory(t, root, "candidate-state"),
		BootstrapRootFile:  bootstrapRootFile, BootstrapControllerFile: bootstrapControllerFile,
		BootstrapControllerKeyFile: bootstrapControllerKeyFile,
		CandidateBuildRoot:         makePrivateDirectory(t, root, "build"),
		TrustedSourceRoot:          prepareProductionTrustedSourceRoot(t),
		SeccompProfile:             writeProductionSeccompProfile(t, root),
		Platform:                   "linux/arm64", RepoID: "example/repository",
		TrustedRef: "refs/heads/main", TrustedRepository: repository.trustedRepository,
		GitExecutable: mustResolveProductionGitExecutable(t),
		AcceptedImageSigners: []productionTrustedKey{{
			Signer: authority.signer, PublicKey: base64.StdEncoding.EncodeToString(authority.publicKey),
		}},
		ResultReceiptAuthority: productionResultReceiptAuthorityConfig{
			Signer:         authority.receiptSigner,
			PublicKey:      base64.StdEncoding.EncodeToString(authority.receiptPublicKey),
			PrivateKeyFile: authority.receiptKeyPath,
		},
		ActionGrantAuthority: productionActionGrantAuthorityConfig{
			Signer: authority.grantSigner, PublicKey: base64.StdEncoding.EncodeToString(authority.grantPublicKey),
			PrivateKeyFile: authority.grantKeyPath, TTLSeconds: 60,
		},
		PromotionSigner: productionPromotionKey{
			Signer: authority.signer, PrivateKeyFile: authority.promotionKeyPath,
		},
		CandidateTTLSeconds: 3600, PromotionPollMillis: 5_000,
		ShardsPerJob: 3, MaxActiveCIWorkloads: 3,
	}
	bootstrapRoot, rootTrust, rootPrivateKey := productionBootstrapRootForFixture(
		t, config, repository.commit, repository.tree, authority.signer, authority.publicKey,
	)
	config.AcceptedImageSigners = append(config.AcceptedImageSigners, rootTrust)
	writePrivateJSON(t, bootstrapControllerKeyFile, productionBootstrapControllerPrivateKey{
		Signer: authority.signer, PrivateKey: base64.StdEncoding.EncodeToString(authority.privateKey),
	})
	writeProductionBootstrapRootFixture(t, bootstrapRootFile, bootstrapRoot, rootPrivateKey)
	fixture := productionTestFixture{
		config: config, signer: authority.signer, privateKey: authority.privateKey,
		bootstrapRootKey: rootPrivateKey,
		receiptKey:       authority.receiptPrivateKey, grantKey: authority.grantPrivateKey,
		commit: repository.commit, tree: repository.tree, sourceRepo: repository.sourceRepo,
	}
	baseTree, err := localci.LoadReadOnlyGitTree(
		context.Background(), repository.sourceRepo, productionSourceSpec(fixture),
	)
	if err != nil {
		t.Fatal(err)
	}
	baseInputs, err := localci.ResolveGateImageInputs(baseTree, productionDigest("1"), config.Platform)
	if err != nil {
		t.Fatal(err)
	}
	bootstrapInputs, err := localci.ResolveGateImageInputs(baseTree, bootstrapRoot.PolicyDigest, config.Platform)
	if err != nil {
		t.Fatal(err)
	}
	bootstrapRoot.ImageInputDigest = bootstrapInputs.ImageInputDigest
	bootstrapRoot.ToolchainDigest = bootstrapInputs.ToolchainDigest
	bootstrapRoot.ImageSchemaVersion = bootstrapInputs.ImageSchemaVersion
	writeProductionBootstrapRootFixture(t, bootstrapRootFile, bootstrapRoot, rootPrivateKey)
	fixture.acceptedInputDigest = baseInputs.ImageInputDigest
	bootstrapAcceptedState(t, fixture)
	fixture.configPath = filepath.Join(root, "production.json")
	writeProductionCoordinatorConfigFixture(t, fixture.configPath, config)
	return fixture
}

func mustResolveProductionGitExecutable(t *testing.T) string {
	t.Helper()
	path, err := resolveProductionGitExecutable()
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func writeProductionCoordinatorConfigFixture(
	t *testing.T,
	path string,
	config productionCoordinatorConfig,
) {
	t.Helper()
	portable, err := portableProductionCoordinatorConfig(filepath.Dir(path), config)
	if err != nil {
		t.Fatal(err)
	}
	writePrivateJSON(t, path, portable)
}

func writeProductionTestBootstrapController(t *testing.T, root string) string {
	t.Helper()
	bootstrapControllerFile := filepath.Join(root, "bootstrap-controller")
	if err := os.WriteFile(bootstrapControllerFile, []byte("#!/bin/sh\nexit 97\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return bootstrapControllerFile
}
