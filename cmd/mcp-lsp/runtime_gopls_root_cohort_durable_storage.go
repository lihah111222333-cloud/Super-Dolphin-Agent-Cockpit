package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
)

// 加锁读取并提交一个 durable cohort 状态转换，保证跨 sidecar 的单写者约束。
// runtimeServerDurableGoplsRootCohortWithStateLock serializes one durable
// cohort mutation behind its private filesystem lock and loads the current
// state before invoking the caller's bounded state transition.
func runtimeServerDurableGoplsRootCohortWithStateLock[T any](c *runtimeServerDurableGoplsRootCohortController, config multilsp.GoplsRootCohortConfig, fn func(string, *runtimeServerDurableGoplsRootCohortState) (T, error)) (result T, retErr error) {
	var zero T
	if err := config.Validate(); err != nil {
		return zero, err
	}
	if c.root == "" {
		return zero, errors.New("durable gopls root cohort cache root is empty")
	}
	dir := runtimeServerGoplsRootCohortDir(c.root, config)
	if err := runtimeServerEnsurePrivateDescendant(c.root, dir); err != nil {
		return zero, fmt.Errorf("secure gopls root cohort directory: %w", err)
	}
	lock, err := runtimeServerAcquireResourceLeaseLock(dir)
	if err != nil {
		return zero, err
	}
	defer func() {
		retErr = errors.Join(retErr, runtimeServerReleaseResourceLeaseLock(lock))
	}()
	state, err := runtimeServerReadGoplsRootCohortState(filepath.Join(dir, "state.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return zero, err
	}
	if errors.Is(err, os.ErrNotExist) {
		state = nil
	}
	return fn(dir, state)
}

// runtimeServerGoplsRootCohortDir returns the private durable directory for a
// canonical root proof; unrelated roots therefore never share a state lock.
func runtimeServerGoplsRootCohortDir(root string, config multilsp.GoplsRootCohortConfig) string {
	key := runtimeServerDigestString("gopls-root-cohort-path-v1\x00" + config.RepositoryInstanceProof.CanonicalRootDigest)
	return filepath.Join(root, "gopls-root-cohorts", key)
}

// runtimeServerDurableGoplsRootCohortConfigFrom projects the immutable public
// admission proof into the strict JSON state schema.
func runtimeServerDurableGoplsRootCohortConfigFrom(config multilsp.GoplsRootCohortConfig) runtimeServerDurableGoplsRootCohortConfig {
	return runtimeServerDurableGoplsRootCohortConfig{
		CohortID:            config.CohortID,
		CanonicalRootDigest: config.RepositoryInstanceProof.CanonicalRootDigest,
		FilesystemIdentity:  config.RepositoryInstanceProof.FilesystemIdentity,
		GitMarkerDigest:     config.RepositoryInstanceProof.GitMarkerDigest,
		InstanceNonce:       config.RepositoryInstanceProof.InstanceNonce,
		EffectiveConfig:     config.EffectiveConfigDigest,
	}
}

// value converts the persisted immutable config back to the public config.
func (c runtimeServerDurableGoplsRootCohortConfig) value() multilsp.GoplsRootCohortConfig {
	return multilsp.GoplsRootCohortConfig{
		CohortID: c.CohortID,
		RepositoryInstanceProof: multilsp.GoplsRepositoryInstanceProof{
			CanonicalRootDigest: c.CanonicalRootDigest,
			FilesystemIdentity:  c.FilesystemIdentity,
			GitMarkerDigest:     c.GitMarkerDigest,
			InstanceNonce:       c.InstanceNonce,
		},
		EffectiveConfigDigest: c.EffectiveConfig,
	}
}

// 校验持久化状态 schema 与不可变配置摘要，并还原公共配置。
// configValue validates the persisted state schema and immutable config digest.
func (s runtimeServerDurableGoplsRootCohortState) configValue() (multilsp.GoplsRootCohortConfig, error) {
	if s.SchemaVersion != runtimeGoplsRootCohortSchemaVersion || s.Epoch == 0 || s.ConfigDigest == "" {
		return multilsp.GoplsRootCohortConfig{}, errors.New("gopls root cohort state schema is invalid")
	}
	switch s.DrainStatus {
	case runtimeGoplsRootCohortDrainActive,
		runtimeGoplsRootCohortDrainDraining,
		runtimeGoplsRootCohortDrainAttempting,
		runtimeGoplsRootCohortDrainCleanupPending,
		runtimeGoplsRootCohortDrainCompleted:
	default:
		return multilsp.GoplsRootCohortConfig{}, errors.New("gopls root cohort drain status is invalid")
	}
	config := s.Config.value()
	if err := config.Validate(); err != nil {
		return multilsp.GoplsRootCohortConfig{}, fmt.Errorf("gopls root cohort state config is invalid: %w", err)
	}
	for _, evidence := range s.PendingCleanups {
		if err := runtimeServerGoplsRootCohortCleanupEvidenceValid(evidence); err != nil {
			return multilsp.GoplsRootCohortConfig{}, err
		}
	}
	if multilsp.DigestGoplsRootCohortConfig(config) != s.ConfigDigest {
		return multilsp.GoplsRootCohortConfig{}, errors.New("gopls root cohort state config digest mismatch")
	}
	return config, nil
}

// storedEqualGoplsRootCohortConfig compares every immutable admission field.
func storedEqualGoplsRootCohortConfig(left, right multilsp.GoplsRootCohortConfig) bool {
	return left.CohortID == right.CohortID &&
		left.EffectiveConfigDigest == right.EffectiveConfigDigest &&
		left.RepositoryInstanceProof == right.RepositoryInstanceProof
}

// runtimeServerDurableGoplsRootCohortFenceFrom projects a public fence into
// the persisted lease schema.
func runtimeServerDurableGoplsRootCohortFenceFrom(fence multilsp.GoplsRootCohortFence) runtimeServerDurableGoplsRootCohortFence {
	return runtimeServerDurableGoplsRootCohortFence{
		Epoch: fence.Epoch, JournalRevision: fence.JournalRevision, MemberID: fence.MemberID,
		MemberGeneration: fence.MemberGeneration, LeaseID: fence.LeaseID,
	}
}

// toValue converts the persisted fence back to the public fence type.
func (f runtimeServerDurableGoplsRootCohortFence) toValue() multilsp.GoplsRootCohortFence {
	return multilsp.GoplsRootCohortFence{
		Epoch: f.Epoch, JournalRevision: f.JournalRevision, MemberID: f.MemberID,
		MemberGeneration: f.MemberGeneration, LeaseID: f.LeaseID,
	}
}

// runtimeServerGoplsRootCohortLeasePath returns the private path for one lease.
func runtimeServerGoplsRootCohortLeasePath(dir string, fence multilsp.GoplsRootCohortFence) string {
	return filepath.Join(dir, "lease-"+runtimeServerDigestString("gopls-root-lease-v1\x00"+fence.LeaseID)+".json")
}

// 以排他创建方式写入一个 owner-only lease，并同步文件内容。
// runtimeServerCreateGoplsRootCohortLease writes one exclusive owner-only lease.
func runtimeServerCreateGoplsRootCohortLease(path string, lease runtimeServerDurableGoplsRootCohortLease) error {
	payload, err := json.Marshal(lease)
	if err != nil {
		return fmt.Errorf("encode gopls root cohort lease: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create gopls root cohort lease: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		return errors.Join(fmt.Errorf("secure gopls root cohort lease: %w", err), file.Close())
	}
	if _, err := file.Write(payload); err != nil {
		return errors.Join(fmt.Errorf("write gopls root cohort lease: %w", err), file.Close())
	}
	if err := file.Sync(); err != nil {
		return errors.Join(fmt.Errorf("sync gopls root cohort lease: %w", err), file.Close())
	}
	return file.Close()
}

// 读取并校验一个持久化 lease，拒绝未知字段和不完整 fence。
// runtimeServerReadGoplsRootCohortLease loads and validates one persisted lease.
func runtimeServerReadGoplsRootCohortLease(path string) (runtimeServerDurableGoplsRootCohortLease, error) {
	var lease runtimeServerDurableGoplsRootCohortLease
	if err := runtimeServerReadGoplsRootCohortJSON(path, &lease, 16*1024); err != nil {
		return lease, err
	}
	if err := runtimeServerValidateGoplsRootCohortLease(lease); err != nil {
		return lease, err
	}
	return lease, nil
}

// runtimeServerValidateGoplsRootCohortLease 校验 owner、时间和 fence 字段的完整性。
func runtimeServerValidateGoplsRootCohortLease(lease runtimeServerDurableGoplsRootCohortLease) error {
	if lease.SchemaVersion != runtimeGoplsRootCohortSchemaVersion {
		return errors.New("gopls root cohort lease is invalid")
	}
	if lease.ConfigDigest == "" {
		return errors.New("gopls root cohort lease is invalid")
	}
	if lease.OwnerPID <= 1 {
		return errors.New("gopls root cohort lease owner is invalid")
	}
	if lease.OwnerStartIdentity == "" {
		return errors.New("gopls root cohort lease owner is invalid")
	}
	if lease.CreatedAtUnixNano <= 0 {
		return errors.New("gopls root cohort lease owner is invalid")
	}
	if err := runtimeServerValidateGoplsRootCohortFence(lease.Fence); err != nil {
		return err
	}
	return nil
}

// runtimeServerValidateGoplsRootCohortFence 校验持久化 fence 的序列和身份字段。
func runtimeServerValidateGoplsRootCohortFence(fence runtimeServerDurableGoplsRootCohortFence) error {
	if fence.Epoch == 0 {
		return errors.New("gopls root cohort lease fence is invalid")
	}
	if fence.JournalRevision == 0 {
		return errors.New("gopls root cohort lease fence is invalid")
	}
	if fence.MemberGeneration == 0 {
		return errors.New("gopls root cohort lease fence is invalid")
	}
	if fence.MemberID == "" {
		return errors.New("gopls root cohort lease fence identity is invalid")
	}
	if fence.LeaseID == "" {
		return errors.New("gopls root cohort lease fence identity is invalid")
	}
	return nil
}

// runtimeServerReadGoplsRootCohortState loads and validates the strict state.
func runtimeServerReadGoplsRootCohortState(path string) (*runtimeServerDurableGoplsRootCohortState, error) {
	var state runtimeServerDurableGoplsRootCohortState
	if err := runtimeServerReadGoplsRootCohortJSON(path, &state, 32*1024); err != nil {
		return nil, err
	}
	if _, err := state.configValue(); err != nil {
		return nil, err
	}
	return &state, nil
}

// runtimeServerWriteGoplsRootCohortState publishes one strict state record.
func runtimeServerWriteGoplsRootCohortState(path string, state runtimeServerDurableGoplsRootCohortState) error {
	return runtimeServerWriteGoplsRootCohortJSON(path, state)
}

// 读取 owner-only 严格 JSON，并拒绝符号链接、过大记录和尾随 payload。
// runtimeServerReadGoplsRootCohortJSON reads owner-only JSON with strict fields.
func runtimeServerReadGoplsRootCohortJSON(path string, target any, maxSize int64) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > maxSize {
		return fmt.Errorf("gopls root cohort record is insecure: %s", path)
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read gopls root cohort record: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode gopls root cohort record: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("gopls root cohort record has trailing payload")
	}
	return nil
}

// 原子发布严格 JSON，并在返回前同步文件与父目录。
// runtimeServerWriteGoplsRootCohortJSON atomically publishes strict JSON and
// fsyncs the containing directory before returning.
func runtimeServerWriteGoplsRootCohortJSON(path string, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode gopls root cohort record: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".gopls-root-cohort-*.tmp")
	if err != nil {
		return fmt.Errorf("create gopls root cohort record temp file: %w", err)
	}
	tempPath := file.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := file.Chmod(0o600); err != nil {
		return errors.Join(fmt.Errorf("secure gopls root cohort temp file: %w", err), file.Close())
	}
	if _, err := file.Write(payload); err != nil {
		return errors.Join(fmt.Errorf("write gopls root cohort record: %w", err), file.Close())
	}
	if err := file.Sync(); err != nil {
		return errors.Join(fmt.Errorf("sync gopls root cohort record: %w", err), file.Close())
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("publish gopls root cohort record: %w", err)
	}
	return runtimeServerSyncGoplsRootCohortDirectory(filepath.Dir(path))
}

// runtimeServerSyncGoplsRootCohortDirectory fsyncs a durable cohort directory.
func runtimeServerSyncGoplsRootCohortDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open gopls root cohort directory for sync: %w", err)
	}
	return errors.Join(dir.Sync(), dir.Close())
}

// 扫描并只删除已证明 owner 失效或 PID 已复用的 lease。
// runtimeServerCleanupGoplsRootCohortLeases removes only leases whose owner
// process identity is proven dead or reused.
func runtimeServerCleanupGoplsRootCohortLeases(dir, configDigest string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var cleanupErr error
	removed := false
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "lease-") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		removedEntry, entryErr := runtimeServerCleanupGoplsRootCohortLease(filepath.Join(dir, entry.Name()), configDigest)
		if entryErr != nil {
			cleanupErr = errors.Join(cleanupErr, entryErr)
		}
		if removedEntry {
			removed = true
		}
	}
	if cleanupErr != nil {
		return cleanupErr
	}
	if removed {
		return runtimeServerSyncGoplsRootCohortDirectory(dir)
	}
	return nil
}

// runtimeServerCleanupGoplsRootCohortLease 判断单个 lease 的 owner 是否已失效并按需删除。
func runtimeServerCleanupGoplsRootCohortLease(path, configDigest string) (bool, error) {
	lease, err := runtimeServerReadGoplsRootCohortLease(path)
	if err != nil {
		return false, err
	}
	if lease.ConfigDigest != configDigest {
		return false, errors.New("gopls root cohort lease config digest mismatch")
	}
	alive, err := hiddenexec.ProcessAlive(lease.OwnerPID)
	if err != nil {
		return false, fmt.Errorf("check gopls root cohort lease owner: %w", err)
	}
	if alive {
		ownerStart, err := hiddenexec.ProcessStartIdentity(lease.OwnerPID)
		if err != nil {
			return false, fmt.Errorf("verify gopls root cohort lease owner: %w", err)
		}
		if ownerStart == lease.OwnerStartIdentity {
			return false, nil
		}
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("remove stale gopls root cohort lease: %w", err)
	}
	return true, nil
}

// 统计一个不可变配置摘要对应的已校验 lease 数量。
// runtimeServerCountGoplsRootCohortLeases counts only validated leases for a
// single immutable config digest.
func runtimeServerCountGoplsRootCohortLeases(dir, configDigest string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	active := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "lease-") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		lease, err := runtimeServerReadGoplsRootCohortLease(filepath.Join(dir, entry.Name()))
		if err != nil {
			return 0, err
		}
		if lease.ConfigDigest != configDigest {
			return 0, errors.New("gopls root cohort lease config digest mismatch")
		}
		active++
	}
	return active, nil
}
