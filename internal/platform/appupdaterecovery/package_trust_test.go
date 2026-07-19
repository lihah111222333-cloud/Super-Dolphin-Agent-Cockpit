package appupdaterecovery

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPackageTrustRejectsRuntimeOverrideAndWrongKey(t *testing.T) {
	trust := testPackageTrust(t, "darwin-arm64")
	resources := canonicalTestTempDir(t)
	writePackageTrustFixture(t, resources, trust)

	loaded, digest, err := LoadPackageTrust(resources, "darwin-arm64")
	if err != nil {
		t.Fatalf("LoadPackageTrust() error = %v", err)
	}
	if digest == "" || loaded.ManifestPublicKey != trust.ManifestPublicKey {
		t.Fatalf("LoadPackageTrust() = (%+v, %q), want exact package trust", loaded, digest)
	}
	if err := RejectPackageTrustOverrides([]string{"SUPER_DOLPHIN_UPDATE_PUBLIC_KEY=wrong"}); err == nil || !strings.Contains(err.Error(), "SUPER_DOLPHIN_UPDATE_PUBLIC_KEY") {
		t.Fatalf("RejectPackageTrustOverrides() error = %v, want stable override rejection", err)
	}

	wrong := trust
	wrong.ManifestPublicKey = base64.StdEncoding.EncodeToString(make([]byte, ed25519.PublicKeySize-1))
	raw, err := json.Marshal(wrong)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resources, PackageTrustFilename), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadPackageTrust(resources, "darwin-arm64"); err == nil || !strings.Contains(err.Error(), "manifest_public_key") {
		t.Fatalf("LoadPackageTrust(wrong key) error = %v, want key rejection", err)
	}
}

func TestPackageTrustRejectsTrustFileAlias(t *testing.T) {
	root := canonicalTestTempDir(t)
	resources := filepath.Join(root, "Resources")
	if err := os.MkdirAll(resources, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := EncodePackageTrust(testPackageTrust(t, "darwin-arm64"))
	if err != nil {
		t.Fatal(err)
	}
	realTrust := filepath.Join(root, "attacker-trust.json")
	if err := os.WriteFile(realTrust, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realTrust, filepath.Join(resources, PackageTrustFilename)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadPackageTrust(resources, "darwin-arm64"); err == nil || !strings.Contains(err.Error(), "trust file alias") {
		t.Fatalf("LoadPackageTrust(trust alias) error = %v", err)
	}
}

func TestPackageTrustRejectsTransactionTargetAlias(t *testing.T) {
	root := canonicalTestTempDir(t)
	realTarget := filepath.Join(root, "Real.app")
	resources := filepath.Join(realTarget, "Contents", "Resources")
	if err := os.MkdirAll(resources, 0o700); err != nil {
		t.Fatal(err)
	}
	writePackageTrustFixture(t, resources, testPackageTrust(t, "darwin-arm64"))
	aliasTarget := filepath.Join(root, "Super Dolphin.app")
	if err := os.Symlink(realTarget, aliasTarget); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ResolveTransactionBoundPackageTrust(context.Background(), aliasTarget, "darwin-arm64"); err == nil || !strings.Contains(err.Error(), "target alias") {
		t.Fatalf("ResolveTransactionBoundPackageTrust(target alias) error = %v", err)
	}
}

func TestPackageTrustFieldGuardDynamicallyRejectsMissingAndUnknown(t *testing.T) {
	trust := testPackageTrust(t, "darwin-arm64")
	raw, err := EncodePackageTrust(trust)
	if err != nil {
		t.Fatal(err)
	}
	producer := reflect.TypeFor[PackageTrust]()
	for index := 0; index < producer.NumField(); index++ {
		assertPackageTrustFieldRequired(t, raw, producer.Field(index))
	}
	var unknown map[string]any
	if err := json.Unmarshal(raw, &unknown); err != nil {
		t.Fatal(err)
	}
	unknown["unknown_field"] = true
	assertPackageTrustRawRejected(t, unknown, "unknown field")
}

func assertPackageTrustFieldRequired(t *testing.T, raw []byte, field reflect.StructField) {
	t.Helper()
	name, include := producerJSONField(field)
	if !include {
		t.Fatalf("producer PackageTrust field %s is not serialized", field.Name)
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	delete(value, name)
	assertPackageTrustRawRejected(t, value, "producer=PackageTrust")
}

func assertPackageTrustRawRejected(t *testing.T, value map[string]any, evidence string) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	resources := canonicalTestTempDir(t)
	if err := os.WriteFile(filepath.Join(resources, PackageTrustFilename), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadPackageTrust(resources, "darwin-arm64"); err == nil || !strings.Contains(err.Error(), evidence) {
		t.Fatalf("LoadPackageTrust(mutated) error = %v, want %q", err, evidence)
	}
}

func TestPackageTrustRealMutationIsRejectedByTransactionTerminal(t *testing.T) {
	fixture := newTrustStateFixture(t)
	mustInstallTrustCandidate(t, fixture)
	path := filepath.Join(fixture.target, "Contents", "Resources", PackageTrustFilename)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ResolveTransactionBoundPackageTrust(context.Background(), fixture.target, "darwin-arm64"); err == nil {
		t.Fatal("ResolveTransactionBoundPackageTrust(real trust mutation) error = nil")
	}
}

func TestUpdateCapabilityMatrixIsFailClosed(t *testing.T) {
	assertUpdateCapability(t, "darwin-amd64", false)
	assertUpdateCapability(t, "darwin-arm64", true)
	assertUpdateCapability(t, "linux-amd64", false)
	assertUpdateCapability(t, "linux-arm64", false)
	assertUpdateCapability(t, "windows-amd64", false)
	assertUpdateCapability(t, "windows-arm64", false)
	if _, err := UpdateCapabilityFor("freebsd-amd64"); err == nil {
		t.Fatal("UpdateCapabilityFor(unknown) error = nil")
	}
}

func assertUpdateCapability(t *testing.T, platform string, supported bool) {
	t.Helper()
	capability, err := UpdateCapabilityFor(platform)
	if err != nil {
		t.Fatalf("UpdateCapabilityFor(%q) error = %v", platform, err)
	}
	if capability.Check != supported || capability.Install != supported || capability.Publish != supported {
		t.Fatalf("UpdateCapabilityFor(%q) = %+v, want all=%v", platform, capability, supported)
	}
}

func TestPendingTrustGenerationActivatesOnlyAfterHealthyAndRollbackDiscardsIt(t *testing.T) {
	fixture := newPendingTrustFixture(t)
	transaction := installPendingTrustCandidate(t, fixture)
	assertResolvedTrust(t, fixture.target, fixture.oldTrust, fixture.oldGeneration)

	transaction.Trust.Generation = strings.Repeat("f", 64)
	if _, _, err := ResolvePackageTrustForTransaction(t.Context(), fixture.target, "darwin-arm64", transaction); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("ResolvePackageTrustForTransaction(stale) error = %v", err)
	}
	if _, err := fixture.store.RollbackUnclaimedProbation(context.Background(), fixture.request.Identity); err != nil {
		t.Fatal(err)
	}
	assertResolvedTrust(t, fixture.target, fixture.oldTrust, fixture.oldGeneration)
}

type pendingTrustFixture struct {
	store         *Store
	request       CreateRequest
	target        string
	oldTrust      PackageTrust
	oldGeneration string
}

func newPendingTrustFixture(t *testing.T) pendingTrustFixture {
	t.Helper()
	ctx := context.Background()
	root := canonicalTestTempDir(t)
	target := filepath.Join(root, "Super Dolphin.app")
	id := TransactionID("00112233445566778899aabbccddeeff")
	paths, err := PathsFor(target, id)
	if err != nil {
		t.Fatal(err)
	}
	oldTrust, oldGeneration, oldRelease, oldHelpers := writeExactTrustedRelease(t, target, "old-pending")
	_, candidateGeneration, candidateRelease, candidateHelpers := writeExactTrustedRelease(t, paths.Staging, "candidate-pending")
	if err := os.MkdirAll(paths.RecoveryDir, 0o700); err != nil {
		t.Fatal(err)
	}
	copyTrustFixture(t, filepath.Join(target, "Contents", "Resources", PackageTrustFilename), filepath.Join(paths.RecoveryDir, PackageTrustFilename))
	copyTrustFixture(t, filepath.Join(target, "Contents", "Resources", "bin", "super-dolphin-updater"), filepath.Join(paths.RecoveryDir, "super-dolphin-updater"))
	copyTrustFixture(t, filepath.Join(target, "Contents", "Resources", "bin", "super-dolphin-guard"), filepath.Join(paths.RecoveryDir, "super-dolphin-guard"))
	store, err := NewStore(TransactionRootForTarget(target))
	if err != nil {
		t.Fatal(err)
	}
	request := CreateRequest{
		Identity: Identity{
			TransactionID: id, AttemptID: "attempt-1", OldRelease: oldRelease, CandidateRelease: candidateRelease,
			OldHelpers: oldHelpers, CandidateHelpers: candidateHelpers,
			UpdaterProcess: fixtureUpdaterProcess(),
		},
		Paths: paths,
		Trust: TrustGeneration{PreviousGeneration: oldGeneration, Generation: candidateGeneration, PackageSigner: "TEAM-A", State: TrustPending},
	}
	if _, err := store.Create(ctx, request); err != nil {
		t.Fatal(err)
	}
	return pendingTrustFixture{store: store, request: request, target: target, oldTrust: oldTrust, oldGeneration: oldGeneration}
}

func installPendingTrustCandidate(t *testing.T, fixture pendingTrustFixture) Transaction {
	t.Helper()
	if _, err := fixture.store.RetainBackup(context.Background(), fixture.request.Identity); err != nil {
		t.Fatal(err)
	}
	transaction, err := fixture.store.InstallCandidate(context.Background(), fixture.request.Identity)
	if err != nil {
		t.Fatal(err)
	}
	return transaction
}

func assertResolvedTrust(t *testing.T, target string, want PackageTrust, wantGeneration string) {
	t.Helper()
	resolved, resolvedGeneration, err := ResolveTransactionBoundPackageTrust(context.Background(), target, "darwin-arm64")
	if err != nil {
		t.Fatalf("ResolveTransactionBoundPackageTrust() error = %v", err)
	}
	if resolvedGeneration != wantGeneration || resolved.ManifestPublicKey != want.ManifestPublicKey {
		t.Fatalf("resolved trust = (%+v, %s), want generation %s", resolved, resolvedGeneration, wantGeneration)
	}
}

func testPackageTrust(t *testing.T, platform string) PackageTrust {
	t.Helper()
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return PackageTrust{
		SchemaVersion:     PackageTrustSchemaVersion,
		Enabled:           true,
		Production:        true,
		Platform:          platform,
		Source:            UpdateSource{Kind: UpdateSourceGitHub, Value: "super-dolphin/releases"},
		ManifestPublicKey: base64.StdEncoding.EncodeToString(publicKey),
		Channel:           "gray",
		SignerPolicy:      PackageSignerPolicyExact,
		SignerIdentity:    "TEAM-A",
		UpdaterSHA256:     strings.Repeat("a", 64),
		GuardSHA256:       strings.Repeat("b", 64),
	}
}

func writePackageTrustFixture(t *testing.T, resources string, trust PackageTrust) {
	t.Helper()
	raw, err := EncodePackageTrust(trust)
	if err != nil {
		t.Fatalf("EncodePackageTrust() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(resources, PackageTrustFilename), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func canonicalTestTempDir(t *testing.T) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func releaseIdentityForTest(t *testing.T, path, signer string) ReleaseIdentity {
	t.Helper()
	digest, err := ComputeReleaseDigest(path)
	if err != nil {
		t.Fatal(err)
	}
	return ReleaseIdentity{SHA256: digest, SignerIdentity: signer}
}
