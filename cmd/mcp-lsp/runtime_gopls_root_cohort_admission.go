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
	stored, err := state.configValue()
	if err != nil {
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
		if active != 0 || !runtimeServerGoplsRootCohortConfigRotationAllowed(state) {
			return runtimeServerGoplsRootCohortConfigConflict(config)
		}
		return runtimeServerRotateGoplsRootCohortConfig(state, config)
	}
	return runtimeServerAdvanceGoplsRootCohortEpoch(state, active)
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
	if state == nil || len(state.PendingCleanups) != 0 {
		return false
	}
	if state.DrainStatus != runtimeGoplsRootCohortDrainActive && state.DrainStatus != runtimeGoplsRootCohortDrainCompleted {
		return false
	}
	return state.IdleDeadlineUnixNano == 0 &&
		state.DrainEpoch == 0 &&
		state.OwnerPID == 0 &&
		state.OwnerStartIdentity == "" &&
		state.OwnerMemberID == "" &&
		state.OwnerJournalRevision == 0 &&
		state.OwnerMemberGeneration == 0 &&
		state.OwnerLeaseID == "" &&
		state.LastDrainError == "" &&
		state.DrainRetryUnixNano == 0
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
