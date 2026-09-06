// Package contentcrypto 提供CONTENT owner使用的encrypted-single密封原语。
// 它不拥有内容生命周期、SQLite记录、object backend或provider dispatch。
package contentcrypto

import (
	"context"
	"errors"
)

const (
	AlgorithmAES256GCM = "aes-256-gcm"
	AADProfileV1       = "orch.content.single.aad/v1"
	MaxSingleBytes     = 64 * 1024
	AES256KeyBytes     = 32
	GCMNonceBytes      = 12
)

var (
	ErrInvalidSealRequest  = errors.New("content seal request is invalid")
	ErrInvalidSealedResult = errors.New("sealed content result is invalid")
	ErrKeyProviderMissing  = errors.New("content key provider is required")
	ErrKeyLeaseInvalid     = errors.New("content key lease is invalid")
)

// SealRequest 只携有界明文内存和owner已产生的keyed content intent digest。
type SealRequest struct {
	OperationID         string `json:"operation_id"`
	TicketID            string `json:"ticket_id"`
	ObjectID            string `json:"object_id"`
	ObjectVersion       int64  `json:"object_version"`
	SourceFenceRef      string `json:"source_fence_ref"`
	MediaType           string `json:"media_type"`
	AADProfile          string `json:"aad_profile"`
	ContentIntentDigest string `json:"content_intent_digest"`
	Plaintext           []byte `json:"-"`
	AAD                 []byte `json:"-"`
}

// SealedPayload 包含可持久化到私有object backend的密文字节；不含raw DEK或明文。
type SealedPayload struct {
	OperationID      string `json:"operation_id"`
	RequestDigest    string `json:"-"`
	Algorithm        string `json:"algorithm"`
	KeyWrapProfile   string `json:"key_wrap_profile"`
	KeyHandleRef     string `json:"key_handle_ref"`
	KeyReceiptRef    string `json:"key_receipt_ref"`
	KeyReceiptDigest string `json:"key_receipt_digest"`
	Nonce            []byte `json:"nonce"`
	Ciphertext       []byte `json:"-"`
	AADDigest        string `json:"aad_digest"`
	CiphertextHash   string `json:"ciphertext_hash"`
	PlaintextSize    int64  `json:"plaintext_size"`
	CiphertextSize   int64  `json:"ciphertext_size"`
}

// KeyMetadata 是key provider的opaque handle与创建receipt；不暴露raw key。
type KeyMetadata struct {
	Algorithm        string `json:"algorithm"`
	KeyWrapProfile   string `json:"key_wrap_profile"`
	KeyHandleRef     string `json:"key_handle_ref"`
	KeyReceiptRef    string `json:"key_receipt_ref"`
	KeyReceiptDigest string `json:"key_receipt_digest"`
	Nonce            []byte `json:"nonce"`
}

// KeyCreateRequest 将新DEK创建绑定到原seal operation和request digest。
type KeyCreateRequest struct {
	OperationID   string `json:"operation_id"`
	RequestDigest string `json:"-"`
	ObjectID      string `json:"object_id"`
	ObjectVersion int64  `json:"object_version"`
	Algorithm     string `json:"algorithm"`
}

// KeyLeaseCreator 按operation/request幂等返回已登记的短期AES key与随机nonce；异digest必须冲突。
type KeyLeaseCreator interface {
	CreateAES256KeyLease(context.Context, KeyCreateRequest) (*EphemeralKeyLease, error)
}

// KeyCreatorSealer 是CONTENT store消费的最小密封能力；实现不得返回raw DEK。
type KeyCreatorSealer interface {
	Validate() error
	CreateAndSeal(context.Context, SealRequest) (SealedPayload, error)
}

// UnavailableSealer 是当前没有key provider时的fail-fast实现。
type UnavailableSealer struct{}

// Validate 明确报告key provider缺失，阻止CONTENT owner构造成功。
func (UnavailableSealer) Validate() error { return ErrKeyProviderMissing }

// CreateAndSeal 永远拒绝，禁止临时进程密钥或明文fallback。
func (UnavailableSealer) CreateAndSeal(context.Context, SealRequest) (SealedPayload, error) {
	return SealedPayload{}, ErrKeyProviderMissing
}

var _ KeyCreatorSealer = UnavailableSealer{}
