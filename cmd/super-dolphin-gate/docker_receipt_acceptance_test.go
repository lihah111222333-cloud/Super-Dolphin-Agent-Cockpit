package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
)

const (
	dockerReceiptAcceptanceSwitch = "SUPER_DOLPHIN_LOCALCI_DOCKER_ACCEPTANCE"
	dockerReceiptAcceptanceTag    = "super-dolphin-receipt-acceptance:fixture"
)

type dockerReceiptAcceptanceFixture struct {
	image             gatecontract.ImageIdentity
	truth             localci.FreshContainerImageTruth
	buildSourceTree   string
	jobSourceTree     string
	trustedSourceRoot string
	seccompPath       string
}

type dockerReceiptImageInspect struct {
	RepoDigests []string `json:"RepoDigests"`
	Descriptor  *struct {
		Digest      string            `json:"digest"`
		Annotations map[string]string `json:"annotations"`
	} `json:"Descriptor"`
	RootFS *struct {
		Layers []string `json:"Layers"`
	} `json:"RootFS"`
	OS           string `json:"Os"`
	Architecture string `json:"Architecture"`
	Variant      string `json:"Variant"`
}

func TestRealDockerResultProducesVerifiableSignedReceiptAcceptance(t *testing.T) {
	if os.Getenv(dockerReceiptAcceptanceSwitch) != "1" {
		t.Skip("real Docker signed receipt acceptance is disabled")
	}
	plan := dockerReceiptAcceptancePlan(t)
	fixture := buildDockerReceiptAcceptanceFixture(t, plan.PolicyDigest)
	runner, err := localci.NewFreshContainerRunner(fixture.seccompPath, fixture.trustedSourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	execution := runDockerReceiptAcceptancePlan(t, runner, fixture, plan)
	execution.Accepted = signedDockerReceiptAcceptedRecord(t, plan, fixture)
	receipt, verifier := signDockerReceiptAcceptance(t, plan, execution)
	if err := verifier.VerifyResultReceipt(receipt); err != nil {
		t.Fatalf("verify real Docker result receipt: %v", err)
	}
	if !receipt.Container.Removed || !receipt.Container.NetworkRemoved || len(receipt.GateResults) != len(plan.Gates) {
		t.Fatalf("signed receipt omitted real Docker closure: %#v", receipt)
	}
	tampered := receipt
	tampered.Container.Removed = false
	if err := verifier.VerifyResultReceipt(tampered); err == nil {
		t.Fatal("receipt verifier accepted tampered real Docker removal evidence")
	}
	assertNoDockerReceiptAcceptanceContainers(t)
	t.Logf("receipt_id=%s gates=%d signature_bytes=%d status=%s aggregate_container=%s removed=true", receipt.ReceiptID, len(receipt.GateResults), len(receipt.Signature), receipt.Status, receipt.Container.ContainerID)
}

func dockerReceiptAcceptancePlan(t *testing.T) gatecontract.GatePlan {
	t.Helper()
	tree := strings.Repeat("d", 40)
	plan, err := gatecontract.BuildGatePlan(gatecontract.ProfileLocalFast, gatecontract.SourceSpec{
		Kind: gatecontract.SourceKindTree, ObjectFormat: gatecontract.GitObjectFormatSHA1,
		Tree: &gatecontract.TreeSource{SHA: tree}, SourceTreeSHA: tree,
	})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func buildDockerReceiptAcceptanceFixture(t *testing.T, policyDigest string) dockerReceiptAcceptanceFixture {
	t.Helper()
	buildTree := strings.Repeat("c", 40)
	inputDigest := acceptanceDigest("3")
	toolchainDigest := acceptanceDigest("4")
	fixtureDirectory := acceptanceCanonicalPath(t, "../../internal/devtools/localci/testdata/scheduler-docker-acceptance")
	labels := []string{
		"org.super-dolphin.policy-sha=" + policyDigest,
		"org.super-dolphin.source-tree-sha=" + buildTree,
		"org.super-dolphin.image-input-digest=" + inputDigest,
		"org.super-dolphin.toolchain-digest=" + toolchainDigest,
		"org.super-dolphin.schema-version=1",
	}
	args := []string{
		"buildx", "build", "--load", "--provenance=false", "--network=none", "--platform=linux/arm64",
		"--file=" + filepath.Join(fixtureDirectory, "Dockerfile"), "--tag=" + dockerReceiptAcceptanceTag,
	}
	for _, label := range labels {
		args = append(args, "--label="+label)
	}
	args = append(args, fixtureDirectory)
	runBootstrapDocker(t, args...)
	t.Cleanup(func() { _ = execDockerAcceptanceCleanup("image", "rm", "--force", dockerReceiptAcceptanceTag) })
	root := acceptanceCanonicalPath(t, t.TempDir())
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	return dockerReceiptAcceptanceFixture{
		image: inspectDockerReceiptAcceptanceImage(t),
		truth: localci.FreshContainerImageTruth{
			PolicyDigest: policyDigest, BuildSourceTreeSHA: buildTree,
			InputDigest: inputDigest, ToolchainDigest: toolchainDigest, SchemaVersion: "1",
		},
		buildSourceTree: buildTree, jobSourceTree: strings.Repeat("d", 40),
		trustedSourceRoot: root,
		seccompPath:       acceptanceCanonicalPath(t, "../../internal/devtools/localci/testdata/fresh-container-smoke/seccomp.json"),
	}
}

func inspectDockerReceiptAcceptanceImage(t *testing.T) gatecontract.ImageIdentity {
	t.Helper()
	output := runBootstrapDocker(t, "image", "inspect", dockerReceiptAcceptanceTag)
	var documents []dockerReceiptImageInspect
	if err := json.Unmarshal([]byte(output), &documents); err != nil {
		t.Fatal(err)
	}
	if len(documents) != 1 || documents[0].Descriptor == nil || documents[0].RootFS == nil || len(documents[0].RepoDigests) != 1 {
		t.Fatalf("Docker receipt image identity is incomplete: %#v", documents)
	}
	document := documents[0]
	registry, manifestDigest, found := strings.Cut(document.RepoDigests[0], "@")
	if !found || manifestDigest != document.Descriptor.Digest {
		t.Fatal("Docker receipt image RepoDigest drifted from descriptor")
	}
	return gatecontract.ImageIdentity{
		Registry: registry, OCIIndexDigest: manifestDigest, PlatformManifestDigest: manifestDigest,
		ConfigDigest:  document.Descriptor.Annotations["config.digest"],
		RootFSDiffIDs: append([]string(nil), document.RootFS.Layers...),
		OS:            document.OS, Architecture: document.Architecture, Variant: document.Variant,
	}
}

func runDockerReceiptAcceptancePlan(
	t *testing.T,
	runner *localci.FreshContainerRunner,
	fixture dockerReceiptAcceptanceFixture,
	plan gatecontract.GatePlan,
) receiptExecution {
	t.Helper()
	execution := receiptExecution{Deadline: time.Now().UTC().Add(10 * time.Minute)}
	for index, gateSpec := range plan.Gates {
		source := filepath.Join(fixture.trustedSourceRoot, "gate-"+acceptanceIndex(index))
		if err := os.Mkdir(source, 0o700); err != nil {
			t.Fatal(err)
		}
		result, err := runner.RunFreshContainer(context.Background(), localci.FreshContainerRequest{
			Image: fixture.image, ImageTruth: fixture.truth,
			SourceTreeSHA: fixture.jobSourceTree, SourceSnapshotDir: source,
			Profile: plan.Profile, Plan: plan, GateID: gateSpec.ID, Deadline: execution.Deadline,
			ContainerLabels: map[string]string{"super-dolphin.acceptance": "signed-receipt"},
		})
		if err != nil {
			t.Fatalf("run real Docker receipt gate %s: %v", gateSpec.ID, err)
		}
		if err := execution.appendResult(result); err != nil {
			t.Fatalf("append real Docker receipt gate %s: %v", gateSpec.ID, err)
		}
	}
	return execution
}

func signedDockerReceiptAcceptedRecord(
	t *testing.T,
	plan gatecontract.GatePlan,
	fixture dockerReceiptAcceptanceFixture,
) gatecontract.AcceptedImageRecord {
	t.Helper()
	record := testAcceptedImageRecord(plan)
	record.SourceTree = fixture.buildSourceTree
	record.Image = fixture.image
	record.ImageInputDigest = fixture.truth.InputDigest
	_, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	record.Signer = gatecontract.SignerIdentity{
		KeyID: "docker-acceptance-image", KeyEpoch: 1, Algorithm: gatecontract.SignatureAlgorithmEd25519,
	}
	record.Signature = ""
	payload, err := gatecontract.AcceptedImageSigningPayload(record)
	if err != nil {
		t.Fatal(err)
	}
	record.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	if err := record.Validate(); err != nil {
		t.Fatal(err)
	}
	return record
}

func signDockerReceiptAcceptance(
	t *testing.T,
	plan gatecontract.GatePlan,
	execution receiptExecution,
) (gatecontract.ResultReceipt, *ed25519ResultReceiptVerifier) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	identity := gatecontract.SignerIdentity{
		KeyID: "docker-acceptance-receipt", KeyEpoch: 1, Algorithm: gatecontract.SignatureAlgorithmEd25519,
	}
	signer, err := newEd25519ResultReceiptSigner(identity, privateKey, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := newEd25519ResultReceiptVerifier(identity, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := buildPassedResultReceipt(coordinatorJobRecord{
		InvocationID: "docker-acceptance-invocation", JobID: "docker-acceptance-job",
		Plan: plan, Profile: plan.Profile, JobSourceTreeSHA: plan.Source.SourceTreeSHA,
	}, execution, signer)
	if err != nil {
		t.Fatal(err)
	}
	return receipt, verifier
}

func assertNoDockerReceiptAcceptanceContainers(t *testing.T) {
	t.Helper()
	output := runBootstrapDocker(t, "ps", "--all", "--filter=label=super-dolphin.acceptance=signed-receipt", "--format={{.ID}}")
	if strings.TrimSpace(output) != "" {
		t.Fatalf("signed receipt acceptance containers remain: %s", strings.TrimSpace(output))
	}
}

func acceptanceCanonicalPath(t *testing.T, path string) string {
	t.Helper()
	absolute, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func acceptanceDigest(character string) string {
	return "sha256:" + strings.Repeat(character, 64)
}

func acceptanceIndex(index int) string {
	return string(rune('0' + index))
}

func execDockerAcceptanceCleanup(args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	return exec.CommandContext(ctx, "docker", args...).Run()
}
