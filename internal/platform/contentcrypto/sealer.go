package contentcrypto

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"unicode/utf8"
)

const sealRequestDigestDomain = "orch.content.single.seal-request/v1"

// AESGCMSealer 使用显式key lease provider完成AES-256-GCM密封。
type AESGCMSealer struct {
	keys KeyLeaseCreator
}

// NewAESGCMSealer 构造production sealer；缺key provider时立即失败。
func NewAESGCMSealer(keys KeyLeaseCreator) (*AESGCMSealer, error) {
	if nilCryptoDependency(keys) {
		return nil, ErrKeyProviderMissing
	}
	return &AESGCMSealer{keys: keys}, nil
}

// Validate 确认显式key lease provider仍已配置。
func (s *AESGCMSealer) Validate() error {
	if s == nil || nilCryptoDependency(s.keys) {
		return ErrKeyProviderMissing
	}
	return nil
}

// CreateAndSeal 校验请求、创建opaque key lease、密封并在返回前覆写raw key副本。
func (s *AESGCMSealer) CreateAndSeal(ctx context.Context, request SealRequest) (SealedPayload, error) {
	if s == nil || nilCryptoDependency(s.keys) {
		return SealedPayload{}, ErrKeyProviderMissing
	}
	if err := ctx.Err(); err != nil {
		return SealedPayload{}, err
	}
	requestDigest, aadDigest, err := validateSealRequest(request)
	if err != nil {
		return SealedPayload{}, err
	}
	lease, err := s.keys.CreateAES256KeyLease(ctx, KeyCreateRequest{
		OperationID: request.OperationID, RequestDigest: requestDigest,
		ObjectID: request.ObjectID, ObjectVersion: request.ObjectVersion, Algorithm: AlgorithmAES256GCM,
	})
	if err != nil {
		return SealedPayload{}, fmt.Errorf("create content key lease: %w", err)
	}
	if lease == nil {
		return SealedPayload{}, ErrKeyLeaseInvalid
	}
	result, sealErr := s.sealWithLease(request, requestDigest, aadDigest, lease)
	closeErr := lease.Close()
	if sealErr != nil || closeErr != nil {
		return SealedPayload{}, errors.Join(sealErr, closeErr)
	}
	return result, nil
}

// sealWithLease 仅在短期key副本范围内执行AES-GCM并验证输出。
func (s *AESGCMSealer) sealWithLease(request SealRequest, requestDigest, aadDigest string, lease *EphemeralKeyLease) (SealedPayload, error) {
	metadata, key, err := lease.snapshot()
	if err != nil {
		return SealedPayload{}, err
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return SealedPayload{}, fmt.Errorf("create AES-256 cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return SealedPayload{}, fmt.Errorf("create GCM: %w", err)
	}
	nonce := append([]byte(nil), metadata.Nonce...)
	if len(nonce) != gcm.NonceSize() {
		return SealedPayload{}, ErrKeyLeaseInvalid
	}
	ciphertext := gcm.Seal(nil, nonce, request.Plaintext, request.AAD)
	result := SealedPayload{
		OperationID: request.OperationID, RequestDigest: requestDigest, Algorithm: metadata.Algorithm,
		KeyWrapProfile: metadata.KeyWrapProfile, KeyHandleRef: metadata.KeyHandleRef, KeyReceiptRef: metadata.KeyReceiptRef,
		KeyReceiptDigest: metadata.KeyReceiptDigest,
		Nonce:            append([]byte(nil), nonce...), Ciphertext: ciphertext, AADDigest: aadDigest,
		CiphertextHash: hashBytes(ciphertext), PlaintextSize: int64(len(request.Plaintext)), CiphertextSize: int64(len(ciphertext)),
	}
	if err := result.Validate(); err != nil {
		return SealedPayload{}, err
	}
	return result, nil
}

// Validate 校验密封结果形状、算法、nonce、密文hash与GCM tag长度。
func (p SealedPayload) Validate() error {
	if err := p.validateSizesAndAlgorithm(); err != nil {
		return err
	}
	for _, digest := range []string{p.RequestDigest, p.KeyReceiptDigest, p.AADDigest, p.CiphertextHash} {
		if !validDigest(digest) {
			return ErrInvalidSealedResult
		}
	}
	for _, value := range []string{p.OperationID, p.KeyWrapProfile, p.KeyHandleRef, p.KeyReceiptRef} {
		if !validText(value, 512) {
			return ErrInvalidSealedResult
		}
	}
	return nil
}

// ValidateSealedForRequest 独立重算request/AAD摘要，供CONTENT owner拒绝错绑或伪造receipt。
func ValidateSealedForRequest(request SealRequest, payload SealedPayload) error {
	requestDigest, aadDigest, err := validateSealRequest(request)
	if err != nil {
		return err
	}
	if err := payload.Validate(); err != nil {
		return err
	}
	if payload.OperationID != request.OperationID || payload.RequestDigest != requestDigest || payload.AADDigest != aadDigest {
		return ErrInvalidSealedResult
	}
	if payload.PlaintextSize != int64(len(request.Plaintext)) {
		return ErrInvalidSealedResult
	}
	return nil
}

// validateSizesAndAlgorithm 校验AES-GCM nonce/tag开销、上限和密文hash。
func (p SealedPayload) validateSizesAndAlgorithm() error {
	if p.Algorithm != AlgorithmAES256GCM || len(p.Nonce) != GCMNonceBytes {
		return ErrInvalidSealedResult
	}
	if p.PlaintextSize <= 0 || p.PlaintextSize > MaxSingleBytes {
		return ErrInvalidSealedResult
	}
	if p.CiphertextSize != int64(len(p.Ciphertext)) || p.CiphertextSize != p.PlaintextSize+aes.BlockSize {
		return ErrInvalidSealedResult
	}
	if p.CiphertextHash != hashBytes(p.Ciphertext) {
		return ErrInvalidSealedResult
	}
	return nil
}

// validateSealRequest 校验CONTENT owner输入并派生无明文hash的request/AAD摘要。
func validateSealRequest(request SealRequest) (string, string, error) {
	if !validSealRequestText(request) {
		return "", "", ErrInvalidSealRequest
	}
	if request.ObjectVersion <= 0 || request.AADProfile != AADProfileV1 {
		return "", "", ErrInvalidSealRequest
	}
	if !validSealRequestPayload(request) {
		return "", "", ErrInvalidSealRequest
	}
	aadDigest := hashBytes(request.AAD)
	digest, err := sealRequestDigest(request, aadDigest)
	if err != nil {
		return "", "", err
	}
	return digest, aadDigest, nil
}

func validSealRequestText(request SealRequest) bool {
	return validText(request.OperationID, 128) && validText(request.TicketID, 128) &&
		validText(request.ObjectID, 128) && validText(request.SourceFenceRef, 512) && validText(request.MediaType, 128)
}

func validSealRequestPayload(request SealRequest) bool {
	return len(request.Plaintext) > 0 && len(request.Plaintext) <= MaxSingleBytes &&
		len(request.AAD) > 0 && validDigest(request.ContentIntentDigest)
}

func sealRequestDigest(request SealRequest, aadDigest string) (string, error) {
	metadata, err := json.Marshal(struct {
		Domain              string `json:"domain"`
		OperationID         string `json:"operation_id"`
		TicketID            string `json:"ticket_id"`
		ObjectID            string `json:"object_id"`
		ObjectVersion       int64  `json:"object_version"`
		SourceFenceRef      string `json:"source_fence_ref"`
		MediaType           string `json:"media_type"`
		AADProfile          string `json:"aad_profile"`
		ContentIntentDigest string `json:"content_intent_digest"`
		PlaintextSize       int    `json:"plaintext_size"`
		AADDigest           string `json:"aad_digest"`
	}{sealRequestDigestDomain, request.OperationID, request.TicketID, request.ObjectID, request.ObjectVersion,
		request.SourceFenceRef, request.MediaType, request.AADProfile, request.ContentIntentDigest, len(request.Plaintext), aadDigest})
	if err != nil {
		return "", fmt.Errorf("encode content seal request digest: %w", err)
	}
	hasher := sha256.New()
	writeSealDigestPart(hasher, []byte(sealRequestDigestDomain))
	writeSealDigestPart(hasher, metadata)
	writeSealDigestPart(hasher, request.Plaintext)
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

type sealDigestWriter interface {
	Write([]byte) (int, error)
}

// writeSealDigestPart 用固定长度前缀消除metadata与实际明文字节之间的拼接歧义。
func writeSealDigestPart(writer sealDigestWriter, value []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = writer.Write(size[:])
	_, _ = writer.Write(value)
}

// validate 校验opaque key metadata、算法和provider登记的nonce。
func (m KeyMetadata) validate() error {
	if m.Algorithm != AlgorithmAES256GCM || !validText(m.KeyWrapProfile, 128) {
		return ErrKeyLeaseInvalid
	}
	if !validText(m.KeyHandleRef, 512) || !validText(m.KeyReceiptRef, 512) {
		return ErrKeyLeaseInvalid
	}
	if !validDigest(m.KeyReceiptDigest) {
		return ErrKeyLeaseInvalid
	}
	if len(m.Nonce) != GCMNonceBytes {
		return ErrKeyLeaseInvalid
	}
	return nil
}

func hashBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func validDigest(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validText(value string, limit int) bool {
	return value != "" && len(value) <= limit && utf8.ValidString(value) &&
		strings.TrimSpace(value) == value && !strings.ContainsRune(value, 0)
}

func nilCryptoDependency(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

var _ KeyCreatorSealer = (*AESGCMSealer)(nil)
