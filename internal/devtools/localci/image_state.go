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

var (
	ErrPromotionTrustedRefMismatch = errors.New("trusted ref does not exactly select the promotion candidate")
	ErrPromotionTrustedRefRollback = errors.New("trusted ref rolled back or left accepted ancestry")
)

// TrustedRefObservation is the only external Git state the promotion controller consumes.
type TrustedRefObservation struct {
	RepoID     string
	TrustedRef string
	Commit     string
	SourceTree string
}

// PromotionTrustedRefObserver observes a configured external bare ref and its ancestry.
type PromotionTrustedRefObserver interface {
	ObserveTrustedRef(context.Context, string, string) (TrustedRefObservation, error)
	IsAncestor(context.Context, string, string, string, string) (bool, error)
}

// AcceptedImageSigner holds host-side signing authority without exposing key bytes.
type AcceptedImageSigner interface {
	SignerIdentity() gate.SignerIdentity
	SignAcceptedImage(context.Context, []byte) (string, error)
}

// PromotionController promotes only exact, unexpired, externally selected candidates.
type PromotionController struct {
	store     *PromotionCandidateStore
	accepted  *AcceptedImageState
	observer  PromotionTrustedRefObserver
	signer    AcceptedImageSigner
	poll      time.Duration
	now       func() time.Time
	afterSign func() error
	beforeCAS func() error
	afterCAS  func() error
}

// NewPromotionController 校验宿主 authority 并构造生命周期托管的 watcher。
func NewPromotionController(
	store *PromotionCandidateStore,
	accepted *AcceptedImageState,
	observer PromotionTrustedRefObserver,
	signer AcceptedImageSigner,
	poll time.Duration,
) (*PromotionController, error) {
	if store == nil || accepted == nil || interfaceValueIsNil(observer) || interfaceValueIsNil(signer) {
		return nil, errors.New("promotion store, accepted state, observer, and signer are required")
	}
	if poll <= 0 {
		return nil, errors.New("promotion watcher poll interval must be positive")
	}
	if err := signer.SignerIdentity().Validate(); err != nil {
		return nil, fmt.Errorf("validate promotion signer: %w", err)
	}
	return &PromotionController{
		store: store, accepted: accepted, observer: observer, signer: signer, poll: poll, now: time.Now,
	}, nil
}

// Run 在 coordinator owner 生命周期内轮询，绝不创建非托管 goroutine。
func (controller *PromotionController) Run(ctx context.Context) error {
	if err := controller.validateCall(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(controller.poll)
	defer ticker.Stop()
	for {
		if err := controller.PromoteReady(ctx); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// PromoteReady 按 candidate 创建顺序执行一次确定性 watcher 扫描。
func (controller *PromotionController) PromoteReady(ctx context.Context) error {
	if err := controller.validateCall(ctx); err != nil {
		return err
	}
	candidates, err := controller.store.Awaiting(ctx)
	if err != nil {
		return err
	}
	for _, candidate := range candidates {
		if err := controller.promoteCandidate(ctx, candidate); err != nil {
			return err
		}
	}
	return nil
}

// promoteCandidate 在精确 trusted tip 与 CAS 合同同时满足时签名并晋升。
func (controller *PromotionController) promoteCandidate(ctx context.Context, candidate PromotionCandidate) error {
	candidate, next, ready, err := controller.preparePromotion(ctx, candidate)
	if err != nil || !ready {
		return err
	}
	return controller.commitPromotion(ctx, candidate, next)
}

// preparePromotion 观察 trusted ref、固化 acceptedAt 并生成确定性签名记录。
func (controller *PromotionController) preparePromotion(
	ctx context.Context,
	candidate PromotionCandidate,
) (PromotionCandidate, gate.AcceptedImageRecord, bool, error) {
	now := controller.now().UTC()
	if !candidate.ExpiresAt.After(now) {
		return candidate, gate.AcceptedImageRecord{}, false,
			fmt.Errorf("%w: %s", ErrPromotionCandidateExpired, candidate.CandidateID)
	}
	observation, err := controller.observer.ObserveTrustedRef(ctx, candidate.RepoID, candidate.TrustedRef)
	if err != nil {
		return candidate, gate.AcceptedImageRecord{}, false, err
	}
	ready, err := controller.validateObservation(ctx, candidate, observation)
	if err != nil || !ready {
		return candidate, gate.AcceptedImageRecord{}, ready, err
	}
	acceptedAt := now
	if candidate.PromotionAcceptedAt != nil {
		acceptedAt = *candidate.PromotionAcceptedAt
	}
	candidate, err = controller.store.setPromotionAcceptedAt(ctx, candidate.WorkloadID, acceptedAt)
	if err != nil {
		return candidate, gate.AcceptedImageRecord{}, false, err
	}
	next, err := controller.signedNextRecord(ctx, candidate)
	if err != nil {
		return candidate, gate.AcceptedImageRecord{}, false, err
	}
	if controller.afterSign != nil {
		if err := controller.afterSign(); err != nil {
			return candidate, gate.AcceptedImageRecord{}, false, err
		}
	}
	return candidate, next, true, nil
}

// commitPromotion 处理重启幂等、CAS hooks、原子晋升与 candidate 终态。
func (controller *PromotionController) commitPromotion(
	ctx context.Context,
	candidate PromotionCandidate,
	next gate.AcceptedImageRecord,
) error {
	if promoted, err := controller.alreadyPromoted(ctx, candidate, next); err != nil {
		return err
	} else if promoted {
		return controller.store.markPromoted(ctx, candidate.WorkloadID)
	}
	if controller.beforeCAS != nil {
		if err := controller.beforeCAS(); err != nil {
			return err
		}
	}
	promotion := gate.PromotionRecord{
		SchemaVersion: gate.PromotionRecordSchemaVersion, ExpectedRecordDigest: candidate.ExpectedAcceptedRecordDigest,
		ExpectedGeneration: candidate.ExpectedAcceptedGeneration, Next: next,
	}
	if err := controller.accepted.PromoteCAS(ctx, promotion); err != nil {
		return err
	}
	if controller.afterCAS != nil {
		if err := controller.afterCAS(); err != nil {
			return err
		}
	}
	return controller.store.markPromoted(ctx, candidate.WorkloadID)
}

// validateObservation 拒绝回滚、非祖先、跳过 candidate 或 tree 漂移。
func (controller *PromotionController) validateObservation(
	ctx context.Context,
	candidate PromotionCandidate,
	observation TrustedRefObservation,
) (bool, error) {
	if observation.RepoID != candidate.RepoID || observation.TrustedRef != candidate.TrustedRef {
		return false, errors.New("trusted ref observer returned a different repository authority")
	}
	if observation.Commit == candidate.PreviousTrustedCommit {
		return false, nil
	}
	if observation.Commit != candidate.TrustedCommit {
		return controller.rejectUnexpectedTip(ctx, candidate, observation.Commit)
	}
	return controller.validateExactCandidateObservation(ctx, candidate, observation.SourceTree)
}

// rejectUnexpectedTip 区分 ancestry 回滚与跳过 candidate 的前进 ref。
func (controller *PromotionController) rejectUnexpectedTip(
	ctx context.Context,
	candidate PromotionCandidate,
	observedCommit string,
) (bool, error) {
	ancestor, err := controller.observer.IsAncestor(
		ctx, candidate.RepoID, candidate.TrustedRef, candidate.PreviousTrustedCommit, observedCommit,
	)
	if err != nil {
		return false, err
	}
	if !ancestor {
		return false, ErrPromotionTrustedRefRollback
	}
	return false, ErrPromotionTrustedRefMismatch
}

// validateExactCandidateObservation 复核精确 candidate tip 的 tree、ancestry 与 image digest。
func (controller *PromotionController) validateExactCandidateObservation(
	ctx context.Context,
	candidate PromotionCandidate,
	observedTree string,
) (bool, error) {
	if observedTree != candidate.SourceTree {
		return false, errors.New("trusted ref candidate commit tree does not match built source tree")
	}
	ancestor, err := controller.observer.IsAncestor(
		ctx, candidate.RepoID, candidate.TrustedRef, candidate.PreviousTrustedCommit, candidate.TrustedCommit,
	)
	if err != nil {
		return false, err
	}
	if !ancestor {
		return false, ErrPromotionTrustedRefRollback
	}
	if candidate.Image.PlatformManifestDigest != candidate.PlatformManifestDigest {
		return false, errors.New("promotion candidate image digest does not match durable build result")
	}
	return true, nil
}

func (controller *PromotionController) signedNextRecord(
	ctx context.Context,
	candidate PromotionCandidate,
) (gate.AcceptedImageRecord, error) {
	if candidate.PromotionAcceptedAt == nil {
		return gate.AcceptedImageRecord{}, errors.New("promotion candidate accepted timestamp was not persisted")
	}
	next := gate.AcceptedImageRecord{
		SchemaVersion: gate.AcceptedImageRecordSchemaVersion, RepoID: candidate.RepoID,
		TrustedRef: candidate.TrustedRef, TrustedCommit: candidate.TrustedCommit, SourceTree: candidate.SourceTree,
		PolicyDigest: candidate.PolicyDigest, ImageInputDigest: candidate.ImageInputDigest,
		Image: cloneImageIdentity(candidate.Image), Runner: candidate.Runner,
		Generation:           candidate.ExpectedAcceptedGeneration + 1,
		PreviousRecordDigest: candidate.ExpectedAcceptedRecordDigest, AcceptedAt: *candidate.PromotionAcceptedAt,
		Signer: controller.signer.SignerIdentity(),
	}
	payload, err := gate.AcceptedImageSigningPayload(next)
	if err != nil {
		return gate.AcceptedImageRecord{}, err
	}
	next.Signature, err = controller.signer.SignAcceptedImage(ctx, payload)
	if err != nil {
		return gate.AcceptedImageRecord{}, err
	}
	if err := next.Validate(); err != nil {
		return gate.AcceptedImageRecord{}, err
	}
	return next, nil
}

// alreadyPromoted 区分初始 CAS 状态、同一晋升已提交和并发冲突。
func (controller *PromotionController) alreadyPromoted(
	ctx context.Context,
	candidate PromotionCandidate,
	next gate.AcceptedImageRecord,
) (bool, error) {
	current, err := controller.accepted.Load(ctx)
	if err != nil {
		return false, err
	}
	digest, err := gate.AcceptedImageRecordDigest(current)
	if err != nil {
		return false, err
	}
	if digest == candidate.ExpectedAcceptedRecordDigest && current.Generation == candidate.ExpectedAcceptedGeneration {
		return false, nil
	}
	nextDigest, err := gate.AcceptedImageRecordDigest(next)
	if err != nil {
		return false, err
	}
	if digest == nextDigest && current.Generation == candidate.ExpectedAcceptedGeneration+1 {
		return true, nil
	}
	return false, ErrAcceptedImageCASConflict
}

// validateCall 在每次 watcher 操作前复核全部 authority 依赖。
func (controller *PromotionController) validateCall(ctx context.Context) error {
	if controller == nil || controller.store == nil || controller.accepted == nil ||
		interfaceValueIsNil(controller.observer) || interfaceValueIsNil(controller.signer) || controller.now == nil {
		return errors.New("promotion controller is not configured")
	}
	if ctx == nil {
		return errors.New("promotion controller context is required")
	}
	return ctx.Err()
}
