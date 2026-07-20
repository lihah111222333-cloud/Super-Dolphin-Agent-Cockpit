package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestReleaseSubmitRejectsForgedDigestAndRequiresVerifiedOwnerSignature(t *testing.T) {
	request, signer, publicKey := signedReleaseSubmitRequest(t)
	request.AuthorityAttestation = "sha256:" + strings.Repeat("b", 64)
	if err := validateSubmissionAuthority(request); err == nil || !strings.Contains(err.Error(), "verified release-owner") {
		t.Fatalf("forged digest authority error = %v", err)
	}
	request, signer, publicKey = signedReleaseSubmitRequest(t)
	verified, err := verifyReleaseSubmissionAuthority(request, signer, publicKey)
	if err != nil {
		t.Fatalf("verifyReleaseSubmissionAuthority() error = %v", err)
	}
	request.VerifiedRelease = verified
	if err := validateSubmissionAuthority(request); err != nil {
		t.Fatalf("verified release authority error = %v", err)
	}
}

func signedReleaseSubmitRequest(t *testing.T) (submitRequest, gatecontract.SignerIdentity, ed25519.PublicKey) {
	t.Helper()
	plan := mustTestGatePlan(t, "b")
	plan.Profile = gatecontract.ProfileRelease
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	signer := gatecontract.SignerIdentity{KeyID: "release-owner", KeyEpoch: 1, Algorithm: gatecontract.SignatureAlgorithmEd25519}
	request := submitRequest{InvocationID: "release-" + strings.Repeat("b", 64), Plan: plan,
		Entrypoint: gatecontract.CIEntrypointRelease, AuthorityOwner: gatecontract.CIEntrypointOwnerRelease}
	attestation := gatecontract.ReleaseAuthorityAttestation{SchemaVersion: 1, Entrypoint: request.Entrypoint,
		Owner: request.AuthorityOwner, InvocationID: request.InvocationID, Source: plan.Source, PlanDigest: plan.PlanDigest, Signer: signer}
	payload, err := gatecontract.ReleaseAuthorityAttestationSigningPayload(attestation)
	if err != nil {
		t.Fatal(err)
	}
	attestation.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	request.AuthorityAttestation, err = gatecontract.EncodeReleaseAuthorityAttestation(attestation)
	if err != nil {
		t.Fatal(err)
	}
	return request, signer, publicKey
}

func TestProductionLauncherReleaseSubmitSignsAndVerifiesAuthority(t *testing.T) {
	fixture := newProductionTestFixture(t)
	client := &releaseAuthorityCaptureClient{status: jobStatus{
		JobID: "release-job", Profile: gatecontract.ProfileRelease, State: jobStateQueued,
	}}
	var output bytes.Buffer
	err := runProductionLauncherWithConnector(
		[]string{
			"submit",
			"--profile", string(gatecontract.ProfileRelease),
			"--object-format", string(gatecontract.GitObjectFormatSHA1),
			"--source-tree", fixture.tree, "--commit", fixture.commit,
		},
		&output,
		func() (productionCoordinatorConfig, error) { return fixture.config, nil },
		func() (string, error) { return fixture.sourceRepo, nil },
		func(context.Context) (coordinatorClient, error) { return client, nil },
		func([]string, io.Writer) error { return errors.New("unexpected launcher fallback") },
	)
	if err != nil {
		t.Fatalf("production launcher release submit error = %v", err)
	}
	assertProductionLauncherReleaseRequest(t, client.request)
	assertProductionLauncherReleaseAttestation(t, fixture, client.request)
}

func TestProductionLauncherFallsBackForOrdinaryCommands(t *testing.T) {
	var got []string
	fallbackErr := errors.New("fallback invoked")
	err := runProductionLauncherWithConnector(
		[]string{"status", "--job", "job-1"},
		&bytes.Buffer{},
		func() (productionCoordinatorConfig, error) {
			return productionCoordinatorConfig{}, errors.New("unexpected config load")
		},
		func() (string, error) { return "", errors.New("unexpected repository root") },
		nil,
		func(args []string, _ io.Writer) error {
			got = append([]string(nil), args...)
			return fallbackErr
		},
	)
	if !errors.Is(err, fallbackErr) || !reflect.DeepEqual(got, []string{"status", "--job", "job-1"}) {
		t.Fatalf("launcher fallback error=%v args=%q", err, got)
	}
}

func TestProductionLauncherNoArgsReturnsProtocolError(t *testing.T) {
	err := runProductionLauncherWithConnector(
		nil, &bytes.Buffer{}, nil, nil, nil, dispatchProductionLauncherFallback,
	)
	if err == nil || !strings.Contains(err.Error(), "subcommand is required") {
		t.Fatalf("launcher no-args error = %v", err)
	}
}

func TestProductionLauncherLocalFastSubmitUsesOriginalSubmitPath(t *testing.T) {
	fixture := newProductionTestFixture(t)
	client := &releaseAuthorityCaptureClient{status: jobStatus{
		JobID: "local-job", Profile: gatecontract.ProfileLocalFast, State: jobStateQueued,
	}}
	err := runProductionLauncherWithConnector(
		[]string{
			"submit", "--profile", string(gatecontract.ProfileLocalFast),
			"--object-format", string(gatecontract.GitObjectFormatSHA1),
			"--source-tree", fixture.tree, "--commit", fixture.commit,
		},
		&bytes.Buffer{},
		func() (productionCoordinatorConfig, error) {
			return productionCoordinatorConfig{}, errors.New("unexpected config load")
		},
		func() (string, error) { return fixture.sourceRepo, nil },
		func(context.Context) (coordinatorClient, error) { return client, nil },
		func([]string, io.Writer) error { return errors.New("unexpected launcher fallback") },
	)
	if err != nil {
		t.Fatalf("production launcher local-fast submit error = %v", err)
	}
	if client.request.Plan.Profile != gatecontract.ProfileLocalFast || client.request.authority() != manualSubmissionAuthority() {
		t.Fatalf("production launcher local-fast request = %#v", client.request)
	}
}

func TestProductionLauncherReleaseRejectsMismatchedActionGrantKey(t *testing.T) {
	fixture := newProductionTestFixture(t)
	config := fixture.config
	config.ActionGrantAuthority.PublicKey = config.ResultReceiptAuthority.PublicKey
	client := &releaseAuthorityCaptureClient{}
	err := runProductionLauncherWithConnector(
		[]string{
			"submit", "--profile", string(gatecontract.ProfileRelease),
			"--object-format", string(gatecontract.GitObjectFormatSHA1),
			"--source-tree", fixture.tree, "--commit", fixture.commit,
		},
		&bytes.Buffer{},
		func() (productionCoordinatorConfig, error) { return config, nil },
		func() (string, error) { return fixture.sourceRepo, nil },
		func(context.Context) (coordinatorClient, error) { return client, nil },
		func([]string, io.Writer) error { return errors.New("unexpected launcher fallback") },
	)
	if err == nil || !strings.Contains(err.Error(), "private and public keys do not match") || client.request.Plan.Profile != "" {
		t.Fatalf("mismatched production action grant key error=%v request=%#v", err, client.request)
	}
}

func assertProductionLauncherReleaseRequest(t *testing.T, request submitRequest) {
	t.Helper()
	if request.Entrypoint != gatecontract.CIEntrypointRelease ||
		request.AuthorityOwner != gatecontract.CIEntrypointOwnerRelease ||
		request.VerifiedRelease == nil || !strings.HasPrefix(request.InvocationID, "release-") ||
		len(request.InvocationID) != len("release-")+64 {
		t.Fatalf("production launcher release request = %#v", request)
	}
}

func assertProductionLauncherReleaseAttestation(t *testing.T, fixture productionTestFixture, request submitRequest) {
	t.Helper()
	attestation, err := gatecontract.DecodeReleaseAuthorityAttestation(request.AuthorityAttestation)
	if err != nil {
		t.Fatalf("decode release authority attestation: %v", err)
	}
	publicKey, err := decodeActionGrantPublicKey(fixture.config.ActionGrantAuthority.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := gatecontract.VerifyReleaseAuthorityAttestation(attestation, publicKey); err != nil {
		t.Fatalf("verify release authority attestation: %v", err)
	}
	if attestation.InvocationID != request.InvocationID || !reflect.DeepEqual(attestation.Source, request.Plan.Source) ||
		attestation.PlanDigest != request.Plan.PlanDigest {
		t.Fatalf("release authority binding = %#v, request = %#v", attestation, request)
	}
}

type releaseAuthorityCaptureClient struct {
	request submitRequest
	status  jobStatus
}

func (client *releaseAuthorityCaptureClient) Submit(_ context.Context, request submitRequest) (jobStatus, error) {
	client.request = request
	return client.status, nil
}

func (client *releaseAuthorityCaptureClient) Status(context.Context, string) (jobStatus, error) {
	return client.status, nil
}

func (client *releaseAuthorityCaptureClient) Wait(context.Context, string) (jobStatus, error) {
	return client.status, nil
}

func (*releaseAuthorityCaptureClient) Close() error { return nil }

func TestDirectManualReleaseSubmitRemainsRejected(t *testing.T) {
	client := &releaseAuthorityCaptureClient{}
	var output bytes.Buffer
	err := runSubmitWithConnector(
		[]string{
			"--profile", string(gatecontract.ProfileRelease),
			"--object-format", string(gatecontract.GitObjectFormatSHA1),
			"--source-tree", strings.Repeat("b", 40), "--commit", strings.Repeat("a", 40),
		},
		&output,
		func(context.Context) (coordinatorClient, error) { return client, nil },
	)
	if err == nil || !strings.Contains(err.Error(), "authoritative release entrypoint") || client.request.Plan.Profile != "" {
		t.Fatalf("manual release error=%v request=%#v", err, client.request)
	}
}
