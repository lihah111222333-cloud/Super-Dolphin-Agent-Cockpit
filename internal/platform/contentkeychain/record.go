package contentkeychain

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/contentcrypto"
)

const wrapProfile = "macos-keychain/v1"
const maxRecordBytes = 4096

// keyRecord 只存在于私有原生存储边界；独立保存上游禁止公开JSON的digest。
type keyRecord struct {
	Schema        int
	Request       contentcrypto.KeyCreateRequest
	RequestDigest string
	Key           []byte
	Nonce         []byte
}

// String 防止调试格式化泄露私有记录。
func (keyRecord) String() string { return "<private keychain record>" }

// GoString 同时阻断Go语法格式化输出秘密。
func (keyRecord) GoString() string { return "<private keychain record>" }

// validRequest 完整校验上游算法、身份、版本与私有摘要形状。
func validRequest(r contentcrypto.KeyCreateRequest) bool {
	digest, err := hex.DecodeString(r.RequestDigest)
	return err == nil && len(digest) == sha256.Size && strings.ToLower(r.RequestDigest) == r.RequestDigest &&
		validText(r.OperationID, 128) && validText(r.ObjectID, 128) && r.ObjectVersion > 0 && r.Algorithm == contentcrypto.AlgorithmAES256GCM
}

// accountFor 使用域分离摘要避免向系统账户字段传播原始operation。
func accountFor(operation string) string {
	sum := sha256.Sum256([]byte("orch.content.keychain.account/v1\x00" + operation))
	return hex.EncodeToString(sum[:])
}

// create 仅在明确缺失后创建随机材料，原子Add竞争后丢弃本地副本并回读赢家。
func (p *Provider) create(request contentcrypto.KeyCreateRequest) (*contentcrypto.EphemeralKeyLease, bool, error) {
	account := accountFor(request.OperationID)
	data, err := p.store.read(p.config.Namespace, account)
	if err == nil {
		lease, err := leaseFrom(data, request)
		return lease, false, err
	}
	clear(data)
	if !errors.Is(err, errNotFound) {
		return nil, false, err
	}
	lease, err := p.addAndRead(request, account)
	return lease, err == nil, err
}

// addAndRead 仅在明确不存在后生成材料，成功添加时验证回读完全一致。
func (p *Provider) addAndRead(request contentcrypto.KeyCreateRequest, account string) (*contentcrypto.EphemeralKeyLease, error) {
	record := keyRecord{Schema: 1, Request: request, RequestDigest: request.RequestDigest, Key: make([]byte, 32), Nonce: make([]byte, 12)}
	defer clear(record.Key)
	defer clear(record.Nonce)
	if _, err := rand.Read(record.Key); err != nil {
		return nil, ErrNative
	}
	if _, err := rand.Read(record.Nonce); err != nil {
		return nil, ErrNative
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return nil, ErrCorrupt
	}
	expected := sha256.Sum256(encoded)
	err = p.store.add(p.config.Namespace, account, encoded)
	clear(encoded)
	clear(record.Key)
	clear(record.Nonce)
	if err != nil && !errors.Is(err, errDuplicate) {
		return nil, err
	}
	added := err == nil
	data, err := p.store.read(p.config.Namespace, account)
	if err != nil {
		clear(data)
		return nil, err
	}
	if added && sha256.Sum256(data) != expected {
		clear(data)
		return nil, ErrCorrupt
	}
	return leaseFrom(data, request)
}

// decodeRecord 接受唯一canonical编码，拒绝缺字段、重复字段、未知字段和损坏材料。
func decodeRecord(data []byte) (keyRecord, error) {
	var r keyRecord
	if len(data) == 0 || len(data) > maxRecordBytes {
		return r, ErrCorrupt
	}
	if err := json.Unmarshal(data, &r); err != nil {
		clear(r.Key)
		clear(r.Nonce)
		return keyRecord{}, ErrCorrupt
	}
	canonical, err := json.Marshal(r)
	defer clear(canonical)
	if err != nil || !bytes.Equal(canonical, data) || r.Schema != 1 || len(r.Key) != 32 || len(r.Nonce) != 12 {
		clear(r.Key)
		clear(r.Nonce)
		return keyRecord{}, ErrCorrupt
	}
	r.Request.RequestDigest = r.RequestDigest
	if !validRequest(r.Request) {
		clear(r.Key)
		clear(r.Nonce)
		return keyRecord{}, ErrCorrupt
	}
	return r, nil
}

// leaseFrom 只在完整请求匹配后生成不含原始请求摘要的opaque收据。
func leaseFrom(data []byte, request contentcrypto.KeyCreateRequest) (*contentcrypto.EphemeralKeyLease, error) {
	defer clear(data)
	r, err := decodeRecord(data)
	if err != nil {
		return nil, err
	}
	defer clear(r.Key)
	defer clear(r.Nonce)
	if r.Request != request {
		return nil, ErrConflict
	}
	bindings := make([]string, 0, 3)
	for _, purpose := range []string{"handle", "reference", "receipt"} {
		value, err := recordMAC(r, purpose)
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, value)
	}
	metadata := contentcrypto.KeyMetadata{
		Algorithm: request.Algorithm, KeyWrapProfile: wrapProfile, Nonce: r.Nonce,
		KeyHandleRef:     "content-keychain://v1/" + bindings[0],
		KeyReceiptRef:    "content-keychain-receipt://v1/" + bindings[1],
		KeyReceiptDigest: bindings[2],
	}
	return contentcrypto.NewEphemeralKeyLease(metadata, r.Key)
}

// recordMAC 用DEK和独立用途域绑定canonical请求与nonce，不公开plaintext绑定摘要。
func recordMAC(r keyRecord, purpose string) (string, error) {
	mac := hmac.New(sha256.New, r.Key)
	// 已验证记录只包含可编码的固定类型；JSON编码器直接写入HMAC避免额外秘密缓冲。
	_, _ = mac.Write([]byte("orch.content.keychain/" + purpose + "/v1\x00"))
	r.Key = nil
	if err := json.NewEncoder(mac).Encode(r); err != nil {
		return "", ErrCorrupt
	}
	return hex.EncodeToString(mac.Sum(nil)), nil
}
