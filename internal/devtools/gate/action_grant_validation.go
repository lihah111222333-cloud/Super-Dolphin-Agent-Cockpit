package gate

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Validate 校验签名 ActionGrant 及其唯一终态时间字段。
func (g ActionGrant) Validate() error {
	return g.validate(true)
}

// validate 按签名要求校验 ActionGrant 的身份、请求、时间线和状态。
func (g ActionGrant) validate(requireSignature bool) error {
	if err := validateActionGrantIdentity(g, requireSignature); err != nil {
		return err
	}
	if err := g.Request.Validate(); err != nil {
		return fmt.Errorf("grant request: %w", err)
	}
	if err := validateActionGrantTimeline(g); err != nil {
		return err
	}
	if err := g.validateState(); err != nil {
		return err
	}
	return g.Signer.Validate()
}

// validateActionGrantIdentity 校验 schema、grant ID 与可选签名字段。
func validateActionGrantIdentity(g ActionGrant, requireSignature bool) error {
	if g.SchemaVersion != 1 {
		return fmt.Errorf("unsupported action grant schema_version %d", g.SchemaVersion)
	}
	if strings.TrimSpace(g.GrantID) == "" {
		return errors.New("grant_id is required")
	}
	if requireSignature && strings.TrimSpace(g.Signature) == "" {
		return errors.New("signature is required")
	}
	return nil
}

// validateActionGrantTimeline 校验 grant 与请求携带完全一致的签发和过期时间。
func validateActionGrantTimeline(g ActionGrant) error {
	if g.IssuedAt.IsZero() || !g.IssuedAt.Equal(g.Request.RequestedAt) ||
		!g.ExpiresAt.After(g.IssuedAt) || !g.ExpiresAt.Equal(g.Request.ExpiresAt) {
		return errors.New("grant issued_at and expires_at are invalid")
	}
	return nil
}

// validateState 将 grant 状态分派到唯一合法的终态时间字段组合。
func (g ActionGrant) validateState() error {
	switch g.State {
	case ActionGrantStateIssued:
		return validateIssuedGrant(g)
	case ActionGrantStateConsumed:
		return validateConsumedGrant(g)
	case ActionGrantStateExpired:
		return validateExpiredGrant(g)
	case ActionGrantStateRevoked:
		return validateRevokedGrant(g)
	default:
		return fmt.Errorf("unsupported action grant state %q", g.State)
	}
}

func validateIssuedGrant(grant ActionGrant) error {
	if grant.ConsumedAt != nil || grant.RevokedAt != nil {
		return errors.New("issued grant cannot have terminal timestamps")
	}
	return nil
}

func validateConsumedGrant(grant ActionGrant) error {
	if grant.ConsumedAt == nil || grant.RevokedAt != nil {
		return errors.New("consumed grant requires only consumed_at")
	}
	if grant.ConsumedAt.Before(grant.IssuedAt) || grant.ConsumedAt.After(grant.ExpiresAt) {
		return errors.New("consumed_at must be within the grant lifetime")
	}
	return nil
}

func validateExpiredGrant(grant ActionGrant) error {
	if grant.ConsumedAt != nil || grant.RevokedAt != nil {
		return errors.New("expired grant cannot have consumed_at or revoked_at")
	}
	return nil
}

func validateRevokedGrant(grant ActionGrant) error {
	if grant.RevokedAt == nil || grant.ConsumedAt != nil {
		return errors.New("revoked grant requires only revoked_at")
	}
	if grant.RevokedAt.Before(grant.IssuedAt) {
		return errors.New("revoked_at cannot precede issued_at")
	}
	return nil
}

// ActionGrantSigningPayload 返回排除 signature 值后的规范 JSON 签名载荷。
func ActionGrantSigningPayload(grant ActionGrant) ([]byte, error) {
	unsigned := grant
	unsigned.Signature = ""
	if err := unsigned.validate(false); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(unsigned)
	if err != nil {
		return nil, fmt.Errorf("marshal action grant signing payload: %w", err)
	}
	return payload, nil
}

// VerifyActionGrant 使用真实 Ed25519 公钥校验完整规范授权。
func VerifyActionGrant(grant ActionGrant, publicKey ed25519.PublicKey) error {
	if len(publicKey) != ed25519.PublicKeySize {
		return errors.New("action grant public key must be Ed25519")
	}
	if err := grant.Validate(); err != nil {
		return err
	}
	payload, err := ActionGrantSigningPayload(grant)
	if err != nil {
		return err
	}
	signature, err := base64.StdEncoding.Strict().DecodeString(grant.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return errors.New("action grant signature must be canonical base64 Ed25519")
	}
	if !ed25519.Verify(publicKey, payload, signature) {
		return errors.New("action grant Ed25519 signature verification failed")
	}
	return nil
}

// ActionGrantDigest 返回完整校验后签名授权的规范摘要。
func ActionGrantDigest(grant ActionGrant) (string, error) {
	if err := grant.Validate(); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(grant)
	if err != nil {
		return "", fmt.Errorf("marshal action grant: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", sum), nil
}
