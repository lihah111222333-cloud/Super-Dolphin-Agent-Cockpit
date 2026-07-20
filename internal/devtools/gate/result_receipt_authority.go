package gate

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// ReleaseAuthorityAttestation is the owner-signed release admission bound to
// one invocation, source tree, and canonical plan digest.
type ReleaseAuthorityAttestation struct {
	SchemaVersion uint32            `json:"schema_version"`
	Entrypoint    CIEntrypointID    `json:"entrypoint"`
	Owner         CIEntrypointOwner `json:"owner"`
	InvocationID  string            `json:"invocation_id"`
	Source        SourceSpec        `json:"source"`
	PlanDigest    string            `json:"plan_digest"`
	Signer        SignerIdentity    `json:"signer"`
	Signature     string            `json:"signature"`
}

// Validate 校验 release 授权 envelope 的签名载荷结构。
func (attestation ReleaseAuthorityAttestation) Validate() error {
	if attestation.SchemaVersion != 1 || attestation.Entrypoint != CIEntrypointRelease ||
		attestation.Owner != CIEntrypointOwnerRelease || strings.TrimSpace(attestation.InvocationID) == "" {
		return errors.New("release authority attestation identity is invalid")
	}
	if err := attestation.Source.Validate(); err != nil {
		return fmt.Errorf("release authority attestation source: %w", err)
	}
	if !validResultReceiptAttestation(attestation.PlanDigest) {
		return errors.New("release authority attestation plan_digest must be a sha256 identity")
	}
	if err := attestation.Signer.Validate(); err != nil {
		return fmt.Errorf("release authority attestation signer: %w", err)
	}
	signature, err := base64.StdEncoding.Strict().DecodeString(attestation.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return errors.New("release authority attestation signature must be canonical base64 Ed25519")
	}
	return nil
}

// ReleaseAuthorityAttestationSigningPayload 返回供 owner key 验签的规范无签名载荷。
func ReleaseAuthorityAttestationSigningPayload(attestation ReleaseAuthorityAttestation) ([]byte, error) {
	unsigned := attestation
	unsigned.Signature = ""
	if unsigned.SchemaVersion != 1 || unsigned.Entrypoint != CIEntrypointRelease ||
		unsigned.Owner != CIEntrypointOwnerRelease || strings.TrimSpace(unsigned.InvocationID) == "" ||
		!validResultReceiptAttestation(unsigned.PlanDigest) {
		return nil, errors.New("release authority attestation signing fields are invalid")
	}
	if err := unsigned.Source.Validate(); err != nil {
		return nil, fmt.Errorf("release authority attestation source: %w", err)
	}
	if err := unsigned.Signer.Validate(); err != nil {
		return nil, fmt.Errorf("release authority attestation signer: %w", err)
	}
	return json.Marshal(unsigned)
}

// EncodeReleaseAuthorityAttestation 将已签 envelope 规范编码到 receipt/store 字段。
func EncodeReleaseAuthorityAttestation(attestation ReleaseAuthorityAttestation) (string, error) {
	if err := attestation.Validate(); err != nil {
		return "", err
	}
	document, err := json.Marshal(attestation)
	if err != nil {
		return "", fmt.Errorf("marshal release authority attestation: %w", err)
	}
	return base64.StdEncoding.EncodeToString(document), nil
}

// DecodeReleaseAuthorityAttestation 严格解码已签 release envelope。
func DecodeReleaseAuthorityAttestation(encoded string) (ReleaseAuthorityAttestation, error) {
	document, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return ReleaseAuthorityAttestation{}, errors.New("release authority attestation must be canonical base64 JSON")
	}
	var attestation ReleaseAuthorityAttestation
	if err := DecodeStrictJSON(document, &attestation); err != nil {
		return ReleaseAuthorityAttestation{}, fmt.Errorf("decode release authority attestation: %w", err)
	}
	return attestation, nil
}

// VerifyReleaseAuthorityAttestation 使用配置的 release owner 公钥验签，不信任摘要形字符串。
func VerifyReleaseAuthorityAttestation(attestation ReleaseAuthorityAttestation, publicKey ed25519.PublicKey) error {
	if len(publicKey) != ed25519.PublicKeySize {
		return errors.New("release authority public key must be Ed25519")
	}
	if err := attestation.Validate(); err != nil {
		return err
	}
	payload, err := ReleaseAuthorityAttestationSigningPayload(attestation)
	if err != nil {
		return err
	}
	signature, _ := base64.StdEncoding.Strict().DecodeString(attestation.Signature)
	if !ed25519.Verify(publicKey, payload, signature) {
		return errors.New("release authority attestation Ed25519 signature verification failed")
	}
	return nil
}

// validateResultReceiptAuthority 将签名回执绑定到唯一 entrypoint owner 和 attestation。
func validateResultReceiptAuthority(receipt ResultReceipt) error {
	entrypoint, err := receiptEntrypoint(receipt.Entrypoint)
	if err != nil {
		return err
	}
	if entrypoint.Owner != receipt.AuthorityOwner {
		return errors.New("receipt authority owner does not match entrypoint")
	}
	if entrypoint.ID == CIEntrypointRelease {
		return validateReleaseReceiptAttestation(receipt)
	}
	return nil
}

func receiptEntrypoint(id CIEntrypointID) (CIEntrypoint, error) {
	for _, entrypoint := range CIEntrypointRegistry() {
		if entrypoint.ID == id {
			return entrypoint, nil
		}
	}
	return CIEntrypoint{}, fmt.Errorf("unknown receipt entrypoint %q", id)
}

func validateReleaseReceiptAttestation(receipt ResultReceipt) error {
	attestation, err := DecodeReleaseAuthorityAttestation(receipt.AuthorityAttestation)
	if err != nil {
		return err
	}
	if attestation.InvocationID != receipt.InvocationID || !reflect.DeepEqual(attestation.Source, receipt.Source) ||
		attestation.PlanDigest != receipt.PlanDigest {
		return errors.New("release receipt authority attestation does not bind receipt invocation, source, and plan")
	}
	return nil
}

// validResultReceiptAttestation 只接受规范 sha256 identity，避免把任意字符串签入权威闭包。
func validResultReceiptAttestation(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if character < '0' || character > '9' && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
