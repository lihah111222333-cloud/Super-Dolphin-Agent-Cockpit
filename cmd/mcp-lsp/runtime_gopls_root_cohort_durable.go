package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
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
	mu           sync.Mutex
	closed       bool
	root         string
	drainWindow  time.Duration
	drainRetry   time.Duration
	drainMu      sync.Mutex
	pendingOwner map[string]runtimeServerGoplsRootCohortPendingOwner
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
	return &runtimeServerDurableGoplsRootCohortController{
		root:         root,
		drainWindow:  window,
		drainRetry:   runtimeGoplsRootCohortDrainRetry,
		pendingOwner: make(map[string]runtimeServerGoplsRootCohortPendingOwner),
	}, nil
}

func (c *runtimeServerDurableGoplsRootCohortController) AcquireLease(config multilsp.GoplsRootCohortConfig) (multilsp.GoplsRootCohortLease, error) {
	if c == nil {
		return multilsp.GoplsRootCohortLease{}, errors.New("durable gopls root cohort controller is nil")
	}
	if err := config.Validate(); err != nil {
		return multilsp.GoplsRootCohortLease{}, err
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return multilsp.GoplsRootCohortLease{}, multilsp.ErrGoplsRootCohortClosed
	}
	if err := c.cancelPendingDrainForAdmission(config); err != nil {
		return multilsp.GoplsRootCohortLease{}, err
	}

	fence, err := runtimeServerDurableGoplsRootCohortWithStateLock(c, config, func(dir string, state *runtimeServerDurableGoplsRootCohortState) (multilsp.GoplsRootCohortFence, error) {
		if state == nil {
			state = &runtimeServerDurableGoplsRootCohortState{
				SchemaVersion: runtimeGoplsRootCohortSchemaVersion,
				ConfigDigest:  multilsp.DigestGoplsRootCohortConfig(config),
				Config:        runtimeServerDurableGoplsRootCohortConfigFrom(config),
				Epoch:         1,
				DrainStatus:   runtimeGoplsRootCohortDrainActive,
			}
		} else {
			stored, stateErr := state.configValue()
			if stateErr != nil {
				return multilsp.GoplsRootCohortFence{}, stateErr
			}
			if !storedEqualGoplsRootCohortConfig(stored, config) {
				return multilsp.GoplsRootCohortFence{}, fmt.Errorf(
					"%w for canonical root proof %s",
					multilsp.ErrGoplsRootCohortConfigConflict,
					config.RepositoryInstanceProof.CanonicalRootDigest,
				)
			}
		}
		if err := runtimeServerCleanupGoplsRootCohortLeases(dir, state.ConfigDigest); err != nil {
			return multilsp.GoplsRootCohortFence{}, err
		}
		active, err := runtimeServerCountGoplsRootCohortLeases(dir, state.ConfigDigest)
		if err != nil {
			return multilsp.GoplsRootCohortFence{}, err
		}
		if active == 0 && state.JournalRevision > 0 {
			// Admission is fenced atomically with the old idle drain. A sidecar
			// that cannot reach the previous callback must not be blocked for the
			// entire 15 minute window; preserve the old owner evidence in a
			// separate typed journal entry and let only that old owner perform
			// cleanup when its deadline arrives.
			runtimeServerPromoteGoplsRootCohortDrain(state)
			state.Epoch++
			if state.Epoch == 0 {
				return multilsp.GoplsRootCohortFence{}, errors.New("gopls root cohort epoch overflow")
			}
		}
		state.DrainStatus = runtimeGoplsRootCohortDrainActive
		runtimeServerClearCurrentGoplsRootCohortDrain(state)
		state.NextMemberGeneration++
		state.NextSequence++
		state.JournalRevision++
		fence := multilsp.GoplsRootCohortFence{
			Epoch:            state.Epoch,
			JournalRevision:  state.JournalRevision,
			MemberID:         fmt.Sprintf("member-%d", state.NextSequence),
			MemberGeneration: state.NextMemberGeneration,
			LeaseID:          fmt.Sprintf("lease-%d", state.NextSequence),
		}
		if err := runtimeServerWriteGoplsRootCohortState(filepath.Join(dir, "state.json"), *state); err != nil {
			return multilsp.GoplsRootCohortFence{}, err
		}
		ownerStart, err := hiddenexec.ProcessStartIdentity(os.Getpid())
		if err != nil {
			return multilsp.GoplsRootCohortFence{}, fmt.Errorf("read gopls root cohort owner start identity: %w", err)
		}
		lease := runtimeServerDurableGoplsRootCohortLease{
			SchemaVersion:      runtimeGoplsRootCohortSchemaVersion,
			ConfigDigest:       state.ConfigDigest,
			OwnerPID:           os.Getpid(),
			OwnerStartIdentity: ownerStart,
			CreatedAtUnixNano:  time.Now().UnixNano(),
			Fence:              runtimeServerDurableGoplsRootCohortFenceFrom(fence),
		}
		if lease.CreatedAtUnixNano <= 0 {
			return multilsp.GoplsRootCohortFence{}, errors.New("gopls root cohort lease creation time is invalid")
		}
		if err := runtimeServerCreateGoplsRootCohortLease(runtimeServerGoplsRootCohortLeasePath(dir, fence), lease); err != nil {
			return multilsp.GoplsRootCohortFence{}, err
		}
		if err := runtimeServerSyncGoplsRootCohortDirectory(dir); err != nil {
			return multilsp.GoplsRootCohortFence{}, err
		}
		return fence, nil
	})
	if err != nil {
		return multilsp.GoplsRootCohortLease{}, err
	}
	lease, err := multilsp.NewGoplsRootCohortLeaseFromAuthorityWithOwner(
		config,
		fence,
		func() error { return c.release(config, fence, nil) },
		func(owner func() error) error { return c.release(config, fence, owner) },
	)
	if err != nil {
		return multilsp.GoplsRootCohortLease{}, err
	}
	return lease, nil
}

// runtimeServerPromoteGoplsRootCohortDrain fences the current drain before a
// new epoch is admitted. The callback remains in the old controller's memory;
// this journal only records which old fence is still responsible for cleanup.
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
	if evidence.Fence.Epoch == 0 || evidence.Fence.JournalRevision == 0 ||
		evidence.Fence.MemberGeneration == 0 || evidence.Fence.MemberID == "" || evidence.Fence.LeaseID == "" {
		return errors.New("gopls root cohort pending cleanup fence is invalid")
	}
	if evidence.IdleDeadlineUnixNano <= 0 || evidence.OwnerPID <= 1 || evidence.OwnerStartIdentity == "" {
		return errors.New("gopls root cohort pending cleanup owner evidence is invalid")
	}
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

func (c *runtimeServerDurableGoplsRootCohortController) ValidateFence(config multilsp.GoplsRootCohortConfig, fence multilsp.GoplsRootCohortFence) error {
	if c == nil {
		return multilsp.ErrGoplsRootCohortFenceStale
	}
	if err := config.Validate(); err != nil {
		return err
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return multilsp.ErrGoplsRootCohortFenceStale
	}
	_, err := runtimeServerDurableGoplsRootCohortWithStateLock(c, config, func(dir string, state *runtimeServerDurableGoplsRootCohortState) (struct{}, error) {
		if state == nil {
			return struct{}{}, multilsp.ErrGoplsRootCohortFenceStale
		}
		stored, stateErr := state.configValue()
		if stateErr != nil {
			return struct{}{}, stateErr
		}
		if !storedEqualGoplsRootCohortConfig(stored, config) {
			return struct{}{}, multilsp.ErrGoplsRootCohortFenceStale
		}
		lease, leaseErr := runtimeServerReadGoplsRootCohortLease(runtimeServerGoplsRootCohortLeasePath(dir, fence))
		if leaseErr != nil {
			if errors.Is(leaseErr, os.ErrNotExist) {
				return struct{}{}, multilsp.ErrGoplsRootCohortFenceStale
			}
			return struct{}{}, leaseErr
		}
		if lease.ConfigDigest != state.ConfigDigest || lease.Fence.toValue() != fence {
			return struct{}{}, multilsp.ErrGoplsRootCohortFenceStale
		}
		return struct{}{}, nil
	})
	return err
}

func (c *runtimeServerDurableGoplsRootCohortController) Snapshot(config multilsp.GoplsRootCohortConfig) (multilsp.GoplsRootCohortSnapshot, bool) {
	if c == nil || config.Validate() != nil {
		return multilsp.GoplsRootCohortSnapshot{}, false
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	var snapshot multilsp.GoplsRootCohortSnapshot
	_, err := runtimeServerDurableGoplsRootCohortWithStateLock(c, config, func(dir string, state *runtimeServerDurableGoplsRootCohortState) (struct{}, error) {
		if state == nil {
			return struct{}{}, nil
		}
		stored, stateErr := state.configValue()
		if stateErr != nil || !storedEqualGoplsRootCohortConfig(stored, config) {
			return struct{}{}, nil
		}
		active, activeErr := runtimeServerCountGoplsRootCohortLeases(dir, state.ConfigDigest)
		if activeErr != nil {
			return struct{}{}, activeErr
		}
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
		snapshot = multilsp.GoplsRootCohortSnapshot{
			Config:          stored,
			State:           stateValue,
			Epoch:           state.Epoch,
			JournalRevision: state.JournalRevision,
			ActiveMembers:   active,
		}
		return struct{}{}, nil
	})
	return snapshot, err == nil && snapshot.Config.Validate() == nil
}

func (c *runtimeServerDurableGoplsRootCohortController) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
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

func (c *runtimeServerDurableGoplsRootCohortController) release(config multilsp.GoplsRootCohortConfig, fence multilsp.GoplsRootCohortFence, owner func() error) error {
	if c == nil {
		return multilsp.ErrGoplsRootCohortFenceStale
	}
	var closeNow func() error
	var schedule bool
	_, err := runtimeServerDurableGoplsRootCohortWithStateLock(c, config, func(dir string, state *runtimeServerDurableGoplsRootCohortState) (struct{}, error) {
		if state == nil {
			return struct{}{}, multilsp.ErrGoplsRootCohortFenceStale
		}
		stored, stateErr := state.configValue()
		if stateErr != nil {
			return struct{}{}, stateErr
		}
		if !storedEqualGoplsRootCohortConfig(stored, config) {
			return struct{}{}, multilsp.ErrGoplsRootCohortFenceStale
		}
		path := runtimeServerGoplsRootCohortLeasePath(dir, fence)
		lease, leaseErr := runtimeServerReadGoplsRootCohortLease(path)
		if leaseErr != nil {
			if errors.Is(leaseErr, os.ErrNotExist) {
				return struct{}{}, multilsp.ErrGoplsRootCohortFenceStale
			}
			return struct{}{}, leaseErr
		}
		if lease.ConfigDigest != state.ConfigDigest || lease.Fence.toValue() != fence {
			return struct{}{}, multilsp.ErrGoplsRootCohortFenceStale
		}
		if err := os.Remove(path); err != nil {
			return struct{}{}, fmt.Errorf("remove gopls root cohort lease: %w", err)
		}
		if err := runtimeServerSyncGoplsRootCohortDirectory(dir); err != nil {
			return struct{}{}, err
		}
		state.JournalRevision++
		active, countErr := runtimeServerCountGoplsRootCohortLeases(dir, state.ConfigDigest)
		if countErr != nil {
			return struct{}{}, countErr
		}
		if active > 0 {
			state.DrainStatus = runtimeGoplsRootCohortDrainActive
			runtimeServerClearCurrentGoplsRootCohortDrain(state)
			closeNow = owner
		} else {
			if owner == nil {
				// No owner means the client failed before a forwarder was published;
				// release the admission immediately without inventing a daemon drain.
				state.DrainStatus = runtimeGoplsRootCohortDrainCompleted
				runtimeServerClearCurrentGoplsRootCohortDrain(state)
				state.CompletionUnixNano = time.Now().UnixNano()
				state.CompletionReceipt = runtimeServerDigestString(strings.Join([]string{
					"gopls-root-release-no-forwarder-v1", fmt.Sprint(fence.Epoch), fence.LeaseID, fmt.Sprint(state.CompletionUnixNano),
				}, "\x00"))
			} else {
				state.DrainStatus = runtimeGoplsRootCohortDrainDraining
				state.IdleDeadlineUnixNano = time.Now().Add(c.drainWindow).UnixNano()
				state.DrainEpoch = state.Epoch
				state.OwnerPID = lease.OwnerPID
				state.OwnerStartIdentity = lease.OwnerStartIdentity
				state.OwnerMemberID = fence.MemberID
				state.OwnerJournalRevision = fence.JournalRevision
				state.OwnerMemberGeneration = fence.MemberGeneration
				state.OwnerLeaseID = fence.LeaseID
				schedule = true
			}
		}
		if err := runtimeServerWriteGoplsRootCohortState(filepath.Join(dir, "state.json"), *state); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, nil
	})
	if err != nil {
		return err
	}
	if closeNow != nil {
		if closeErr := closeNow(); closeErr != nil {
			return c.recordDrainFailure(config, fence, owner, closeErr)
		}
	}
	if schedule {
		c.rememberPendingOwner(config, fence, owner)
		go c.runScheduledDrain(config, fence)
		return nil
	}
	return nil
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

// persistedGoplsRootCohortDeadline is the only timer authority. Workers never
// recompute a deadline from their own scheduling time, so a sidecar restart or
// a delayed goroutine cannot extend the configured idle window.
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

func (c *runtimeServerDurableGoplsRootCohortController) runScheduledDrain(config multilsp.GoplsRootCohortConfig, fence multilsp.GoplsRootCohortFence) {
	pending, ok := c.peekPendingOwner(config.RepositoryInstanceProof.CanonicalRootDigest, fence)
	if !ok {
		return
	}
	deadline, found, err := c.persistedGoplsRootCohortDeadline(config, fence, false)
	if err != nil {
		c.restorePendingOwner(pending)
		if recordErr := c.recordDrainFailure(config, fence, pending.owner, err); recordErr != nil {
			c.restorePendingOwner(pending)
		}
		return
	}
	if !found {
		// A newer epoch already fenced this owner without a durable cleanup
		// record. It must not execute a stale callback.
		return
	}
	if err := c.waitUntil(deadline); err != nil {
		c.restorePendingOwner(pending)
		if recordErr := c.recordDrainFailure(config, fence, pending.owner, err); recordErr != nil {
			c.restorePendingOwner(pending)
		}
		return
	}
	pending, ok = c.takePendingOwner(config.RepositoryInstanceProof.CanonicalRootDigest, fence)
	if !ok {
		return
	}
	if err := c.executeDrain(pending); err != nil {
		c.restorePendingOwner(pending)
		if recordErr := c.recordDrainFailure(config, fence, pending.owner, err); recordErr != nil {
			c.restorePendingOwner(pending)
		}
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

func (c *runtimeServerDurableGoplsRootCohortController) waitUntil(deadline time.Time) error {
	if c == nil {
		return multilsp.ErrGoplsRootCohortClosed
	}
	delay := time.Until(deadline)
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	<-timer.C
	return nil
}

func (c *runtimeServerDurableGoplsRootCohortController) drainRetryDuration() time.Duration {
	if c != nil && c.drainRetry > 0 {
		return c.drainRetry
	}
	return runtimeGoplsRootCohortDrainRetry
}

func (c *runtimeServerDurableGoplsRootCohortController) executeDrain(pending runtimeServerGoplsRootCohortPendingOwner) error {
	config, fence := pending.config, pending.fence
	shouldRun, err := runtimeServerDurableGoplsRootCohortWithStateLock(c, config, func(dir string, state *runtimeServerDurableGoplsRootCohortState) (bool, error) {
		if state == nil || pending.owner == nil {
			return false, nil
		}
		if state.Epoch == fence.Epoch && state.DrainEpoch == fence.Epoch && state.OwnerLeaseID == fence.LeaseID {
			if state.DrainStatus != runtimeGoplsRootCohortDrainDraining && state.DrainStatus != runtimeGoplsRootCohortDrainCleanupPending && state.DrainStatus != runtimeGoplsRootCohortDrainAttempting {
				return false, nil
			}
			active, countErr := runtimeServerCountGoplsRootCohortLeases(dir, state.ConfigDigest)
			if countErr != nil {
				return false, countErr
			}
			if active != 0 {
				return false, nil
			}
			if state.DrainStatus != runtimeGoplsRootCohortDrainAttempting {
				state.DrainStatus = runtimeGoplsRootCohortDrainAttempting
				state.JournalRevision++
				if err := runtimeServerWriteGoplsRootCohortState(filepath.Join(dir, "state.json"), *state); err != nil {
					return false, err
				}
			}
			return true, nil
		}
		if index, ok := runtimeServerFindGoplsRootCohortCleanup(state, fence); ok {
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
		// A newer epoch fenced this callback without retaining an old cleanup
		// record. Never close a forwarder from a stale callback in that case.
		return false, nil
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
	return c.markDrainCompletion(config, fence)
}

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
		go c.retryPendingDrain(config, fence)
	}
	return result
}

func (c *runtimeServerDurableGoplsRootCohortController) retryPendingDrain(config multilsp.GoplsRootCohortConfig, fence multilsp.GoplsRootCohortFence) {
	deadline, found, err := c.persistedGoplsRootCohortDeadline(config, fence, true)
	if err != nil {
		if pending, ok := c.peekPendingOwner(config.RepositoryInstanceProof.CanonicalRootDigest, fence); ok {
			if recordErr := c.recordDrainFailure(config, fence, pending.owner, err); recordErr != nil {
				c.restorePendingOwner(pending)
			}
		}
		return
	}
	if !found {
		return
	}
	if err := c.waitUntil(deadline); err != nil {
		if pending, ok := c.peekPendingOwner(config.RepositoryInstanceProof.CanonicalRootDigest, fence); ok {
			if recordErr := c.recordDrainFailure(config, fence, pending.owner, err); recordErr != nil {
				c.restorePendingOwner(pending)
			}
		}
		return
	}
	pending, ok := c.takePendingOwner(config.RepositoryInstanceProof.CanonicalRootDigest, fence)
	if !ok {
		return
	}
	if err := c.executeDrain(pending); err != nil {
		c.restorePendingOwner(pending)
		if recordErr := c.recordDrainFailure(config, fence, pending.owner, err); recordErr != nil {
			c.restorePendingOwner(pending)
		}
	}
}

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

func runtimeServerGoplsRootCohortDir(root string, config multilsp.GoplsRootCohortConfig) string {
	key := runtimeServerDigestString("gopls-root-cohort-path-v1\x00" + config.RepositoryInstanceProof.CanonicalRootDigest)
	return filepath.Join(root, "gopls-root-cohorts", key)
}

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

func storedEqualGoplsRootCohortConfig(left, right multilsp.GoplsRootCohortConfig) bool {
	return left.CohortID == right.CohortID &&
		left.EffectiveConfigDigest == right.EffectiveConfigDigest &&
		left.RepositoryInstanceProof == right.RepositoryInstanceProof
}

func runtimeServerDurableGoplsRootCohortFenceFrom(fence multilsp.GoplsRootCohortFence) runtimeServerDurableGoplsRootCohortFence {
	return runtimeServerDurableGoplsRootCohortFence{
		Epoch: fence.Epoch, JournalRevision: fence.JournalRevision, MemberID: fence.MemberID,
		MemberGeneration: fence.MemberGeneration, LeaseID: fence.LeaseID,
	}
}

func (f runtimeServerDurableGoplsRootCohortFence) toValue() multilsp.GoplsRootCohortFence {
	return multilsp.GoplsRootCohortFence{
		Epoch: f.Epoch, JournalRevision: f.JournalRevision, MemberID: f.MemberID,
		MemberGeneration: f.MemberGeneration, LeaseID: f.LeaseID,
	}
}

func runtimeServerGoplsRootCohortLeasePath(dir string, fence multilsp.GoplsRootCohortFence) string {
	return filepath.Join(dir, "lease-"+runtimeServerDigestString("gopls-root-lease-v1\x00"+fence.LeaseID)+".json")
}

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

func runtimeServerReadGoplsRootCohortLease(path string) (runtimeServerDurableGoplsRootCohortLease, error) {
	var lease runtimeServerDurableGoplsRootCohortLease
	if err := runtimeServerReadGoplsRootCohortJSON(path, &lease, 16*1024); err != nil {
		return lease, err
	}
	if lease.SchemaVersion != runtimeGoplsRootCohortSchemaVersion || lease.ConfigDigest == "" ||
		lease.OwnerPID <= 1 || lease.OwnerStartIdentity == "" || lease.CreatedAtUnixNano <= 0 ||
		lease.Fence.Epoch == 0 || lease.Fence.JournalRevision == 0 || lease.Fence.MemberGeneration == 0 ||
		lease.Fence.MemberID == "" || lease.Fence.LeaseID == "" {
		return lease, errors.New("gopls root cohort lease is invalid")
	}
	return lease, nil
}

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

func runtimeServerWriteGoplsRootCohortState(path string, state runtimeServerDurableGoplsRootCohortState) error {
	return runtimeServerWriteGoplsRootCohortJSON(path, state)
}

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

func runtimeServerSyncGoplsRootCohortDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open gopls root cohort directory for sync: %w", err)
	}
	return errors.Join(dir.Sync(), dir.Close())
}

// runtimeServerCleanupGoplsRootCohortLeases removes leases whose owner process
// has exited or whose PID has been reused. A crashed sidecar therefore cannot
// retain an active member indefinitely or keep a root daemon alive forever.
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
		path := filepath.Join(dir, entry.Name())
		lease, readErr := runtimeServerReadGoplsRootCohortLease(path)
		if readErr != nil {
			cleanupErr = errors.Join(cleanupErr, readErr)
			continue
		}
		if lease.ConfigDigest != configDigest {
			cleanupErr = errors.Join(cleanupErr, errors.New("gopls root cohort lease config digest mismatch"))
			continue
		}
		alive, aliveErr := hiddenexec.ProcessAlive(lease.OwnerPID)
		if aliveErr != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("check gopls root cohort lease owner: %w", aliveErr))
			continue
		}
		if alive {
			ownerStart, startErr := hiddenexec.ProcessStartIdentity(lease.OwnerPID)
			if startErr != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("verify gopls root cohort lease owner: %w", startErr))
				continue
			}
			if ownerStart == lease.OwnerStartIdentity {
				continue
			}
		}
		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove stale gopls root cohort lease: %w", removeErr))
		} else {
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
