package trustcandidate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

const (
	contentRefDigestDomain = "orch.trust-candidate.content-ref/v1"
	envelopeDigestDomain   = "orch.trust-candidate.envelope/v1"
)

// NewContentRef 以 ref/version 生成稳定的 opaque content reference digest。
func NewContentRef(ref string, version int64) (ContentRef, error) {
	if !validText(ref, 512) || version <= 0 {
		return ContentRef{}, fmt.Errorf("%w: content ref identity is invalid", ErrInvalidTicket)
	}
	digest, err := ComputeContentRefDigest(ref, version)
	if err != nil {
		return ContentRef{}, err
	}
	return ContentRef{Ref: ref, Version: version, Digest: digest}, nil
}

// ComputeContentRefDigest 计算 domain-separated ref/version 摘要，不读取正文。
func ComputeContentRefDigest(ref string, version int64) (string, error) {
	if !validText(ref, 512) || version <= 0 {
		return "", fmt.Errorf("%w: content ref identity is invalid", ErrInvalidTicket)
	}
	payload, err := json.Marshal(struct {
		Domain  string `json:"domain"`
		Ref     string `json:"ref"`
		Version int64  `json:"version"`
	}{contentRefDigestDomain, ref, version})
	if err != nil {
		return "", fmt.Errorf("%w: content ref digest encoding failed", ErrInvalidTicket)
	}
	return hashBytes(payload), nil
}

// ComputeEnvelopeDigest 计算不含自身 digest 的 canonical envelope 摘要。
func ComputeEnvelopeDigest(envelope TrustEnvelope) (string, error) {
	envelope.EnvelopeDigest = ""
	if err := validateEnvelopeShape(envelope); err != nil {
		return "", err
	}
	payload, err := json.Marshal(struct {
		Domain string        `json:"domain"`
		Value  TrustEnvelope `json:"value"`
	}{envelopeDigestDomain, envelope})
	if err != nil {
		return "", fmt.Errorf("%w: envelope digest encoding failed", ErrInvalidEnvelope)
	}
	return hashBytes(payload), nil
}

// FinalizeEnvelope 校验 envelope 形状并写入唯一 self-digest；不产生任何外部副作用。
func FinalizeEnvelope(envelope TrustEnvelope) (TrustEnvelope, error) {
	envelope.EnvelopeDigest = ""
	if err := validateEnvelopeShape(envelope); err != nil {
		return TrustEnvelope{}, err
	}
	digest, err := ComputeEnvelopeDigest(envelope)
	if err != nil {
		return TrustEnvelope{}, err
	}
	envelope.EnvelopeDigest = digest
	return envelope, nil
}

// ComputeMetadataDigest 计算原始 metadata bytes 摘要；bytes 只在调用方内存中使用。
func ComputeMetadataDigest(raw []byte) (string, error) {
	if len(raw) == 0 || len(raw) > MaxContentBytes {
		return "", fmt.Errorf("%w: metadata bytes exceed bounded size", ErrInvalidTicket)
	}
	return hashBytes(raw), nil
}

func hashBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func validDigest(value string) bool {
	if len(value) != sha256.Size*2 || value != lowerASCII(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func lowerASCII(value string) string {
	result := []byte(value)
	for index, char := range result {
		if char >= 'A' && char <= 'Z' {
			result[index] = char + ('a' - 'A')
		}
	}
	return string(result)
}
