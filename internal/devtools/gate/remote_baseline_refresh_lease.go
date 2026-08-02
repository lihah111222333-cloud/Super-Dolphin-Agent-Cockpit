package gate

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

var (
	ErrRemoteBaselineRefreshBusy                 = errors.New("remote baseline refresh lease is busy")
	ErrRemoteBaselineRefreshThrottled            = errors.New("remote baseline refresh attempt is throttled")
	ErrRemoteBaselineRefreshLeaseLost            = errors.New("remote baseline refresh lease was lost")
	ErrRemoteBaselineRefreshAcceptedStateChanged = errors.New("remote baseline refresh accepted state changed")
)

// RemoteBaselineRefreshLease binds a worker to one candidate baseline generation.
type RemoteBaselineRefreshLease struct {
	AttemptGeneration    uint64
	AcceptedGeneration   uint64
	AcceptedStateSHA256  string
	TargetGeneration     uint64
	Token                string
	BuilderJobID         string
	TargetTreeSHA        string
	Phase                cicontract.RefreshPhase
	LeaseExpiresAt       time.Time
	ImageCacheName       string
	ImageCacheID         string
	SuccessorImage       string
	SuccessorGeneration  uint64
	SuccessorStateSHA256 string
	RetiringImageCacheID string
}

// ResumeRemoteBaselineRefreshLease rehydrates a still-owned candidate lease from its child-worker token.
func (store *DurationLedgerStore) ResumeRemoteBaselineRefreshLease(token string) (RemoteBaselineRefreshLease, error) {
	if store == nil || strings.TrimSpace(token) == "" {
		return RemoteBaselineRefreshLease{}, errors.New("remote baseline refresh resume token is invalid")
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		return RemoteBaselineRefreshLease{}, err
	}
	defer database.Close()
	tx, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		return RemoteBaselineRefreshLease{}, err
	}
	defer tx.Rollback()
	row, found, err := loadRemoteBaselineRefreshLease(tx)
	if err != nil {
		return RemoteBaselineRefreshLease{}, err
	}
	if !found || row.token != token || !cicontract.IsRefreshCandidatePhase(row.phase) || !store.nowFunc().UTC().Before(row.expiresAt) {
		return RemoteBaselineRefreshLease{}, ErrRemoteBaselineRefreshLeaseLost
	}
	return remoteBaselineRefreshLeaseFromRow(row), nil
}

// ResumeRemoteBaselineRefreshCleanup rehydrates the sole active cleanup owner after a detached-worker restart.
func (store *DurationLedgerStore) ResumeRemoteBaselineRefreshCleanup(token string) (RemoteBaselineRefreshLease, error) {
	if store == nil || strings.TrimSpace(token) == "" {
		return RemoteBaselineRefreshLease{}, errors.New("remote baseline refresh cleanup resume token is invalid")
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		return RemoteBaselineRefreshLease{}, err
	}
	defer database.Close()
	tx, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		return RemoteBaselineRefreshLease{}, err
	}
	defer tx.Rollback()
	row, found, err := loadRemoteBaselineRefreshLease(tx)
	if err != nil {
		return RemoteBaselineRefreshLease{}, err
	}
	if !found || row.phase != cicontract.RefreshRetiring || row.token != token || !store.nowFunc().UTC().Before(row.expiresAt) {
		return RemoteBaselineRefreshLease{}, ErrRemoteBaselineRefreshLeaseLost
	}
	if _, err := requireAcceptedRefreshSuccessor(tx, row.successorGeneration, row.successorStateSHA256); err != nil {
		return RemoteBaselineRefreshLease{}, err
	}
	return remoteBaselineRefreshLeaseFromRow(row), nil
}

// RemoteBaselineRefreshLeaseRequest names the accepted state from which a refresh may proceed.
type RemoteBaselineRefreshLeaseRequest struct {
	AcceptedGeneration  uint64
	AcceptedStateSHA256 string
	LeaseDuration       time.Duration
}

// AcquireRemoteBaselineRefreshLease atomically starts a throttled attempt or resumes an expired active one.
func (store *DurationLedgerStore) AcquireRemoteBaselineRefreshLease(request RemoteBaselineRefreshLeaseRequest) (RemoteBaselineRefreshLease, error) {
	if store == nil || request.AcceptedGeneration == 0 || strings.TrimSpace(request.AcceptedStateSHA256) == "" || request.LeaseDuration <= 0 {
		return RemoteBaselineRefreshLease{}, errors.New("remote baseline refresh lease request is invalid")
	}
	database, err := store.openSQLiteAuthority(true)
	if err != nil {
		return RemoteBaselineRefreshLease{}, err
	}
	defer database.Close()
	now := store.nowFunc().UTC()
	var lease RemoteBaselineRefreshLease
	err = withSQLiteWriteTransaction(database, "acquire remote baseline refresh lease", func(transaction *sql.Tx) error {
		if err := requireAcceptedRemoteBaselineState(transaction, request.AcceptedGeneration, request.AcceptedStateSHA256); err != nil {
			return err
		}
		row, found, err := loadRemoteBaselineRefreshLease(transaction)
		if err != nil {
			return err
		}
		if found && cicontract.IsRefreshCandidatePhase(row.phase) && now.Before(row.expiresAt) {
			return ErrRemoteBaselineRefreshBusy
		}
		if found && cicontract.IsRefreshCandidatePhase(row.phase) && !now.Before(row.expiresAt) {
			if row.acceptedGeneration != request.AcceptedGeneration || row.acceptedStateSHA256 != request.AcceptedStateSHA256 {
				return ErrRemoteBaselineRefreshAcceptedStateChanged
			}
			lease, err = newRemoteBaselineRefreshLease(row.attemptGeneration, request, now)
			if err != nil {
				return err
			}
			lease.ImageCacheName, lease.ImageCacheID = row.imageCacheName, row.imageCacheID
			lease.Phase = row.phase
			result, updateErr := transaction.Exec(`UPDATE ci_remote_baseline_refresh_lease
				SET token=?, lease_expires_at_unix_ms=?
				WHERE singleton=1 AND phase=? AND attempt_generation=? AND token=? AND target_generation=? AND lease_expires_at_unix_ms=?`,
				lease.Token, lease.LeaseExpiresAt.UnixMilli(), row.phase, strconv.FormatUint(lease.AttemptGeneration, 10), row.token, strconv.FormatUint(lease.TargetGeneration, 10), row.expiresAt.UnixMilli())
			if updateErr != nil {
				return updateErr
			}
			return requireRemoteBaselineRefreshLeaseUpdate(result)
		}
		if found && now.Sub(row.startedAt) < cicontract.RefreshMinimumInterval {
			return ErrRemoteBaselineRefreshThrottled
		}
		attempt := uint64(1)
		if found {
			attempt = row.attemptGeneration + 1
		}
		lease, err = newRemoteBaselineRefreshLease(attempt, request, now)
		if err != nil {
			return err
		}
		_, err = transaction.Exec(`INSERT INTO ci_remote_baseline_refresh_lease(
			singleton,schema_version,attempt_generation,accepted_generation,accepted_state_sha256,target_generation,token,phase,
			builder_job_id,target_tree_sha,lease_expires_at_unix_ms,last_started_at_unix_ms,completed_at_unix_ms,image_cache_name,image_cache_id,successor_image,successor_generation,successor_state_sha256,retiring_image_cache_id,failure_text
		) VALUES(1,1,?,?,?,?,?,?, '', '', ?, ?,0,'','','','','','','')
			ON CONFLICT(singleton) DO UPDATE SET schema_version=excluded.schema_version,attempt_generation=excluded.attempt_generation,
			accepted_generation=excluded.accepted_generation,accepted_state_sha256=excluded.accepted_state_sha256,target_generation=excluded.target_generation,
			token=excluded.token,phase=excluded.phase,lease_expires_at_unix_ms=excluded.lease_expires_at_unix_ms,
			builder_job_id='',target_tree_sha='',last_started_at_unix_ms=excluded.last_started_at_unix_ms,completed_at_unix_ms=0,image_cache_name='',image_cache_id='',successor_image='',successor_generation='',successor_state_sha256='',retiring_image_cache_id='',failure_text=''`,
			strconv.FormatUint(lease.AttemptGeneration, 10), strconv.FormatUint(lease.AcceptedGeneration, 10), lease.AcceptedStateSHA256,
			strconv.FormatUint(lease.TargetGeneration, 10), lease.Token, cicontract.RefreshClaimed, lease.LeaseExpiresAt.UnixMilli(), now.UnixMilli())
		return err
	})
	return lease, err
}

// BindRemoteBaselineRefreshLeaseBuilder 将当前 lease 绑定到唯一的 builder job 与目标 tree。
func (store *DurationLedgerStore) BindRemoteBaselineRefreshLeaseBuilder(
	lease RemoteBaselineRefreshLease,
	builderJobID, targetTreeSHA string,
) (RemoteBaselineRefreshLease, error) {
	if store == nil || !validRemoteBaselineRefreshLease(lease) || !cicontract.IsRefreshCandidatePhase(lease.Phase) || strings.TrimSpace(builderJobID) == "" || !validCalibrationOID(targetTreeSHA) {
		return RemoteBaselineRefreshLease{}, errors.New("remote baseline refresh builder binding is invalid")
	}
	database, err := store.openSQLiteAuthority(true)
	if err != nil {
		return RemoteBaselineRefreshLease{}, err
	}
	defer database.Close()
	now := store.nowFunc().UTC()
	err = withSQLiteWriteTransaction(database, "bind remote baseline refresh builder", func(transaction *sql.Tx) error {
		if err := requireAcceptedRemoteBaselineState(transaction, lease.AcceptedGeneration, lease.AcceptedStateSHA256); err != nil {
			return err
		}
		result, err := transaction.Exec(`UPDATE ci_remote_baseline_refresh_lease
			SET builder_job_id=?, target_tree_sha=?
			WHERE singleton=1 AND phase=? AND attempt_generation=? AND accepted_generation=? AND accepted_state_sha256=?
				AND target_generation=? AND token=? AND lease_expires_at_unix_ms=? AND lease_expires_at_unix_ms > ?
				AND ((builder_job_id='' AND target_tree_sha='') OR (builder_job_id=? AND target_tree_sha=?))`,
			builderJobID, targetTreeSHA, lease.Phase, strconv.FormatUint(lease.AttemptGeneration, 10), strconv.FormatUint(lease.AcceptedGeneration, 10), lease.AcceptedStateSHA256,
			strconv.FormatUint(lease.TargetGeneration, 10), lease.Token, lease.LeaseExpiresAt.UnixMilli(), now.UnixMilli(), builderJobID, targetTreeSHA)
		if err != nil {
			return err
		}
		return requireRemoteBaselineRefreshLeaseUpdate(result)
	})
	if err != nil {
		return RemoteBaselineRefreshLease{}, err
	}
	lease.BuilderJobID, lease.TargetTreeSHA = builderJobID, targetTreeSHA
	return lease, nil
}

// HeartbeatRemoteBaselineRefreshLease extends an active worker lease and records its candidate ImageCache identity.
func (store *DurationLedgerStore) HeartbeatRemoteBaselineRefreshLease(lease RemoteBaselineRefreshLease, duration time.Duration, imageCacheName, imageCacheID string) (RemoteBaselineRefreshLease, error) {
	if store == nil || duration <= 0 || !validRemoteBaselineRefreshLease(lease) {
		return RemoteBaselineRefreshLease{}, errors.New("remote baseline refresh heartbeat is invalid")
	}
	database, err := store.openSQLiteAuthority(true)
	if err != nil {
		return RemoteBaselineRefreshLease{}, err
	}
	defer database.Close()
	now := store.nowFunc().UTC()
	previousExpiry := lease.LeaseExpiresAt
	lease.LeaseExpiresAt = now.Add(duration)
	err = withSQLiteWriteTransaction(database, "heartbeat remote baseline refresh lease", func(transaction *sql.Tx) error {
		if err := requireAcceptedRemoteBaselineState(transaction, lease.AcceptedGeneration, lease.AcceptedStateSHA256); err != nil {
			return err
		}
		result, err := transaction.Exec(`UPDATE ci_remote_baseline_refresh_lease SET lease_expires_at_unix_ms=?, image_cache_name=?, image_cache_id=?
			WHERE singleton=1 AND phase=? AND attempt_generation=? AND accepted_generation=? AND accepted_state_sha256=?
			AND target_generation=? AND token=? AND lease_expires_at_unix_ms=? AND lease_expires_at_unix_ms > ?`,
			lease.LeaseExpiresAt.UnixMilli(), imageCacheName, imageCacheID, lease.Phase, strconv.FormatUint(lease.AttemptGeneration, 10), strconv.FormatUint(lease.AcceptedGeneration, 10), lease.AcceptedStateSHA256,
			strconv.FormatUint(lease.TargetGeneration, 10), lease.Token, previousExpiry.UnixMilli(), now.UnixMilli())
		if err != nil {
			return err
		}
		return requireRemoteBaselineRefreshLeaseUpdate(result)
	})
	if err == nil {
		lease.ImageCacheName, lease.ImageCacheID = imageCacheName, imageCacheID
	}
	return lease, err
}

// AdvanceRemoteBaselineRefreshLease persists only the permitted successor candidate state for the owning worker.
func (store *DurationLedgerStore) AdvanceRemoteBaselineRefreshLease(lease RemoteBaselineRefreshLease, next cicontract.RefreshPhase) (RemoteBaselineRefreshLease, error) {
	if store == nil || !validRemoteBaselineRefreshLease(lease) || cicontract.ValidateRefreshTransition(lease.Phase, next) != nil {
		return RemoteBaselineRefreshLease{}, errors.New("remote baseline refresh candidate transition is invalid")
	}
	database, err := store.openSQLiteAuthority(true)
	if err != nil {
		return RemoteBaselineRefreshLease{}, err
	}
	defer database.Close()
	now := store.nowFunc().UTC()
	err = withSQLiteWriteTransaction(database, "advance remote baseline refresh lease", func(transaction *sql.Tx) error {
		if cicontract.IsRefreshCandidatePhase(lease.Phase) {
			if err := requireAcceptedRemoteBaselineState(transaction, lease.AcceptedGeneration, lease.AcceptedStateSHA256); err != nil {
				return err
			}
		}
		result, err := transaction.Exec(`UPDATE ci_remote_baseline_refresh_lease SET phase=? WHERE singleton=1 AND phase=?
			AND attempt_generation=? AND accepted_generation=? AND accepted_state_sha256=? AND target_generation=? AND token=? AND lease_expires_at_unix_ms=? AND lease_expires_at_unix_ms > ?`, next, lease.Phase,
			strconv.FormatUint(lease.AttemptGeneration, 10), strconv.FormatUint(lease.AcceptedGeneration, 10), lease.AcceptedStateSHA256, strconv.FormatUint(lease.TargetGeneration, 10), lease.Token, lease.LeaseExpiresAt.UnixMilli(), now.UnixMilli())
		if err != nil {
			return err
		}
		return requireRemoteBaselineRefreshLeaseUpdate(result)
	})
	if err != nil {
		return RemoteBaselineRefreshLease{}, err
	}
	lease.Phase = next
	return lease, nil
}

// FailRemoteBaselineRefreshLease records a terminal failed attempt only while this worker still owns it.
func (store *DurationLedgerStore) FailRemoteBaselineRefreshLease(lease RemoteBaselineRefreshLease, imageCacheName, imageCacheID, failure string) error {
	if store == nil || !validRemoteBaselineRefreshLease(lease) || !cicontract.IsRefreshCandidatePhase(lease.Phase) || strings.TrimSpace(failure) == "" {
		return errors.New("remote baseline refresh failure completion is invalid")
	}
	database, err := store.openSQLiteAuthority(true)
	if err != nil {
		return err
	}
	defer database.Close()
	now := store.nowFunc().UTC()
	return withSQLiteWriteTransaction(database, "fail remote baseline refresh lease", func(transaction *sql.Tx) error {
		if err := requireAcceptedRemoteBaselineState(transaction, lease.AcceptedGeneration, lease.AcceptedStateSHA256); err != nil {
			return err
		}
		result, err := transaction.Exec(`UPDATE ci_remote_baseline_refresh_lease SET phase=?, completed_at_unix_ms=?, image_cache_name=?, image_cache_id=?, failure_text=?
			WHERE singleton=1 AND phase=? AND attempt_generation=? AND accepted_generation=? AND accepted_state_sha256=? AND target_generation=?
			AND token=? AND lease_expires_at_unix_ms=? AND lease_expires_at_unix_ms > ?`, cicontract.RefreshFailed, now.UnixMilli(), imageCacheName, imageCacheID, failure, lease.Phase,
			strconv.FormatUint(lease.AttemptGeneration, 10), strconv.FormatUint(lease.AcceptedGeneration, 10), lease.AcceptedStateSHA256, strconv.FormatUint(lease.TargetGeneration, 10), lease.Token, lease.LeaseExpiresAt.UnixMilli(), now.UnixMilli())
		if err != nil {
			return err
		}
		return requireRemoteBaselineRefreshLeaseUpdate(result)
	})
}

// PromoteRemoteBaselineStateWithRefreshLease atomically accepts the successor state and completes the owning refresh attempt.
func (store *DurationLedgerStore) PromoteRemoteBaselineStateWithRefreshLease(lease RemoteBaselineRefreshLease, successor RemoteBaselineStateRecord, imageCacheName, imageCacheID string) error {
	if store == nil || !validRemoteBaselineRefreshLease(lease) || lease.Phase != cicontract.RefreshReadyValidated || successor.Generation != lease.TargetGeneration || len(successor.StateJSON) == 0 || strings.TrimSpace(successor.StateSHA256) == "" || strings.TrimSpace(imageCacheName) == "" || strings.TrimSpace(imageCacheID) == "" {
		return errors.New("remote baseline refresh promotion is invalid")
	}
	database, err := store.openSQLiteAuthority(true)
	if err != nil {
		return err
	}
	defer database.Close()
	now := store.nowFunc().UTC()
	return withSQLiteWriteTransaction(database, "promote remote baseline state with refresh lease", func(transaction *sql.Tx) error {
		if err := requireAcceptedRemoteBaselineState(transaction, lease.AcceptedGeneration, lease.AcceptedStateSHA256); err != nil {
			return err
		}
		oldCacheID, err := requireAcceptedRefreshSuccessor(transaction, lease.AcceptedGeneration, lease.AcceptedStateSHA256)
		if err != nil {
			return err
		}
		if oldCacheID == imageCacheID {
			return errors.New("remote baseline refresh cannot retire the accepted successor ImageCache")
		}
		result, err := transaction.Exec(`UPDATE ci_remote_baseline_refresh_lease SET phase=?, completed_at_unix_ms=?, image_cache_name=?, image_cache_id=?, successor_generation=?, successor_state_sha256=?, retiring_image_cache_id=?, failure_text=''
			WHERE singleton=1 AND phase=? AND attempt_generation=? AND accepted_generation=? AND accepted_state_sha256=? AND target_generation=?
			AND token=? AND lease_expires_at_unix_ms=? AND lease_expires_at_unix_ms > ?`, cicontract.RefreshPromoted, now.UnixMilli(), imageCacheName, imageCacheID, strconv.FormatUint(successor.Generation, 10), successor.StateSHA256, oldCacheID, cicontract.RefreshReadyValidated,
			strconv.FormatUint(lease.AttemptGeneration, 10), strconv.FormatUint(lease.AcceptedGeneration, 10), lease.AcceptedStateSHA256, strconv.FormatUint(lease.TargetGeneration, 10), lease.Token, lease.LeaseExpiresAt.UnixMilli(), now.UnixMilli())
		if err != nil {
			return err
		}
		if err := requireRemoteBaselineRefreshLeaseUpdate(result); err != nil {
			return err
		}
		result, err = transaction.Exec(`UPDATE ci_remote_baseline_state SET schema_version=3,generation=?,state_json=?,state_sha256=?,updated_at_unix_ms=? WHERE singleton=1 AND generation=? AND state_sha256=?`,
			strconv.FormatUint(successor.Generation, 10), string(successor.StateJSON), successor.StateSHA256, now.UnixMilli(), strconv.FormatUint(lease.AcceptedGeneration, 10), lease.AcceptedStateSHA256)
		if err != nil {
			return err
		}
		if changed, err := result.RowsAffected(); err != nil {
			return err
		} else if changed != 1 {
			return ErrRemoteBaselineRefreshAcceptedStateChanged
		}
		return nil
	})
}

// ClaimRemoteBaselineRefreshCleanup atomically assigns one worker the persisted
// post-promotion cleanup duty. It never starts a refresh attempt or observes the
// two-hour throttle; a crashed deleter may be taken over after its lease expires.
func (store *DurationLedgerStore) ClaimRemoteBaselineRefreshCleanup(leaseDuration time.Duration) (RemoteBaselineRefreshLease, error) {
	if store == nil || leaseDuration <= 0 {
		return RemoteBaselineRefreshLease{}, errors.New("remote baseline refresh cleanup claim is invalid")
	}
	database, err := store.openSQLiteAuthority(true)
	if err != nil {
		return RemoteBaselineRefreshLease{}, err
	}
	defer database.Close()
	now := store.nowFunc().UTC()
	var claimed RemoteBaselineRefreshLease
	err = withSQLiteWriteTransaction(database, "claim remote baseline refresh cleanup", func(transaction *sql.Tx) error {
		row, found, err := loadRemoteBaselineRefreshLease(transaction)
		if err != nil {
			return err
		}
		if !found || (row.phase != cicontract.RefreshPromoted && row.phase != cicontract.RefreshRetiring && row.phase != cicontract.RefreshCleanupPending) {
			return ErrRemoteBaselineRefreshBusy
		}
		if row.phase == cicontract.RefreshRetiring && now.Before(row.expiresAt) {
			return ErrRemoteBaselineRefreshBusy
		}
		if row.successorGeneration == 0 || strings.TrimSpace(row.successorStateSHA256) == "" || strings.TrimSpace(row.retiringImageCacheID) == "" {
			return errors.New("persisted remote baseline refresh cleanup identity is incomplete")
		}
		currentCacheID, err := requireAcceptedRefreshSuccessor(transaction, row.successorGeneration, row.successorStateSHA256)
		if err != nil {
			return err
		}
		if currentCacheID == row.retiringImageCacheID {
			return errors.New("remote baseline refresh cleanup would delete the accepted ImageCache")
		}
		token, err := newRemoteBaselineRefreshToken()
		if err != nil {
			return err
		}
		expires := now.Add(leaseDuration)
		result, err := transaction.Exec(`UPDATE ci_remote_baseline_refresh_lease SET phase=?, token=?, lease_expires_at_unix_ms=?
			WHERE singleton=1 AND phase=? AND attempt_generation=? AND successor_generation=? AND successor_state_sha256=? AND retiring_image_cache_id=?`,
			cicontract.RefreshRetiring, token, expires.UnixMilli(), row.phase, strconv.FormatUint(row.attemptGeneration, 10), strconv.FormatUint(row.successorGeneration, 10), row.successorStateSHA256, row.retiringImageCacheID)
		if err != nil {
			return err
		}
		if err := requireRemoteBaselineRefreshLeaseUpdate(result); err != nil {
			return err
		}
		row.token, row.phase, row.expiresAt = token, cicontract.RefreshRetiring, expires
		claimed = remoteBaselineRefreshLeaseFromRow(row)
		return nil
	})
	return claimed, err
}

// CompleteRemoteBaselineRefreshCleanup records the delete outcome without changing the accepted successor.
func (store *DurationLedgerStore) CompleteRemoteBaselineRefreshCleanup(lease RemoteBaselineRefreshLease, deleteErr error) error {
	if store == nil || lease.Phase != cicontract.RefreshRetiring || !validRemoteBaselineRefreshLease(lease) || lease.SuccessorGeneration == 0 || strings.TrimSpace(lease.SuccessorStateSHA256) == "" || strings.TrimSpace(lease.RetiringImageCacheID) == "" {
		return errors.New("remote baseline refresh cleanup completion is invalid")
	}
	database, err := store.openSQLiteAuthority(true)
	if err != nil {
		return err
	}
	defer database.Close()
	now := store.nowFunc().UTC()
	return withSQLiteWriteTransaction(database, "complete remote baseline refresh cleanup", func(transaction *sql.Tx) error {
		currentCacheID, err := requireAcceptedRefreshSuccessor(transaction, lease.SuccessorGeneration, lease.SuccessorStateSHA256)
		if err != nil {
			return err
		}
		if currentCacheID == lease.RetiringImageCacheID {
			return errors.New("remote baseline refresh cleanup would delete the accepted ImageCache")
		}
		next, failure := cicontract.RefreshIdle, ""
		if deleteErr != nil {
			next, failure = cicontract.RefreshCleanupPending, deleteErr.Error()
		}
		result, err := transaction.Exec(`UPDATE ci_remote_baseline_refresh_lease SET phase=?, completed_at_unix_ms=?, failure_text=?
			WHERE singleton=1 AND phase=? AND attempt_generation=? AND token=? AND lease_expires_at_unix_ms=? AND lease_expires_at_unix_ms > ? AND successor_generation=? AND successor_state_sha256=? AND retiring_image_cache_id=?`,
			next, now.UnixMilli(), failure, cicontract.RefreshRetiring, strconv.FormatUint(lease.AttemptGeneration, 10), lease.Token, lease.LeaseExpiresAt.UnixMilli(), now.UnixMilli(), strconv.FormatUint(lease.SuccessorGeneration, 10), lease.SuccessorStateSHA256, lease.RetiringImageCacheID)
		if err != nil {
			return err
		}
		return requireRemoteBaselineRefreshLeaseUpdate(result)
	})
}

type remoteBaselineRefreshLeaseRow struct {
	attemptGeneration, acceptedGeneration, targetGeneration uint64
	acceptedStateSHA256, token, builderJobID, targetTreeSHA string
	phase                                                   cicontract.RefreshPhase
	imageCacheName, imageCacheID                            string
	successorGeneration                                     uint64
	successorStateSHA256, retiringImageCacheID              string
	expiresAt, startedAt                                    time.Time
}

func loadRemoteBaselineRefreshLease(transaction *sql.Tx) (remoteBaselineRefreshLeaseRow, bool, error) {
	var row remoteBaselineRefreshLeaseRow
	var attempt, accepted, target, successor string
	var expires, started int64
	err := transaction.QueryRow(`SELECT attempt_generation,accepted_generation,accepted_state_sha256,target_generation,token,builder_job_id,target_tree_sha,phase,lease_expires_at_unix_ms,last_started_at_unix_ms,image_cache_name,image_cache_id,successor_generation,successor_state_sha256,retiring_image_cache_id FROM ci_remote_baseline_refresh_lease WHERE singleton=1`).Scan(&attempt, &accepted, &row.acceptedStateSHA256, &target, &row.token, &row.builderJobID, &row.targetTreeSHA, &row.phase, &expires, &started, &row.imageCacheName, &row.imageCacheID, &successor, &row.successorStateSHA256, &row.retiringImageCacheID)
	if errors.Is(err, sql.ErrNoRows) {
		return row, false, nil
	}
	if err != nil {
		return row, false, err
	}
	var parseErr error
	if row.attemptGeneration, parseErr = strconv.ParseUint(attempt, 10, 64); parseErr == nil {
		row.acceptedGeneration, parseErr = strconv.ParseUint(accepted, 10, 64)
	}
	if parseErr == nil {
		row.targetGeneration, parseErr = strconv.ParseUint(target, 10, 64)
	}
	if successor != "" && parseErr == nil {
		row.successorGeneration, parseErr = strconv.ParseUint(successor, 10, 64)
	}
	if parseErr != nil || row.attemptGeneration == 0 || row.acceptedGeneration == 0 || row.targetGeneration != row.acceptedGeneration+1 || !isRemoteBaselineRefreshPhase(row.phase) {
		return row, false, errors.New("stored remote baseline refresh lease is invalid")
	}
	row.expiresAt, row.startedAt = time.UnixMilli(expires).UTC(), time.UnixMilli(started).UTC()
	return row, true, nil
}

func remoteBaselineRefreshLeaseFromRow(row remoteBaselineRefreshLeaseRow) RemoteBaselineRefreshLease {
	return RemoteBaselineRefreshLease{AttemptGeneration: row.attemptGeneration, AcceptedGeneration: row.acceptedGeneration, AcceptedStateSHA256: row.acceptedStateSHA256, TargetGeneration: row.targetGeneration, Token: row.token, BuilderJobID: row.builderJobID, TargetTreeSHA: row.targetTreeSHA, Phase: row.phase, LeaseExpiresAt: row.expiresAt, ImageCacheName: row.imageCacheName, ImageCacheID: row.imageCacheID, SuccessorGeneration: row.successorGeneration, SuccessorStateSHA256: row.successorStateSHA256, RetiringImageCacheID: row.retiringImageCacheID}
}

func requireAcceptedRemoteBaselineState(transaction *sql.Tx, generation uint64, digest string) error {
	var storedGeneration, storedDigest string
	err := transaction.QueryRow(`SELECT generation,state_sha256 FROM ci_remote_baseline_state WHERE singleton=1`).Scan(&storedGeneration, &storedDigest)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRemoteBaselineRefreshAcceptedStateChanged
	}
	if err != nil {
		return err
	}
	if storedGeneration != strconv.FormatUint(generation, 10) || storedDigest != digest {
		return ErrRemoteBaselineRefreshAcceptedStateChanged
	}
	return nil
}

// requireAcceptedRefreshSuccessor binds cleanup to the successor that won promotion and returns its ImageCache ID.
func requireAcceptedRefreshSuccessor(transaction *sql.Tx, generation uint64, digest string) (string, error) {
	var stateJSON string
	var storedGeneration, storedDigest string
	if err := transaction.QueryRow(`SELECT generation,state_sha256,state_json FROM ci_remote_baseline_state WHERE singleton=1`).Scan(&storedGeneration, &storedDigest, &stateJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrRemoteBaselineRefreshAcceptedStateChanged
		}
		return "", err
	}
	if storedGeneration != strconv.FormatUint(generation, 10) || storedDigest != digest {
		return "", ErrRemoteBaselineRefreshAcceptedStateChanged
	}
	var state struct {
		ImageCacheID string `json:"image_cache_id"`
	}
	if err := json.Unmarshal([]byte(stateJSON), &state); err != nil || strings.TrimSpace(state.ImageCacheID) == "" {
		return "", errors.New("accepted remote baseline state has no ImageCache ID")
	}
	return state.ImageCacheID, nil
}

func newRemoteBaselineRefreshLease(attempt uint64, request RemoteBaselineRefreshLeaseRequest, now time.Time) (RemoteBaselineRefreshLease, error) {
	if request.AcceptedGeneration == ^uint64(0) {
		return RemoteBaselineRefreshLease{}, errors.New("remote baseline refresh target generation overflows")
	}
	token, err := newRemoteBaselineRefreshToken()
	if err != nil {
		return RemoteBaselineRefreshLease{}, err
	}
	return RemoteBaselineRefreshLease{AttemptGeneration: attempt, AcceptedGeneration: request.AcceptedGeneration, AcceptedStateSHA256: request.AcceptedStateSHA256, TargetGeneration: request.AcceptedGeneration + 1, Token: token, Phase: cicontract.RefreshClaimed, LeaseExpiresAt: now.Add(request.LeaseDuration)}, nil
}

func newRemoteBaselineRefreshToken() (string, error) {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("generate remote baseline refresh token: %w", err)
	}
	return hex.EncodeToString(bytes[:]), nil
}
func validRemoteBaselineRefreshLease(lease RemoteBaselineRefreshLease) bool {
	return lease.AttemptGeneration > 0 && lease.AcceptedGeneration > 0 && lease.TargetGeneration == lease.AcceptedGeneration+1 && strings.TrimSpace(lease.AcceptedStateSHA256) != "" && strings.TrimSpace(lease.Token) != "" && isRemoteBaselineRefreshPhase(lease.Phase) && !lease.LeaseExpiresAt.IsZero()
}
func requireRemoteBaselineRefreshLeaseUpdate(result sql.Result) error {
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return ErrRemoteBaselineRefreshLeaseLost
	}
	return nil
}

func isRemoteBaselineRefreshPhase(phase cicontract.RefreshPhase) bool {
	return cicontract.IsRefreshCandidatePhase(phase) || phase == cicontract.RefreshUnchanged || phase == cicontract.RefreshPromoted || phase == cicontract.RefreshRetiring || phase == cicontract.RefreshCleanupPending || phase == cicontract.RefreshFailed || phase == cicontract.RefreshIdle
}
