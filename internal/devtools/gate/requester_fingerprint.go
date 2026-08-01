package gate

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const (
	// RequesterFingerprintEnvironment 允许任意客户端在多个 CLI 请求间复用同一逻辑发起方。
	RequesterFingerprintEnvironment = "SUPER_DOLPHIN_GATE_REQUESTER_FINGERPRINT"
	requesterFingerprintPrefix      = "sha256:"
	requesterFingerprintEntropySize = 32
	requesterFingerprintTextLength  = len(requesterFingerprintPrefix) + requesterFingerprintEntropySize*2
)

// RequesterFingerprint 标识发起方的随机实例，不是授权凭证。
type RequesterFingerprint string

// GenerateRequesterFingerprint 使用 32 字节密码学随机数生成规范 requester fingerprint。
func GenerateRequesterFingerprint() (RequesterFingerprint, error) {
	entropy := make([]byte, requesterFingerprintEntropySize)
	if _, err := rand.Read(entropy); err != nil {
		return "", fmt.Errorf("generate requester fingerprint entropy: %w", err)
	}
	return RequesterFingerprint(requesterFingerprintPrefix + hex.EncodeToString(entropy)), nil
}

// ParseRequesterFingerprint 解析并拒绝任何非规范 requester fingerprint 文本。
func ParseRequesterFingerprint(value string) (RequesterFingerprint, error) {
	fingerprint := RequesterFingerprint(value)
	if err := fingerprint.Validate(); err != nil {
		return "", err
	}
	return fingerprint, nil
}

// Validate 验证 requester fingerprint 的精确小写 sha256 文本形态。
func (fingerprint RequesterFingerprint) Validate() error {
	value := string(fingerprint)
	if value == "" {
		return errors.New("requester fingerprint is required")
	}
	if strings.TrimSpace(value) != value {
		return errors.New("requester fingerprint must not contain surrounding whitespace")
	}
	if !strings.HasPrefix(value, requesterFingerprintPrefix) {
		return errors.New("requester fingerprint must use the lowercase sha256: prefix")
	}
	if len(value) != requesterFingerprintTextLength {
		return fmt.Errorf(
			"requester fingerprint length is %d, want %d",
			len(value),
			requesterFingerprintTextLength,
		)
	}
	for _, character := range value[len(requesterFingerprintPrefix):] {
		if character >= '0' && character <= '9' || character >= 'a' && character <= 'f' {
			continue
		}
		return errors.New("requester fingerprint digest must be lowercase hexadecimal")
	}
	return nil
}

// String 返回 requester fingerprint 的规范文本。
func (fingerprint RequesterFingerprint) String() string {
	return string(fingerprint)
}
