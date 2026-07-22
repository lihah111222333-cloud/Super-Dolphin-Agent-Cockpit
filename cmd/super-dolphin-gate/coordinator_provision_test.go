package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

type productionProvisionRuntimeStub struct {
	verifyErr  error
	cloneErr   error
	afterClone func() error
	verified   gatecontract.ImageIdentity
	clones     int
}

func (stub *productionProvisionRuntimeStub) VerifyRunner(
	_ context.Context,
	identity gatecontract.ImageIdentity,
) error {
	stub.verified = identity
	return stub.verifyErr
}

func (stub *productionProvisionRuntimeStub) CloneTrustedRepository(
	_ context.Context,
	_ productionBootstrapRoot,
	destination string,
) error {
	stub.clones++
	if stub.cloneErr != nil {
		return stub.cloneErr
	}
	if err := os.Mkdir(destination, 0o700); err != nil {
		return err
	}
	if stub.afterClone != nil {
		return stub.afterClone()
	}
	return nil
}

func (stub *productionProvisionRuntimeStub) VerifyTrustedRepository(
	_ context.Context,
	_ productionBootstrapRoot,
	destination string,
) error {
	info, err := os.Stat(destination)
	if err != nil || !info.IsDir() {
		return errors.Join(errors.New("stub trusted repository is unavailable"), err)
	}
	return nil
}

func TestProductionProvisionInstallsClosureWithoutAcceptedSeed(t *testing.T) {
	fixture := newProductionProvisionFixture(t)
	runtimeStub := &productionProvisionRuntimeStub{}
	result, err := provisionProductionWithRuntime(context.Background(), fixture.manifest, runtimeStub)
	if err != nil {
		t.Fatalf("provisionProductionWithRuntime() error = %v", err)
	}
	if !reflect.DeepEqual(runtimeStub.verified, fixture.root.Runner) || runtimeStub.clones != 1 {
		t.Fatalf("provision runtime evidence = %#v, clones = %d", runtimeStub.verified, runtimeStub.clones)
	}
	config, err := loadProductionCoordinatorConfigFile(result.ProductionConfigFile)
	if err != nil {
		t.Fatalf("load installed config: %v", err)
	}
	if _, err := os.Stat(filepath.Join(config.AcceptedImageRoot, "accepted-image.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("provision seeded accepted state: %v", err)
	}
	launcher, err := os.ReadFile(result.LauncherPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(launcher), productionCoordinatorConfigEnv+"='") ||
		!strings.Contains(string(launcher), config.BootstrapControllerFile) {
		t.Fatalf("launcher does not pin installed production closure: %s", launcher)
	}
}

func TestProductionProvisionRepeatReusesVerifiedRoot(t *testing.T) {
	fixture := newProductionProvisionFixture(t)
	runtimeStub := &productionProvisionRuntimeStub{}
	first, err := provisionProductionWithRuntime(context.Background(), fixture.manifest, runtimeStub)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := provisionProductionWithRuntime(context.Background(), fixture.manifest, runtimeStub)
	if err != nil {
		t.Fatalf("repeat verified provision: %v", err)
	}
	if repeated != first || runtimeStub.clones != 1 {
		t.Fatalf("repeat provision result = %#v, clones = %d", repeated, runtimeStub.clones)
	}
}

func TestProductionProvisionFailurePublishesNothing(t *testing.T) {
	fixture := newProductionProvisionFixture(t)
	runtimeStub := &productionProvisionRuntimeStub{cloneErr: errors.New("clone failed")}
	if _, err := provisionProductionWithRuntime(context.Background(), fixture.manifest, runtimeStub); err == nil {
		t.Fatal("provision accepted failed trusted repository clone")
	}
	for _, path := range []string{fixture.manifest.InstallRoot, fixture.manifest.LauncherPath} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("failed provision published %q: %v", path, err)
		}
	}
}

func TestProductionProvisionRetriesVerifiedRootAfterLauncherConflict(t *testing.T) {
	fixture := newProductionProvisionFixture(t)
	foreignLauncher := []byte("foreign launcher\n")
	runtimeStub := &productionProvisionRuntimeStub{afterClone: func() error {
		return os.WriteFile(fixture.manifest.LauncherPath, foreignLauncher, 0o700)
	}}
	if _, err := provisionProductionWithRuntime(context.Background(), fixture.manifest, runtimeStub); err == nil {
		t.Fatal("provision overwrote launcher created after preflight")
	}
	if _, err := os.Stat(fixture.manifest.InstallRoot); err != nil {
		t.Fatalf("launcher conflict did not leave the published repairable root: %v", err)
	}
	assertFileData(t, fixture.manifest.LauncherPath, foreignLauncher)
	if err := os.Remove(fixture.manifest.LauncherPath); err != nil {
		t.Fatal(err)
	}
	runtimeStub.afterClone = nil
	if _, err := provisionProductionWithRuntime(context.Background(), fixture.manifest, runtimeStub); err != nil {
		t.Fatalf("retry verified provision residue: %v", err)
	}
	if runtimeStub.clones != 1 {
		t.Fatalf("repairable provision cloned %d times, want 1", runtimeStub.clones)
	}
}

func TestProductionProvisionDoesNotTakeOverUnknownRoot(t *testing.T) {
	fixture := newProductionProvisionFixture(t)
	if err := os.Mkdir(fixture.manifest.InstallRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(fixture.manifest.InstallRoot, "sentinel")
	if err := os.WriteFile(sentinel, []byte("foreign root\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := provisionProductionWithRuntime(context.Background(), fixture.manifest, &productionProvisionRuntimeStub{}); err == nil {
		t.Fatal("provision accepted an unknown install root")
	}
	assertFileData(t, sentinel, []byte("foreign root\n"))
	if _, err := os.Lstat(fixture.manifest.LauncherPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unknown root provision published launcher: %v", err)
	}
}

func TestProductionProvisionDoesNotReplaceRootPublishedAfterPreflight(t *testing.T) {
	fixture := newProductionProvisionFixture(t)
	var foreignRoot os.FileInfo
	runtimeStub := &productionProvisionRuntimeStub{afterClone: func() error {
		if err := os.Mkdir(fixture.manifest.InstallRoot, 0o700); err != nil {
			return err
		}
		var err error
		foreignRoot, err = os.Stat(fixture.manifest.InstallRoot)
		return err
	}}
	if _, err := provisionProductionWithRuntime(context.Background(), fixture.manifest, runtimeStub); err == nil {
		t.Fatal("provision replaced a root created after preflight")
	}
	installedRoot, err := os.Stat(fixture.manifest.InstallRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(foreignRoot, installedRoot) {
		t.Fatal("provision replaced the concurrently created unknown root")
	}
	entries, err := os.ReadDir(fixture.manifest.InstallRoot)
	if err != nil || len(entries) != 0 {
		t.Fatalf("concurrently created unknown root was modified: entries=%v err=%v", entries, err)
	}
	if _, err := os.Lstat(fixture.manifest.LauncherPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("root publication conflict installed launcher: %v", err)
	}
}

func TestProductionProvisionDoesNotTakeOverUnknownLauncher(t *testing.T) {
	fixture := newProductionProvisionFixture(t)
	foreignLauncher := []byte("foreign launcher\n")
	if err := os.WriteFile(fixture.manifest.LauncherPath, foreignLauncher, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := provisionProductionWithRuntime(context.Background(), fixture.manifest, &productionProvisionRuntimeStub{}); err == nil {
		t.Fatal("provision overwrote an unknown launcher")
	}
	assertFileData(t, fixture.manifest.LauncherPath, foreignLauncher)
	if _, err := os.Lstat(fixture.manifest.InstallRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unknown launcher provision published install root: %v", err)
	}
}

func TestProductionProvisionRejectsAuthorityIdentityAndPublicKeyReuse(t *testing.T) {
	t.Run("receipt signer matches bootstrap", func(t *testing.T) {
		fixture := newProductionProvisionFixture(t)
		key, err := loadProductionProvisionSigningKey("receipt", fixture.manifest.ReceiptKeyFile)
		if err != nil {
			t.Fatal(err)
		}
		key.Signer = fixture.root.BootstrapSigner
		writePrivateJSON(t, fixture.manifest.ReceiptKeyFile, key)
		if _, err := provisionProductionWithRuntime(context.Background(), fixture.manifest, &productionProvisionRuntimeStub{}); err == nil {
			t.Fatal("provision accepted receipt signer equal to bootstrap signer")
		}
	})

	t.Run("distinct receipt signer reuses bootstrap public key", func(t *testing.T) {
		fixture := newProductionProvisionFixture(t)
		key, err := loadProductionProvisionSigningKey("receipt", fixture.manifest.ReceiptKeyFile)
		if err != nil {
			t.Fatal(err)
		}
		key.PublicKey = fixture.root.BootstrapPublicKey
		key.PrivateKey = fixture.bootstrapPrivateKey
		writePrivateJSON(t, fixture.manifest.ReceiptKeyFile, key)
		if _, err := provisionProductionWithRuntime(context.Background(), fixture.manifest, &productionProvisionRuntimeStub{}); err == nil {
			t.Fatal("provision accepted receipt authority reusing bootstrap public key")
		}
	})
}

func assertFileData(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s data = %q, want %q", path, got, want)
	}
}

func TestProductionProvisionRejectsTimingAndWritableLauncherDirectory(t *testing.T) {
	fixture := newProductionProvisionFixture(t)
	fixture.manifest.CandidateTTLSeconds = 604_801
	if _, err := provisionProductionWithRuntime(context.Background(), fixture.manifest, &productionProvisionRuntimeStub{}); err == nil {
		t.Fatal("provision accepted out-of-contract candidate TTL")
	}
	if _, err := os.Lstat(fixture.manifest.InstallRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid manifest published install root: %v", err)
	}
	fixture = newProductionProvisionFixture(t)
	fixture.manifest.PromotionPollMillis = 4_999
	if _, err := provisionProductionWithRuntime(context.Background(), fixture.manifest, &productionProvisionRuntimeStub{}); err == nil ||
		!strings.Contains(err.Error(), "promotion_poll_millis must be within 5000..60000") {
		t.Fatalf("provision promotion poll validation error = %v", err)
	}
	fixture = newProductionProvisionFixture(t)
	if err := os.Chmod(filepath.Dir(fixture.manifest.LauncherPath), 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := provisionProductionWithRuntime(context.Background(), fixture.manifest, &productionProvisionRuntimeStub{}); err == nil {
		t.Fatal("provision accepted group-writable launcher directory")
	}
}

func TestProductionProvisionRejectsBootstrapKeyAsReleaseTrustAnchor(t *testing.T) {
	fixture := newProductionProvisionFixture(t)
	fixture.manifest.TrustedRootKeys = []productionTrustedKey{{
		Signer: fixture.root.BootstrapSigner, PublicKey: fixture.root.BootstrapPublicKey,
	}}
	if _, err := provisionProductionWithRuntime(
		context.Background(), fixture.manifest, &productionProvisionRuntimeStub{},
	); err == nil {
		t.Fatal("provision accepted candidate bootstrap signer as the external release trust anchor")
	}
	if _, err := os.Lstat(fixture.manifest.InstallRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("self-trusted provision published install root: %v", err)
	}
}

func TestProductionProvisionFieldRegistryIsComplete(t *testing.T) {
	assertProductionFields(t, reflect.TypeFor[productionProvisionManifest](), map[string]string{
		"SchemaVersion": "strict installer schema", "InstallRoot": "private installation root",
		"LauncherPath": "stable hook executable", "ControllerBinary": "release controller artifact",
		"BootstrapRootFile": "release-signed root", "BootstrapControllerKeyFile": "local bootstrap signer",
		"ReceiptKeyFile": "receipt authority", "ActionGrantKeyFile": "action grant authority",
		"SeccompProfile": "container policy", "TrustedSourceRoot": "owner runtime and snapshot boundary",
		"Platform":        "target platform",
		"TrustedRootKeys": "release trust anchors", "CandidateTTLSeconds": "candidate expiry",
		"PromotionPollMillis": "watcher cadence", "ActionGrantTTLSeconds": "grant expiry",
	})
	assertProductionFields(t, reflect.TypeFor[productionProvisionSigningKey](), map[string]string{
		"Signer": "authority identity", "PublicKey": "verification key", "PrivateKey": "external signing material",
	})
	assertProductionFields(t, reflect.TypeFor[productionProvisionResult](), map[string]string{
		"SchemaVersion": "result schema", "ProductionConfigFile": "installed config", "LauncherPath": "installed launcher",
	})
	assertProductionFields(t, reflect.TypeFor[productionBootstrapControllerPrivateKey](), map[string]string{
		"Signer": "bootstrap signer", "PrivateKey": "external bootstrap signing material",
	})
	assertProductionFields(t, reflect.TypeFor[productionBootstrapRunnerResult](), map[string]string{
		"SchemaVersion": "runner protocol", "Image": "immutable candidate identity",
	})
	assertProductionFields(t, reflect.TypeFor[productionBootstrapRequest](), map[string]string{
		"SchemaVersion": "controller protocol", "Challenge": "fresh host challenge", "RootDigest": "signed root identity",
		"RepoID": "repository identity", "RemoteURL": "signed source remote", "TrustedRef": "signed source ref",
		"ObjectFormat":   "signed Git object format",
		"BaselineCommit": "signed baseline commit", "BaselineTree": "signed baseline tree",
		"PolicyDigest": "signed gate policy", "ImageInputDigest": "signed build closure",
		"ToolchainDigest": "signed toolchain closure", "ImageSchemaVersion": "signed image label schema",
		"CandidateRegistry": "signed candidate publication target", "Platform": "target platform",
		"Runner": "immutable bootstrap runner", "Controller": "external controller identity",
		"BootstrapSigner": "generation-one signer", "BootstrapPublicKey": "generation-one verification key",
	})
	assertProductionFields(t, reflect.TypeFor[productionBootstrapAttestation](), map[string]string{
		"SchemaVersion": "attestation protocol", "Challenge": "host challenge", "RootDigest": "signed root identity",
		"RequestDigest": "controller request identity", "ControllerDigest": "external controller identity",
		"Record": "signed generation-one record", "ContainerID": "observed runner container",
		"ContainerArgvDigest": "required argv evidence", "ContainerLogDigest": "runner log evidence",
		"ContainerInspectDigest": "runner inspect evidence", "StartedAt": "execution lower bound",
		"CompletedAt": "execution upper bound", "Signature": "controller Ed25519 proof",
	})
}

type productionProvisionFixture struct {
	manifest            productionProvisionManifest
	root                productionBootstrapRoot
	bootstrapPrivateKey string
}

func newProductionProvisionFixture(t *testing.T) productionProvisionFixture {
	t.Helper()
	if _, err := os.Stat("/usr/bin/codesign"); err != nil {
		t.Skip("macOS codesign is unavailable")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	chmodPrivate(t, base)
	inputs := makePrivateDirectory(t, base, "release-inputs")
	launcherDirectory := makePrivateDirectory(t, base, "bin")
	if err := os.Chmod(launcherDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	controller := filepath.Join(inputs, "super-dolphin-gate-controller")
	copyProductionProvisionTestExecutable(t, controller)

	bootstrapPublic, bootstrapPrivate, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	bootstrapSigner := gatecontract.SignerIdentity{
		KeyID: "provision-bootstrap", KeyEpoch: 1, Algorithm: gatecontract.SignatureAlgorithmEd25519,
	}
	root, trust, rootPrivate := productionBootstrapRootForFixture(t, productionCoordinatorConfig{
		RepoID: "example/repository", TrustedRef: "refs/heads/main", BootstrapControllerFile: controller,
	}, strings.Repeat("1", 40), strings.Repeat("2", 40), bootstrapSigner, bootstrapPublic)
	root.Runner.Architecture = runtime.GOARCH
	root.Controller.DesignatedRequirement = productionBootstrapDesignatedRequirement(t, controller)
	root.Controller.BinaryDigest = productionBootstrapFileDigest(t, controller)
	rootFile := filepath.Join(inputs, "bootstrap-root.json")
	writeProductionBootstrapRootFixture(t, rootFile, root, rootPrivate)
	root, err = loadProductionBootstrapRoot(rootFile, []productionTrustedKey{trust})
	if err != nil {
		t.Fatal(err)
	}
	bootstrapKeyFile := filepath.Join(inputs, "bootstrap-key.json")
	writePrivateJSON(t, bootstrapKeyFile, productionBootstrapControllerPrivateKey{
		Signer: bootstrapSigner, PrivateKey: base64.StdEncoding.EncodeToString(bootstrapPrivate),
	})
	receiptFile := writeProductionProvisionTestAuthority(t, inputs, "receipt", "provision-receipt")
	grantFile := writeProductionProvisionTestAuthority(t, inputs, "grant", "provision-grant")
	seccomp := writeProductionSeccompProfile(t, inputs)
	trustedSourceRoot := makePrivateDirectory(t, base, "trusted-source")
	return productionProvisionFixture{root: root, bootstrapPrivateKey: base64.StdEncoding.EncodeToString(bootstrapPrivate), manifest: productionProvisionManifest{
		SchemaVersion: productionProvisionSchemaVersion, InstallRoot: filepath.Join(base, "installed"),
		LauncherPath: filepath.Join(launcherDirectory, "super-dolphin-gate"), ControllerBinary: controller,
		BootstrapRootFile: rootFile, BootstrapControllerKeyFile: bootstrapKeyFile,
		ReceiptKeyFile: receiptFile, ActionGrantKeyFile: grantFile, SeccompProfile: seccomp,
		TrustedSourceRoot: trustedSourceRoot,
		Platform:          root.Runner.OS + "/" + root.Runner.Architecture, TrustedRootKeys: []productionTrustedKey{trust},
		CandidateTTLSeconds: 3600, PromotionPollMillis: 5_000, ActionGrantTTLSeconds: 60,
	}}
}

func copyProductionProvisionTestExecutable(t *testing.T, destination string) {
	t.Helper()
	data, err := os.ReadFile("/usr/bin/true")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, data, 0o500); err != nil {
		t.Fatal(err)
	}
}

func writeProductionProvisionTestAuthority(t *testing.T, root string, name string, keyID string) string {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, name+"-key.json")
	writePrivateJSON(t, path, productionProvisionSigningKey{
		Signer: gatecontract.SignerIdentity{
			KeyID: keyID, KeyEpoch: 1, Algorithm: gatecontract.SignatureAlgorithmEd25519,
		},
		PublicKey: base64.StdEncoding.EncodeToString(publicKey), PrivateKey: base64.StdEncoding.EncodeToString(privateKey),
	})
	return path
}
