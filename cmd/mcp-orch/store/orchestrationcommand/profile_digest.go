package orchestrationcommand

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// SendProfileDigestKeyResolver 返回指定 command digest key 的独占副本；实现不得返回共享可变切片。
type SendProfileDigestKeyResolver interface {
	ResolveSendProfileDigestKey(context.Context, string, int64) ([]byte, error)
}

// SendProfileDigestVerifier 在 profile 落盘前验证完整 domain-separated HMAC。
type SendProfileDigestVerifier interface {
	VerifySendProfileDigest(context.Context, Intent, SendMessageProfile) error
}

// SendProfileDigestSigner 为 composition assembler 生成与 verifier 同源的完整 intent digest。
type SendProfileDigestSigner interface {
	SignSendProfileDigest(context.Context, Intent, SendMessageProfile) (string, error)
}

// HMACSendProfileDigestVerifier 使用显式 key resolver 验证 send_message_v1 完整 intent digest。
type HMACSendProfileDigestVerifier struct{ resolver SendProfileDigestKeyResolver }

// NewHMACSendProfileDigestVerifier 构造 verifier；缺 key owner 时 fail-fast，不提供无密钥摘要降级。
func NewHMACSendProfileDigestVerifier(resolver SendProfileDigestKeyResolver) (*HMACSendProfileDigestVerifier, error) {
	if isNilProfileInterface(resolver) {
		return nil, errors.New("send profile digest key resolver is required")
	}
	return &HMACSendProfileDigestVerifier{resolver: resolver}, nil
}

// VerifySendProfileDigest 从 key owner 取一次性副本并常量时间比较持久摘要。
func (v *HMACSendProfileDigestVerifier) VerifySendProfileDigest(ctx context.Context, intent Intent, profile SendMessageProfile) error {
	want, err := v.SignSendProfileDigest(ctx, intent, profile)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(want), []byte(profile.CompleteIntentDigest)) != 1 {
		return ErrProfileConflict
	}
	return nil
}

// SignSendProfileDigest 解析指定 key/version 并计算 canonical HMAC，不持久化 key bytes。
func (v *HMACSendProfileDigestVerifier) SignSendProfileDigest(ctx context.Context, intent Intent, profile SendMessageProfile) (string, error) {
	if v == nil || isNilProfileInterface(v.resolver) {
		return "", errors.New("send profile digest verifier is required")
	}
	key, err := v.resolver.ResolveSendProfileDigestKey(ctx, profile.DigestKeyID, profile.DigestKeyVersion)
	if err != nil {
		return "", fmt.Errorf("resolve send profile digest key: %w", err)
	}
	if len(key) == 0 {
		return "", errors.New("send profile digest key is empty")
	}
	defer clear(key)
	return ComputeSendProfileDigest(key, intent, profile)
}

// ComputeSendProfileDigest 对基础 intent 与完整 profile 业务字段计算 HMAC-SHA256；排除摘要自身和数据库时间。
func ComputeSendProfileDigest(key []byte, intent Intent, profile SendMessageProfile) (string, error) {
	if len(key) == 0 {
		return "", errors.New("send profile digest key is empty")
	}
	if profile.DigestDomain != SendMessageDigestDomain || profile.DigestAlgorithm != HMACSHA256DigestAlgorithm {
		return "", ErrInvalidProfile
	}
	intent.CommandID = ""
	intent.RequestDigest = ""
	profile.CompleteIntentDigest = ""
	payload, err := json.Marshal(struct {
		Domain  string             `json:"domain"`
		Intent  Intent             `json:"intent"`
		Profile SendMessageProfile `json:"profile"`
	}{Domain: SendMessageDigestDomain, Intent: intent, Profile: profile})
	if err != nil {
		return "", fmt.Errorf("encode send profile digest payload: %w", err)
	}
	hasher := hmac.New(sha256.New, key)
	if _, err := hasher.Write(payload); err != nil {
		return "", fmt.Errorf("hash send profile digest payload: %w", err)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}
