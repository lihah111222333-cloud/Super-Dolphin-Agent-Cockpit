// Package contentobject 持久化CONTENT-1密文封装到私有state-dir。
// 它不提供明文读取、解密、删除、GC或remote custody能力。
package contentobject

import (
	"context"
	"errors"
	"time"
)

const MaxCiphertextBytes = 64*1024 + 16

var (
	ErrInvalidObjectRequest       = errors.New("content object request is invalid")
	ErrObjectConflict             = errors.New("content object conflicts with the original sealed payload")
	ErrObjectNotFound             = errors.New("content object is not found")
	ErrObjectCorrupt              = errors.New("content object envelope is corrupt")
	ErrObjectCommitOutcomeUnknown = errors.New("content object commit outcome is unknown")
	ErrObjectStoreMissing         = errors.New("content object store is required")
	ErrUnsafeStateDir             = errors.New("content object state directory is unsafe")
)

// Config 固定独立private state-dir、workspace隔离边界和store identity。
type Config struct {
	StateDir      string `json:"state_dir"`
	WorkspaceRoot string `json:"workspace_root"`
	StoreID       string `json:"store_id"`
}

// PutRequest 只携AES-GCM密文与完整性元数据，不允许明文或raw key。
type PutRequest struct {
	SchemaVersion  int64  `json:"schema_version"`
	OperationID    string `json:"operation_id"`
	RequestDigest  string `json:"request_digest"`
	ObjectID       string `json:"object_id"`
	ObjectVersion  int64  `json:"object_version"`
	CiphertextHash string `json:"ciphertext_hash"`
	CiphertextSize int64  `json:"ciphertext_size"`
	Ciphertext     []byte `json:"-"`
}

// QueryRequest 只查询原object put operation，不生成新operation。
type QueryRequest struct {
	SchemaVersion int64  `json:"schema_version"`
	OperationID   string `json:"operation_id"`
	RequestDigest string `json:"request_digest"`
	ObjectID      string `json:"object_id"`
	ObjectVersion int64  `json:"object_version"`
}

// PutReceipt 是密文封装完整性与定位回执；仅Put成功返回证明本地durability barrier完成，QueryPut只证明当前可读匹配。
type PutReceipt struct {
	SchemaVersion  int64     `json:"schema_version"`
	StoreID        string    `json:"store_id"`
	OperationID    string    `json:"operation_id"`
	RequestDigest  string    `json:"request_digest"`
	ObjectID       string    `json:"object_id"`
	ObjectVersion  int64     `json:"object_version"`
	ObjectRef      string    `json:"object_ref"`
	ReceiptRef     string    `json:"receipt_ref"`
	CiphertextHash string    `json:"ciphertext_hash"`
	CiphertextSize int64     `json:"ciphertext_size"`
	ReceiptDigest  string    `json:"receipt_digest"`
	RecordedAt     time.Time `json:"recorded_at"`
}

// Store 是CONTENT owner消费的密文put/query窄口。
type Store interface {
	Validate() error
	Put(context.Context, PutRequest) (PutReceipt, error)
	QueryPut(context.Context, QueryRequest) (PutReceipt, error)
}

// UnavailableStore 是没有显式private object backend时的fail-fast实现。
type UnavailableStore struct{}

// Validate 明确报告private object backend缺失，阻止CONTENT owner构造成功。
func (UnavailableStore) Validate() error { return ErrObjectStoreMissing }

// Put 永远拒绝，禁止回退sharedfile、temp或SQLite密文字节。
func (UnavailableStore) Put(context.Context, PutRequest) (PutReceipt, error) {
	return PutReceipt{}, ErrObjectStoreMissing
}

// QueryPut 永远拒绝，不能从本地缺失状态猜object commit结果。
func (UnavailableStore) QueryPut(context.Context, QueryRequest) (PutReceipt, error) {
	return PutReceipt{}, ErrObjectStoreMissing
}

var _ Store = UnavailableStore{}
