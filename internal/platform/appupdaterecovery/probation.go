package appupdaterecovery

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

const maxProbationLeaseTTL = 24 * time.Hour

var (
	// ErrNoActiveProbation 表示事务已经终态或尚未进入 probation。
	ErrNoActiveProbation = errors.New("no active update probation")
	// ErrProbationLeaseMismatch 表示调用方没有当前 exact lease。
	ErrProbationLeaseMismatch = errors.New("update probation lease mismatch")
	// ErrProbationRollbackRequiresUnclaimed 表示无 lease probation 必须走锁内 unclaimed CAS。
	ErrProbationRollbackRequiresUnclaimed = errors.New("update probation rollback requires unclaimed CAS")
	// ErrHealthyCommitRequiresClaimed 表示 healthy commit 必须由 current lease owner 完成。
	ErrHealthyCommitRequiresClaimed = errors.New("healthy commit requires current claimed probation lease")
	// ErrProbationLeaseNotExpired 表示 Guard 不能接管仍有效的 lease。
	ErrProbationLeaseNotExpired = errors.New("update probation lease is not expired")
)

// ProbationLeaseRequest 是首次 supervisor lease 的完整输入。
type ProbationLeaseRequest struct {
	OwnerID string
	Process ProcessIdentity
	TTL     time.Duration
}

// AcquireProbationLease 为 exact probation 创建 generation 1 lease。
func (store *Store) AcquireProbationLease(ctx context.Context, identity Identity, req ProbationLeaseRequest) (ProbationLease, error) {
	if err := validateLeaseRequest(req); err != nil {
		return ProbationLease{}, err
	}
	var lease ProbationLease
	_, err := store.withExact(ctx, identity, func(journal *journalPayload) error {
		if journal.transaction().State != StateProbation {
			return ErrNoActiveProbation
		}
		if journal.Probation.LeasePresent {
			return ErrProbationLeaseMismatch
		}
		now := store.now().UTC()
		lease = newProbationLease(req.OwnerID, 1, req.Process, now, req.TTL)
		journal.Probation.LeasePresent = true
		journal.Probation.Lease = lease
		return store.writeLocked(*journal)
	})
	return lease, err
}

// TakeOverProbationLease 仅允许在 expected lease 到期后以 generation+1 接管。
func (store *Store) TakeOverProbationLease(
	ctx context.Context,
	identity Identity,
	expected ProbationLease,
	ownerID string,
	now time.Time,
	ttl time.Duration,
) (ProbationLease, error) {
	if strings.TrimSpace(ownerID) == "" {
		return ProbationLease{}, errors.New("probation takeover owner is required")
	}
	if err := validateLeaseTTL(ttl); err != nil {
		return ProbationLease{}, err
	}
	var replacement ProbationLease
	_, err := store.withExact(ctx, identity, func(journal *journalPayload) error {
		if journal.transaction().State != StateProbation {
			return ErrNoActiveProbation
		}
		if !journal.Probation.LeasePresent || journal.Probation.Lease != expected {
			return ErrProbationLeaseMismatch
		}
		expiresAt, err := parseProbationTime("lease expiry", expected.ExpiresAt)
		if err != nil {
			return err
		}
		now = now.UTC()
		if now.Before(expiresAt) {
			return ErrProbationLeaseNotExpired
		}
		replacement = newProbationLease(ownerID, expected.Generation+1, expected.Process, now, ttl)
		journal.Probation.Lease = replacement
		journal.Probation.ACKPresent = false
		journal.Probation.ACK = HealthyACK{}
		return store.writeLocked(*journal)
	})
	return replacement, err
}

// RecordHealthyACK 持久化与 current lease 完全一致的 candidate ACK。
func (store *Store) RecordHealthyACK(
	ctx context.Context,
	identity Identity,
	lease ProbationLease,
	ack HealthyACK,
) (Transaction, error) {
	return store.withExact(ctx, identity, func(journal *journalPayload) error {
		if journal.transaction().State != StateProbation {
			return ErrNoActiveProbation
		}
		if !journal.Probation.LeasePresent || journal.Probation.Lease != lease {
			return ErrProbationLeaseMismatch
		}
		if err := validateHealthyACK(journal.Identity, lease, ack); err != nil {
			return err
		}
		if journal.Probation.ACKPresent {
			if journal.Probation.ACK != ack {
				return errors.New("conflicting update probation ACK")
			}
			return nil
		}
		journal.Probation.ACKPresent = true
		journal.Probation.ACK = ack
		return store.writeLocked(*journal)
	})
}

// commitHealthyClaimed 只允许同包 supervisor 以 current lease 在 exact ACK 后提交。
func (store *Store) commitHealthyClaimed(ctx context.Context, identity Identity, lease ProbationLease) (Transaction, error) {
	return store.withExact(ctx, identity, func(journal *journalPayload) error {
		if !journal.Probation.LeasePresent || journal.Probation.Lease != lease {
			return ErrProbationLeaseMismatch
		}
		if !journal.Probation.ACKPresent {
			return errors.New("healthy commit requires exact probation ACK")
		}
		return store.commitHealthyLocked(ctx, journal)
	})
}

// RollbackClaimed 只允许 current lease 对 exact transaction 执行回滚。
func (store *Store) RollbackClaimed(ctx context.Context, identity Identity, lease ProbationLease) (Transaction, error) {
	return store.withExact(ctx, identity, func(journal *journalPayload) error {
		if !journal.Probation.LeasePresent || journal.Probation.Lease != lease {
			return ErrProbationLeaseMismatch
		}
		return store.rollbackLocked(ctx, journal)
	})
}

// RollbackUnclaimedProbation 只在锁内确认 probation 尚无 lease 时执行 exact rollback。
func (store *Store) RollbackUnclaimedProbation(ctx context.Context, identity Identity) (Transaction, error) {
	return store.withExact(ctx, identity, func(journal *journalPayload) error {
		if journal.transaction().State != StateProbation {
			return ErrNoActiveProbation
		}
		if journal.Probation.LeasePresent {
			return ErrProbationLeaseMismatch
		}
		return store.rollbackLocked(ctx, journal)
	})
}

// BuildHealthyACK 从已校验 transaction 和 current process 生成 exact ACK。
func BuildHealthyACK(transaction Transaction, process ProcessIdentity, now time.Time) HealthyACK {
	return HealthyACK{
		TransactionID:    transaction.Identity.TransactionID,
		AttemptID:        transaction.Identity.AttemptID,
		CandidateRelease: transaction.Identity.CandidateRelease,
		Process:          process,
		AcknowledgedAt:   now.UTC().Format(time.RFC3339Nano),
	}
}

func newProbationLease(owner string, generation uint64, process ProcessIdentity, now time.Time, ttl time.Duration) ProbationLease {
	return ProbationLease{
		OwnerID: owner, Generation: generation, Process: process,
		AcquiredAt: now.Format(time.RFC3339Nano),
		ExpiresAt:  now.Add(ttl).Format(time.RFC3339Nano),
	}
}

func validateLeaseRequest(req ProbationLeaseRequest) error {
	if strings.TrimSpace(req.OwnerID) == "" {
		return errors.New("probation lease owner is required")
	}
	if err := validateProcessIdentity(req.Process); err != nil {
		return err
	}
	return validateLeaseTTL(req.TTL)
}

func validateLeaseTTL(ttl time.Duration) error {
	if ttl <= 0 || ttl > maxProbationLeaseTTL {
		return fmt.Errorf("probation lease TTL must be within (0,%s]", maxProbationLeaseTTL)
	}
	return nil
}

// validateProcessIdentity 校验 candidate 的稳定进程身份与完整协作终止契约。
func validateProcessIdentity(process ProcessIdentity) error {
	if process.PID <= 0 {
		return errors.New("candidate process PID must be positive")
	}
	if strings.TrimSpace(process.StartToken) == "" {
		return errors.New("candidate process start token is required")
	}
	if strings.TrimSpace(process.ExecutableIdentity) == "" {
		return errors.New("candidate process executable identity is required")
	}
	hasEndpoint := strings.TrimSpace(process.TerminationEndpoint) != ""
	hasToken := strings.TrimSpace(process.TerminationToken) != ""
	if hasEndpoint != hasToken {
		return errors.New("candidate cooperative termination contract is partial")
	}
	if hasEndpoint {
		if !filepath.IsAbs(process.TerminationEndpoint) ||
			filepath.Clean(process.TerminationEndpoint) != process.TerminationEndpoint {
			return errors.New("candidate process termination endpoint must be absolute and clean")
		}
		if !validLowerHex(process.TerminationToken, 64) {
			return errors.New("candidate process termination token must be 64 lowercase hex characters")
		}
	}
	if err := validateReleaseIdentity("candidate executable", ReleaseIdentity{
		SHA256: process.ExecutableSHA256, SignerIdentity: "process",
	}); err != nil {
		return err
	}
	return nil
}

// validateHealthyACK 校验 ACK 的 transaction/release/process exact identity 与 lease 时间窗。
func validateHealthyACK(identity Identity, lease ProbationLease, ack HealthyACK) error {
	if ack.TransactionID != identity.TransactionID ||
		ack.AttemptID != identity.AttemptID ||
		ack.CandidateRelease != identity.CandidateRelease ||
		ack.Process != lease.Process {
		return ErrIdentityMismatch
	}
	ackAt, err := parseProbationTime("ACK", ack.AcknowledgedAt)
	if err != nil {
		return err
	}
	acquiredAt, err := parseProbationTime("lease acquired", lease.AcquiredAt)
	if err != nil {
		return err
	}
	expiresAt, err := parseProbationTime("lease expiry", lease.ExpiresAt)
	if err != nil {
		return err
	}
	if ackAt.Before(acquiredAt) || ackAt.After(expiresAt) {
		return errors.New("probation ACK timestamp is outside current lease")
	}
	return nil
}

// validateProbationRecord 校验 presence 位、lease 和 ACK 的持久组合。
func validateProbationRecord(journal journalPayload) error {
	record := journal.Probation
	if !record.LeasePresent {
		if record.Lease != (ProbationLease{}) || record.ACKPresent || record.ACK != (HealthyACK{}) {
			return errors.New("probation record has data without a lease")
		}
		return nil
	}
	if err := validatePersistedLease(record.Lease); err != nil {
		return err
	}
	if record.ACKPresent {
		if err := validateHealthyACK(journal.Identity, record.Lease, record.ACK); err != nil {
			return err
		}
	} else if !reflect.ValueOf(record.ACK).IsZero() {
		return errors.New("probation record has ACK data without presence")
	}
	return nil
}

// validatePersistedLease 校验持久 lease 的 owner、generation、进程和有界时间窗。
func validatePersistedLease(lease ProbationLease) error {
	if strings.TrimSpace(lease.OwnerID) == "" || lease.Generation == 0 {
		return errors.New("probation lease owner and generation are required")
	}
	if err := validateProcessIdentity(lease.Process); err != nil {
		return err
	}
	acquiredAt, err := parseProbationTime("lease acquired", lease.AcquiredAt)
	if err != nil {
		return err
	}
	expiresAt, err := parseProbationTime("lease expiry", lease.ExpiresAt)
	if err != nil {
		return err
	}
	if !expiresAt.After(acquiredAt) {
		return errors.New("probation lease expiry must follow acquisition")
	}
	return nil
}

func parseProbationTime(name string, value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse probation %s: %w", name, err)
	}
	return parsed, nil
}
