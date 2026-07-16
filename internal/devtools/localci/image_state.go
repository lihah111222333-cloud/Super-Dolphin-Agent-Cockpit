package localci

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const (
	acceptedImageStateName = "accepted-image.json"
	acceptedImageLockName  = "accepted-image.lock"
	acceptedImageMaxBytes  = 1 << 20
	acceptedImageLockRetry = 10 * time.Millisecond
)

var (
	ErrAcceptedImageStateNotFound = errors.New("accepted image state does not exist")
	ErrAcceptedImageStateExists   = errors.New("accepted image state already exists")
	ErrAcceptedImageCASConflict   = errors.New("accepted image CAS conflict")
	ErrAcceptedImageRollback      = errors.New("accepted image trusted commit would roll back")
)

// AcceptedImageSignatureVerifier 只验证 authority 签名，不提供签名能力。
type AcceptedImageSignatureVerifier interface {
	VerifyAcceptedImage(context.Context, gate.SignerIdentity, []byte, string) error
}

// RefAncestryVerifier 判断 trusted ref 上的 commit 是否保持祖先关系。
type RefAncestryVerifier interface {
	IsAncestor(context.Context, string, string, string, string) (bool, error)
}

// AcceptedImageState 持有 owner-private canonical 文件 authority。
type AcceptedImageState struct {
	root      string
	statePath string
	lockPath  string
	ownerUID  int
	verifier  AcceptedImageSignatureVerifier
	ancestry  RefAncestryVerifier
}

// NewAcceptedImageState 构造不持有私钥的 accepted image authority。
func NewAcceptedImageState(
	root string,
	verifier AcceptedImageSignatureVerifier,
	ancestry RefAncestryVerifier,
) (*AcceptedImageState, error) {
	if interfaceValueIsNil(verifier) {
		return nil, errors.New("accepted image signature verifier is required")
	}
	if interfaceValueIsNil(ancestry) {
		return nil, errors.New("accepted image ref ancestry verifier is required")
	}
	ownerUID, err := currentSchedulerOwnerUID()
	if err != nil {
		return nil, fmt.Errorf("resolve accepted image owner: %w", err)
	}
	state := &AcceptedImageState{
		root:      root,
		statePath: filepath.Join(root, acceptedImageStateName),
		lockPath:  filepath.Join(root, acceptedImageLockName),
		ownerUID:  ownerUID,
		verifier:  verifier,
		ancestry:  ancestry,
	}
	if err := state.validateRoot(); err != nil {
		return nil, err
	}
	return state, nil
}

// Load 在返回 authority 记录前严格读取并验签。
func (s *AcceptedImageState) Load(ctx context.Context) (record gate.AcceptedImageRecord, retErr error) {
	if err := s.validateCall(ctx); err != nil {
		return record, err
	}
	lock, err := s.acquireLock(ctx)
	if err != nil {
		return record, err
	}
	defer func() { retErr = errors.Join(retErr, lock.close()) }()
	return s.loadLocked(ctx)
}

// Bootstrap 只接受 generation 1 且没有前驱的已签初始记录。
func (s *AcceptedImageState) Bootstrap(ctx context.Context, record gate.AcceptedImageRecord) (retErr error) {
	if err := s.validateCall(ctx); err != nil {
		return err
	}
	if record.Generation != 1 || record.PreviousRecordDigest != "" {
		return errors.New("accepted image bootstrap requires generation 1 and an empty predecessor")
	}
	if err := s.verifyRecord(ctx, record); err != nil {
		return err
	}
	lock, err := s.acquireLock(ctx)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, lock.close()) }()
	exists, err := validateCurrentUIDPrivatePath(s.statePath, s.ownerUID)
	if err != nil {
		return fmt.Errorf("validate accepted image bootstrap target: %w", err)
	}
	if exists {
		return ErrAcceptedImageStateExists
	}
	return s.writeLocked(record, false)
}

// PromoteCAS 在锁内比较当前 digest/generation 并原子提升一代。
func (s *AcceptedImageState) PromoteCAS(ctx context.Context, promotion gate.PromotionRecord) (retErr error) {
	if err := s.validateCall(ctx); err != nil {
		return err
	}
	if err := promotion.Validate(); err != nil {
		return err
	}
	lock, err := s.acquireLock(ctx)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, lock.close()) }()
	current, err := s.loadLocked(ctx)
	if err != nil {
		return err
	}
	currentDigest, err := gate.AcceptedImageRecordDigest(current)
	if err != nil {
		return fmt.Errorf("digest current accepted image: %w", err)
	}
	if currentDigest != promotion.ExpectedRecordDigest || current.Generation != promotion.ExpectedGeneration {
		return ErrAcceptedImageCASConflict
	}
	if err := s.validatePromotion(ctx, current, currentDigest, promotion.Next); err != nil {
		return err
	}
	return s.writeLocked(promotion.Next, true)
}

// validatePromotion 校验下一代精确前驱、身份连续性、签名与 commit ancestry。
func (s *AcceptedImageState) validatePromotion(
	ctx context.Context,
	current gate.AcceptedImageRecord,
	currentDigest string,
	next gate.AcceptedImageRecord,
) error {
	if current.Generation == ^uint64(0) || next.Generation != current.Generation+1 {
		return errors.New("accepted image promotion must advance exactly one generation")
	}
	if next.PreviousRecordDigest != currentDigest {
		return errors.New("accepted image promotion predecessor does not match current record")
	}
	if next.RepoID != current.RepoID {
		return errors.New("accepted image promotion cannot change repo_id")
	}
	if next.TrustedRef != current.TrustedRef {
		return errors.New("accepted image promotion cannot change trusted_ref")
	}
	if err := s.verifyRecord(ctx, next); err != nil {
		return err
	}
	isAncestor, err := s.ancestry.IsAncestor(ctx, current.RepoID, current.TrustedRef, current.TrustedCommit, next.TrustedCommit)
	if err != nil {
		return fmt.Errorf("verify accepted image ref ancestry: %w", err)
	}
	if !isAncestor {
		return ErrAcceptedImageRollback
	}
	return nil
}

// loadLocked 从已锁定的 canonical 文件读取、严格解码并验签。
func (s *AcceptedImageState) loadLocked(ctx context.Context) (gate.AcceptedImageRecord, error) {
	var record gate.AcceptedImageRecord
	if err := ctx.Err(); err != nil {
		return record, err
	}
	file, info, err := s.openStateFile()
	if err != nil {
		return record, err
	}
	data, readErr := readAcceptedImageFile(file, info)
	if readErr != nil {
		return record, readErr
	}
	if err := gate.DecodeStrictJSON(data, &record); err != nil {
		return record, fmt.Errorf("decode accepted image state: %w", err)
	}
	canonical, err := canonicalAcceptedImageBytes(record)
	if err != nil {
		return record, err
	}
	if !bytes.Equal(data, canonical) {
		return record, errors.New("accepted image state JSON is not canonical")
	}
	if err := s.verifyRecord(ctx, record); err != nil {
		return record, err
	}
	return record, nil
}

// openStateFile 以 no-follow 语义打开并复核 pathname 与 fd 身份。
func (s *AcceptedImageState) openStateFile() (*os.File, os.FileInfo, error) {
	exists, err := validateCurrentUIDPrivatePath(s.statePath, s.ownerUID)
	if err != nil {
		return nil, nil, fmt.Errorf("validate accepted image state path: %w", err)
	}
	if !exists {
		return nil, nil, ErrAcceptedImageStateNotFound
	}
	file, _, err := openSchedulerFileNoFollow(s.statePath, s.ownerUID, true)
	if err != nil {
		return nil, nil, fmt.Errorf("open accepted image state: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		return nil, nil, closeAcceptedImageFile(file, nil, err)
	}
	if err := validateOpenedAcceptedImageFile(s.statePath, info, s.ownerUID); err != nil {
		return nil, nil, closeAcceptedImageFile(file, nil, err)
	}
	return file, info, nil
}

// validateOpenedAcceptedImageFile 拒绝 pathname/fd 竞态、链接、owner 和 mode 漂移。
func validateOpenedAcceptedImageFile(path string, info os.FileInfo, ownerUID int) error {
	if !info.Mode().IsRegular() {
		return errors.New("accepted image state fd is not a regular file")
	}
	if err := validatePrivateOwnerAndMode(info, ownerUID, false); err != nil {
		return fmt.Errorf("accepted image state fd metadata: %w", err)
	}
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("lstat accepted image state after open: %w", err)
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() {
		return errors.New("accepted image state path is not a regular non-symlink file")
	}
	if !os.SameFile(info, pathInfo) {
		return errors.New("accepted image state changed while opening")
	}
	return nil
}

func readAcceptedImageFile(file *os.File, info os.FileInfo) ([]byte, error) {
	if info.Size() > acceptedImageMaxBytes {
		return nil, closeAcceptedImageFile(file, nil, errors.New("accepted image state exceeds size limit"))
	}
	data, readErr := io.ReadAll(io.LimitReader(file, acceptedImageMaxBytes+1))
	if len(data) > acceptedImageMaxBytes {
		readErr = errors.Join(readErr, errors.New("accepted image state exceeds size limit"))
	}
	return data, closeAcceptedImageFile(file, readErr, nil)
}

func closeAcceptedImageFile(file *os.File, prior error, cause error) error {
	if closeErr := file.Close(); closeErr != nil {
		return errors.Join(prior, cause, fmt.Errorf("close accepted image state: %w", closeErr))
	}
	return errors.Join(prior, cause)
}

// verifyRecord 将 canonical payload 和 signer 身份交给注入的 verifier。
func (s *AcceptedImageState) verifyRecord(ctx context.Context, record gate.AcceptedImageRecord) error {
	if err := record.Validate(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	payload, err := gate.AcceptedImageSigningPayload(record)
	if err != nil {
		return err
	}
	if err := s.verifier.VerifyAcceptedImage(ctx, record.Signer, payload, record.Signature); err != nil {
		return fmt.Errorf("verify accepted image signature: %w", err)
	}
	return nil
}

// writeLocked 通过私有临时文件、fsync、rename 和目录 fsync 原子提交。
func (s *AcceptedImageState) writeLocked(record gate.AcceptedImageRecord, expectedExists bool) (retErr error) {
	data, err := canonicalAcceptedImageBytes(record)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(s.root, ".accepted-image-*.tmp")
	if err != nil {
		return fmt.Errorf("create accepted image temp file: %w", err)
	}
	tempPath := file.Name()
	defer func() { retErr = errors.Join(retErr, removeAcceptedImageTemp(tempPath)) }()
	if err := writeAcceptedImageTemp(file, data, s.ownerUID); err != nil {
		return err
	}
	exists, err := validateCurrentUIDPrivatePath(s.statePath, s.ownerUID)
	if err != nil {
		return fmt.Errorf("validate accepted image replace target: %w", err)
	}
	if exists != expectedExists {
		return ErrAcceptedImageCASConflict
	}
	if err := os.Rename(tempPath, s.statePath); err != nil {
		return fmt.Errorf("replace accepted image state: %w", err)
	}
	tempPath = ""
	return syncAcceptedImageRoot(s.root)
}

// writeAcceptedImageTemp 写满、同步并关闭 owner-private 临时文件。
func writeAcceptedImageTemp(file *os.File, data []byte, ownerUID int) error {
	if err := file.Chmod(privateSchedulerFileMode); err != nil {
		return closeAcceptedImageFile(file, nil, fmt.Errorf("chmod accepted image temp file: %w", err))
	}
	info, err := file.Stat()
	if err != nil {
		return closeAcceptedImageFile(file, nil, fmt.Errorf("stat accepted image temp file: %w", err))
	}
	if err := validatePrivateOwnerAndMode(info, ownerUID, false); err != nil {
		return closeAcceptedImageFile(file, nil, fmt.Errorf("validate accepted image temp file: %w", err))
	}
	written, err := file.Write(data)
	if err == nil && written != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return closeAcceptedImageFile(file, nil, fmt.Errorf("write accepted image temp file: %w", err))
	}
	if err := file.Sync(); err != nil {
		return closeAcceptedImageFile(file, nil, fmt.Errorf("sync accepted image temp file: %w", err))
	}
	return closeAcceptedImageFile(file, nil, nil)
}

func canonicalAcceptedImageBytes(record gate.AcceptedImageRecord) ([]byte, error) {
	if err := record.Validate(); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("marshal accepted image state: %w", err)
	}
	return append(encoded, '\n'), nil
}

func syncAcceptedImageRoot(root string) error {
	directory, err := os.Open(root)
	if err != nil {
		return fmt.Errorf("open accepted image root for sync: %w", err)
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil {
		return errors.Join(fmt.Errorf("sync accepted image root: %w", syncErr), closeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close accepted image root: %w", closeErr)
	}
	return nil
}

func removeAcceptedImageTemp(path string) error {
	if path == "" {
		return nil
	}
	err := os.Remove(path)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("remove accepted image temp file: %w", err)
}

// validateRoot 拒绝非 canonical、链接、非当前 owner 或共享 authority 根。
func (s *AcceptedImageState) validateRoot() error {
	if strings.TrimSpace(s.root) == "" || !filepath.IsAbs(s.root) || filepath.Clean(s.root) != s.root {
		return errors.New("accepted image root must be canonical and absolute")
	}
	canonical, err := filepath.EvalSymlinks(s.root)
	if err != nil {
		return fmt.Errorf("canonicalize accepted image root: %w", err)
	}
	if canonical != s.root {
		return errors.New("accepted image root must not contain symlinks")
	}
	info, err := os.Lstat(s.root)
	if err != nil {
		return fmt.Errorf("lstat accepted image root: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("accepted image root must be a real directory")
	}
	if err := validatePrivateOwnerAndMode(info, s.ownerUID, true); err != nil {
		return fmt.Errorf("accepted image root metadata: %w", err)
	}
	return nil
}

// validateCall 在每次操作前重新验证依赖、context 和 authority 根。
func (s *AcceptedImageState) validateCall(ctx context.Context) error {
	if s == nil || interfaceValueIsNil(s.verifier) || interfaceValueIsNil(s.ancestry) {
		return errors.New("accepted image state is not initialized")
	}
	if interfaceValueIsNil(ctx) {
		return errors.New("accepted image context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.validateRoot()
}

type acceptedImageLock struct {
	file *os.File
}

// acquireLock 在 context 可取消的重试循环中取得跨进程独占锁。
func (s *AcceptedImageState) acquireLock(ctx context.Context) (*acceptedImageLock, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		file, err := openCurrentUIDPrivateFile(s.lockPath, s.ownerUID)
		if err != nil {
			return nil, fmt.Errorf("open accepted image lock: %w", err)
		}
		if err := lockSchedulerFile(file); err == nil {
			return &acceptedImageLock{file: file}, nil
		} else if !schedulerLockAlreadyOwned(err) {
			return nil, closeAcceptedImageFile(file, nil, fmt.Errorf("acquire accepted image lock: %w", err))
		}
		if err := file.Close(); err != nil {
			return nil, fmt.Errorf("close contended accepted image lock: %w", err)
		}
		if err := waitAcceptedImageLock(ctx); err != nil {
			return nil, err
		}
	}
}

func waitAcceptedImageLock(ctx context.Context) error {
	timer := time.NewTimer(acceptedImageLockRetry)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (l *acceptedImageLock) close() error {
	if l == nil || l.file == nil {
		return errors.New("accepted image lock is not open")
	}
	unlockErr := unlockSchedulerFile(l.file)
	closeErr := l.file.Close()
	l.file = nil
	return errors.Join(unlockErr, closeErr)
}

func interfaceValueIsNil(value any) bool {
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

// validateImageBuilderEntry 在读取请求或命中缓存前校验构建器和调用上下文。
func validateImageBuilderEntry(builder *ImageBuilder, ctx context.Context) error {
	if builder == nil || buildKitRunnerIsNil(builder.runner) {
		return errors.New("image builder is not initialized")
	}
	if ctx == nil {
		return errors.New("candidate build context is required")
	}
	return ctx.Err()
}

// buildKitRunnerIsNil 识别接口本身为空以及接口内承载的各类 typed nil。
func buildKitRunnerIsNil(runner BuildKitRunner) bool {
	if runner == nil {
		return true
	}
	value := reflect.ValueOf(runner)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
