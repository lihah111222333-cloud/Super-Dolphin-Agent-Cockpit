package gate

import (
	"crypto/ed25519"
	"encoding/base64"
	"reflect"
	"testing"
	"time"
)

func TestReleaseAuthorityAttestationFieldGuardAndReceiptRoundTrip(t *testing.T) {
	attestation, publicKey, _ := signedReleaseAuthorityAttestation(t)
	producer, err := JSONFieldNames(reflect.TypeFor[ReleaseAuthorityAttestation]())
	if err != nil {
		t.Fatal(err)
	}
	coverage := []string{"entrypoint", "invocation_id", "owner", "plan_digest", "schema_version", "signature", "signer", "source"}
	if missing, stale := FieldCoverageDiff(producer, coverage); len(missing) != 0 || len(stale) != 0 {
		t.Fatalf("release attestation field coverage missing=%v stale=%v", missing, stale)
	}
	encoded, err := EncodeReleaseAuthorityAttestation(attestation)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeReleaseAuthorityAttestation(encoded)
	if err != nil || !reflect.DeepEqual(decoded, attestation) {
		t.Fatalf("release attestation roundtrip decoded=%#v err=%v", decoded, err)
	}
	if err := VerifyReleaseAuthorityAttestation(decoded, publicKey); err != nil {
		t.Fatalf("VerifyReleaseAuthorityAttestation() error = %v", err)
	}
}

func signedReleaseAuthorityAttestation(t *testing.T) (ReleaseAuthorityAttestation, ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	receipt := validResultReceiptForProfile(t, time.Now().UTC(), ProfileRelease)
	attestation := ReleaseAuthorityAttestation{SchemaVersion: 1, Entrypoint: CIEntrypointRelease,
		Owner: CIEntrypointOwnerRelease, InvocationID: receipt.InvocationID, Source: receipt.Source, PlanDigest: receipt.PlanDigest,
		Signer: SignerIdentity{KeyID: "release-owner", KeyEpoch: 1, Algorithm: SignatureAlgorithmEd25519}}
	payload, err := ReleaseAuthorityAttestationSigningPayload(attestation)
	if err != nil {
		t.Fatal(err)
	}
	attestation.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	return attestation, publicKey, privateKey
}

func TestReleaseReceiptSignatureCoversAuthorityAttestation(t *testing.T) {
	attestation, publicKey, privateKey := signedReleaseAuthorityAttestation(t)
	encoded, err := EncodeReleaseAuthorityAttestation(attestation)
	if err != nil {
		t.Fatal(err)
	}
	receipt := validResultReceiptForProfile(t, time.Now().UTC(), ProfileRelease)
	receipt.Entrypoint = CIEntrypointRelease
	receipt.AuthorityOwner = CIEntrypointOwnerRelease
	receipt.AuthorityAttestation = encoded
	receipt.Signer = attestation.Signer
	payload, err := ResultReceiptSigningPayload(receipt)
	if err != nil {
		t.Fatal(err)
	}
	receipt.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	if err := VerifyResultReceipt(receipt, publicKey); err != nil {
		t.Fatalf("release receipt verification error = %v", err)
	}
	receipt.AuthorityAttestation = "forged"
	if err := VerifyResultReceipt(receipt, publicKey); err == nil {
		t.Fatal("result receipt verification accepted attestation tampering")
	}
}
