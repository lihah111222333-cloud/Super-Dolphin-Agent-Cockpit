package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
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
	config              productionCoordinatorConfig
	configPath          string
	signer              gatecontract.SignerIdentity
	privateKey          ed25519.PrivateKey
	commit              string
	tree                string
	acceptedInputDigest string
	sourceRepo          string
}

type productionCandidatePromotionFixture struct {
	production      *productionTestFixture
	authority       *productionPromotionAuthority
	controller      *localci.PromotionController
	tree            localci.ReadOnlyGitTree
	accepted        gatecontract.AcceptedImageRecord
	candidateCommit string
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

type productionBuildKitRunnerStub struct {
	digest string
}

func (runner productionBuildKitRunnerStub) Build(
	context.Context,
	localci.BuildKitBuildRequest,
) (string, error) {
	return runner.digest, nil
}

type productionCandidateIdentityResolverStub struct{}

func (productionCandidateIdentityResolverStub) ResolveCandidateIdentity(
	_ context.Context,
	_ localci.PromotionCandidate,
	result localci.CandidateResult,
) (gatecontract.ImageIdentity, error) {
	return gatecontract.ImageIdentity{
		Registry: "docker.io/library/super-dolphin-gate-local", OCIIndexDigest: result.ImageDigest,
		PlatformManifestDigest: result.ImageDigest, ConfigDigest: productionDigest("9"),
		RootFSDiffIDs: []string{productionDigest("a")}, OS: "linux", Architecture: "arm64",
	}, nil
}

func (runner *capturingFreshContainerRunner) RunFreshContainer(
	_ context.Context,
	request localci.FreshContainerRequest,
) (localci.FreshContainerResult, error) {
	runner.request = request
	return localci.FreshContainerResult{Status: gatecontract.ResultStatusPassed}, nil
}

func (*capturingFreshContainerRunner) RecoverFreshContainer(
	context.Context,
	localci.FreshContainerRecoveryRequest,
) (localci.FreshContainerResult, error) {
	return localci.FreshContainerResult{}, errors.New("unexpected container recovery")
}

func (*capturingFreshContainerRunner) ProbeFreshContainerRecovery(
	context.Context,
	localci.FreshContainerRecoveryRequest,
) (localci.FreshContainerRecoveryObservation, error) {
	return localci.FreshContainerRecoveryObservation{}, errors.New("unexpected container recovery probe")
}

func (*capturingFreshContainerRunner) CleanupUnprovedFreshContainer(
	context.Context,
	localci.FreshContainerCleanupRequest,
) (localci.FreshContainerResult, error) {
	return localci.FreshContainerResult{}, errors.New("unexpected container recovery cleanup")
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
	if _, ok := dependencies.CandidateBuilder.(*productionCandidateBuildService); !ok {
		t.Fatalf("CandidateBuilder = %T", dependencies.CandidateBuilder)
	}
	if _, ok := dependencies.PromotionWatcher.(*localci.PromotionController); !ok {
		t.Fatalf("PromotionWatcher = %T", dependencies.PromotionWatcher)
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

func TestProductionBareTrustedRefPromotesBuiltCandidateWithRealEd25519(t *testing.T) {
	fixture := newProductionTestFixture(t)
	candidateCommit, tree := commitProductionCandidate(t, fixture)
	promotion := prepareProductionCandidatePromotion(t, &fixture, candidateCommit, tree)
	advanceAndPromoteProductionCandidate(t, promotion)
	assertProductionCandidatePromoted(t, promotion)
}

func commitProductionCandidate(t *testing.T, fixture productionTestFixture) (string, localci.ReadOnlyGitTree) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(fixture.sourceRepo, "go.mod"), []byte("module example.invalid/promoted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runProductionGit(t, fixture.sourceRepo, "add", "--", "go.mod")
	runProductionGit(t, fixture.sourceRepo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "candidate")
	candidateCommit := productionGitLine(t, fixture.sourceRepo, "rev-parse", "HEAD^{commit}")
	candidateTree := productionGitLine(t, fixture.sourceRepo, "rev-parse", "HEAD^{tree}")
	source := gatecontract.SourceSpec{
		Kind: gatecontract.SourceKindCommit, ObjectFormat: gatecontract.GitObjectFormatSHA1,
		Commit: &gatecontract.CommitSource{SHA: candidateCommit}, SourceTreeSHA: candidateTree,
	}
	tree, err := localci.LoadReadOnlyGitTree(context.Background(), fixture.sourceRepo, source)
	if err != nil {
		t.Fatal(err)
	}
	return candidateCommit, tree
}

func prepareProductionCandidatePromotion(
	t *testing.T,
	fixture *productionTestFixture,
	candidateCommit string,
	tree localci.ReadOnlyGitTree,
) productionCandidatePromotionFixture {
	t.Helper()
	promotion, err := newProductionPromotionAuthority(context.Background(), fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := promotion.state.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	createdAt := time.Now().UTC().Truncate(time.Second)
	plan, err := promotion.candidates.Plan(context.Background(), accepted, localci.PromotionCandidatePlanRequest{
		Tree: tree, PolicyDigest: accepted.PolicyDigest, Platform: fixture.config.Platform,
		RepoID: fixture.config.RepoID, TrustedRef: fixture.config.TrustedRef, TrustedCommit: candidateCommit,
		CreatedAt: createdAt, ExpiresAt: createdAt.Add(time.Hour),
	})
	if err != nil || !plan.BuildRequired {
		t.Fatalf("Plan() = %+v, err=%v", plan, err)
	}
	builder, err := localci.NewImageBuilder(productionBuildKitRunnerStub{digest: productionDigest("8")})
	if err != nil {
		t.Fatal(err)
	}
	if err := promotion.candidates.ExecuteBuild(
		context.Background(), plan.WorkloadID, builder, productionCandidateIdentityResolverStub{},
	); err != nil {
		t.Fatal(err)
	}
	controller, err := localci.NewPromotionController(
		promotion.candidates, promotion.state, promotion.authority, promotion.signer, 20*time.Millisecond,
	)
	if err != nil {
		t.Fatal(err)
	}
	return productionCandidatePromotionFixture{
		production: fixture, authority: promotion, controller: controller,
		tree: tree, accepted: accepted, candidateCommit: candidateCommit,
	}
}

func advanceAndPromoteProductionCandidate(t *testing.T, fixture productionCandidatePromotionFixture) {
	t.Helper()
	controller := fixture.controller
	if err := controller.PromoteReady(context.Background()); err != nil {
		t.Fatalf("PromoteReady(old trusted tip) error = %v", err)
	}
	config := fixture.production.config
	runProductionGit(t, "", "--git-dir="+config.TrustedRepository, "fetch", "-q", "--no-tags", "--", fixture.production.sourceRepo, fixture.candidateCommit)
	runProductionGit(t, "", "--git-dir="+config.TrustedRepository, "update-ref", config.TrustedRef, fixture.candidateCommit, fixture.production.commit)
	if err := controller.PromoteReady(context.Background()); err != nil {
		t.Fatalf("PromoteReady(candidate trusted tip) error = %v", err)
	}
}

func assertProductionCandidatePromoted(t *testing.T, fixture productionCandidatePromotionFixture) {
	t.Helper()
	promotion := fixture.authority
	promoted, err := promotion.accepted.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if promoted.Generation != 2 || promoted.TrustedCommit != fixture.candidateCommit ||
		promoted.Image.PlatformManifestDigest != productionDigest("8") {
		t.Fatalf("promoted accepted record = %+v", promoted)
	}
	truth, err := localci.NewTruthImageEnsurer(promotion.accepted, promotion.candidates)
	if err != nil {
		t.Fatal(err)
	}
	result, err := truth.EnsureImage(context.Background(), localci.TruthImageEnsureRequest{
		Tree: fixture.tree, PolicyDigest: fixture.accepted.PolicyDigest, Platform: fixture.production.config.Platform,
	})
	if err != nil || result.Status != localci.TruthImageEnsureAccepted ||
		result.Image.PlatformManifestDigest != productionDigest("8") {
		t.Fatalf("EnsureImage(after promotion) = %+v, err=%v", result, err)
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
		"AcceptedImageRoot": "signed state", "CandidateStateRoot": "durable candidate authority",
		"CandidateBuildRoot": "candidate isolation",
		"TrustedSourceRoot":  "snapshot mount boundary", "SeccompProfile": "container policy",
		"Platform": "image platform", "RepoID": "repository identity", "TrustedRef": "admission ref",
		"TrustedRepository": "external bare mirror", "AcceptedImageSigners": "signature trust root",
		"PromotionSigner": "host signing authority", "CandidateTTLSeconds": "candidate expiry",
		"PromotionPollMillis": "watcher cadence",
	})
	assertProductionFields(t, reflect.TypeFor[productionTrustedKey](), map[string]string{
		"Signer": "key identity", "PublicKey": "Ed25519 verification material",
	})
	assertProductionFields(t, reflect.TypeFor[productionPromotionKey](), map[string]string{
		"Signer": "host key identity", "PrivateKeyFile": "repository-external signing key",
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
	writeProductionBuildInputs(t, sourceRepo)
	runProductionGit(t, sourceRepo, "add", "--", ".")
	runProductionGit(t, sourceRepo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "trusted")
	commit := productionGitLine(t, sourceRepo, "rev-parse", "HEAD^{commit}")
	tree := productionGitLine(t, sourceRepo, "rev-parse", "HEAD^{tree}")

	trustedRepository := filepath.Join(root, "trusted.git")
	runProductionGit(t, "", "clone", "-q", "--bare", "--", sourceRepo, trustedRepository)
	chmodPrivate(t, trustedRepository)
	acceptedRoot := makePrivateDirectory(t, root, "accepted")
	candidateStateRoot := makePrivateDirectory(t, root, "candidate-state")
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
	privateKeyFile := filepath.Join(root, "promotion-private.key")
	privateKeyData := base64.StdEncoding.EncodeToString(privateKey) + "\n"
	if err := os.WriteFile(privateKeyFile, []byte(privateKeyData), 0o600); err != nil {
		t.Fatal(err)
	}
	config := productionCoordinatorConfig{
		AcceptedImageRoot: acceptedRoot, CandidateStateRoot: candidateStateRoot,
		CandidateBuildRoot: buildRoot, TrustedSourceRoot: trustedSourceRoot,
		SeccompProfile: seccomp, Platform: "linux/arm64", RepoID: "example/repository",
		TrustedRef: "refs/heads/main", TrustedRepository: trustedRepository,
		AcceptedImageSigners: []productionTrustedKey{{
			Signer: signer, PublicKey: base64.StdEncoding.EncodeToString(publicKey),
		}},
		PromotionSigner:     productionPromotionKey{Signer: signer, PrivateKeyFile: privateKeyFile},
		CandidateTTLSeconds: 3600, PromotionPollMillis: 20,
	}
	fixture := productionTestFixture{
		config: config, signer: signer, privateKey: privateKey, commit: commit, tree: tree, sourceRepo: sourceRepo,
	}
	baseTree, err := localci.LoadReadOnlyGitTree(context.Background(), sourceRepo, productionSourceSpec(fixture))
	if err != nil {
		t.Fatal(err)
	}
	baseInputs, err := localci.ResolveGateImageInputs(baseTree, productionDigest("1"), config.Platform)
	if err != nil {
		t.Fatal(err)
	}
	fixture.acceptedInputDigest = baseInputs.ImageInputDigest
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
		PolicyDigest: policyDigest, ImageInputDigest: fixture.acceptedInputDigest,
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

func writeProductionBuildInputs(t *testing.T, repository string) {
	t.Helper()
	files := map[string]string{
		"go.mod":                         "module example.invalid/gate\n",
		"go.sum":                         "sum\n",
		"cmd/super-dolphin-gate/main.go": "package main\n",
		"build/gate/Dockerfile": "ARG GO_IMAGE=golang@sha256:" + strings.Repeat("b", 64) + "\n" +
			"FROM ${GO_IMAGE} AS build\nCOPY go.mod go.sum ./\n" +
			"COPY cmd/super-dolphin-gate/main.go ./cmd/super-dolphin-gate/main.go\n" +
			"RUN --network=none go build -o /out/gate ./cmd/super-dolphin-gate\n" +
			"FROM scratch\nCOPY --from=build /out/gate /gate\nENTRYPOINT [\"/gate\"]\n",
		"build/gate/inputs.json": "{\n  \"schema_version\": \"1\",\n  \"dockerfile\": \"build/gate/Dockerfile\",\n" +
			"  \"inputs\": [\"build/gate/Dockerfile\", \"build/gate/inputs.json\", \"build/gate/toolchain.lock\", " +
			"\"cmd/super-dolphin-gate/main.go\", \"go.mod\", \"go.sum\"]\n}\n",
		"build/gate/toolchain.lock": "{\n  \"schema_version\": \"1\",\n  \"buildkit_version\": \"v0.26.2\",\n" +
			"  \"dockerfile_frontend\": \"builtin:dockerfile.v1\",\n  \"target_platforms\": [\"linux/arm64\"],\n" +
			"  \"base_images\": [{\"name\":\"GO_IMAGE\",\"reference\":\"golang@sha256:" + strings.Repeat("b", 64) + "\"}],\n" +
			"  \"dependency_sources\": [\"go.sum\"],\n  \"network_policy\": \"locked-dependencies\"\n}\n",
	}
	for path, data := range files {
		absolute := filepath.Join(repository, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
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
