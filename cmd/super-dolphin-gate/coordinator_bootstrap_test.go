package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
	"golang.org/x/sync/errgroup"
)

type productionBootstrapRunnerVerifierStub struct {
	err      error
	observed gatecontract.ImageIdentity
}

type productionBootstrapRuntimeStub struct {
	mu            sync.Mutex
	privateKey    ed25519.PrivateKey
	executeErr    error
	containerErr  error
	imageRegistry string
	executeCount  int
	cleanupCount  int
}

func (runtime *productionBootstrapRuntimeStub) VerifyRunner(context.Context, gatecontract.ImageIdentity) error {
	return nil
}

func (runtime *productionBootstrapRuntimeStub) ExecuteController(
	_ context.Context,
	_ productionCoordinatorConfig,
	root productionBootstrapRoot,
	request productionBootstrapRequest,
	requestDigest string,
) (productionBootstrapAttestation, error) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.executeCount++
	if runtime.executeErr != nil {
		return productionBootstrapAttestation{}, runtime.executeErr
	}
	started := time.Now().UTC().Truncate(time.Millisecond)
	image := root.Runner
	image.Registry = root.CandidateRegistry
	if runtime.imageRegistry != "" {
		image.Registry = runtime.imageRegistry
	}
	record := gatecontract.AcceptedImageRecord{
		SchemaVersion: gatecontract.AcceptedImageRecordSchemaVersion,
		RepoID:        root.RepoID, TrustedRef: root.TrustedRef, TrustedCommit: root.BaselineCommit,
		SourceTree: root.BaselineTree, PolicyDigest: root.PolicyDigest, ImageInputDigest: root.ImageInputDigest,
		Image: image, Runner: gatecontract.TrustedRunnerIdentity{
			BinaryDigest: root.Controller.BinaryDigest, Signer: root.Controller.Signer, PolicyDigest: root.PolicyDigest,
		},
		Generation: 1, AcceptedAt: started.Add(time.Second), Signer: root.BootstrapSigner,
	}
	payload, err := gatecontract.AcceptedImageSigningPayload(record)
	if err != nil {
		return productionBootstrapAttestation{}, err
	}
	record.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(runtime.privateKey, payload))
	argvDigest, err := productionBootstrapJSONDigest(productionBootstrapContainerArgv(requestDigest))
	if err != nil {
		return productionBootstrapAttestation{}, err
	}
	attestation := productionBootstrapAttestation{
		SchemaVersion: productionBootstrapProtocolVersion, Challenge: request.Challenge,
		RootDigest: request.RootDigest, RequestDigest: requestDigest, ControllerDigest: root.Controller.BinaryDigest,
		Record: record, ContainerID: strings.Repeat("a", 64), ContainerArgvDigest: argvDigest,
		ContainerLogDigest: productionDigest("e"), ContainerInspectDigest: productionDigest("f"),
		StartedAt: started, CompletedAt: started.Add(2 * time.Second),
	}
	unsigned := attestation
	data, err := json.Marshal(unsigned)
	if err != nil {
		return productionBootstrapAttestation{}, err
	}
	attestation.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(runtime.privateKey, data))
	return attestation, nil
}

func (runtime *productionBootstrapRuntimeStub) VerifyAndRemoveContainer(
	context.Context,
	productionCoordinatorConfig,
	productionBootstrapRoot,
	productionBootstrapRequest,
	productionBootstrapAttestation,
) error {
	return runtime.containerErr
}

func (runtime *productionBootstrapRuntimeStub) CleanupStaleContainers(
	context.Context,
	productionBootstrapRoot,
	string,
) error {
	runtime.mu.Lock()
	runtime.cleanupCount++
	runtime.mu.Unlock()
	return nil
}

func (verifier *productionBootstrapRunnerVerifierStub) VerifyRunner(
	_ context.Context,
	identity gatecontract.ImageIdentity,
) error {
	verifier.observed = identity
	return verifier.err
}

func TestProductionBootstrapRootRealEd25519InstallLoadAndConcurrentSingleWinner(t *testing.T) {
	root, trusted, privateKey := newProductionBootstrapRootCryptoFixture(t)
	directory := privateProductionBootstrapDirectory(t)
	path := filepath.Join(directory, "bootstrap-root.json")
	assertProductionBootstrapConcurrentInstall(t, path, root, trusted)
	loaded, err := loadProductionBootstrapRoot(path, trusted)
	if err != nil {
		t.Fatalf("loadProductionBootstrapRoot() error = %v", err)
	}
	if !reflect.DeepEqual(loaded, root) {
		t.Fatalf("loaded root = %#v, want %#v", loaded, root)
	}
	if info, err := os.Lstat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("bootstrap root metadata = %v, %v", info, err)
	}
	root.BaselineTree = strings.Repeat("c", 40)
	payload, err := productionBootstrapRootSigningPayload(root)
	if err != nil {
		t.Fatal(err)
	}
	root.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	if reflect.DeepEqual(root, loaded) {
		t.Fatal("loaded root aliases caller mutation")
	}
}

func TestProductionBootstrapRootRejectsDriftUnknownFieldsAndSharedMode(t *testing.T) {
	root, trusted, _ := newProductionBootstrapRootCryptoFixture(t)
	tests := []struct {
		name   string
		mutate func(*productionBootstrapRoot)
	}{
		{name: "baseline", mutate: func(root *productionBootstrapRoot) { root.BaselineCommit = strings.Repeat("f", 40) }},
		{name: "runner", mutate: func(root *productionBootstrapRoot) { root.Runner.ConfigDigest = productionDigest("f") }},
		{name: "signature", mutate: func(root *productionBootstrapRoot) {
			root.Signature = base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
		}},
		{name: "public_key", mutate: func(root *productionBootstrapRoot) {
			root.Ed25519PublicKey = base64.StdEncoding.EncodeToString(make([]byte, ed25519.PublicKeySize))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := cloneProductionBootstrapRoot(root)
			test.mutate(&changed)
			if err := verifyProductionBootstrapRoot(changed, trusted); err == nil {
				t.Fatal("verifyProductionBootstrapRoot() accepted drift")
			}
		})
	}
	directory := privateProductionBootstrapDirectory(t)
	path := filepath.Join(directory, "bootstrap-root.json")
	var object map[string]any
	if err := json.Unmarshal(mustProductionBootstrapJSON(t, root), &object); err != nil {
		t.Fatal(err)
	}
	object["fallback_to_host"] = true
	writePrivateJSON(t, path, object)
	if _, err := loadProductionBootstrapRoot(path, trusted); err == nil {
		t.Fatal("loadProductionBootstrapRoot() accepted unknown fallback field")
	}
	writeProductionBootstrapRootFixture(t, path, root, nil)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadProductionBootstrapRoot(path, trusted); err == nil || !strings.Contains(err.Error(), "private regular file") {
		t.Fatalf("loadProductionBootstrapRoot(shared mode) error = %v", err)
	}
}

func TestProductionBootstrapEmptyStateBuildsAndAtomicallyInstallsGenerationOne(t *testing.T) {
	fixture := newProductionTestFixture(t)
	if err := os.Remove(filepath.Join(fixture.config.AcceptedImageRoot, "accepted-image.json")); err != nil {
		t.Fatal(err)
	}
	root, err := loadProductionBootstrapRoot(fixture.config.BootstrapRootFile, fixture.config.AcceptedImageSigners)
	if err != nil {
		t.Fatal(err)
	}
	runProductionGit(t, "", "--git-dir="+fixture.config.TrustedRepository, "config", "remote.origin.url", root.RemoteURL)
	promotion, err := newProductionPromotionAuthority(context.Background(), fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &productionBootstrapRuntimeStub{privateKey: fixture.privateKey}
	record, err := loadOrBootstrapProductionAcceptedImage(context.Background(), fixture.config, promotion, runtime)
	if err != nil {
		t.Fatalf("loadOrBootstrapProductionAcceptedImage() error = %v", err)
	}
	if record.Generation != 1 || record.TrustedCommit != root.BaselineCommit || runtime.executeCount != 1 {
		t.Fatalf("bootstrap record = %#v, execute count = %d", record, runtime.executeCount)
	}
	stored, err := promotion.state.Load(context.Background())
	if err != nil || !reflect.DeepEqual(stored, record) {
		t.Fatalf("stored bootstrap record = %#v, %v", stored, err)
	}
}

func TestProductionBootstrapConcurrentFirstSubmitBuildsOnceAndReusesGenerationOne(t *testing.T) {
	fixture, promotion := newEmptyProductionBootstrapFixture(t)
	runtime := &productionBootstrapRuntimeStub{privateKey: fixture.privateKey}
	const workers = 12
	records := make(chan gatecontract.AcceptedImageRecord, workers)
	var group errgroup.Group
	for range workers {
		group.Go(func() error {
			record, err := loadOrBootstrapProductionAcceptedImage(context.Background(), fixture.config, promotion, runtime)
			if err == nil {
				records <- record
			}
			return err
		})
	}
	if err := group.Wait(); err != nil {
		t.Fatalf("concurrent bootstrap error = %v", err)
	}
	close(records)
	var first gatecontract.AcceptedImageRecord
	for record := range records {
		if first.Generation == 0 {
			first = record
			continue
		}
		if !reflect.DeepEqual(record, first) {
			t.Fatal("concurrent submit did not reuse the same generation-one record")
		}
	}
	runtime.mu.Lock()
	executeCount := runtime.executeCount
	runtime.mu.Unlock()
	if executeCount != 1 || first.Generation != 1 {
		t.Fatalf("controller executions = %d, generation = %d", executeCount, first.Generation)
	}
}

func TestProductionBootstrapFailureLeavesNoAcceptedStateAndRetrySucceeds(t *testing.T) {
	fixture, promotion := newEmptyProductionBootstrapFixture(t)
	injected := errors.New("controller attestation unavailable")
	failing := &productionBootstrapRuntimeStub{privateKey: fixture.privateKey, executeErr: injected}
	if _, err := loadOrBootstrapProductionAcceptedImage(context.Background(), fixture.config, promotion, failing); !errors.Is(err, injected) {
		t.Fatalf("failed bootstrap error = %v", err)
	}
	if _, err := promotion.state.Load(context.Background()); !errors.Is(err, localci.ErrAcceptedImageStateNotFound) {
		t.Fatalf("accepted state after failed controller = %v", err)
	}
	failing.executeErr = nil
	failing.containerErr = errors.New("forged container evidence")
	if _, err := loadOrBootstrapProductionAcceptedImage(context.Background(), fixture.config, promotion, failing); err == nil {
		t.Fatal("bootstrap accepted forged container evidence")
	}
	if _, err := promotion.state.Load(context.Background()); !errors.Is(err, localci.ErrAcceptedImageStateNotFound) {
		t.Fatalf("accepted state after failed container verification = %v", err)
	}
	failing.containerErr = nil
	failing.imageRegistry = "registry.example.invalid/forged-candidate"
	if _, err := loadOrBootstrapProductionAcceptedImage(context.Background(), fixture.config, promotion, failing); err == nil {
		t.Fatal("bootstrap accepted candidate registry outside signed root")
	}
	if _, err := promotion.state.Load(context.Background()); !errors.Is(err, localci.ErrAcceptedImageStateNotFound) {
		t.Fatalf("accepted state after candidate registry drift = %v", err)
	}
	successful := &productionBootstrapRuntimeStub{privateKey: fixture.privateKey}
	record, err := loadOrBootstrapProductionAcceptedImage(context.Background(), fixture.config, promotion, successful)
	if err != nil || record.Generation != 1 {
		t.Fatalf("bootstrap retry record = %#v, %v", record, err)
	}
}

func TestProductionBootstrapRestartReusesGenerationOneWithoutController(t *testing.T) {
	fixture, promotion := newEmptyProductionBootstrapFixture(t)
	firstRuntime := &productionBootstrapRuntimeStub{privateKey: fixture.privateKey}
	first, err := loadOrBootstrapProductionAcceptedImage(context.Background(), fixture.config, promotion, firstRuntime)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := newProductionPromotionAuthority(context.Background(), fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	blockedRuntime := &productionBootstrapRuntimeStub{privateKey: fixture.privateKey, executeErr: errors.New("must not execute")}
	loaded, err := loadOrBootstrapProductionAcceptedImage(context.Background(), fixture.config, restarted, blockedRuntime)
	if err != nil || !reflect.DeepEqual(loaded, first) {
		t.Fatalf("restart record = %#v, %v", loaded, err)
	}
	if blockedRuntime.executeCount != 0 {
		t.Fatalf("restart executed bootstrap controller %d times", blockedRuntime.executeCount)
	}
}

func newEmptyProductionBootstrapFixture(
	t *testing.T,
) (productionTestFixture, *productionPromotionAuthority) {
	t.Helper()
	fixture := newProductionTestFixture(t)
	statePath := filepath.Join(fixture.config.AcceptedImageRoot, "accepted-image.json")
	if err := os.Remove(statePath); err != nil {
		t.Fatal(err)
	}
	root, err := loadProductionBootstrapRoot(fixture.config.BootstrapRootFile, fixture.config.AcceptedImageSigners)
	if err != nil {
		t.Fatal(err)
	}
	runProductionGit(t, "", "--git-dir="+fixture.config.TrustedRepository, "config", "remote.origin.url", root.RemoteURL)
	promotion, err := newProductionPromotionAuthority(context.Background(), fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	return fixture, promotion
}

func TestProductionBootstrapPrerequisitesFailClosedOnBaselineAndRunnerDrift(t *testing.T) {
	fixture := newProductionTestFixture(t)
	root, err := loadProductionBootstrapRoot(fixture.config.BootstrapRootFile, fixture.config.AcceptedImageSigners)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := newProductionGitAuthority(context.Background(), fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	runProductionGit(t, "", "--git-dir="+fixture.config.TrustedRepository, "config", "remote.origin.url", root.RemoteURL)
	root.BaselineCommit = strings.Repeat("f", 40)
	writeProductionBootstrapRootFixture(t, fixture.config.BootstrapRootFile, root, fixture.bootstrapRootKey)
	if _, _, err := verifyProductionBootstrapPrerequisites(context.Background(), fixture.config, authority, &productionBootstrapRunnerVerifierStub{}); err == nil || !strings.Contains(err.Error(), "baseline commit") {
		t.Fatalf("verifyProductionBootstrapPrerequisites(unknown baseline) error = %v", err)
	}
	root.BaselineCommit = fixture.commit
	root.BaselineTree = strings.Repeat("f", 40)
	writeProductionBootstrapRootFixture(t, fixture.config.BootstrapRootFile, root, fixture.bootstrapRootKey)
	if _, _, err := verifyProductionBootstrapPrerequisites(context.Background(), fixture.config, authority, &productionBootstrapRunnerVerifierStub{}); err == nil || !strings.Contains(err.Error(), "tree drifted") {
		t.Fatalf("verifyProductionBootstrapPrerequisites(tree drift) error = %v", err)
	}
	root.BaselineTree = fixture.tree
	writeProductionBootstrapRootFixture(t, fixture.config.BootstrapRootFile, root, fixture.bootstrapRootKey)
	runnerErr := errors.New("forged container identity")
	if _, _, err := verifyProductionBootstrapPrerequisites(context.Background(), fixture.config, authority, &productionBootstrapRunnerVerifierStub{err: runnerErr}); !errors.Is(err, runnerErr) {
		t.Fatalf("verifyProductionBootstrapPrerequisites(runner drift) error = %v", err)
	}
}

func TestProductionBootstrapInspectAcceptsExactIdentityAndRejectsForgery(t *testing.T) {
	root, _, _ := newProductionBootstrapRootCryptoFixture(t)
	document := productionBootstrapInspectFixture(root.Runner)
	reference := root.Runner.Registry + "@" + root.Runner.PlatformManifestDigest
	if err := validateProductionBootstrapInspect(document, root.Runner, reference); err != nil {
		t.Fatalf("validateProductionBootstrapInspect() error = %v", err)
	}
	document.Descriptor.Digest = productionDigest("f")
	if err := validateProductionBootstrapInspect(document, root.Runner, reference); err == nil {
		t.Fatal("validateProductionBootstrapInspect() accepted forged manifest")
	}
}

func TestProductionBootstrapInspectDigestsRejectsReferenceAndRepoDigestDrift(t *testing.T) {
	root, _, _ := newProductionBootstrapRootCryptoFixture(t)
	reference := root.Runner.Registry + "@" + root.Runner.PlatformManifestDigest

	for _, testCase := range []struct {
		name      string
		document  productionBootstrapImageInspect
		reference string
	}{
		{name: "requested reference", document: productionBootstrapInspectFixture(root.Runner), reference: root.Runner.Registry},
		{
			name: "manifest repo digest",
			document: productionBootstrapImageInspect{ID: root.Runner.ConfigDigest, Descriptor: &struct {
				Digest      string            `json:"digest"`
				Annotations map[string]string `json:"annotations"`
			}{Digest: root.Runner.PlatformManifestDigest, Annotations: map[string]string{"config.digest": root.Runner.ConfigDigest}},
			},
			reference: reference,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if err := validateProductionBootstrapInspectDigests(testCase.document, root.Runner, testCase.reference); err == nil {
				t.Fatal("validateProductionBootstrapInspectDigests() accepted identity drift")
			}
		})
	}
}

func TestProductionBootstrapInspectAcceptsManifestBoundConfigWithoutAnnotation(t *testing.T) {
	root, _, _ := newProductionBootstrapRootCryptoFixture(t)
	document := productionBootstrapInspectFixture(root.Runner)
	delete(document.Descriptor.Annotations, "config.digest")
	document.ID = root.Runner.PlatformManifestDigest
	reference := root.Runner.Registry + "@" + root.Runner.PlatformManifestDigest
	if err := validateProductionBootstrapInspect(document, root.Runner, reference); err != nil {
		t.Fatalf("validateProductionBootstrapInspect() error = %v", err)
	}
	document.ID = productionDigest("f")
	if err := validateProductionBootstrapInspect(document, root.Runner, reference); err == nil {
		t.Fatal("validateProductionBootstrapInspect() accepted an unbound display ID")
	}
	document.ID = root.Runner.PlatformManifestDigest
	document.Descriptor.Annotations["config.digest"] = productionDigest("f")
	if err := validateProductionBootstrapInspect(document, root.Runner, reference); err == nil {
		t.Fatal("validateProductionBootstrapInspect() accepted a forged config annotation")
	}
}

func TestProductionBootstrapInspectAcceptsDockerHubFamiliarDigestReference(t *testing.T) {
	root, _, _ := newProductionBootstrapRootCryptoFixture(t)
	root.Runner.Registry = "docker.io/library/bootstrap-runner"
	document := productionBootstrapInspectFixture(root.Runner)
	document.RepoDigests = []string{
		"bootstrap-runner@" + root.Runner.OCIIndexDigest,
		"bootstrap-runner@" + root.Runner.PlatformManifestDigest,
	}
	reference := root.Runner.Registry + "@" + root.Runner.PlatformManifestDigest
	if err := validateProductionBootstrapInspect(document, root.Runner, reference); err != nil {
		t.Fatalf("validateProductionBootstrapInspect() error = %v", err)
	}
}

func TestProductionBootstrapControllerSnapshotVerifiesMacOSCodeRequirementAndDigest(t *testing.T) {
	if _, err := os.Stat("/usr/bin/codesign"); err != nil {
		t.Skip("macOS codesign is unavailable")
	}
	directory := privateProductionBootstrapDirectory(t)
	controller := filepath.Join(directory, "controller")
	data, err := os.ReadFile("/usr/bin/true")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(controller, data, 0o500); err != nil {
		t.Fatal(err)
	}
	acceptedRoot := privateProductionBootstrapDirectory(t)
	identity := productionBootstrapControllerIdentity{
		BinaryDigest:          productionBootstrapFileDigest(t, controller),
		DesignatedRequirement: `identifier "com.apple.true" and anchor apple`,
		Signer: gatecontract.SignerIdentity{
			KeyID: "bootstrap-test", KeyEpoch: 1, Algorithm: gatecontract.SignatureAlgorithmEd25519,
		},
	}
	snapshot, cleanup, err := snapshotProductionBootstrapController(productionCoordinatorConfig{
		AcceptedImageRoot: acceptedRoot, BootstrapControllerFile: controller,
	}, identity)
	if err != nil {
		t.Fatalf("snapshotProductionBootstrapController() error = %v", err)
	}
	defer cleanup()
	if snapshot == controller || productionBootstrapFileDigest(t, snapshot) != identity.BinaryDigest {
		t.Fatal("controller snapshot did not preserve the verified executable identity")
	}
	identity.BinaryDigest = productionDigest("0")
	if _, cleanup, err := snapshotProductionBootstrapController(productionCoordinatorConfig{
		AcceptedImageRoot: acceptedRoot, BootstrapControllerFile: controller,
	}, identity); err == nil {
		cleanup()
		t.Fatal("controller snapshot accepted a drifted executable digest")
	}
}

func TestProductionBootstrapExternalControllerProtocolProducesVerifiableAttestation(t *testing.T) {
	if _, err := os.Stat("/usr/bin/codesign"); err != nil {
		t.Skip("macOS codesign is unavailable")
	}
	fixture := newProductionTestFixture(t)
	home, _, _ := configureProductionHostDockerCommandTest(t)
	root := installProductionBootstrapTestController(t, &fixture)
	key := productionBootstrapControllerTestKey{
		Signer: root.BootstrapSigner, PrivateKey: base64.StdEncoding.EncodeToString(fixture.privateKey),
	}
	writePrivateJSON(t, filepath.Join(home, productionBootstrapControllerTestKeyFile), key)
	rootDigest, err := productionBootstrapRootDigest(root, fixture.config.AcceptedImageSigners)
	if err != nil {
		t.Fatal(err)
	}
	request, requestDigest, err := newProductionBootstrapRequest(fixture.config, root, rootDigest)
	if err != nil {
		t.Fatal(err)
	}
	attestation, err := (productionBootstrapHostRuntime{}).ExecuteController(
		context.Background(), fixture.config, root, request, requestDigest,
	)
	if err != nil {
		t.Fatalf("ExecuteController() error = %v", err)
	}
	if err := verifyProductionBootstrapAttestation(fixture.config, root, request, requestDigest, attestation); err != nil {
		t.Fatalf("verifyProductionBootstrapAttestation() error = %v", err)
	}
}

func installProductionBootstrapTestController(t *testing.T, fixture *productionTestFixture) productionBootstrapRoot {
	t.Helper()
	controllerDirectory := privateProductionBootstrapDirectory(t)
	controller := filepath.Join(controllerDirectory, "bootstrap-controller")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(controller, data, 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(
		"/usr/bin/codesign", "--force", "--sign", "-", "--identifier",
		"com.super-dolphin.bootstrap.controller-protocol-e2e", controller,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("codesign bootstrap controller protocol fixture: %v: %s", err, strings.TrimSpace(string(output)))
	}
	if err := os.Chmod(controller, 0o500); err != nil {
		t.Fatal(err)
	}
	requirement := productionBootstrapDesignatedRequirement(t, controller)
	root, err := loadProductionBootstrapRoot(fixture.config.BootstrapRootFile, fixture.config.AcceptedImageSigners)
	if err != nil {
		t.Fatal(err)
	}
	root.Controller = productionBootstrapControllerIdentity{
		BinaryDigest:          productionBootstrapFileDigest(t, controller),
		DesignatedRequirement: requirement, Signer: root.BootstrapSigner,
	}
	fixture.config.BootstrapControllerFile = controller
	writeProductionBootstrapRootFixture(t, fixture.config.BootstrapRootFile, root, fixture.bootstrapRootKey)
	root, err = loadProductionBootstrapRoot(fixture.config.BootstrapRootFile, fixture.config.AcceptedImageSigners)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func productionBootstrapDesignatedRequirement(t *testing.T, path string) string {
	t.Helper()
	output, err := exec.Command("/usr/bin/codesign", "-d", "-r-", path).CombinedOutput()
	if err != nil {
		t.Fatalf("read controller designated requirement: %v: %s", err, strings.TrimSpace(string(output)))
	}
	for line := range strings.SplitSeq(string(output), "\n") {
		line = strings.TrimPrefix(strings.TrimSpace(line), "# ")
		if requirement, ok := strings.CutPrefix(line, "designated => "); ok {
			return strings.TrimSpace(requirement)
		}
	}
	t.Fatalf("codesign output lacks designated requirement: %s", strings.TrimSpace(string(output)))
	return ""
}

func assertProductionBootstrapConcurrentInstall(
	t *testing.T,
	path string,
	root productionBootstrapRoot,
	trusted []productionTrustedKey,
) {
	t.Helper()
	const workers = 8
	errorsSeen := make(chan error, workers)
	var group errgroup.Group
	for range workers {
		group.Go(func() error {
			errorsSeen <- installProductionBootstrapRoot(path, root, trusted)
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		t.Fatal(err)
	}
	close(errorsSeen)
	assertProductionBootstrapInstallResults(t, errorsSeen, workers)
}

func assertProductionBootstrapInstallResults(t *testing.T, errorsSeen <-chan error, workers int) {
	t.Helper()
	successes, conflicts := 0, 0
	for err := range errorsSeen {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, errProductionBootstrapRootExists):
			conflicts++
		default:
			t.Fatalf("installProductionBootstrapRoot() error = %v", err)
		}
	}
	if successes != 1 || conflicts != workers-1 {
		t.Fatalf("install results successes=%d conflicts=%d", successes, conflicts)
	}
}

func productionBootstrapInspectFixture(identity gatecontract.ImageIdentity) productionBootstrapImageInspect {
	document := productionBootstrapImageInspect{
		ID: identity.ConfigDigest, RepoDigests: []string{
			identity.Registry + "@" + identity.OCIIndexDigest,
			identity.Registry + "@" + identity.PlatformManifestDigest,
		},
		OS: identity.OS, Architecture: identity.Architecture, Variant: identity.Variant,
	}
	document.Descriptor = &struct {
		Digest      string            `json:"digest"`
		Annotations map[string]string `json:"annotations"`
	}{Digest: identity.PlatformManifestDigest, Annotations: map[string]string{"config.digest": identity.ConfigDigest}}
	document.RootFS = &struct {
		Type   string   `json:"Type"`
		Layers []string `json:"Layers"`
	}{Type: "layers", Layers: append([]string(nil), identity.RootFSDiffIDs...)}
	return document
}

func productionBootstrapRootForFixture(
	t *testing.T,
	config productionCoordinatorConfig,
	commit string,
	tree string,
	signer gatecontract.SignerIdentity,
	publicKey ed25519.PublicKey,
) (productionBootstrapRoot, productionTrustedKey, ed25519.PrivateKey) {
	t.Helper()
	rootPublicKey, rootPrivateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	rootSigner := gatecontract.SignerIdentity{
		KeyID: "bootstrap-root-test", KeyEpoch: 1, Algorithm: gatecontract.SignatureAlgorithmEd25519,
	}
	root := productionBootstrapRoot{
		SchemaVersion: productionBootstrapRootSchemaVersion, RepoID: config.RepoID,
		RemoteURL: "https://example.invalid/repository.git", TrustedRef: config.TrustedRef,
		ObjectFormat:   gatecontract.GitObjectFormatSHA1,
		BaselineCommit: commit, BaselineTree: tree,
		PolicyDigest: productionDigest("c"), ImageInputDigest: productionDigest("d"),
		ToolchainDigest: productionDigest("e"), ImageSchemaVersion: "1",
		CandidateRegistry: "registry.example.invalid/bootstrap-candidate",
		Runner: gatecontract.ImageIdentity{
			Registry: "registry.example.invalid/bootstrap-runner", OCIIndexDigest: productionDigest("8"),
			PlatformManifestDigest: productionDigest("9"), ConfigDigest: productionDigest("a"),
			RootFSDiffIDs: []string{productionDigest("b")}, OS: "linux", Architecture: "arm64",
		},
		Controller: productionBootstrapControllerIdentity{
			BinaryDigest:          productionBootstrapFileDigest(t, config.BootstrapControllerFile),
			DesignatedRequirement: `identifier "super-dolphin-bootstrap-test"`, Signer: signer,
		},
		Signer: rootSigner, Ed25519PublicKey: base64.StdEncoding.EncodeToString(rootPublicKey),
		BootstrapSigner: signer, BootstrapPublicKey: base64.StdEncoding.EncodeToString(publicKey),
		VerifierVersion: productionBootstrapVerifierVersion,
	}
	trusted := productionTrustedKey{Signer: rootSigner, PublicKey: root.Ed25519PublicKey}
	return root, trusted, rootPrivateKey
}

func newProductionBootstrapRootCryptoFixture(
	t *testing.T,
) (productionBootstrapRoot, []productionTrustedKey, ed25519.PrivateKey) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	bootstrapPublicKey, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	signer := gatecontract.SignerIdentity{KeyID: "bootstrap-root-test", KeyEpoch: 1, Algorithm: gatecontract.SignatureAlgorithmEd25519}
	bootstrapSigner := gatecontract.SignerIdentity{KeyID: "bootstrap-execution-test", KeyEpoch: 1, Algorithm: gatecontract.SignatureAlgorithmEd25519}
	root := productionBootstrapRoot{
		SchemaVersion: productionBootstrapRootSchemaVersion, RepoID: "example/repository",
		RemoteURL: "https://example.invalid/repository.git", TrustedRef: "refs/heads/main",
		ObjectFormat:   gatecontract.GitObjectFormatSHA1,
		BaselineCommit: strings.Repeat("1", 40), BaselineTree: strings.Repeat("2", 40),
		PolicyDigest: productionDigest("7"), ImageInputDigest: productionDigest("8"),
		ToolchainDigest: productionDigest("a"), ImageSchemaVersion: "1",
		CandidateRegistry: "registry.example.invalid/bootstrap-candidate",
		Runner: gatecontract.ImageIdentity{
			Registry: "registry.example.invalid/bootstrap-runner", OCIIndexDigest: productionDigest("3"),
			PlatformManifestDigest: productionDigest("4"), ConfigDigest: productionDigest("5"),
			RootFSDiffIDs: []string{productionDigest("6")}, OS: "linux", Architecture: "arm64",
		},
		Controller: productionBootstrapControllerIdentity{
			BinaryDigest:          productionDigest("9"),
			DesignatedRequirement: `identifier "super-dolphin-bootstrap-test"`, Signer: bootstrapSigner,
		},
		Signer: signer, Ed25519PublicKey: base64.StdEncoding.EncodeToString(publicKey),
		BootstrapSigner:    bootstrapSigner,
		BootstrapPublicKey: base64.StdEncoding.EncodeToString(bootstrapPublicKey),
		VerifierVersion:    productionBootstrapVerifierVersion,
	}
	payload, err := productionBootstrapRootSigningPayload(root)
	if err != nil {
		t.Fatal(err)
	}
	root.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	return root, []productionTrustedKey{{Signer: signer, PublicKey: root.Ed25519PublicKey}}, privateKey
}

func productionBootstrapFileDigest(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func writeProductionBootstrapRootFixture(
	t *testing.T,
	path string,
	root productionBootstrapRoot,
	privateKey ed25519.PrivateKey,
) {
	t.Helper()
	if privateKey != nil {
		root.Signature = ""
		payload, err := productionBootstrapRootSigningPayload(root)
		if err != nil {
			t.Fatal(err)
		}
		root.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	}
	if err := os.WriteFile(path, append(mustProductionBootstrapJSON(t, root), '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustProductionBootstrapJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func privateProductionBootstrapDirectory(t *testing.T) string {
	t.Helper()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	return directory
}
