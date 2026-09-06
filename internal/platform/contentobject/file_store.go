package contentobject

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

const envelopeFormatVersion int64 = 1
const maxEnvelopeFileBytes int64 = 128 * 1024

type fileEnvelope struct {
	FormatVersion int64      `json:"format_version"`
	Receipt       PutReceipt `json:"receipt"`
	Ciphertext    []byte     `json:"ciphertext"`
}

// FileStore 在显式private state-dir下原子发布密文封装；不暴露读取密文字节的API。
type FileStore struct {
	root    string
	storeID string
	mu      sync.Mutex
	now     func() time.Time
	ops     filePublishOps
}

type filePublishOps struct {
	link          func(string, string) error
	remove        func(string) error
	syncDirectory func(string) error
}

type filePublishOutcome uint8

const (
	filePublishNotPublished filePublishOutcome = iota
	filePublishNoReplaceConflict
	filePublishOutcomeUnknown
	filePublishCommitted
)

// NewFileStore 创建owner-only private object backend；workspace重叠与symlink/reparse root立即拒绝。
func NewFileStore(config Config) (*FileStore, error) {
	cleanRoot, err := preparePrivateRoot(config.StateDir, config.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	if !validObjectText(config.StoreID, 128) {
		return nil, ErrInvalidObjectRequest
	}
	return &FileStore{
		root:    cleanRoot,
		storeID: config.StoreID,
		now:     func() time.Time { return time.Now().UTC() },
		ops:     defaultFilePublishOps(),
	}, nil
}

// Validate 确认private object root与store identity已构造。
func (s *FileStore) Validate() error {
	if s == nil || s.root == "" || s.storeID == "" || s.now == nil || !s.ops.valid() {
		return ErrObjectStoreMissing
	}
	return nil
}

// Put 原子发布或精确重放同一object/version；同位置异digest拒绝覆盖。
func (s *FileStore) Put(ctx context.Context, request PutRequest) (PutReceipt, error) {
	if err := s.ready(ctx); err != nil {
		return PutReceipt{}, err
	}
	if err := request.validate(); err != nil {
		return PutReceipt{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path, objectRef, err := s.objectPath(request.ObjectID, request.ObjectVersion, true)
	if err != nil {
		return PutReceipt{}, err
	}
	if receipt, found, err := s.replayExisting(path, objectRef, request); found || err != nil {
		return receipt, err
	}
	receipt, envelope, err := s.newEnvelope(request, objectRef)
	if err != nil {
		return PutReceipt{}, err
	}
	outcome, err := publishEnvelope(path, envelope, s.ops)
	if err != nil {
		if outcome != filePublishNoReplaceConflict {
			return PutReceipt{}, err
		}
		return s.reconcilePublication(path, objectRef, request, err)
	}
	if outcome != filePublishCommitted {
		return PutReceipt{}, ErrObjectCommitOutcomeUnknown
	}
	return receipt, nil
}

// reconcilePublication 仅在no-replace竞争后读取既有封装并验证exact replay。
func (s *FileStore) reconcilePublication(
	path string,
	objectRef string,
	request PutRequest,
	publishErr error,
) (PutReceipt, error) {
	if replay, found, err := s.replayExisting(path, objectRef, request); found || err != nil {
		return replay, err
	}
	return PutReceipt{}, publishErr
}

// replayExisting 复验exact envelope并重试父目录durability barrier后才返回receipt。
func (s *FileStore) replayExisting(path, objectRef string, request PutRequest) (PutReceipt, bool, error) {
	receipt, found, err := s.readExisting(path, objectRef, request)
	if !found || err != nil {
		return receipt, found, err
	}
	if err := s.syncPublishedDirectory(filepath.Dir(path)); err != nil {
		return PutReceipt{}, true, err
	}
	return receipt, true, nil
}

// syncPublishedDirectory 在exact-existing receipt升级为成功前确认目录项已持久化。
func (s *FileStore) syncPublishedDirectory(dir string) error {
	if err := s.ops.syncDirectory(dir); err != nil {
		return syncPublishedDirectoryError(dir, err)
	}
	return nil
}

// QueryPut 只查询当前可读且匹配的本地封装receipt；不创建文件，也不声明durable Put成功。
func (s *FileStore) QueryPut(ctx context.Context, request QueryRequest) (PutReceipt, error) {
	if err := s.ready(ctx); err != nil {
		return PutReceipt{}, err
	}
	if err := request.validate(); err != nil {
		return PutReceipt{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path, objectRef, err := s.objectPath(request.ObjectID, request.ObjectVersion, false)
	if errors.Is(err, fs.ErrNotExist) {
		return PutReceipt{}, ErrObjectNotFound
	}
	if err != nil {
		return PutReceipt{}, err
	}
	envelope, err := readEnvelope(path)
	if errors.Is(err, fs.ErrNotExist) {
		return PutReceipt{}, ErrObjectNotFound
	}
	if err != nil {
		return PutReceipt{}, err
	}
	if envelope.Receipt.StoreID != s.storeID || envelope.Receipt.ObjectRef != objectRef || !queryMatchesReceipt(request, envelope.Receipt) {
		return PutReceipt{}, ErrObjectConflict
	}
	return envelope.Receipt, nil
}

// ready 校验store构造完整并传播调用取消。
func (s *FileStore) ready(ctx context.Context) error {
	if s == nil || s.root == "" || s.storeID == "" || s.now == nil || !s.ops.valid() {
		return ErrObjectStoreMissing
	}
	return ctx.Err()
}

func (s *FileStore) newEnvelope(request PutRequest, objectRef string) (PutReceipt, fileEnvelope, error) {
	storeSum := sha256.Sum256([]byte(s.storeID))
	operationSum := sha256.Sum256([]byte(request.OperationID))
	receiptRef := "content-object-receipt://v1/" + hex.EncodeToString(storeSum[:]) + "/" + hex.EncodeToString(operationSum[:])
	receipt := PutReceipt{
		SchemaVersion: 1, StoreID: s.storeID, OperationID: request.OperationID,
		RequestDigest: request.RequestDigest, ObjectID: request.ObjectID, ObjectVersion: request.ObjectVersion,
		ObjectRef: objectRef, ReceiptRef: receiptRef, CiphertextHash: request.CiphertextHash, CiphertextSize: request.CiphertextSize,
		RecordedAt: s.now().UTC(),
	}
	digest, err := computeReceiptDigest(receipt)
	if err != nil {
		return PutReceipt{}, fileEnvelope{}, err
	}
	receipt.ReceiptDigest = digest
	if err := receipt.validate(); err != nil {
		return PutReceipt{}, fileEnvelope{}, err
	}
	envelope := fileEnvelope{FormatVersion: envelopeFormatVersion, Receipt: receipt, Ciphertext: append([]byte(nil), request.Ciphertext...)}
	return receipt, envelope, nil
}

// readExisting 返回exact replay、冲突或not-found，不覆盖既有object。
func (s *FileStore) readExisting(path, objectRef string, request PutRequest) (PutReceipt, bool, error) {
	envelope, err := readEnvelope(path)
	if errors.Is(err, fs.ErrNotExist) {
		return PutReceipt{}, false, nil
	}
	if err != nil {
		return PutReceipt{}, true, err
	}
	if envelope.Receipt.StoreID != s.storeID || envelope.Receipt.ObjectRef != objectRef || !putMatchesEnvelope(request, envelope) {
		return PutReceipt{}, true, ErrObjectConflict
	}
	return envelope.Receipt, true, nil
}

func (s *FileStore) objectPath(objectID string, version int64, create bool) (string, string, error) {
	sum := sha256.Sum256([]byte(objectID))
	hash := hex.EncodeToString(sum[:])
	storeSum := sha256.Sum256([]byte(s.storeID))
	storeHash := hex.EncodeToString(storeSum[:])
	dir := filepath.Join(s.root, "objects", "v1", hash[:2])
	if !pathContains(s.root, dir) {
		return "", "", ErrUnsafeStateDir
	}
	if err := ensurePrivateDirectory(dir, create); err != nil {
		return "", "", err
	}
	filename := hash + "-v" + strconv.FormatInt(version, 10) + ".sealed"
	objectRef := "content-object://v1/" + storeHash + "/" + hash + "/" + strconv.FormatInt(version, 10)
	return filepath.Join(dir, filename), objectRef, nil
}

// preparePrivateRoot 创建固定content-objects/v1目录并拒绝与workspace重叠。
func preparePrivateRoot(stateDir, workspaceRoot string) (string, error) {
	if !canonicalAbsolutePath(stateDir) || !canonicalAbsolutePath(workspaceRoot) {
		return "", ErrInvalidObjectRequest
	}
	cleanState := filepath.Clean(stateDir)
	cleanWorkspace := filepath.Clean(workspaceRoot)
	if err := validateCanonicalPathPrefix(cleanState); err != nil {
		return "", err
	}
	if err := validateCanonicalPathPrefix(cleanWorkspace); err != nil {
		return "", err
	}
	if pathsOverlap(cleanState, cleanWorkspace) {
		return "", ErrUnsafeStateDir
	}
	clean := filepath.Join(cleanState, "content-objects", "v1")
	if err := ensurePrivateDirectory(clean, true); err != nil {
		return "", err
	}
	return clean, nil
}

func canonicalAbsolutePath(path string) bool {
	return path != "" && strings.TrimSpace(path) == path && filepath.IsAbs(path)
}

func pathsOverlap(first, second string) bool {
	return pathContains(first, second) || pathContains(second, first)
}

func pathContains(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

// ensurePrivateDirectory 创建/校验owner-only真实目录，拒绝symlink。
func ensurePrivateDirectory(path string, create bool) error {
	if err := validateCanonicalPathPrefix(path); err != nil {
		return err
	}
	if create {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return fmt.Errorf("create private content directory %s: %s", securefs.RedactPath(path), safeContentFileError(err, path))
		}
	}
	return validatePrivateDirectory(path)
}

// validatePrivateDirectory 校验叶目录真实、规范且为owner-only。
func validatePrivateDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return fs.ErrNotExist
	}
	if err != nil {
		return fmt.Errorf("inspect private content directory %s: %s", securefs.RedactPath(path), safeContentFileError(err, path))
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: content object path is not a private directory: %s", ErrUnsafeStateDir, securefs.RedactPath(path))
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil || filepath.Clean(real) != filepath.Clean(path) {
		return fmt.Errorf("%w: content object path is not canonical: %s", ErrUnsafeStateDir, securefs.RedactPath(path))
	}
	if err := securefs.RestrictPrivateOwnerOnly(path, 0o700); err != nil {
		return fmt.Errorf("restrict private content directory %s: %s", securefs.RedactPath(path), safeContentFileError(err, path))
	}
	return nil
}

// validateCanonicalPathPrefix 在创建任何目录前拒绝既有symlink或非规范父路径。
func validateCanonicalPathPrefix(path string) error {
	current := filepath.Clean(path)
	for {
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("%w: content object path has a symlink ancestor: %s", ErrUnsafeStateDir, securefs.RedactPath(current))
			}
			real, evalErr := filepath.EvalSymlinks(current)
			if evalErr != nil || filepath.Clean(real) != current {
				return fmt.Errorf("%w: content object path has a non-canonical ancestor: %s", ErrUnsafeStateDir, securefs.RedactPath(current))
			}
			return nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("inspect content object path prefix %s: %s", securefs.RedactPath(current), safeContentFileError(err, current))
		}
		parent := filepath.Dir(current)
		if parent == current {
			return fmt.Errorf("%w: content object path has no existing canonical ancestor", ErrUnsafeStateDir)
		}
		current = parent
	}
}

// publishEnvelope 以同目录private temp、fsync与no-replace hard-link原子发布。
func publishEnvelope(path string, envelope fileEnvelope, ops filePublishOps) (filePublishOutcome, error) {
	data, err := json.Marshal(envelope)
	if err != nil {
		return filePublishNotPublished, fmt.Errorf("encode sealed content envelope: %w", err)
	}
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".content-sealed-*")
	if err != nil {
		return filePublishNotPublished, fmt.Errorf("create sealed content temp in %s: %s", securefs.RedactPath(dir), safeContentFileError(err, dir))
	}
	tempPath := temp.Name()
	if err := writeAndSyncEnvelope(temp, data); err != nil {
		return cleanupUnpublishedTemp(tempPath, dir, filePublishNotPublished, err, ops.remove)
	}
	if err := ops.link(tempPath, path); err != nil {
		outcome := filePublishNotPublished
		if errors.Is(err, fs.ErrExist) {
			outcome = filePublishNoReplaceConflict
		}
		publishErr := fmt.Errorf("publish sealed content object %s: %s", securefs.RedactPath(path), safeContentFileError(err, tempPath, path))
		return cleanupUnpublishedTemp(tempPath, dir, outcome, publishErr, ops.remove)
	}
	if err := ops.remove(tempPath); err != nil {
		return filePublishOutcomeUnknown, objectCommitOutcomeUnknown(
			fmt.Errorf("remove sealed content temp in %s: %s", securefs.RedactPath(dir), safeContentFileError(err, tempPath, dir)),
		)
	}
	if err := ops.syncDirectory(dir); err != nil {
		return filePublishOutcomeUnknown, syncPublishedDirectoryError(dir, err)
	}
	return filePublishCommitted, nil
}

// cleanupUnpublishedTemp 清理尚未由本次调用成功link的临时文件；清理失败禁止竞争协调。
func cleanupUnpublishedTemp(
	tempPath string,
	dir string,
	outcome filePublishOutcome,
	cause error,
	remove func(string) error,
) (filePublishOutcome, error) {
	removeErr := remove(tempPath)
	if removeErr == nil || errors.Is(removeErr, fs.ErrNotExist) {
		return outcome, cause
	}
	cleanupErr := fmt.Errorf("cleanup sealed content temp in %s: %s", securefs.RedactPath(dir), safeContentFileError(removeErr, tempPath, dir))
	return filePublishNotPublished, errors.Join(cause, cleanupErr)
}

// objectCommitOutcomeUnknown 保留稳定结果未知分类并附带内部根因。
func objectCommitOutcomeUnknown(cause error) error {
	return errors.Join(ErrObjectCommitOutcomeUnknown, cause)
}

// syncPublishedDirectoryError 构造脱敏的durability barrier失败。
func syncPublishedDirectoryError(dir string, cause error) error {
	return objectCommitOutcomeUnknown(
		fmt.Errorf("sync sealed content directory %s: %s", securefs.RedactPath(dir), safeContentFileError(cause, dir)),
	)
}

// defaultFilePublishOps 返回生产文件发布原语。
func defaultFilePublishOps() filePublishOps {
	return filePublishOps{link: os.Link, remove: os.Remove, syncDirectory: securefs.SyncDirectory}
}

// valid 校验文件发布原语已完整注入。
func (ops filePublishOps) valid() bool {
	return ops.link != nil && ops.remove != nil && ops.syncDirectory != nil
}

// writeAndSyncEnvelope 写入owner-only临时封装并完成文件级fsync。
func writeAndSyncEnvelope(file *os.File, data []byte) error {
	if err := file.Chmod(0o600); err != nil {
		return closeObjectTemp(file, objectTempError("chmod", file.Name(), err))
	}
	if err := securefs.RestrictPrivateOwnerOnly(file.Name(), 0o600); err != nil {
		return closeObjectTemp(file, objectTempError("restrict", file.Name(), err))
	}
	if _, err := file.Write(data); err != nil {
		return closeObjectTemp(file, objectTempError("write", file.Name(), err))
	}
	if err := file.Sync(); err != nil {
		return closeObjectTemp(file, objectTempError("sync", file.Name(), err))
	}
	if err := file.Close(); err != nil {
		return objectTempError("close", file.Name(), err)
	}
	return nil
}

// objectTempError 脱敏临时文件路径与底层系统错误。
func objectTempError(operation, path string, cause error) error {
	dir := filepath.Dir(path)
	return fmt.Errorf("%s sealed content temp in %s: %s", operation, securefs.RedactPath(dir), safeContentFileError(cause, path, dir))
}

// safeContentFileError 脱敏PathError、LinkError及已知路径文本。
func safeContentFileError(cause error, paths ...string) string {
	message := securefs.SafeError(cause)
	for _, path := range paths {
		message = securefs.SafeErrorForPath(errors.New(message), path)
	}
	return message
}

// closeObjectTemp 关闭失败写入的临时文件并保留脱敏根因。
func closeObjectTemp(file *os.File, cause error) error {
	if closeErr := file.Close(); closeErr != nil {
		return errors.Join(cause, objectTempError("close", file.Name(), closeErr))
	}
	return cause
}

// readEnvelope 读取并完整校验单文件密文封装、receipt和payload hash。
func readEnvelope(path string) (fileEnvelope, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return fileEnvelope{}, fs.ErrNotExist
	}
	if err != nil {
		return fileEnvelope{}, fmt.Errorf("inspect sealed content envelope %s: %s", securefs.RedactPath(path), safeContentFileError(err, path))
	}
	if err := validateEnvelopeFile(path, info); err != nil {
		return fileEnvelope{}, ErrObjectCorrupt
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fileEnvelope{}, fmt.Errorf("read sealed content envelope %s: %s", securefs.RedactPath(path), safeContentFileError(err, path))
	}
	var envelope fileEnvelope
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return fileEnvelope{}, ErrObjectCorrupt
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fileEnvelope{}, ErrObjectCorrupt
	}
	if err := validateEnvelope(envelope); err != nil {
		return fileEnvelope{}, ErrObjectCorrupt
	}
	return envelope, nil
}

func validateEnvelopeFile(path string, info os.FileInfo) error {
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return ErrObjectCorrupt
	}
	if info.Size() <= 0 || info.Size() > maxEnvelopeFileBytes {
		return ErrObjectCorrupt
	}
	return securefs.CheckPrivateOwnerOnly(path, info)
}

func validateEnvelope(envelope fileEnvelope) error {
	if envelope.FormatVersion != envelopeFormatVersion || envelope.Receipt.CiphertextSize != int64(len(envelope.Ciphertext)) {
		return ErrObjectCorrupt
	}
	if envelope.Receipt.CiphertextHash != hashCiphertext(envelope.Ciphertext) {
		return ErrObjectCorrupt
	}
	return envelope.Receipt.validate()
}

func putMatchesEnvelope(request PutRequest, envelope fileEnvelope) bool {
	return queryMatchesReceipt(QueryRequest{SchemaVersion: 1, OperationID: request.OperationID,
		RequestDigest: request.RequestDigest, ObjectID: request.ObjectID, ObjectVersion: request.ObjectVersion}, envelope.Receipt) &&
		request.CiphertextHash == envelope.Receipt.CiphertextHash && request.CiphertextSize == envelope.Receipt.CiphertextSize
}

func queryMatchesReceipt(request QueryRequest, receipt PutReceipt) bool {
	return request.OperationID == receipt.OperationID && request.RequestDigest == receipt.RequestDigest &&
		request.ObjectID == receipt.ObjectID && request.ObjectVersion == receipt.ObjectVersion
}

var _ Store = (*FileStore)(nil)
