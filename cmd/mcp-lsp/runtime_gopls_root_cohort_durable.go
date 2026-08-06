package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
)

const (
	runtimeGoplsRootCohortSchemaVersion = 1
	runtimeGoplsRootCohortIdleDrain     = 15 * time.Minute
	runtimeGoplsRootCohortDrainRetry    = time.Minute
)

const (
	runtimeGoplsRootCohortDrainActive         = "active"
	runtimeGoplsRootCohortDrainDraining       = "draining"
	runtimeGoplsRootCohortDrainAttempting     = "drain_attempting"
	runtimeGoplsRootCohortDrainCleanupPending = "cleanup_pending"
	runtimeGoplsRootCohortDrainCompleted      = "completed"
)

// runtimeGoplsRootCohortCleanupEvidence 是已经被 admission fence 隔离的
// 旧 drain。它与当前 epoch 的 DrainStatus 分离，保证新 sidecar 可以原子
// 重准入，而旧 owner 仍只能清理它自己持有的 forwarder。
type runtimeGoplsRootCohortCleanupEvidence struct {
	Fence                runtimeServerDurableGoplsRootCohortFence `json:"fence"`
	IdleDeadlineUnixNano int64                                    `json:"idle_deadline_unix_nano"`
	OwnerPID             int                                      `json:"owner_pid"`
	OwnerStartIdentity   string                                   `json:"owner_start_identity"`
	Status               string                                   `json:"status"`
	LastError            string                                   `json:"last_error"`
	RetryUnixNano        int64                                    `json:"retry_unix_nano"`
}

// runtimeServerDurableGoplsRootCohortController is the cross-sidecar owner for
// root admission. The cache-root lock serializes every state transition; the
// state file and member files are private, strict JSON records rather than an
// in-memory approximation of a durable fence.
type runtimeServerDurableGoplsRootCohortController struct {
	mu            sync.Mutex
	closed        bool
	root          string
	drainWindow   time.Duration
	drainRetry    time.Duration
	drainCtx      context.Context
	drainCancel   context.CancelFunc
	drainWG       sync.WaitGroup
	drainLaunchMu sync.Mutex
	drainMu       sync.Mutex
	pendingOwner  map[string]runtimeServerGoplsRootCohortPendingOwner
}

type runtimeServerGoplsRootCohortPendingOwner struct {
	config multilsp.GoplsRootCohortConfig
	fence  multilsp.GoplsRootCohortFence
	owner  func() error
}

type runtimeServerDurableGoplsRootCohortConfig struct {
	CohortID            string `json:"cohort_id"`
	CanonicalRootDigest string `json:"canonical_root_digest"`
	FilesystemIdentity  string `json:"filesystem_identity"`
	GitMarkerDigest     string `json:"git_marker_digest"`
	InstanceNonce       string `json:"instance_nonce"`
	EffectiveConfig     string `json:"effective_config_digest"`
}

type runtimeServerDurableGoplsRootCohortState struct {
	SchemaVersion         int                                       `json:"schema_version"`
	ConfigDigest          string                                    `json:"config_digest"`
	Config                runtimeServerDurableGoplsRootCohortConfig `json:"config"`
	Epoch                 uint64                                    `json:"epoch"`
	JournalRevision       uint64                                    `json:"journal_revision"`
	NextMemberGeneration  uint64                                    `json:"next_member_generation"`
	NextSequence          uint64                                    `json:"next_sequence"`
	DrainStatus           string                                    `json:"drain_status"`
	IdleDeadlineUnixNano  int64                                     `json:"idle_deadline_unix_nano"`
	DrainEpoch            uint64                                    `json:"drain_epoch"`
	OwnerPID              int                                       `json:"owner_pid"`
	OwnerStartIdentity    string                                    `json:"owner_start_identity"`
	OwnerMemberID         string                                    `json:"owner_member_id"`
	OwnerJournalRevision  uint64                                    `json:"owner_journal_revision"`
	OwnerMemberGeneration uint64                                    `json:"owner_member_generation"`
	OwnerLeaseID          string                                    `json:"owner_lease_id"`
	CompletionReceipt     string                                    `json:"completion_receipt"`
	CompletionUnixNano    int64                                     `json:"completion_unix_nano"`
	LastDrainError        string                                    `json:"last_drain_error"`
	DrainRetryUnixNano    int64                                     `json:"drain_retry_unix_nano"`
	PendingCleanups       []runtimeGoplsRootCohortCleanupEvidence   `json:"pending_cleanups,omitempty"`
}

type runtimeServerDurableGoplsRootCohortFence struct {
	Epoch            uint64 `json:"epoch"`
	JournalRevision  uint64 `json:"journal_revision"`
	MemberID         string `json:"member_id"`
	MemberGeneration uint64 `json:"member_generation"`
	LeaseID          string `json:"lease_id"`
}

type runtimeServerDurableGoplsRootCohortLease struct {
	SchemaVersion      int                                      `json:"schema_version"`
	ConfigDigest       string                                   `json:"config_digest"`
	OwnerPID           int                                      `json:"owner_pid"`
	OwnerStartIdentity string                                   `json:"owner_start_identity"`
	CreatedAtUnixNano  int64                                    `json:"created_at_unix_nano"`
	Fence              runtimeServerDurableGoplsRootCohortFence `json:"fence"`
}

func runtimeServerNewDurableGoplsRootCohortController() (multilsp.GoplsRootCohortController, error) {
	return runtimeServerNewDurableGoplsRootCohortControllerWithDrainWindow(runtimeGoplsRootCohortIdleDrain)
}

func runtimeServerNewDurableGoplsRootCohortControllerWithDrainWindow(window time.Duration) (multilsp.GoplsRootCohortController, error) {
	if runtime.GOOS == "windows" {
		return nil, multilsp.ErrGoplsRootCohortDurabilityUnsupported
	}
	if window <= 0 {
		return nil, errors.New("gopls root cohort idle drain window must be positive")
	}
	root, err := runtimeServerCacheRoot()
	if err != nil {
		return nil, err
	}
	drainCtx, drainCancel := context.WithCancel(context.Background())
	return &runtimeServerDurableGoplsRootCohortController{
		root:         root,
		drainWindow:  window,
		drainRetry:   runtimeGoplsRootCohortDrainRetry,
		drainCtx:     drainCtx,
		drainCancel:  drainCancel,
		pendingOwner: make(map[string]runtimeServerGoplsRootCohortPendingOwner),
	}, nil
}

// runtimeServerPromoteGoplsRootCohortDrain 在新 epoch 准入前隔离当前 drain。
// callback 仍由旧 controller 持有，journal 只记录仍负责清理的旧 fence。
func runtimeServerPromoteGoplsRootCohortDrain(state *runtimeServerDurableGoplsRootCohortState) {
	if state == nil || state.DrainEpoch == 0 || state.OwnerLeaseID == "" {
		return
	}
	switch state.DrainStatus {
	case runtimeGoplsRootCohortDrainDraining,
		runtimeGoplsRootCohortDrainAttempting,
		runtimeGoplsRootCohortDrainCleanupPending:
	default:
		return
	}
	evidence := runtimeGoplsRootCohortCleanupEvidence{
		Fence: runtimeServerDurableGoplsRootCohortFence{
			Epoch:            state.DrainEpoch,
			JournalRevision:  state.OwnerJournalRevision,
			MemberID:         state.OwnerMemberID,
			MemberGeneration: state.OwnerMemberGeneration,
			LeaseID:          state.OwnerLeaseID,
		},
		IdleDeadlineUnixNano: state.IdleDeadlineUnixNano,
		OwnerPID:             state.OwnerPID,
		OwnerStartIdentity:   state.OwnerStartIdentity,
		Status:               runtimeGoplsRootCohortDrainCleanupPending,
		LastError:            state.LastDrainError,
		RetryUnixNano:        state.DrainRetryUnixNano,
	}
	if evidence.RetryUnixNano == 0 {
		evidence.RetryUnixNano = evidence.IdleDeadlineUnixNano
	}
	for index := range state.PendingCleanups {
		if state.PendingCleanups[index].Fence.Epoch == evidence.Fence.Epoch &&
			state.PendingCleanups[index].Fence.LeaseID == evidence.Fence.LeaseID {
			state.PendingCleanups[index] = evidence
			return
		}
	}
	state.PendingCleanups = append(state.PendingCleanups, evidence)
}

func runtimeServerFindGoplsRootCohortCleanup(state *runtimeServerDurableGoplsRootCohortState, fence multilsp.GoplsRootCohortFence) (int, bool) {
	if state == nil {
		return 0, false
	}
	for index := range state.PendingCleanups {
		if state.PendingCleanups[index].Fence.toValue() == fence {
			return index, true
		}
	}
	return 0, false
}

func runtimeServerClearCurrentGoplsRootCohortDrain(state *runtimeServerDurableGoplsRootCohortState) {
	if state == nil {
		return
	}
	state.IdleDeadlineUnixNano = 0
	state.DrainEpoch = 0
	state.OwnerPID = 0
	state.OwnerStartIdentity = ""
	state.OwnerMemberID = ""
	state.OwnerJournalRevision = 0
	state.OwnerMemberGeneration = 0
	state.OwnerLeaseID = ""
	state.LastDrainError = ""
	state.DrainRetryUnixNano = 0
}

func runtimeServerGoplsRootCohortCleanupEvidenceValid(evidence runtimeGoplsRootCohortCleanupEvidence) error {
	if err := runtimeServerValidateGoplsRootCohortCleanupFence(evidence.Fence); err != nil {
		return err
	}
	if err := runtimeServerValidateGoplsRootCohortCleanupOwner(evidence); err != nil {
		return err
	}
	return runtimeServerValidateGoplsRootCohortCleanupStatus(evidence)
}

// runtimeServerValidateGoplsRootCohortCleanupFence 校验历史 cleanup 的不可变 fence 字段。
func runtimeServerValidateGoplsRootCohortCleanupFence(fence runtimeServerDurableGoplsRootCohortFence) error {
	if fence.Epoch == 0 || fence.JournalRevision == 0 || fence.MemberGeneration == 0 || fence.MemberID == "" || fence.LeaseID == "" {
		return errors.New("gopls root cohort pending cleanup fence is invalid")
	}
	return nil
}

func runtimeServerValidateGoplsRootCohortCleanupOwner(evidence runtimeGoplsRootCohortCleanupEvidence) error {
	if evidence.IdleDeadlineUnixNano <= 0 || evidence.OwnerPID <= 1 || evidence.OwnerStartIdentity == "" {
		return errors.New("gopls root cohort pending cleanup owner evidence is invalid")
	}
	return nil
}

func runtimeServerValidateGoplsRootCohortCleanupStatus(evidence runtimeGoplsRootCohortCleanupEvidence) error {
	switch evidence.Status {
	case runtimeGoplsRootCohortDrainDraining,
		runtimeGoplsRootCohortDrainAttempting,
		runtimeGoplsRootCohortDrainCleanupPending:
	default:
		return errors.New("gopls root cohort pending cleanup status is invalid")
	}
	if evidence.Status == runtimeGoplsRootCohortDrainCleanupPending && evidence.RetryUnixNano <= 0 {
		return errors.New("gopls root cohort pending cleanup retry deadline is invalid")
	}
	return nil
}

func (c *runtimeServerDurableGoplsRootCohortController) cancelPendingDrainForAdmission(config multilsp.GoplsRootCohortConfig) error {
	rootKey := config.RepositoryInstanceProof.CanonicalRootDigest
	pending, ok := c.peekPendingOwnerForRoot(rootKey)
	if !ok {
		return nil
	}
	pending, ok = c.takePendingOwner(rootKey, pending.fence)
	if !ok {
		return nil
	}
	if err := pending.owner(); err != nil {
		c.restorePendingOwner(pending)
		return c.recordDrainFailure(config, pending.fence, pending.owner, err)
	}
	return c.markDrainCompletion(config, pending.fence)
}

// markDrainCompletion 以 fence 为边界提交当前或历史 cleanup 的完成凭据。
func (c *runtimeServerDurableGoplsRootCohortController) markDrainCompletion(config multilsp.GoplsRootCohortConfig, fence multilsp.GoplsRootCohortFence) error {
	_, err := runtimeServerDurableGoplsRootCohortWithStateLock(c, config, func(dir string, state *runtimeServerDurableGoplsRootCohortState) (struct{}, error) {
		if state == nil {
			return struct{}{}, nil
		}
		matched := state.Epoch == fence.Epoch && state.OwnerLeaseID == fence.LeaseID
		if matched {
			state.DrainStatus = runtimeGoplsRootCohortDrainCompleted
			runtimeServerClearCurrentGoplsRootCohortDrain(state)
		} else if index, ok := runtimeServerFindGoplsRootCohortCleanup(state, fence); ok {
			state.PendingCleanups = append(state.PendingCleanups[:index], state.PendingCleanups[index+1:]...)
			matched = true
		}
		if !matched {
			return struct{}{}, nil
		}
		state.CompletionUnixNano = time.Now().UnixNano()
		state.CompletionReceipt = runtimeServerDigestString(strings.Join([]string{
			"gopls-root-drain-complete-v1", fmt.Sprint(fence.Epoch), fence.LeaseID, fmt.Sprint(state.CompletionUnixNano),
		}, "\x00"))
		state.JournalRevision++
		return struct{}{}, runtimeServerWriteGoplsRootCohortState(filepath.Join(dir, "state.json"), *state)
	})
	return err
}

// ValidateFence 校验调用方仍持有当前 durable lease，拒绝陈旧或跨配置 fence。
func (c *runtimeServerDurableGoplsRootCohortController) ValidateFence(config multilsp.GoplsRootCohortConfig, fence multilsp.GoplsRootCohortFence) error {
	if c == nil {
		return multilsp.ErrGoplsRootCohortFenceStale
	}
	if err := config.Validate(); err != nil {
		return err
	}
	if c.isClosed() {
		return multilsp.ErrGoplsRootCohortFenceStale
	}
	_, err := runtimeServerDurableGoplsRootCohortWithStateLock(c, config, func(dir string, state *runtimeServerDurableGoplsRootCohortState) (struct{}, error) {
		return runtimeServerValidateGoplsRootCohortFenceState(dir, state, config, fence)
	})
	return err
}

// runtimeServerValidateGoplsRootCohortFenceState 在状态锁内读取并核对 lease fence。
func runtimeServerValidateGoplsRootCohortFenceState(dir string, state *runtimeServerDurableGoplsRootCohortState, config multilsp.GoplsRootCohortConfig, fence multilsp.GoplsRootCohortFence) (struct{}, error) {
	if state == nil {
		return struct{}{}, multilsp.ErrGoplsRootCohortFenceStale
	}
	stored, err := state.configValue()
	if err != nil {
		return struct{}{}, err
	}
	if !storedEqualGoplsRootCohortConfig(stored, config) {
		return struct{}{}, multilsp.ErrGoplsRootCohortFenceStale
	}
	lease, err := runtimeServerReadGoplsRootCohortLease(runtimeServerGoplsRootCohortLeasePath(dir, fence))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return struct{}{}, multilsp.ErrGoplsRootCohortFenceStale
		}
		return struct{}{}, err
	}
	if lease.ConfigDigest != state.ConfigDigest || lease.Fence.toValue() != fence {
		return struct{}{}, multilsp.ErrGoplsRootCohortFenceStale
	}
	return struct{}{}, nil
}

// Snapshot 返回根 cohort 的 durable 状态和当前活跃 lease 数量。
func (c *runtimeServerDurableGoplsRootCohortController) Snapshot(config multilsp.GoplsRootCohortConfig) (multilsp.GoplsRootCohortSnapshot, bool) {
	if c == nil || config.Validate() != nil {
		return multilsp.GoplsRootCohortSnapshot{}, false
	}
	closed := c.isClosed()
	var snapshot multilsp.GoplsRootCohortSnapshot
	_, err := runtimeServerDurableGoplsRootCohortWithStateLock(c, config, func(dir string, state *runtimeServerDurableGoplsRootCohortState) (struct{}, error) {
		var snapshotErr error
		snapshot, snapshotErr = runtimeServerSnapshotGoplsRootCohortState(dir, state, config, closed)
		return struct{}{}, snapshotErr
	})
	return snapshot, err == nil && snapshot.Config.Validate() == nil
}

func runtimeServerSnapshotGoplsRootCohortState(dir string, state *runtimeServerDurableGoplsRootCohortState, config multilsp.GoplsRootCohortConfig, closed bool) (multilsp.GoplsRootCohortSnapshot, error) {
	if state == nil {
		return multilsp.GoplsRootCohortSnapshot{}, nil
	}
	stored, err := state.configValue()
	if err != nil || !storedEqualGoplsRootCohortConfig(stored, config) {
		return multilsp.GoplsRootCohortSnapshot{}, nil
	}
	active, err := runtimeServerCountGoplsRootCohortLeases(dir, state.ConfigDigest)
	if err != nil {
		return multilsp.GoplsRootCohortSnapshot{}, err
	}
	stateValue := runtimeServerGoplsRootCohortSnapshotState(state, active, closed)
	return multilsp.GoplsRootCohortSnapshot{
		Config:          stored,
		State:           stateValue,
		Epoch:           state.Epoch,
		JournalRevision: state.JournalRevision,
		ActiveMembers:   active,
	}, nil
}

func runtimeServerGoplsRootCohortSnapshotState(state *runtimeServerDurableGoplsRootCohortState, active int, closed bool) multilsp.GoplsRootCohortState {
	stateValue := multilsp.GoplsRootCohortStateIdle
	if active > 0 {
		stateValue = multilsp.GoplsRootCohortStateAdmitted
	}
	if state.DrainStatus == runtimeGoplsRootCohortDrainCleanupPending || len(state.PendingCleanups) > 0 {
		stateValue = multilsp.GoplsRootCohortStateCleanupPending
	}
	if closed {
		stateValue = multilsp.GoplsRootCohortStateClosed
	}
	return stateValue
}

// Close 取消 drain runner，等待受控任务结束，并同步处理仍挂起的 owner。
func (c *runtimeServerDurableGoplsRootCohortController) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	if c.drainCancel != nil {
		c.drainCancel()
	}
	c.drainLaunchMu.Lock()
	c.drainLaunchMu.Unlock()
	c.drainWG.Wait()
	c.drainMu.Lock()
	pending := make([]runtimeServerGoplsRootCohortPendingOwner, 0, len(c.pendingOwner))
	for key, owner := range c.pendingOwner {
		pending = append(pending, owner)
		delete(c.pendingOwner, key)
	}
	c.drainMu.Unlock()
	var closeErr error
	for _, owner := range pending {
		if owner.owner == nil {
			continue
		}
		if err := owner.owner(); err != nil {
			closeErr = errors.Join(closeErr, c.recordDrainFailure(owner.config, owner.fence, owner.owner, err))
			continue
		}
		closeErr = errors.Join(closeErr, c.markDrainCompletion(owner.config, owner.fence))
	}
	return closeErr
}

// runtimeServerGoplsRootCohortReleasePlan records owner work that runs after
// the filesystem lock is released.
type runtimeServerGoplsRootCohortReleasePlan struct {
	closeNow func() error
	schedule bool
}

// release 删除一个 member fence，并为最后 owner 安排固定根级 idle drain。
func (c *runtimeServerDurableGoplsRootCohortController) release(config multilsp.GoplsRootCohortConfig, fence multilsp.GoplsRootCohortFence, owner func() error) error {
	if c == nil {
		return multilsp.ErrGoplsRootCohortFenceStale
	}
	plan, err := runtimeServerDurableGoplsRootCohortWithStateLock(c, config, func(dir string, state *runtimeServerDurableGoplsRootCohortState) (runtimeServerGoplsRootCohortReleasePlan, error) {
		return c.prepareGoplsRootCohortRelease(dir, state, config, fence, owner)
	})
	if err != nil {
		return err
	}
	if plan.closeNow != nil {
		if closeErr := plan.closeNow(); closeErr != nil {
			return c.recordDrainFailure(config, fence, owner, closeErr)
		}
	}
	if plan.schedule {
		c.rememberPendingOwner(config, fence, owner)
		c.startDrainTask("mcp-lsp.gopls-root-cohort.idle-drain", func(ctx context.Context) {
			c.runScheduledDrain(ctx, config, fence)
		})
	}
	return nil
}

// prepareGoplsRootCohortRelease 在状态锁内准备 lease 释放和 owner 后续动作。
func (c *runtimeServerDurableGoplsRootCohortController) prepareGoplsRootCohortRelease(dir string, state *runtimeServerDurableGoplsRootCohortState, config multilsp.GoplsRootCohortConfig, fence multilsp.GoplsRootCohortFence, owner func() error) (runtimeServerGoplsRootCohortReleasePlan, error) {
	lease, err := runtimeServerLoadGoplsRootCohortReleaseLease(dir, state, config, fence)
	if err != nil {
		return runtimeServerGoplsRootCohortReleasePlan{}, err
	}
	path := runtimeServerGoplsRootCohortLeasePath(dir, fence)
	if err := os.Remove(path); err != nil {
		return runtimeServerGoplsRootCohortReleasePlan{}, fmt.Errorf("remove gopls root cohort lease: %w", err)
	}
	if err := runtimeServerSyncGoplsRootCohortDirectory(dir); err != nil {
		return runtimeServerGoplsRootCohortReleasePlan{}, err
	}
	state.JournalRevision++
	active, err := runtimeServerCountGoplsRootCohortLeases(dir, state.ConfigDigest)
	if err != nil {
		return runtimeServerGoplsRootCohortReleasePlan{}, err
	}
	plan := runtimeServerApplyGoplsRootCohortReleaseState(c, state, fence, lease, owner, active)
	if err := runtimeServerWriteGoplsRootCohortState(filepath.Join(dir, "state.json"), *state); err != nil {
		return runtimeServerGoplsRootCohortReleasePlan{}, err
	}
	return plan, nil
}

// runtimeServerLoadGoplsRootCohortReleaseLease 校验不可变配置并读取待释放的精确 lease fence。
func runtimeServerLoadGoplsRootCohortReleaseLease(dir string, state *runtimeServerDurableGoplsRootCohortState, config multilsp.GoplsRootCohortConfig, fence multilsp.GoplsRootCohortFence) (runtimeServerDurableGoplsRootCohortLease, error) {
	if state == nil {
		return runtimeServerDurableGoplsRootCohortLease{}, multilsp.ErrGoplsRootCohortFenceStale
	}
	stored, err := state.configValue()
	if err != nil {
		return runtimeServerDurableGoplsRootCohortLease{}, err
	}
	if !storedEqualGoplsRootCohortConfig(stored, config) {
		return runtimeServerDurableGoplsRootCohortLease{}, multilsp.ErrGoplsRootCohortFenceStale
	}
	lease, err := runtimeServerReadGoplsRootCohortLease(runtimeServerGoplsRootCohortLeasePath(dir, fence))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return runtimeServerDurableGoplsRootCohortLease{}, multilsp.ErrGoplsRootCohortFenceStale
		}
		return runtimeServerDurableGoplsRootCohortLease{}, err
	}
	if lease.ConfigDigest != state.ConfigDigest || lease.Fence.toValue() != fence {
		return runtimeServerDurableGoplsRootCohortLease{}, multilsp.ErrGoplsRootCohortFenceStale
	}
	return lease, nil
}

func runtimeServerApplyGoplsRootCohortReleaseState(c *runtimeServerDurableGoplsRootCohortController, state *runtimeServerDurableGoplsRootCohortState, fence multilsp.GoplsRootCohortFence, lease runtimeServerDurableGoplsRootCohortLease, owner func() error, active int) runtimeServerGoplsRootCohortReleasePlan {
	if active > 0 {
		state.DrainStatus = runtimeGoplsRootCohortDrainActive
		runtimeServerClearCurrentGoplsRootCohortDrain(state)
		return runtimeServerGoplsRootCohortReleasePlan{closeNow: owner}
	}
	if owner == nil {
		state.DrainStatus = runtimeGoplsRootCohortDrainCompleted
		runtimeServerClearCurrentGoplsRootCohortDrain(state)
		state.CompletionUnixNano = time.Now().UnixNano()
		state.CompletionReceipt = runtimeServerDigestString(strings.Join([]string{
			"gopls-root-release-no-forwarder-v1", fmt.Sprint(fence.Epoch), fence.LeaseID, fmt.Sprint(state.CompletionUnixNano),
		}, "\x00"))
		return runtimeServerGoplsRootCohortReleasePlan{}
	}
	state.DrainStatus = runtimeGoplsRootCohortDrainDraining
	state.IdleDeadlineUnixNano = time.Now().Add(c.drainWindow).UnixNano()
	state.DrainEpoch = state.Epoch
	state.OwnerPID = lease.OwnerPID
	state.OwnerStartIdentity = lease.OwnerStartIdentity
	state.OwnerMemberID = fence.MemberID
	state.OwnerJournalRevision = fence.JournalRevision
	state.OwnerMemberGeneration = fence.MemberGeneration
	state.OwnerLeaseID = fence.LeaseID
	return runtimeServerGoplsRootCohortReleasePlan{schedule: true}
}

func (c *runtimeServerDurableGoplsRootCohortController) rememberPendingOwner(config multilsp.GoplsRootCohortConfig, fence multilsp.GoplsRootCohortFence, owner func() error) {
	if c == nil || owner == nil {
		return
	}
	c.drainMu.Lock()
	if c.pendingOwner == nil {
		c.pendingOwner = make(map[string]runtimeServerGoplsRootCohortPendingOwner)
	}
	c.pendingOwner[config.RepositoryInstanceProof.CanonicalRootDigest] = runtimeServerGoplsRootCohortPendingOwner{config: config, fence: fence, owner: owner}
	c.drainMu.Unlock()
}

func (c *runtimeServerDurableGoplsRootCohortController) takePendingOwner(rootKey string, fence multilsp.GoplsRootCohortFence) (runtimeServerGoplsRootCohortPendingOwner, bool) {
	if c == nil {
		return runtimeServerGoplsRootCohortPendingOwner{}, false
	}
	c.drainMu.Lock()
	defer c.drainMu.Unlock()
	pending, ok := c.pendingOwner[rootKey]
	if !ok || pending.fence != fence {
		return runtimeServerGoplsRootCohortPendingOwner{}, false
	}
	delete(c.pendingOwner, rootKey)
	return pending, true
}

func (c *runtimeServerDurableGoplsRootCohortController) restorePendingOwner(pending runtimeServerGoplsRootCohortPendingOwner) {
	if c == nil || pending.owner == nil {
		return
	}
	c.rememberPendingOwner(pending.config, pending.fence, pending.owner)
}

// startDrainTask launches one scheduler under a controller-owned context and
// wait group, so Close can cancel and join every background runner.
func (c *runtimeServerDurableGoplsRootCohortController) startDrainTask(label string, fn func(context.Context)) {
	if c == nil || fn == nil {
		return
	}
	c.drainLaunchMu.Lock()
	defer c.drainLaunchMu.Unlock()
	if c.isClosed() || c.drainCtx == nil {
		return
	}
	c.drainWG.Add(1)
	runtimesafe.SafeGo(c.drainCtx, nil, label, func(ctx context.Context) {
		defer c.drainWG.Done()
		fn(ctx)
	})
}

// persistedGoplsRootCohortDeadline 是唯一 timer authority；worker 不从调度时间重算 deadline。
func (c *runtimeServerDurableGoplsRootCohortController) persistedGoplsRootCohortDeadline(
	config multilsp.GoplsRootCohortConfig,
	fence multilsp.GoplsRootCohortFence,
	retry bool,
) (time.Time, bool, error) {
	type deadlineResult struct {
		unixNano int64
		found    bool
	}
	result, err := runtimeServerDurableGoplsRootCohortWithStateLock(c, config, func(_ string, state *runtimeServerDurableGoplsRootCohortState) (deadlineResult, error) {
		if state == nil {
			return deadlineResult{}, nil
		}
		if state.Epoch == fence.Epoch && state.OwnerLeaseID == fence.LeaseID {
			if retry {
				return deadlineResult{unixNano: state.DrainRetryUnixNano, found: state.DrainRetryUnixNano > 0}, nil
			}
			return deadlineResult{unixNano: state.IdleDeadlineUnixNano, found: state.IdleDeadlineUnixNano > 0}, nil
		}
		if index, ok := runtimeServerFindGoplsRootCohortCleanup(state, fence); ok {
			evidence := state.PendingCleanups[index]
			if retry {
				return deadlineResult{unixNano: evidence.RetryUnixNano, found: evidence.RetryUnixNano > 0}, nil
			}
			return deadlineResult{unixNano: evidence.IdleDeadlineUnixNano, found: evidence.IdleDeadlineUnixNano > 0}, nil
		}
		return deadlineResult{}, nil
	})
	if err != nil || !result.found {
		return time.Time{}, result.found, err
	}
	return time.Unix(0, result.unixNano), true, nil
}

// runScheduledDrain 等待持久化 deadline，再执行一次带 fence 的 owner close。
func (c *runtimeServerDurableGoplsRootCohortController) runScheduledDrain(ctx context.Context, config multilsp.GoplsRootCohortConfig, fence multilsp.GoplsRootCohortFence) {
	pending, ok := c.peekPendingOwner(config.RepositoryInstanceProof.CanonicalRootDigest, fence)
	if !ok {
		return
	}
	deadline, found, err := c.persistedGoplsRootCohortDeadline(config, fence, false)
	if err != nil {
		c.restoreAndRecordDrainFailure(config, fence, pending, err)
		return
	}
	if !found {
		// A newer epoch already fenced this owner without a durable cleanup
		// record. It must not execute a stale callback.
		return
	}
	if err := c.waitUntil(ctx, deadline); err != nil {
		c.restoreAndRecordDrainFailure(config, fence, pending, err)
		return
	}
	if err := ctx.Err(); err != nil {
		c.restorePendingOwner(pending)
		return
	}
	pending, ok = c.takePendingOwner(config.RepositoryInstanceProof.CanonicalRootDigest, fence)
	if !ok {
		return
	}
	if err := c.executeDrain(pending); err != nil {
		c.restoreAndRecordDrainFailure(config, fence, pending, err)
	}
}

func (c *runtimeServerDurableGoplsRootCohortController) restoreAndRecordDrainFailure(config multilsp.GoplsRootCohortConfig, fence multilsp.GoplsRootCohortFence, pending runtimeServerGoplsRootCohortPendingOwner, drainErr error) {
	c.restorePendingOwner(pending)
	if errors.Is(drainErr, context.Canceled) {
		return
	}
	if recordErr := c.recordDrainFailure(config, fence, pending.owner, drainErr); recordErr != nil {
		c.restorePendingOwner(pending)
	}
}

func (c *runtimeServerDurableGoplsRootCohortController) peekPendingOwner(rootKey string, fence multilsp.GoplsRootCohortFence) (runtimeServerGoplsRootCohortPendingOwner, bool) {
	if c == nil {
		return runtimeServerGoplsRootCohortPendingOwner{}, false
	}
	c.drainMu.Lock()
	defer c.drainMu.Unlock()
	pending, ok := c.pendingOwner[rootKey]
	if !ok || pending.fence != fence {
		return runtimeServerGoplsRootCohortPendingOwner{}, false
	}
	return pending, true
}

func (c *runtimeServerDurableGoplsRootCohortController) peekPendingOwnerForRoot(rootKey string) (runtimeServerGoplsRootCohortPendingOwner, bool) {
	if c == nil {
		return runtimeServerGoplsRootCohortPendingOwner{}, false
	}
	c.drainMu.Lock()
	defer c.drainMu.Unlock()
	pending, ok := c.pendingOwner[rootKey]
	return pending, ok
}

func (c *runtimeServerDurableGoplsRootCohortController) waitUntil(ctx context.Context, deadline time.Time) error {
	if c == nil {
		return multilsp.ErrGoplsRootCohortClosed
	}
	delay := time.Until(deadline)
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *runtimeServerDurableGoplsRootCohortController) drainRetryDuration() time.Duration {
	if c != nil && c.drainRetry > 0 {
		return c.drainRetry
	}
	return runtimeGoplsRootCohortDrainRetry
}

// executeDrain 在持有目标 fence 时切换 attempting、执行 owner 并提交完成凭据。
func (c *runtimeServerDurableGoplsRootCohortController) executeDrain(pending runtimeServerGoplsRootCohortPendingOwner) error {
	shouldRun, err := runtimeServerDurableGoplsRootCohortWithStateLock(c, pending.config, func(dir string, state *runtimeServerDurableGoplsRootCohortState) (bool, error) {
		return runtimeServerPrepareGoplsRootCohortDrain(dir, state, pending)
	})
	if err != nil {
		return err
	}
	if !shouldRun {
		return nil
	}
	if err := pending.owner(); err != nil {
		return err
	}
	return c.markDrainCompletion(pending.config, pending.fence)
}

// runtimeServerPrepareGoplsRootCohortDrain 只允许当前 epoch 或保留的历史 cleanup 进入 attempting。
func runtimeServerPrepareGoplsRootCohortDrain(dir string, state *runtimeServerDurableGoplsRootCohortState, pending runtimeServerGoplsRootCohortPendingOwner) (bool, error) {
	if state == nil || pending.owner == nil {
		return false, nil
	}
	if state.Epoch == pending.fence.Epoch && state.DrainEpoch == pending.fence.Epoch && state.OwnerLeaseID == pending.fence.LeaseID {
		return runtimeServerPrepareCurrentGoplsRootCohortDrain(dir, state)
	}
	if index, ok := runtimeServerFindGoplsRootCohortCleanup(state, pending.fence); ok {
		return runtimeServerPreparePendingGoplsRootCohortDrain(dir, state, index)
	}
	// A newer epoch fenced this callback without retaining an old cleanup record.
	return false, nil
}

// runtimeServerPrepareCurrentGoplsRootCohortDrain 检查当前 epoch 无活跃 lease 后持久化 attempting。
func runtimeServerPrepareCurrentGoplsRootCohortDrain(dir string, state *runtimeServerDurableGoplsRootCohortState) (bool, error) {
	if state.DrainStatus != runtimeGoplsRootCohortDrainDraining && state.DrainStatus != runtimeGoplsRootCohortDrainCleanupPending && state.DrainStatus != runtimeGoplsRootCohortDrainAttempting {
		return false, nil
	}
	active, err := runtimeServerCountGoplsRootCohortLeases(dir, state.ConfigDigest)
	if err != nil || active != 0 {
		return false, err
	}
	if state.DrainStatus == runtimeGoplsRootCohortDrainAttempting {
		return true, nil
	}
	state.DrainStatus = runtimeGoplsRootCohortDrainAttempting
	state.JournalRevision++
	if err := runtimeServerWriteGoplsRootCohortState(filepath.Join(dir, "state.json"), *state); err != nil {
		return false, err
	}
	return true, nil
}

func runtimeServerPreparePendingGoplsRootCohortDrain(dir string, state *runtimeServerDurableGoplsRootCohortState, index int) (bool, error) {
	evidence := &state.PendingCleanups[index]
	switch evidence.Status {
	case runtimeGoplsRootCohortDrainDraining, runtimeGoplsRootCohortDrainCleanupPending:
		evidence.Status = runtimeGoplsRootCohortDrainAttempting
		state.JournalRevision++
		if err := runtimeServerWriteGoplsRootCohortState(filepath.Join(dir, "state.json"), *state); err != nil {
			return false, err
		}
		return true, nil
	case runtimeGoplsRootCohortDrainAttempting:
		return true, nil
	default:
		return false, nil
	}
}

// recordDrainFailure 持久化 cleanup_pending 并安排下一次受控重试。
func (c *runtimeServerDurableGoplsRootCohortController) recordDrainFailure(config multilsp.GoplsRootCohortConfig, fence multilsp.GoplsRootCohortFence, owner func() error, drainErr error) error {
	if owner != nil {
		c.rememberPendingOwner(config, fence, owner)
	}
	matched, stateErr := runtimeServerDurableGoplsRootCohortWithStateLock(c, config, func(dir string, state *runtimeServerDurableGoplsRootCohortState) (bool, error) {
		if state == nil {
			return false, nil
		}
		retryUnixNano := time.Now().Add(c.drainRetryDuration()).UnixNano()
		if state.Epoch == fence.Epoch && state.OwnerLeaseID == fence.LeaseID {
			state.OwnerMemberID = fence.MemberID
			state.OwnerJournalRevision = fence.JournalRevision
			state.OwnerMemberGeneration = fence.MemberGeneration
			state.DrainStatus = runtimeGoplsRootCohortDrainCleanupPending
			state.LastDrainError = drainErr.Error()
			state.DrainRetryUnixNano = retryUnixNano
			state.JournalRevision++
			return true, runtimeServerWriteGoplsRootCohortState(filepath.Join(dir, "state.json"), *state)
		}
		if index, ok := runtimeServerFindGoplsRootCohortCleanup(state, fence); ok {
			evidence := &state.PendingCleanups[index]
			evidence.Status = runtimeGoplsRootCohortDrainCleanupPending
			evidence.LastError = drainErr.Error()
			evidence.RetryUnixNano = retryUnixNano
			state.JournalRevision++
			return true, runtimeServerWriteGoplsRootCohortState(filepath.Join(dir, "state.json"), *state)
		}
		return false, nil
	})
	result := errors.Join(multilsp.ErrGoplsRootCohortDrainCleanupPending, drainErr, stateErr)
	if owner != nil && stateErr == nil && matched {
		c.startDrainTask("mcp-lsp.gopls-root-cohort.retry-drain", func(ctx context.Context) {
			c.retryPendingDrain(ctx, config, fence)
		})
	}
	return result
}

// retryPendingDrain 等待持久化重试 deadline，再以同一 owner/fence 重入，不创建新 epoch。
func (c *runtimeServerDurableGoplsRootCohortController) retryPendingDrain(ctx context.Context, config multilsp.GoplsRootCohortConfig, fence multilsp.GoplsRootCohortFence) {
	deadline, found, err := c.persistedGoplsRootCohortDeadline(config, fence, true)
	if err != nil {
		c.recordPendingDrainFailure(config, fence, err)
		return
	}
	if !found {
		return
	}
	if err := c.waitUntil(ctx, deadline); err != nil {
		c.recordPendingDrainFailure(config, fence, err)
		return
	}
	pending, ok := c.takePendingOwner(config.RepositoryInstanceProof.CanonicalRootDigest, fence)
	if !ok {
		return
	}
	if err := ctx.Err(); err != nil {
		c.restorePendingOwner(pending)
		return
	}
	if err := c.executeDrain(pending); err != nil {
		c.restoreAndRecordDrainFailure(config, fence, pending, err)
	}
}

func (c *runtimeServerDurableGoplsRootCohortController) recordPendingDrainFailure(config multilsp.GoplsRootCohortConfig, fence multilsp.GoplsRootCohortFence, drainErr error) {
	if errors.Is(drainErr, context.Canceled) {
		return
	}
	pending, ok := c.peekPendingOwner(config.RepositoryInstanceProof.CanonicalRootDigest, fence)
	if !ok {
		return
	}
	if recordErr := c.recordDrainFailure(config, fence, pending.owner, drainErr); recordErr != nil {
		c.restorePendingOwner(pending)
	}
}
