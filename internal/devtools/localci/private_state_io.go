package localci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

var errSchedulerOwned = errors.New("scheduler daemon is already owned")

type schedulerLock struct {
	file *os.File
}

// acquireSchedulerLock 取得 daemon identity 对应的进程级非阻塞独占锁。
func acquireSchedulerLock(lockPath string, identity daemonIdentity) (*schedulerLock, error) {
	if strings.TrimSpace(lockPath) == "" {
		return nil, errors.New("scheduler lock path is required")
	}
	if strings.TrimSpace(identity.key) == "" {
		return nil, errors.New("validated daemon identity is required")
	}
	file, err := openCurrentUIDPrivateFile(lockPath, identity.ownerUID)
	if err != nil {
		return nil, fmt.Errorf("open scheduler lock: %w", err)
	}
	if err := verifySchedulerLockFile(file, identity); err != nil {
		return nil, closeFileAfterError(file, err, "close scheduler lock after verification failure")
	}
	if err := lockSchedulerFile(file); err != nil {
		closeErr := file.Close()
		if schedulerLockAlreadyOwned(err) {
			return nil, errors.Join(errSchedulerOwned, closeErr)
		}
		return nil, errors.Join(fmt.Errorf("acquire scheduler lock: %w", err), closeErr)
	}
	return &schedulerLock{file: file}, nil
}

// verifySchedulerLockFile 校验私有 regular file 元数据和持久化 daemon identity。
func verifySchedulerLockFile(file *os.File, identity daemonIdentity) error {
	if err := verifySchedulerLockFileMetadata(file, identity.ownerUID); err != nil {
		return err
	}
	return verifySchedulerLockIdentity(file, identity)
}

// verifySchedulerLockFileMetadata 复核已打开 lock fd 的类型、owner 与权限。
func verifySchedulerLockFileMetadata(file *os.File, ownerUID int) error {
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat scheduler lock: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("scheduler lock must be a regular file")
	}
	if err := validatePrivateOwnerAndMode(info, ownerUID, false); err != nil {
		return fmt.Errorf("scheduler lock ownership: %w", err)
	}
	if info.Mode().Perm() != privateSchedulerFileMode {
		return fmt.Errorf("scheduler lock mode is %04o, want 0600", info.Mode().Perm())
	}
	return nil
}

// verifySchedulerLockIdentity 拒绝复用属于其他 daemon identity 的锁文件。
func verifySchedulerLockIdentity(file *os.File, identity daemonIdentity) error {
	contents, err := io.ReadAll(file)
	if err != nil {
		return fmt.Errorf("read scheduler lock identity: %w", err)
	}
	storedKey := strings.TrimSpace(string(contents))
	if storedKey != "" && storedKey != identity.key {
		return errors.New("scheduler lock daemon identity mismatch")
	}
	if storedKey == identity.key {
		return nil
	}
	if _, err := file.WriteAt([]byte(identity.key+"\n"), 0); err != nil {
		return fmt.Errorf("write scheduler lock identity: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync scheduler lock identity: %w", err)
	}
	return nil
}

func (l *schedulerLock) close() error {
	if l == nil || l.file == nil {
		return errors.New("scheduler lock is not open")
	}
	unlockErr := unlockSchedulerFile(l.file)
	closeErr := l.file.Close()
	l.file = nil
	return errors.Join(unlockErr, closeErr)
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

// Candidate 按调度 workload 标识读取单个持久化候选。
func (s *PromotionCandidateStore) Candidate(ctx context.Context, workloadID string) (PromotionCandidate, error) {
	if err := s.validateCall(ctx); err != nil {
		return PromotionCandidate{}, err
	}
	if workloadID == "" {
		return PromotionCandidate{}, errors.New("promotion candidate workload is required")
	}
	snapshot, err := s.candidateSnapshot(ctx)
	if err != nil {
		return PromotionCandidate{}, err
	}
	for _, candidate := range snapshot.Candidates {
		if candidate.WorkloadID == workloadID {
			return clonePromotionCandidate(candidate), nil
		}
	}
	return PromotionCandidate{}, ErrPromotionCandidateNotFound
}

// CandidateForTrustedCommit 返回唯一绑定到目标 trusted tip 的持久化候选。
func (s *PromotionCandidateStore) CandidateForTrustedCommit(ctx context.Context, repoID, trustedRef, trustedCommit string) (PromotionCandidate, error) {
	if err := s.validateCall(ctx); err != nil {
		return PromotionCandidate{}, err
	}
	if repoID == "" || trustedRef == "" || trustedCommit == "" {
		return PromotionCandidate{}, errors.New("promotion candidate trusted identity is required")
	}
	snapshot, err := s.candidateSnapshot(ctx)
	if err != nil {
		return PromotionCandidate{}, err
	}
	return uniqueCandidateForTrustedCommit(snapshot.Candidates, repoID, trustedRef, trustedCommit)
}

func (s *PromotionCandidateStore) candidateSnapshot(ctx context.Context) (promotionCandidateSnapshot, error) {
	lock, err := s.acquireLock(ctx)
	if err != nil {
		return promotionCandidateSnapshot{}, err
	}
	defer lock.close()
	return s.loadLocked()
}

// failBuild 使用有界清理上下文把已开始的构建固定为终态，禁止复用其 scheduler identity。
func (s *PromotionCandidateStore) failBuild(ctx context.Context, workloadID string, buildErr error) error {
	cleanupCtx, cancel := BoundedCleanupContext(ctx, promotionCandidateFailureWriteLimit)
	defer cancel()
	err := s.mutate(cleanupCtx, workloadID, func(candidate *PromotionCandidate) error {
		if candidate.Status != PromotionCandidateBuilding {
			return fmt.Errorf("%w: candidate %q cannot fail from %q", ErrPromotionCandidateState, candidate.CandidateID, candidate.Status)
		}
		candidate.Status = PromotionCandidateFailed
		candidate.compactTerminalBuildPayload()
		return nil
	})
	if err != nil {
		return errors.Join(buildErr, fmt.Errorf("persist failed promotion candidate: %w", err))
	}
	return buildErr
}

// uniqueCandidateForTrustedCommit 优先唯一活跃候选，并严格验证失败历史的恢复权威绑定。
func uniqueCandidateForTrustedCommit(candidates []PromotionCandidate, repoID, trustedRef, trustedCommit string) (PromotionCandidate, error) {
	matches := matchingCandidatesForTrustedCommit(candidates, repoID, trustedRef, trustedCommit)
	active, found, err := uniqueActiveCandidate(matches)
	if err != nil || found {
		return active, err
	}
	return consistentFailedCandidate(matches)
}

func matchingCandidatesForTrustedCommit(candidates []PromotionCandidate, repoID, trustedRef, trustedCommit string) []PromotionCandidate {
	matches := make([]PromotionCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.RepoID == repoID && candidate.TrustedRef == trustedRef && candidate.TrustedCommit == trustedCommit {
			matches = append(matches, clonePromotionCandidate(candidate))
		}
	}
	return matches
}

func uniqueActiveCandidate(candidates []PromotionCandidate) (PromotionCandidate, bool, error) {
	var active *PromotionCandidate
	for i := range candidates {
		if !candidates[i].statusIsActive() {
			continue
		}
		if active != nil {
			return PromotionCandidate{}, false, errors.New("promotion candidate trusted commit is ambiguous")
		}
		active = &candidates[i]
	}
	if active == nil {
		return PromotionCandidate{}, false, nil
	}
	return *active, true, nil
}

// consistentFailedCandidate 确保全部失败历史绑定同一恢复权威，并确定性返回首条记录。
func consistentFailedCandidate(candidates []PromotionCandidate) (PromotionCandidate, error) {
	var failed *PromotionCandidate
	for i := range candidates {
		if candidates[i].Status != PromotionCandidateFailed && candidates[i].Status != PromotionCandidateAdvanceFailed {
			continue
		}
		if failed == nil {
			failed = &candidates[i]
			continue
		}
		if !sameFailedCandidateRecoveryAuthority(*failed, candidates[i]) {
			return PromotionCandidate{}, errors.New("promotion candidate trusted commit is ambiguous")
		}
	}
	if failed == nil {
		return PromotionCandidate{}, ErrPromotionCandidateNotFound
	}
	return *failed, nil
}

func sameFailedCandidateRecoveryAuthority(left, right PromotionCandidate) bool {
	return left.PromotionMode == right.PromotionMode && left.SourceTree == right.SourceTree &&
		left.PreviousTrustedCommit == right.PreviousTrustedCommit &&
		left.ExpectedAcceptedRecordDigest == right.ExpectedAcceptedRecordDigest &&
		left.ExpectedAcceptedGeneration == right.ExpectedAcceptedGeneration
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

// loadLocked 在持锁状态下严格读取 canonical candidate snapshot。
func (s *PromotionCandidateStore) loadLocked() (promotionCandidateSnapshot, error) {
	snapshot := promotionCandidateSnapshot{SchemaVersion: promotionCandidateSchemaVersion, Candidates: []PromotionCandidate{}}
	exists, err := validateCurrentUIDPrivatePath(s.statePath, s.ownerUID)
	if err != nil {
		return promotionCandidateSnapshot{}, fmt.Errorf("validate promotion candidate state: %w", err)
	}
	if !exists {
		return snapshot, nil
	}
	file, _, err := openSchedulerFileNoFollow(s.statePath, s.ownerUID, true)
	if err != nil {
		return promotionCandidateSnapshot{}, fmt.Errorf("open promotion candidate state: %w", err)
	}
	info, err := promotionCandidateFileInfo(file, s.statePath, s.ownerUID)
	if err != nil {
		return promotionCandidateSnapshot{}, err
	}
	data, err := readPromotionCandidateState(file, info)
	if err != nil {
		return promotionCandidateSnapshot{}, err
	}
	if err := gate.DecodeStrictJSON(data, &snapshot); err != nil {
		return promotionCandidateSnapshot{}, fmt.Errorf("decode promotion candidate state: %w", err)
	}
	if err := snapshot.Validate(); err != nil {
		return promotionCandidateSnapshot{}, err
	}
	return snapshot, nil
}

// promotionCandidateFileInfo 复核 pathname/fd 身份并在失败时关闭 fd。
func promotionCandidateFileInfo(file *os.File, path string, ownerUID int) (os.FileInfo, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, errors.Join(err, file.Close())
	}
	if err := validateOpenedAcceptedImageFile(path, info, ownerUID); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return info, nil
}

// readPromotionCandidateState 限长读取并在所有路径关闭 canonical state fd。
func readPromotionCandidateState(file *os.File, info os.FileInfo) ([]byte, error) {
	if info.Size() > promotionCandidateMaxBytes {
		return nil, errors.Join(errors.New("promotion candidate state exceeds size limit"), file.Close())
	}
	data, readErr := io.ReadAll(io.LimitReader(file, promotionCandidateMaxBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return nil, errors.Join(readErr, closeErr)
	}
	if len(data) > promotionCandidateMaxBytes {
		return nil, errors.New("promotion candidate state exceeds size limit")
	}
	return data, nil
}

// saveLocked 通过私有临时文件、fsync 与 rename 原子替换 canonical snapshot。
func (s *PromotionCandidateStore) saveLocked(snapshot promotionCandidateSnapshot) (retErr error) {
	data, err := encodePromotionCandidateSnapshot(snapshot)
	if err != nil {
		return err
	}
	if s.beforeSave != nil {
		if err := s.beforeSave(); err != nil {
			return err
		}
	}
	if err := s.replaceSnapshot(data); err != nil {
		return err
	}
	if s.afterSave != nil {
		return s.afterSave()
	}
	return nil
}

// encodePromotionCandidateSnapshot 生成受大小上限约束的 canonical JSON。
func encodePromotionCandidateSnapshot(snapshot promotionCandidateSnapshot) ([]byte, error) {
	if err := snapshot.Validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("marshal promotion candidate state: %w", err)
	}
	data = append(data, '\n')
	if len(data) > promotionCandidateMaxBytes {
		return nil, errors.New("promotion candidate state exceeds size limit")
	}
	return data, nil
}

// replaceSnapshot 通过 fsync 临时文件与 rename 原子替换 candidate authority。
func (s *PromotionCandidateStore) replaceSnapshot(data []byte) (retErr error) {
	file, err := os.CreateTemp(s.root, ".promotion-candidates-*.tmp")
	if err != nil {
		return fmt.Errorf("create promotion candidate temp file: %w", err)
	}
	tempPath := file.Name()
	defer func() { retErr = errors.Join(retErr, removeAcceptedImageTemp(tempPath)) }()
	if err := writeAcceptedImageTemp(file, data, s.ownerUID); err != nil {
		return err
	}
	if err := os.Rename(tempPath, s.statePath); err != nil {
		return fmt.Errorf("replace promotion candidate state: %w", err)
	}
	tempPath = ""
	if err := syncAcceptedImageRoot(s.root); err != nil {
		return err
	}
	return nil
}

// acquireLock 使用可取消重试取得 candidate authority 跨进程锁。
func (s *PromotionCandidateStore) acquireLock(ctx context.Context) (*acceptedImageLock, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		file, err := openCurrentUIDPrivateFile(s.lockPath, s.ownerUID)
		if err != nil {
			return nil, fmt.Errorf("open promotion candidate lock: %w", err)
		}
		if err := lockSchedulerFile(file); err == nil {
			return &acceptedImageLock{file: file}, nil
		} else if !schedulerLockAlreadyOwned(err) {
			return nil, errors.Join(fmt.Errorf("acquire promotion candidate lock: %w", err), file.Close())
		}
		if err := file.Close(); err != nil {
			return nil, err
		}
		if err := waitAcceptedImageLock(ctx); err != nil {
			return nil, err
		}
	}
}
