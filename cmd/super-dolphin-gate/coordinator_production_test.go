package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
)

type productionTestFixture struct {
	config     productionCoordinatorConfig
	configPath string
	signer     gatecontract.SignerIdentity
	privateKey ed25519.PrivateKey
	commit     string
	tree       string
	sourceRepo string
}

type capturingTruthImageEnsurer struct {
	request localci.TruthImageEnsureRequest
	result  localci.TruthImageEnsureResult
}

func (ensurer *capturingTruthImageEnsurer) EnsureImage(
	_ context.Context,
	request localci.TruthImageEnsureRequest,
) (localci.TruthImageEnsureResult, error) {
	ensurer.request = request
	return ensurer.result, nil
}

type capturingFreshContainerRunner struct {
	request localci.FreshContainerRequest
}

func (runner *capturingFreshContainerRunner) RunFreshContainer(
	_ context.Context,
	request localci.FreshContainerRequest,
) (localci.FreshContainerResult, error) {
	runner.request = request
	return localci.FreshContainerResult{Status: gatecontract.ResultStatusPassed}, nil
}

func TestProductionCoordinatorDependenciesAssembleRealAdapters(t *testing.T) {
	fixture := newProductionTestFixture(t)
	t.Setenv(productionCoordinatorConfigEnv, fixture.configPath)

	dependencies, err := productionCoordinatorDependencies(context.Background())
	if err != nil {
		t.Fatalf("productionCoordinatorDependencies() error = %v", err)
	}
	if _, ok := dependencies.ImageEnsurer.(*productionImageEnsurer); !ok {
		t.Fatalf("ImageEnsurer = %T", dependencies.ImageEnsurer)
	}
	if _, ok := dependencies.SourceMaterializer.(*productionSourceMaterializer); !ok {
		t.Fatalf("SourceMaterializer = %T", dependencies.SourceMaterializer)
	}
	if _, ok := dependencies.FreshRunner.(*productionFreshContainerRunner); !ok {
		t.Fatalf("FreshRunner = %T", dependencies.FreshRunner)
	}
}

func TestProductionSignatureVerifierChecksRealEd25519Signature(t *testing.T) {
	fixture := newProductionTestFixture(t)
	verifier, err := newProductionSignatureVerifier(fixture.config.AcceptedImageSigners)
	if err != nil {
		t.Fatalf("newProductionSignatureVerifier() error = %v", err)
	}
	payload := []byte("accepted image payload")
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(fixture.privateKey, payload))
	if err := verifier.VerifyAcceptedImage(context.Background(), fixture.signer, payload, signature); err != nil {
		t.Fatalf("VerifyAcceptedImage() error = %v", err)
	}
	if err := verifier.VerifyAcceptedImage(context.Background(), fixture.signer, []byte("tampered"), signature); err == nil {
		t.Fatal("VerifyAcceptedImage() accepted tampered payload")
	}
}

func TestProductionGitAuthorityRequiresExactTrustedRefTip(t *testing.T) {
	fixture := newProductionTestFixture(t)
	authority, err := newProductionGitAuthority(context.Background(), fixture.config)
	if err != nil {
		t.Fatalf("newProductionGitAuthority() error = %v", err)
	}
	record := productionAcceptedRecord(t, fixture)
	if err := authority.verifyRecord(context.Background(), record); err != nil {
		t.Fatalf("verifyRecord() error = %v", err)
	}
	record.TrustedCommit = strings.Repeat("f", len(record.TrustedCommit))
	if err := authority.verifyRecord(context.Background(), record); err == nil {
		t.Fatal("verifyRecord() accepted a non-tip trusted commit")
	}
}

func TestProductionSourceMaterializerUsesGitObjectSnapshot(t *testing.T) {
	fixture := newProductionTestFixture(t)
	if err := os.WriteFile(filepath.Join(fixture.sourceRepo, "trusted.txt"), []byte("tampered checkout\n"), 0o600); err != nil {
		t.Fatal(err)
	}
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
	result, err := materializer.Materialize(context.Background(), sourceMaterializeRequest{
		RepositoryRoot: fixture.sourceRepo, OutputRoot: outputRoot, Source: productionSourceSpec(fixture),
	})
	if err != nil {
		t.Fatalf("Materialize() error = %v", err)
	}
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
	if err := result.Cleanup(); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
	if _, err := os.Stat(outputRoot); !os.IsNotExist(err) {
		t.Fatalf("output root still exists: %v", err)
	}
}

func TestProductionImageEnsurerKeepsJobAndAcceptedBuildTreesSeparate(t *testing.T) {
	fixture := newProductionTestFixture(t)
	plan, err := gatecontract.BuildGatePlan(gatecontract.ProfileLocalFast, productionSourceSpec(fixture))
	if err != nil {
		t.Fatal(err)
	}
	acceptedTree := strings.Repeat("f", len(fixture.tree))
	truth := &capturingTruthImageEnsurer{result: localci.TruthImageEnsureResult{
		Status: localci.TruthImageEnsureAccepted, SubmittedJobSourceTree: fixture.tree,
		AcceptedImageBuildSourceTree: acceptedTree, PolicyDigest: plan.PolicyDigest, ImageSchemaVersion: "1",
		ImageInputDigest: productionDigest("8"), ToolchainDigest: productionDigest("9"),
	}}
	ensurer := &productionImageEnsurer{truth: truth, platform: "linux/arm64"}
	result, err := ensurer.EnsureImage(context.Background(), imageEnsureRequest{
		RepositoryRoot: fixture.sourceRepo, Plan: plan, JobSourceTreeSHA: fixture.tree,
	})
	if err != nil {
		t.Fatalf("EnsureImage() error = %v", err)
	}
	if truth.request.Tree.Source.SourceTreeSHA != fixture.tree {
		t.Fatalf("truth input tree = %s", truth.request.Tree.Source.SourceTreeSHA)
	}
	if result.ImageProvenanceSourceTreeSHA != acceptedTree || result.Truth.BuildSourceTreeSHA != acceptedTree {
		t.Fatalf("ensured image provenance = %#v", result)
	}
	if result.ImageProvenanceSourceTreeSHA == fixture.tree {
		t.Fatal("production image adapter collapsed accepted build provenance into submitted job tree")
	}
}

func TestProductionFreshRunnerForwardsOnlyMatchingAcceptedProvenance(t *testing.T) {
	capture := &capturingFreshContainerRunner{}
	runner := &productionFreshContainerRunner{runner: capture}
	acceptedTree := strings.Repeat("a", 40)
	jobTree := strings.Repeat("b", 40)
	request := freshContainerRequest{
		ImageProvenanceSourceTreeSHA: acceptedTree, JobSourceTreeSHA: jobTree,
		ImageTruth:        localci.FreshContainerImageTruth{BuildSourceTreeSHA: acceptedTree},
		SourceSnapshotDir: "/private/source", GateID: gatecontract.GateIDWhitespaceCheck,
	}
	if _, err := runner.RunFreshContainer(context.Background(), request); err != nil {
		t.Fatalf("RunFreshContainer() error = %v", err)
	}
	if capture.request.SourceTreeSHA != jobTree || capture.request.ImageTruth.BuildSourceTreeSHA != acceptedTree {
		t.Fatalf("forwarded request = %#v", capture.request)
	}
	request.ImageProvenanceSourceTreeSHA = jobTree
	if _, err := runner.RunFreshContainer(context.Background(), request); err == nil {
		t.Fatal("RunFreshContainer() accepted mismatched image provenance")
	}
}

func TestProductionCoordinatorConfigFieldRegistryIsComplete(t *testing.T) {
	assertProductionFields(t, reflect.TypeFor[productionCoordinatorConfig](), map[string]string{
		"AcceptedImageRoot": "signed state", "CandidateBuildRoot": "candidate isolation",
		"TrustedSourceRoot": "snapshot mount boundary", "SeccompProfile": "container policy",
		"Platform": "image platform", "RepoID": "repository identity", "TrustedRef": "admission ref",
		"TrustedRepository": "external bare mirror", "AcceptedImageSigners": "signature trust root",
	})
	assertProductionFields(t, reflect.TypeFor[productionTrustedKey](), map[string]string{
		"Signer": "key identity", "PublicKey": "Ed25519 verification material",
	})
}

func TestProductionCoordinatorConfigRejectsUnknownFieldsAndWorktreeRoots(t *testing.T) {
	fixture := newProductionTestFixture(t)
	data, err := os.ReadFile(fixture.configPath)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	object["fallback_to_host"] = true
	unknownPath := filepath.Join(filepath.Dir(fixture.configPath), "unknown.json")
	writePrivateJSON(t, unknownPath, object)
	if _, err := loadProductionCoordinatorConfigFile(unknownPath); err == nil {
		t.Fatal("production config accepted an unknown fallback field")
	}

	worktree := filepath.Join(t.TempDir(), "candidate")
	runProductionGit(t, "", "init", "-q", "--", worktree)
	worktreeConfig := filepath.Join(worktree, "production.json")
	writePrivateJSON(t, worktreeConfig, fixture.config)
	if _, err := loadProductionCoordinatorConfigFile(worktreeConfig); err == nil {
		t.Fatal("production config accepted a candidate worktree trust path")
	}
}

func TestLoadProductionCoordinatorConfigFileValidatesDecodedConfig(t *testing.T) {
	fixture := newProductionTestFixture(t)
	tests := []struct {
		name   string
		want   string
		mutate func(*productionCoordinatorConfig)
	}{
		{name: "repo_id", want: "repo_id is required", mutate: func(config *productionCoordinatorConfig) {
			config.RepoID = ""
		}},
		{name: "trusted_ref", want: "trusted_ref must be a canonical full ref", mutate: func(config *productionCoordinatorConfig) {
			config.TrustedRef = "main"
		}},
		{name: "platform", want: "platform must be explicit", mutate: func(config *productionCoordinatorConfig) {
			config.Platform = "linux"
		}},
		{name: "root_overlap", want: "roots must not overlap", mutate: func(config *productionCoordinatorConfig) {
			config.CandidateBuildRoot = config.AcceptedImageRoot
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := fixture.config
			test.mutate(&config)
			path := filepath.Join(filepath.Dir(fixture.configPath), test.name+".json")
			writePrivateJSON(t, path, config)
			if _, err := loadProductionCoordinatorConfigFile(path); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("loadProductionCoordinatorConfigFile() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestProductionCoordinatorRejectsGitObjectEnvironmentOverrides(t *testing.T) {
	t.Setenv("GIT_OBJECT_DIRECTORY", "/candidate/objects")
	if _, err := productionCoordinatorDependencies(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "GIT_OBJECT_DIRECTORY") {
		t.Fatalf("productionCoordinatorDependencies() error = %v", err)
	}
}

func newProductionTestFixture(t *testing.T) productionTestFixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	chmodPrivate(t, root)
	sourceRepo := filepath.Join(root, "source")
	runProductionGit(t, "", "init", "-q", "-b", "main", "--", sourceRepo)
	if err := os.WriteFile(filepath.Join(sourceRepo, "trusted.txt"), []byte("trusted object content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runProductionGit(t, sourceRepo, "add", "--", "trusted.txt")
	runProductionGit(t, sourceRepo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "trusted")
	commit := productionGitLine(t, sourceRepo, "rev-parse", "HEAD^{commit}")
	tree := productionGitLine(t, sourceRepo, "rev-parse", "HEAD^{tree}")

	trustedRepository := filepath.Join(root, "trusted.git")
	runProductionGit(t, "", "clone", "-q", "--bare", "--", sourceRepo, trustedRepository)
	chmodPrivate(t, trustedRepository)
	acceptedRoot := makePrivateDirectory(t, root, "accepted")
	buildRoot := makePrivateDirectory(t, root, "build")
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	trustedSourceRoot := filepath.Join(filepath.Clean(cacheRoot), "super-dolphin", "localci")
	if err := os.MkdirAll(trustedSourceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	chmodPrivate(t, trustedSourceRoot)
	seccomp := filepath.Join(root, "seccomp.json")
	if err := os.WriteFile(seccomp, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	signer := gatecontract.SignerIdentity{
		KeyID: "production-test", KeyEpoch: 1, Algorithm: gatecontract.SignatureAlgorithmEd25519,
	}
	config := productionCoordinatorConfig{
		AcceptedImageRoot: acceptedRoot, CandidateBuildRoot: buildRoot, TrustedSourceRoot: trustedSourceRoot,
		SeccompProfile: seccomp, Platform: "linux/arm64", RepoID: "example/repository",
		TrustedRef: "refs/heads/main", TrustedRepository: trustedRepository,
		AcceptedImageSigners: []productionTrustedKey{{
			Signer: signer, PublicKey: base64.StdEncoding.EncodeToString(publicKey),
		}},
	}
	fixture := productionTestFixture{
		config: config, signer: signer, privateKey: privateKey, commit: commit, tree: tree, sourceRepo: sourceRepo,
	}
	bootstrapAcceptedState(t, fixture)
	fixture.configPath = filepath.Join(root, "production.json")
	writePrivateJSON(t, fixture.configPath, config)
	return fixture
}

func bootstrapAcceptedState(t *testing.T, fixture productionTestFixture) {
	t.Helper()
	verifier, err := newProductionSignatureVerifier(fixture.config.AcceptedImageSigners)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := newProductionGitAuthority(context.Background(), fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	state, err := localci.NewAcceptedImageState(fixture.config.AcceptedImageRoot, verifier, authority)
	if err != nil {
		t.Fatal(err)
	}
	record := productionAcceptedRecord(t, fixture)
	if err := state.Bootstrap(context.Background(), record); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
}

func productionAcceptedRecord(t *testing.T, fixture productionTestFixture) gatecontract.AcceptedImageRecord {
	t.Helper()
	policyDigest := productionDigest("1")
	record := gatecontract.AcceptedImageRecord{
		SchemaVersion: gatecontract.AcceptedImageRecordSchemaVersion,
		RepoID:        fixture.config.RepoID, TrustedRef: fixture.config.TrustedRef,
		TrustedCommit: fixture.commit, SourceTree: fixture.tree,
		PolicyDigest: policyDigest, ImageInputDigest: productionDigest("2"),
		Image: gatecontract.ImageIdentity{
			Registry:       "registry.example.invalid/super-dolphin/gate",
			OCIIndexDigest: productionDigest("3"), PlatformManifestDigest: productionDigest("4"),
			ConfigDigest: productionDigest("5"), RootFSDiffIDs: []string{productionDigest("6")},
			OS: "linux", Architecture: "arm64",
		},
		Runner: gatecontract.TrustedRunnerIdentity{
			BinaryDigest: productionDigest("7"), Signer: fixture.signer, PolicyDigest: policyDigest,
		},
		Generation: 1, AcceptedAt: time.Now().UTC(), Signer: fixture.signer,
	}
	payload, err := gatecontract.AcceptedImageSigningPayload(record)
	if err != nil {
		t.Fatal(err)
	}
	record.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(fixture.privateKey, payload))
	return record
}

func productionSourceSpec(fixture productionTestFixture) gatecontract.SourceSpec {
	return gatecontract.SourceSpec{
		Kind: gatecontract.SourceKindCommit, ObjectFormat: gatecontract.GitObjectFormatSHA1,
		Commit: &gatecontract.CommitSource{SHA: fixture.commit}, SourceTreeSHA: fixture.tree,
	}
}

func assertProductionFields(t *testing.T, producer reflect.Type, registry map[string]string) {
	t.Helper()
	for index := range producer.NumField() {
		field := producer.Field(index).Name
		if registry[field] == "" {
			t.Fatalf("%s field %q is not registered", producer.Name(), field)
		}
		delete(registry, field)
	}
	if len(registry) != 0 {
		t.Fatalf("%s field registry has stale entries: %v", producer.Name(), registry)
	}
}

func makePrivateDirectory(t *testing.T, root string, name string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func chmodPrivate(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

func writePrivateJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func productionGitLine(t *testing.T, directory string, args ...string) string {
	t.Helper()
	commandArgs := append([]string{"-C", directory}, args...)
	output, err := exec.Command("git", commandArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func runProductionGit(t *testing.T, directory string, args ...string) {
	t.Helper()
	commandArgs := append([]string(nil), args...)
	if directory != "" {
		commandArgs = append([]string{"-C", directory}, commandArgs...)
	}
	if output, err := exec.Command("git", commandArgs...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func productionDigest(character string) string {
	return "sha256:" + strings.Repeat(character, 64)
}
