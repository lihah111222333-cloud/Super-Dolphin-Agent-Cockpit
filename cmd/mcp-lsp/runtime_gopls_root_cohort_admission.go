package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
)

// AcquireLease 将一个 forwarder 纳入不可变 root cohort epoch。
// 状态、fence 和 owner-only lease 的发布共享同一 filesystem lock。
func (c *runtimeServerDurableGoplsRootCohortController) AcquireLease(config multilsp.GoplsRootCohortConfig) (multilsp.GoplsRootCohortLease, error) {
	if c == nil {
		return multilsp.GoplsRootCohortLease{}, errors.New("durable gopls root cohort controller is nil")
	}
	if err := config.Validate(); err != nil {
		return multilsp.GoplsRootCohortLease{}, err
	}
	if c.isClosed() {
		return multilsp.GoplsRootCohortLease{}, multilsp.ErrGoplsRootCohortClosed
	}
	if err := c.cancelPendingDrainForAdmission(config); err != nil {
		return multilsp.GoplsRootCohortLease{}, err
	}
	fence, err := runtimeServerDurableGoplsRootCohortWithStateLock(c, config, func(dir string, state *runtimeServerDurableGoplsRootCohortState) (multilsp.GoplsRootCohortFence, error) {
		return runtimeServerAdmitGoplsRootCohortMember(dir, config, state)
	})
	if err != nil {
		return multilsp.GoplsRootCohortLease{}, err
	}
	lease, err := multilsp.NewGoplsRootCohortLeaseFromAuthorityWithOwner(config, fence,
		func() error { return c.release(config, fence, nil) },
		func(owner func() error) error { return c.release(config, fence, owner) })
	if err != nil {
		return multilsp.GoplsRootCohortLease{}, err
	}
	return lease, nil
}

// isClosed 读取 controller 的本地关闭闸门，避免在关闭后继续 admission。
func (c *runtimeServerDurableGoplsRootCohortController) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// runtimeServerAdmitGoplsRootCohortMember 在持有 durable 锁时完成状态、fence
// 和 lease 文件的原子发布。
func runtimeServerAdmitGoplsRootCohortMember(dir string, config multilsp.GoplsRootCohortConfig, state *runtimeServerDurableGoplsRootCohortState) (multilsp.GoplsRootCohortFence, error) {
	newState := state == nil
	if newState {
		state = &runtimeServerDurableGoplsRootCohortState{}
	}
	if err := runtimeServerPrepareGoplsRootCohortState(dir, config, state, newState); err != nil {
		return multilsp.GoplsRootCohortFence{}, err
	}
	fence, err := runtimeServerAdvanceGoplsRootCohortState(state)
	if err != nil {
		return multilsp.GoplsRootCohortFence{}, err
	}
	if err := runtimeServerWriteGoplsRootCohortState(filepath.Join(dir, "state.json"), *state); err != nil {
		return multilsp.GoplsRootCohortFence{}, err
	}
	lease, err := runtimeServerNewGoplsRootCohortLease(state, fence)
	if err != nil {
		return multilsp.GoplsRootCohortFence{}, err
	}
	if err := runtimeServerCreateGoplsRootCohortLease(runtimeServerGoplsRootCohortLeasePath(dir, fence), lease); err != nil {
		return multilsp.GoplsRootCohortFence{}, err
	}
	if err := runtimeServerSyncGoplsRootCohortDirectory(dir); err != nil {
		return multilsp.GoplsRootCohortFence{}, err
	}
	return fence, nil
}

// runtimeServerPrepareGoplsRootCohortState 初始化首个状态或校验旧配置，并
// 在 admission 前回收已明确失效的 lease、推进旧 drain 的 epoch fence。
func runtimeServerPrepareGoplsRootCohortState(dir string, config multilsp.GoplsRootCohortConfig, state *runtimeServerDurableGoplsRootCohortState, newState bool) error {
	if state == nil {
		return errors.New("gopls root cohort state is nil")
	}
	if newState {
		runtimeServerInitializeGoplsRootCohortState(state, config)
		return nil
	}
	return runtimeServerPrepareExistingGoplsRootCohortState(dir, config, state)
}

// runtimeServerPrepareExistingGoplsRootCohortState 读取并校验已有状态，
// 在同一持久锁内完成失效租约清理和配置代际决策。
func runtimeServerPrepareExistingGoplsRootCohortState(dir string, config multilsp.GoplsRootCohortConfig, state *runtimeServerDurableGoplsRootCohortState) error {
	stored, err := state.configValue()
	if err != nil {
		return err
	}
	if err := runtimeServerRetireStaleGoplsRootCohortCleanupEvidence(state); err != nil {
		return err
	}
	storedDigest := state.ConfigDigest
	if err := runtimeServerCleanupGoplsRootCohortLeases(dir, storedDigest); err != nil {
		return err
	}
	active, err := runtimeServerCountGoplsRootCohortLeases(dir, storedDigest)
	if err != nil {
		return err
	}
	if !storedEqualGoplsRootCohortConfig(stored, config) {
		return runtimeServerResolveGoplsRootCohortConfigChange(state, config, active)
	}
	return runtimeServerAdvanceGoplsRootCohortEpoch(state, active)
}

// runtimeServerRetireStaleGoplsRootCohortCleanupEvidence 仅退役被 boot 边界或
// PID+start identity 复核严格证明已失效的 owner。该路径不发送任何信号；
// 仍存活或无法证明的 evidence 继续作为配置轮换 blocker。
func runtimeServerRetireStaleGoplsRootCohortCleanupEvidence(state *runtimeServerDurableGoplsRootCohortState) error {
	if state == nil {
		return errors.New("gopls root cohort state is nil")
	}
	retireCurrent, err := runtimeServerGoplsRootCohortCurrentOwnerStale(state)
	if err != nil {
		return err
	}
	remaining, err := runtimeServerFilterLiveGoplsRootCohortCleanups(state.PendingCleanups)
	if err != nil {
		return err
	}
	runtimeServerApplyGoplsRootCohortCleanupRetirement(state, retireCurrent, remaining)
	return nil
}

func runtimeServerGoplsRootCohortCurrentOwnerStale(state *runtimeServerDurableGoplsRootCohortState) (bool, error) {
	if state.OwnerStartIdentity == "" {
		return false, nil
	}
	stale, err := runtimeServerGoplsRootCohortOwnerStale(state.OwnerPID, state.OwnerStartIdentity)
	if err != nil {
		return false, fmt.Errorf("verify current gopls root cohort owner identity: %w", err)
	}
	return stale, nil
}

func runtimeServerFilterLiveGoplsRootCohortCleanups(cleanups []runtimeGoplsRootCohortCleanupEvidence) ([]runtimeGoplsRootCohortCleanupEvidence, error) {
	remaining := make([]runtimeGoplsRootCohortCleanupEvidence, 0, len(cleanups))
	for _, evidence := range cleanups {
		stale, err := runtimeServerGoplsRootCohortOwnerStale(evidence.OwnerPID, evidence.OwnerStartIdentity)
		if err != nil {
			return nil, fmt.Errorf("verify pending gopls root cohort owner identity for lease %s: %w", evidence.Fence.LeaseID, err)
		}
		if !stale {
			remaining = append(remaining, evidence)
		}
	}
	return remaining, nil
}

func runtimeServerGoplsRootCohortOwnerStale(ownerPID int, ownerStartIdentity string) (bool, error) {
	if ownerPID <= 1 || ownerStartIdentity == "" {
		return false, errors.New("gopls root cohort cleanup owner evidence is invalid")
	}
	preBoot, err := hiddenexec.ProcessStartIdentityPredatesCurrentBoot(ownerStartIdentity)
	if err != nil || preBoot {
		return preBoot, err
	}
	alive, err := hiddenexec.ProcessAlive(ownerPID)
	if err != nil || !alive {
		return !alive, err
	}
	currentStart, err := hiddenexec.ProcessStartIdentity(ownerPID)
	if err != nil {
		return false, err
	}
	return currentStart != ownerStartIdentity, nil
}

func runtimeServerApplyGoplsRootCohortCleanupRetirement(state *runtimeServerDurableGoplsRootCohortState, retireCurrent bool, remaining []runtimeGoplsRootCohortCleanupEvidence) {
	if retireCurrent {
		state.DrainStatus = runtimeGoplsRootCohortDrainActive
		state.CompletionReceipt = ""
		state.CompletionUnixNano = 0
		runtimeServerClearCurrentGoplsRootCohortDrain(state)
	}
	state.PendingCleanups = remaining
}

// runtimeServerResolveGoplsRootCohortConfigChange 依据活跃租约和状态证明，
// 只在安全的空闲边界切换配置代际，否则返回不可变配置冲突。
func runtimeServerResolveGoplsRootCohortConfigChange(state *runtimeServerDurableGoplsRootCohortState, config multilsp.GoplsRootCohortConfig, active int) error {
	if active != 0 || !runtimeServerGoplsRootCohortConfigRotationAllowed(state) {
		return runtimeServerGoplsRootCohortConfigConflict(config)
	}
	return runtimeServerRotateGoplsRootCohortConfig(state, config)
}

func runtimeServerInitializeGoplsRootCohortState(state *runtimeServerDurableGoplsRootCohortState, config multilsp.GoplsRootCohortConfig) {
	*state = runtimeServerDurableGoplsRootCohortState{
		SchemaVersion: runtimeGoplsRootCohortSchemaVersion,
		ConfigDigest:  multilsp.DigestGoplsRootCohortConfig(config),
		Config:        runtimeServerDurableGoplsRootCohortConfigFrom(config),
		Epoch:         1,
		DrainStatus:   runtimeGoplsRootCohortDrainActive,
	}
}

func runtimeServerGoplsRootCohortConfigConflict(config multilsp.GoplsRootCohortConfig) error {
	return fmt.Errorf("%w for canonical root proof %s", multilsp.ErrGoplsRootCohortConfigConflict, config.RepositoryInstanceProof.CanonicalRootDigest)
}

// runtimeServerGoplsRootCohortConfigRotationAllowed 只允许已证明没有活跃 member、
// 当前 drain owner 或历史 cleanup owner 的状态进入下一配置代际。
func runtimeServerGoplsRootCohortConfigRotationAllowed(state *runtimeServerDurableGoplsRootCohortState) bool {
	if state == nil || !runtimeServerGoplsRootCohortCompletionEvidenceValid(state) {
		return false
	}
	return !runtimeServerGoplsRootCohortHasRotationBlocker(state)
}

// runtimeServerGoplsRootCohortCompletionEvidenceValid 校验可轮换终态与完成凭据一致。
func runtimeServerGoplsRootCohortCompletionEvidenceValid(state *runtimeServerDurableGoplsRootCohortState) bool {
	switch state.DrainStatus {
	case runtimeGoplsRootCohortDrainActive:
		return (state.CompletionReceipt == "" && state.CompletionUnixNano == 0) ||
			(state.CompletionReceipt != "" && state.CompletionUnixNano > 0)
	case runtimeGoplsRootCohortDrainCompleted:
		return state.CompletionReceipt != "" && state.CompletionUnixNano > 0
	default:
		return false
	}
}

// runtimeServerGoplsRootCohortHasRotationBlocker 汇总所有尚未清理的 owner、fence 与 retry 证据。
func runtimeServerGoplsRootCohortHasRotationBlocker(state *runtimeServerDurableGoplsRootCohortState) bool {
	for _, value := range []string{
		state.OwnerStartIdentity,
		state.OwnerMemberID,
		state.OwnerLeaseID,
		state.LastDrainError,
	} {
		if value != "" {
			return true
		}
	}
	for _, value := range []uint64{
		state.DrainEpoch,
		state.OwnerJournalRevision,
		state.OwnerMemberGeneration,
	} {
		if value != 0 {
			return true
		}
	}
	return len(state.PendingCleanups) != 0 ||
		state.IdleDeadlineUnixNano != 0 ||
		state.OwnerPID != 0 ||
		state.DrainRetryUnixNano != 0
}

// runtimeServerRotateGoplsRootCohortConfig 在同一 canonical root 锁内切换到下一
// immutable 配置代际，并保留单调 fence 计数，避免旧 fence 在新配置中重放。
func runtimeServerRotateGoplsRootCohortConfig(state *runtimeServerDurableGoplsRootCohortState, config multilsp.GoplsRootCohortConfig) error {
	nextEpoch := state.Epoch + 1
	if nextEpoch == 0 {
		return errors.New("gopls root cohort config generation overflow")
	}
	journalRevision := state.JournalRevision
	nextMemberGeneration := state.NextMemberGeneration
	nextSequence := state.NextSequence
	runtimeServerInitializeGoplsRootCohortState(state, config)
	state.Epoch = nextEpoch
	state.JournalRevision = journalRevision
	state.NextMemberGeneration = nextMemberGeneration
	state.NextSequence = nextSequence
	return nil
}

func runtimeServerAdvanceGoplsRootCohortEpoch(state *runtimeServerDurableGoplsRootCohortState, active int) error {
	if active != 0 || state.JournalRevision == 0 {
		return nil
	}
	runtimeServerPromoteGoplsRootCohortDrain(state)
	state.Epoch++
	if state.Epoch == 0 {
		return errors.New("gopls root cohort epoch overflow")
	}
	return nil
}

func runtimeServerAdvanceGoplsRootCohortState(state *runtimeServerDurableGoplsRootCohortState) (multilsp.GoplsRootCohortFence, error) {
	state.DrainStatus = runtimeGoplsRootCohortDrainActive
	runtimeServerClearCurrentGoplsRootCohortDrain(state)
	state.NextMemberGeneration++
	state.NextSequence++
	state.JournalRevision++
	if state.NextMemberGeneration == 0 || state.NextSequence == 0 || state.JournalRevision == 0 {
		return multilsp.GoplsRootCohortFence{}, errors.New("gopls root cohort admission sequence overflow")
	}
	return multilsp.GoplsRootCohortFence{Epoch: state.Epoch, JournalRevision: state.JournalRevision, MemberID: fmt.Sprintf("member-%d", state.NextSequence), MemberGeneration: state.NextMemberGeneration, LeaseID: fmt.Sprintf("lease-%d", state.NextSequence)}, nil
}

func runtimeServerNewGoplsRootCohortLease(state *runtimeServerDurableGoplsRootCohortState, fence multilsp.GoplsRootCohortFence) (runtimeServerDurableGoplsRootCohortLease, error) {
	ownerStart, err := hiddenexec.ProcessStartIdentity(os.Getpid())
	if err != nil {
		return runtimeServerDurableGoplsRootCohortLease{}, fmt.Errorf("read gopls root cohort owner start identity: %w", err)
	}
	createdAt := time.Now().UnixNano()
	if createdAt <= 0 {
		return runtimeServerDurableGoplsRootCohortLease{}, errors.New("gopls root cohort lease creation time is invalid")
	}
	return runtimeServerDurableGoplsRootCohortLease{SchemaVersion: runtimeGoplsRootCohortSchemaVersion, ConfigDigest: state.ConfigDigest, OwnerPID: os.Getpid(), OwnerStartIdentity: ownerStart, CreatedAtUnixNano: createdAt, Fence: runtimeServerDurableGoplsRootCohortFenceFrom(fence)}, nil
}
