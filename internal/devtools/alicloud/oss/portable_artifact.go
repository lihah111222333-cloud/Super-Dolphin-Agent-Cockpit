package oss

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"path"
	"reflect"
	"strings"
	"unicode"
)

const (
	// PortableArtifactStoreSchema identifies the content-addressed portable store.
	PortableArtifactStoreSchema = "portable-artifact-store/v1"
	PortableInputKind           = "portable-builder-input/v1"
	PortableArtifactKind        = "source-baseline-git-bundle/v1"
	PortableReceiptKind         = "portable-builder-receipt/v1"
	portableObjectReadLimit     = 1 << 30
)

var (
	// ErrPortableArtifactConflict means that a content-addressed key already
	// names bytes or metadata different from the requested identity.
	ErrPortableArtifactConflict = errors.New("portable artifact content-address conflict")
	// ErrPortableArtifactImmutable means that a final artifact or receipt was
	// sent to the input-only cleanup surface.
	ErrPortableArtifactImmutable = errors.New("portable artifact is immutable")
	// ErrPortableArtifactBodyIncomplete 表示对象流在声明长度验证完成前结束或失败。
	ErrPortableArtifactBodyIncomplete = errors.New("portable artifact body was not fully verified")
	// ErrPortableArtifactBodyTooLong 表示对象流包含超过声明长度的字节。
	ErrPortableArtifactBodyTooLong = errors.New("portable artifact body exceeded its declared length")
	// ErrPortableArtifactBodyMismatch 表示消费后的正文摘要与内容寻址元数据不一致。
	ErrPortableArtifactBodyMismatch = errors.New("portable artifact body hash mismatch")
)

// PortableArtifactMetadata is the only object identity accepted by this store.
type PortableArtifactMetadata struct {
	SHA256    string
	SizeBytes int64
}

// PortableArtifactClient is the narrow OSS port needed by the adapter. The
// implementation owns CLI/HTTP details; this package only enforces identity.
type PortableArtifactClient interface {
	Create(context.Context, string, string) error
	Stat(context.Context, string) (PortableArtifactMetadata, error)
	Get(context.Context, string, int64) (io.ReadCloser, int64, PortableArtifactMetadata, error)
	DeletePrefix(context.Context, string) error
	ConfirmPrefixEmpty(context.Context, string) (bool, error)
}

// PortableArtifactObjectStore is the existing client-shaped port. It lets the
// adapter reuse client.Create/Read without changing that client for Stat/Get.
type PortableArtifactObjectStore interface {
	Create(context.Context, string, string) error
	Read(context.Context, string, int64) ([]byte, error)
	DeletePrefix(context.Context, string) error
	ConfirmPrefixEmpty(context.Context, string) (bool, error)
}

// PortableArtifactKeys contains the three canonical object identities.
type PortableArtifactKeys struct {
	InputStagingKey string
	FinalBundleKey  string
	ReceiptKey      string
}

// PortableArtifactStore enforces the input staging/final artifact/receipt
// lifecycle on top of an injected OSS port.
type PortableArtifactStore struct {
	client PortableArtifactClient
	prefix string
}

// NewPortableArtifactStore 校验显式 OSS 前缀和注入端口。
func NewPortableArtifactStore(client PortableArtifactClient, prefix string) (*PortableArtifactStore, error) {
	if isNilPortableValue(client) {
		return nil, errors.New("portable artifact client is required")
	}
	if err := validatePortableStorePrefix(prefix); err != nil {
		return nil, err
	}
	return &PortableArtifactStore{client: client, prefix: prefix}, nil
}

// NewPortableArtifactStoreFromObjectStore 将现有 OSS client 形状适配到严格端口。
func NewPortableArtifactStoreFromObjectStore(client PortableArtifactObjectStore, prefix string) (*PortableArtifactStore, error) {
	if isNilPortableValue(client) {
		return nil, errors.New("portable artifact object store is required")
	}
	return NewPortableArtifactStore(existingPortableArtifactClient{store: client}, prefix)
}

// CanonicalPortableInputStagingKey 构造可清理且按 job 隔离的输入键。
func CanonicalPortableInputStagingKey(prefix, stagingID, digest string) (string, error) {
	if err := validatePortableStorePrefix(prefix); err != nil {
		return "", err
	}
	if err := validatePortableSegment(stagingID, "staging id"); err != nil {
		return "", err
	}
	canonical, err := canonicalPortableDigest(digest)
	if err != nil {
		return "", err
	}
	return prefix + "input-staging/" + stagingID + "/" + strings.TrimPrefix(canonical, "sha256:") + ".input", nil
}

// CanonicalPortableFinalBundleKey 构造不可变最终 bundle 键。
func CanonicalPortableFinalBundleKey(prefix, digest string) (string, error) {
	return canonicalPortableFinalKey(prefix, digest, ".bundle")
}

// CanonicalPortableReceiptKey 构造绑定最终 artifact 摘要的不可变回执键。
func CanonicalPortableReceiptKey(prefix, digest string) (string, error) {
	if err := validatePortableStorePrefix(prefix); err != nil {
		return "", err
	}
	canonical, err := canonicalPortableDigest(digest)
	if err != nil {
		return "", err
	}
	return prefix + "receipts/" + strings.TrimPrefix(canonical, "sha256:") + ".json", nil
}

// PortableInputStagingKey 返回输入 staging 键的 canonical 别名。
func PortableInputStagingKey(prefix, stagingID, digest string) (string, error) {
	return CanonicalPortableInputStagingKey(prefix, stagingID, digest)
}

// PortableFinalBundleKey 返回最终 bundle 键的 canonical 别名。
func PortableFinalBundleKey(prefix, digest string) (string, error) {
	return CanonicalPortableFinalBundleKey(prefix, digest)
}

// PortableReceiptKey 返回回执键的 canonical 别名。
func PortableReceiptKey(prefix, digest string) (string, error) {
	return CanonicalPortableReceiptKey(prefix, digest)
}

// NewPortableArtifactKeys 在校验全部身份后构造三类对象键。
func NewPortableArtifactKeys(prefix, stagingID, inputDigest, artifactDigest string) (PortableArtifactKeys, error) {
	inputKey, err := CanonicalPortableInputStagingKey(prefix, stagingID, inputDigest)
	if err != nil {
		return PortableArtifactKeys{}, err
	}
	bundleKey, err := CanonicalPortableFinalBundleKey(prefix, artifactDigest)
	if err != nil {
		return PortableArtifactKeys{}, err
	}
	receiptKey, err := CanonicalPortableReceiptKey(prefix, artifactDigest)
	if err != nil {
		return PortableArtifactKeys{}, err
	}
	return PortableArtifactKeys{InputStagingKey: inputKey, FinalBundleKey: bundleKey, ReceiptKey: receiptKey}, nil
}

// CreateOnly 以显式身份 create-only 上传输入或最终 bundle，并严格读回校验冲突。
func (s *PortableArtifactStore) CreateOnly(ctx context.Context, localPath, key string, metadata PortableArtifactMetadata) error {
	if err := s.validateContext(ctx); err != nil {
		return err
	}
	if err := s.validateMutableKey(key); err != nil {
		return err
	}
	if err := validatePortableLocalMetadata(localPath, metadata); err != nil {
		return err
	}
	if err := s.validateKeyIdentity(key, metadata.SHA256); err != nil {
		return err
	}
	if err := s.client.Create(ctx, localPath, key); err == nil {
		if err := s.ensureExistingIdentity(ctx, key, metadata); err != nil {
			return fmt.Errorf("verify portable object after create: %w", err)
		}
		return nil
	} else if same, compareErr := s.existingIdentity(ctx, key, metadata); compareErr == nil && same {
		return nil
	} else if compareErr == nil {
		return fmt.Errorf("%w for %q", ErrPortableArtifactConflict, key)
	} else {
		return fmt.Errorf("create-only portable object %q: %w", key, err)
	}
}

// CreateInput 只允许上传 input-staging 对象。
func (s *PortableArtifactStore) CreateInput(ctx context.Context, localPath, key string, metadata PortableArtifactMetadata) error {
	if !s.isInputStagingKey(key) {
		return errors.New("portable input key is not an input-staging key")
	}
	return s.CreateOnly(ctx, localPath, key, metadata)
}

// CreateArtifact 上传不可变最终 bundle。
func (s *PortableArtifactStore) CreateArtifact(ctx context.Context, localPath, key string, metadata PortableArtifactMetadata) error {
	if !s.isFinalBundleKey(key) {
		return errors.New("portable artifact key is not a final bundle key")
	}
	return s.CreateOnly(ctx, localPath, key, metadata)
}

// CreateReceipt 要求最终 artifact 已按相同身份存在后才创建不可变回执。
func (s *PortableArtifactStore) CreateReceipt(ctx context.Context, localPath, receiptKey, artifactKey string, receiptMetadata, artifactMetadata PortableArtifactMetadata) error {
	if err := s.validateContext(ctx); err != nil {
		return err
	}
	if !s.isReceiptKey(receiptKey) || !s.isFinalBundleKey(artifactKey) {
		return errors.New("portable receipt and artifact keys are invalid")
	}
	if !samePortableKeyDigest(receiptKey, artifactKey) {
		return errors.New("portable receipt key is not bound to the final artifact")
	}
	if err := validatePortableLocalMetadata(localPath, receiptMetadata); err != nil {
		return err
	}
	if err := validatePortableMetadata(artifactMetadata); err != nil {
		return err
	}
	if err := s.ensureExistingIdentity(ctx, artifactKey, artifactMetadata); err != nil {
		return fmt.Errorf("portable receipt requires final artifact: %w", err)
	}
	return s.createReceiptOnly(ctx, localPath, receiptKey, receiptMetadata)
}

// Resolve 返回有界只读 artifact 流及其已验证身份。
func (s *PortableArtifactStore) Resolve(ctx context.Context, kind, key string) (io.ReadCloser, int64, PortableArtifactMetadata, error) {
	if err := s.validateContext(ctx); err != nil {
		return nil, 0, PortableArtifactMetadata{}, err
	}
	if kind != PortableArtifactKind || !s.isFinalBundleKey(key) {
		return nil, 0, PortableArtifactMetadata{}, errors.New("portable resolver accepts only final artifacts")
	}
	return s.getBounded(ctx, key)
}

// getBounded 从注入端口取得受有界大小约束的对象流。
func (s *PortableArtifactStore) getBounded(ctx context.Context, key string) (io.ReadCloser, int64, PortableArtifactMetadata, error) {
	stat, err := s.readBoundedStat(ctx, key)
	if err != nil {
		return nil, 0, PortableArtifactMetadata{}, err
	}
	body, size, metadata, err := s.client.Get(ctx, key, portableObjectReadLimit)
	if err != nil {
		return nil, 0, PortableArtifactMetadata{}, fmt.Errorf("get portable artifact: %w", closePortableBody(body, err))
	}
	if body == nil {
		return nil, 0, PortableArtifactMetadata{}, errors.New("portable artifact get returned a nil body")
	}
	if err := validatePortableMetadata(metadata); err != nil {
		return nil, 0, PortableArtifactMetadata{}, closePortableBody(body, err)
	}
	if err := validateBoundedGetIdentity(size, stat, metadata); err != nil {
		return nil, 0, PortableArtifactMetadata{}, closePortableBody(body, err)
	}
	return newBoundedPortableReadCloser(body, stat), stat.SizeBytes, metadata, nil
}

func (s *PortableArtifactStore) readBoundedStat(ctx context.Context, key string) (PortableArtifactMetadata, error) {
	stat, err := s.client.Stat(ctx, key)
	if err != nil {
		return PortableArtifactMetadata{}, fmt.Errorf("stat portable artifact: %w", err)
	}
	if err := validatePortableMetadata(stat); err != nil {
		return PortableArtifactMetadata{}, fmt.Errorf("validate portable artifact stat: %w", err)
	}
	wantDigest, ok := portableKeyDigest(key)
	if !ok || !samePortableDigest(wantDigest, stat.SHA256) {
		return PortableArtifactMetadata{}, ErrPortableArtifactConflict
	}
	return stat, nil
}

// validateBoundedGetIdentity 校验 Get 返回的长度和元数据与 Stat 身份一致。
func validateBoundedGetIdentity(size int64, stat, metadata PortableArtifactMetadata) error {
	if size == stat.SizeBytes && metadata.SizeBytes == size && portableMetadataMatches(stat, metadata) {
		return nil
	}
	if size <= 0 || size > portableObjectReadLimit {
		return errors.New("portable artifact size is outside the bounded read limit")
	}
	return ErrPortableArtifactConflict
}

// Delete 只删除 input staging generation，最终对象不可删除。
func (s *PortableArtifactStore) Delete(ctx context.Context, key string) error {
	if !s.isInputStagingKey(key) {
		return ErrPortableArtifactImmutable
	}
	prefix := strings.TrimSuffix(key, path.Base(key))
	return s.client.DeletePrefix(ctx, prefix)
}

// ConfirmAbsent 只证明 input staging 对象已不存在。
func (s *PortableArtifactStore) ConfirmAbsent(ctx context.Context, key string) (bool, error) {
	if !s.isInputStagingKey(key) {
		return false, ErrPortableArtifactImmutable
	}
	prefix := strings.TrimSuffix(key, path.Base(key))
	return s.client.ConfirmPrefixEmpty(ctx, prefix)
}

// createReceiptOnly 保持 generic create-only 入口不能绕过回执顺序。
func (s *PortableArtifactStore) createReceiptOnly(ctx context.Context, localPath, key string, metadata PortableArtifactMetadata) error {
	if !s.isReceiptKey(key) {
		return errors.New("portable receipt key is invalid")
	}
	if err := validatePortableLocalMetadata(localPath, metadata); err != nil {
		return err
	}
	if err := s.client.Create(ctx, localPath, key); err == nil {
		if err := s.ensureExistingIdentity(ctx, key, metadata); err != nil {
			return fmt.Errorf("verify portable receipt after create: %w", err)
		}
		return nil
	} else if same, compareErr := s.existingIdentity(ctx, key, metadata); compareErr == nil && same {
		return nil
	} else if compareErr == nil {
		return fmt.Errorf("%w for %q", ErrPortableArtifactConflict, key)
	} else {
		return fmt.Errorf("create-only portable receipt %q: %w", key, err)
	}
}

func (s *PortableArtifactStore) ensureExistingIdentity(ctx context.Context, key string, want PortableArtifactMetadata) error {
	same, err := s.existingIdentity(ctx, key, want)
	if err != nil {
		return err
	}
	if !same {
		return ErrPortableArtifactConflict
	}
	return nil
}

func (s *PortableArtifactStore) existingIdentity(ctx context.Context, key string, want PortableArtifactMetadata) (bool, error) {
	stat, err := s.client.Stat(ctx, key)
	if err != nil {
		return false, err
	}
	if err := validatePortableMetadata(stat); err != nil {
		return false, err
	}
	if !portableMetadataMatches(stat, want) {
		return false, nil
	}
	return s.readExistingIdentity(ctx, key, stat, want)
}

func closePortableBody(body io.ReadCloser, err error) error {
	if body == nil {
		return err
	}
	return errors.Join(err, body.Close())
}

func (s *PortableArtifactStore) readExistingIdentity(ctx context.Context, key string, stat, want PortableArtifactMetadata) (bool, error) {
	body, size, got, err := s.client.Get(ctx, key, portableObjectReadLimit)
	if err != nil {
		return false, closePortableBody(body, err)
	}
	if body == nil {
		return false, errors.New("portable object get returned a nil body")
	}
	if err := validatePortableMetadata(got); err != nil {
		return false, errors.Join(err, body.Close())
	}
	actual, readErr := readPortableBodyIdentity(body)
	if readErr != nil {
		return false, readErr
	}
	return portableReadbackMatches(size, stat, got, actual, want), nil
}

func portableMetadataMatches(left, right PortableArtifactMetadata) bool {
	return left.SizeBytes == right.SizeBytes && samePortableDigest(left.SHA256, right.SHA256)
}

func portableReadbackMatches(size int64, stat, got, actual, want PortableArtifactMetadata) bool {
	return size == actual.SizeBytes && portableMetadataMatches(got, actual) &&
		portableMetadataMatches(stat, actual) && portableMetadataMatches(actual, want)
}

func (s *PortableArtifactStore) validateMutableKey(key string) error {
	if s.isInputStagingKey(key) || s.isFinalBundleKey(key) {
		return nil
	}
	return errors.New("portable create-only key is not mutable input or final artifact")
}

func (s *PortableArtifactStore) isInputStagingKey(key string) bool {
	relative, ok := strings.CutPrefix(key, s.prefix+"input-staging/")
	if !ok || !validPortableObjectKey(key) {
		return false
	}
	parts := strings.Split(relative, "/")
	return validPortableInputParts(parts)
}

func validPortableInputParts(parts []string) bool {
	if len(parts) != 2 || !strings.HasSuffix(parts[1], ".input") {
		return false
	}
	return validatePortableSegment(parts[0], "staging id") == nil && validPortableHexDigest(strings.TrimSuffix(parts[1], ".input"))
}

func (s *PortableArtifactStore) isFinalBundleKey(key string) bool {
	relative, ok := strings.CutPrefix(key, s.prefix+"artifacts/")
	return ok && validPortableObjectKey(key) && validPortableHexDigest(strings.TrimSuffix(relative, ".bundle")) && strings.HasSuffix(relative, ".bundle")
}

func (s *PortableArtifactStore) isReceiptKey(key string) bool {
	relative, ok := strings.CutPrefix(key, s.prefix+"receipts/")
	return ok && validPortableObjectKey(key) && validPortableHexDigest(strings.TrimSuffix(relative, ".json")) && strings.HasSuffix(relative, ".json")
}

func (s *PortableArtifactStore) validateKeyIdentity(key, digest string) error {
	keyDigest, ok := portableKeyDigest(key)
	if !ok || !samePortableDigest(keyDigest, digest) {
		return errors.New("portable object key is not content addressed by its metadata")
	}
	return nil
}

func (s *PortableArtifactStore) validateContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("portable artifact context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

type existingPortableArtifactClient struct{ store PortableArtifactObjectStore }

// Create 将现有 client 的 create-only 能力透传到注入端口。
func (c existingPortableArtifactClient) Create(ctx context.Context, localPath, key string) error {
	return c.store.Create(ctx, localPath, key)
}

// Stat 用现有 client 的 bounded Read 计算对象身份。
func (c existingPortableArtifactClient) Stat(ctx context.Context, key string) (PortableArtifactMetadata, error) {
	payload, err := c.store.Read(ctx, key, portableObjectReadLimit)
	if err != nil {
		return PortableArtifactMetadata{}, err
	}
	return portableBytesMetadata(payload), nil
}

// Get 用现有 client 的 bounded Read 提供只读对象流和身份。
func (c existingPortableArtifactClient) Get(ctx context.Context, key string, maxBytes int64) (io.ReadCloser, int64, PortableArtifactMetadata, error) {
	payload, err := c.store.Read(ctx, key, maxBytes)
	if err != nil {
		return nil, 0, PortableArtifactMetadata{}, err
	}
	metadata := portableBytesMetadata(payload)
	return io.NopCloser(bytes.NewReader(payload)), metadata.SizeBytes, metadata, nil
}

// DeletePrefix 将输入清理操作透传到现有 client。
func (c existingPortableArtifactClient) DeletePrefix(ctx context.Context, prefix string) error {
	return c.store.DeletePrefix(ctx, prefix)
}

// ConfirmPrefixEmpty 将输入缺失确认透传到现有 client。
func (c existingPortableArtifactClient) ConfirmPrefixEmpty(ctx context.Context, prefix string) (bool, error) {
	return c.store.ConfirmPrefixEmpty(ctx, prefix)
}

type boundedPortableReadCloser struct {
	body       io.ReadCloser
	expected   PortableArtifactMetadata
	hash       hash.Hash
	remaining  int64
	consumed   int64
	readErr    error
	noProgress int
	verified   bool
	closed     bool
	closeErr   error
}

func newBoundedPortableReadCloser(body io.ReadCloser, expected PortableArtifactMetadata) *boundedPortableReadCloser {
	return &boundedPortableReadCloser{body: body, expected: expected, hash: sha256.New(), remaining: expected.SizeBytes}
}

// Read 在消费时校验对象的精确长度和内容摘要。
func (r *boundedPortableReadCloser) Read(p []byte) (int, error) {
	if n, err, done := r.readState(); done {
		return n, err
	}
	if len(p) == 0 {
		return 0, nil
	}
	if r.remaining == 0 {
		return r.readBeyondExpected()
	}
	return r.readExpected(p)
}

func (r *boundedPortableReadCloser) readState() (int, error, bool) {
	if r.closed {
		return 0, errors.New("portable artifact reader is closed"), true
	}
	if r.readErr != nil {
		return 0, r.readErr, true
	}
	if r.verified {
		return 0, io.EOF, true
	}
	return 0, nil, false
}

func (r *boundedPortableReadCloser) readExpected(p []byte) (int, error) {
	if int64(len(p)) > r.remaining {
		p = p[:int(r.remaining)]
	}
	n, err := r.body.Read(p)
	if recordErr := r.recordRead(p, n, err); recordErr != nil {
		return n, r.fail(recordErr)
	}
	if err == nil {
		return n, nil
	}
	return n, r.finishExpectedRead(err)
}

// recordRead 记录正文摘要并拒绝非法读取结果或无进展读取。
func (r *boundedPortableReadCloser) recordRead(p []byte, n int, err error) error {
	if n < 0 || n > len(p) {
		return fmt.Errorf("portable artifact reader returned invalid byte count %d", n)
	}
	if n > 0 {
		if writeErr := r.record(p[:n]); writeErr != nil {
			return writeErr
		}
		r.noProgress = 0
	} else if err == nil {
		return r.noteNoProgress()
	}
	return nil
}

func (r *boundedPortableReadCloser) noteNoProgress() error {
	r.noProgress++
	if r.noProgress >= portableReadNoProgressLimit {
		return errors.Join(ErrPortableArtifactBodyIncomplete, io.ErrNoProgress)
	}
	return nil
}

func (r *boundedPortableReadCloser) finishExpectedRead(err error) error {
	if err == io.EOF {
		if r.remaining != 0 {
			return r.fail(errors.Join(ErrPortableArtifactBodyIncomplete, io.ErrUnexpectedEOF))
		}
		if verifyErr := r.verifyDigest(); verifyErr != nil {
			return r.fail(verifyErr)
		}
		r.verified = true
		return io.EOF
	}
	return r.fail(errors.Join(ErrPortableArtifactBodyIncomplete, err))
}

func (r *boundedPortableReadCloser) fail(err error) error {
	if r.readErr == nil {
		r.readErr = err
	}
	return r.readErr
}

func (r *boundedPortableReadCloser) record(p []byte) error {
	written, err := r.hash.Write(p)
	if err != nil {
		return err
	}
	if written != len(p) {
		return io.ErrShortWrite
	}
	r.remaining -= int64(len(p))
	r.consumed += int64(len(p))
	return nil
}

// readBeyondExpected 探测声明长度后的额外字节并完成摘要验证。
func (r *boundedPortableReadCloser) readBeyondExpected() (int, error) {
	var extra [1]byte
	n, err := r.body.Read(extra[:])
	if n < 0 || n > len(extra) {
		return 0, r.fail(fmt.Errorf("portable artifact reader returned invalid byte count %d", n))
	}
	if n > 0 {
		return 0, r.fail(errors.Join(ErrPortableArtifactConflict, ErrPortableArtifactBodyTooLong))
	}
	if err == nil {
		r.noProgress++
		if r.noProgress >= portableReadNoProgressLimit {
			return 0, r.fail(errors.Join(ErrPortableArtifactBodyIncomplete, io.ErrNoProgress))
		}
		return 0, nil
	}
	r.noProgress = 0
	if err == io.EOF {
		if verifyErr := r.verifyDigest(); verifyErr != nil {
			return 0, r.fail(verifyErr)
		}
		r.verified = true
		return 0, io.EOF
	}
	return 0, r.fail(errors.Join(ErrPortableArtifactBodyIncomplete, err))
}

func (r *boundedPortableReadCloser) verifyDigest() error {
	actual := PortableArtifactMetadata{SHA256: "sha256:" + hex.EncodeToString(r.hash.Sum(nil)), SizeBytes: r.consumed}
	if actual.SizeBytes != r.expected.SizeBytes {
		return errors.Join(ErrPortableArtifactBodyIncomplete, io.ErrUnexpectedEOF)
	}
	if !portableMetadataMatches(actual, r.expected) {
		return errors.Join(ErrPortableArtifactConflict, ErrPortableArtifactBodyMismatch)
	}
	return nil
}

// Close 在调用方少读时有界排空并合并底层 Close 错误。
func (r *boundedPortableReadCloser) Close() error {
	if r.closed {
		return r.closeErr
	}
	var verifyErr error
	if r.readErr != nil {
		verifyErr = r.readErr
	} else if !r.verified {
		verifyErr = r.drain()
	}
	r.closed = true
	r.closeErr = errors.Join(verifyErr, r.body.Close())
	return r.closeErr
}

// drain 在关闭前有界排空未消费内容，确保流完整性已经验证。
func (r *boundedPortableReadCloser) drain() error {
	buffer := make([]byte, 32*1024)
	for !r.verified && r.readErr == nil {
		_, err := r.Read(buffer)
		if err == nil {
			continue
		}
		if err == io.EOF && r.verified {
			return nil
		}
		return err
	}
	return r.readErr
}

const portableReadNoProgressLimit = 100

func canonicalPortableFinalKey(prefix, digest, suffix string) (string, error) {
	if err := validatePortableStorePrefix(prefix); err != nil {
		return "", err
	}
	canonical, err := canonicalPortableDigest(digest)
	if err != nil {
		return "", err
	}
	return prefix + "artifacts/" + strings.TrimPrefix(canonical, "sha256:") + suffix, nil
}

func validatePortableStorePrefix(prefix string) error {
	if err := validatePrefix(prefix); err != nil {
		return fmt.Errorf("portable OSS prefix: %w", err)
	}
	if strings.ContainsAny(prefix, "?#\x00\r\n") {
		return errors.New("portable OSS prefix contains forbidden characters")
	}
	return nil
}

func validatePortableSegment(value, label string) error {
	if invalidPortableSegmentShape(value) {
		return fmt.Errorf("portable %s is not a clean relative identity", label)
	}
	if strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return fmt.Errorf("portable %s contains control characters", label)
	}
	return nil
}

// invalidPortableSegmentShape 判断 segment 是否包含路径逃逸或控制字符。
func invalidPortableSegmentShape(value string) bool {
	return value == "" || value == "." || value == ".." || path.IsAbs(value) ||
		path.Clean(value) != value || strings.ContainsAny(value, "/\\?#\x00\r\n")
}

func canonicalPortableDigest(value string) (string, error) {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+sha256.Size*2 {
		return "", errors.New("portable digest must use canonical sha256 form")
	}
	hexValue := strings.TrimPrefix(value, "sha256:")
	if _, err := hex.DecodeString(hexValue); err != nil || strings.ToLower(hexValue) != hexValue {
		return "", errors.New("portable digest is not canonical lowercase SHA-256")
	}
	return value, nil
}

func validPortableHexDigest(value string) bool {
	return len(value) == sha256.Size*2 && func() bool {
		_, err := hex.DecodeString(value)
		return err == nil && strings.ToLower(value) == value
	}()
}

func portableKeyDigest(key string) (string, bool) {
	for _, suffix := range []string{".input", ".bundle", ".json"} {
		if digest, ok := portableKeyDigestWithSuffix(key, suffix); ok {
			return digest, true
		}
	}
	return "", false
}

func portableKeyDigestWithSuffix(key, suffix string) (string, bool) {
	if !strings.HasSuffix(key, suffix) {
		return "", false
	}
	value := strings.TrimSuffix(path.Base(key), suffix)
	if !validPortableHexDigest(value) {
		return "", false
	}
	return "sha256:" + value, true
}

func samePortableKeyDigest(left, right string) bool {
	leftDigest, leftOK := portableKeyDigest(left)
	rightDigest, rightOK := portableKeyDigest(right)
	return leftOK && rightOK && samePortableDigest(leftDigest, rightDigest)
}

func validatePortableMetadata(metadata PortableArtifactMetadata) error {
	if _, err := canonicalPortableDigest(metadata.SHA256); err != nil {
		return err
	}
	if metadata.SizeBytes <= 0 || metadata.SizeBytes > portableObjectReadLimit {
		return errors.New("portable object size is outside the bounded positive range")
	}
	return nil
}

func validatePortableLocalMetadata(localPath string, metadata PortableArtifactMetadata) error {
	if strings.TrimSpace(localPath) == "" {
		return errors.New("portable local object path is required")
	}
	if err := validatePortableMetadata(metadata); err != nil {
		return err
	}
	return validatePortableLocalIdentity(localPath, metadata)
}

func validatePortableLocalIdentity(localPath string, metadata PortableArtifactMetadata) error {
	actualDigest, actualSize, err := localFileDigestAndSize(localPath)
	if err != nil {
		return fmt.Errorf("inspect portable local object: %w", err)
	}
	if actualSize != metadata.SizeBytes || !samePortableDigest(actualDigest, metadata.SHA256) {
		return ErrPortableArtifactConflict
	}
	return nil
}

func portableBytesMetadata(payload []byte) PortableArtifactMetadata {
	sum := sha256.Sum256(payload)
	return PortableArtifactMetadata{SHA256: "sha256:" + hex.EncodeToString(sum[:]), SizeBytes: int64(len(payload))}
}

func readPortableBodyIdentity(body io.ReadCloser) (PortableArtifactMetadata, error) {
	hash := sha256.New()
	count, readErr := io.Copy(hash, io.LimitReader(body, portableObjectReadLimit+1))
	closeErr := body.Close()
	if readErr != nil || closeErr != nil {
		return PortableArtifactMetadata{}, errors.Join(readErr, closeErr)
	}
	if count <= 0 || count > portableObjectReadLimit {
		return PortableArtifactMetadata{}, errors.New("portable object read-back exceeded its bound")
	}
	return PortableArtifactMetadata{SHA256: "sha256:" + hex.EncodeToString(hash.Sum(nil)), SizeBytes: count}, nil
}

func samePortableDigest(left, right string) bool {
	left = strings.TrimPrefix(left, "sha256:")
	right = strings.TrimPrefix(right, "sha256:")
	return left == right
}

func validPortableObjectKey(key string) bool {
	return key != "" && !strings.HasPrefix(key, "/") && !strings.ContainsAny(key, "\\?#\x00\r\n") && path.Clean(key) == key
}

func isNilPortableValue(value any) bool {
	if value == nil {
		return true
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	default:
		return false
	}
}
