package accesswait

import (
	"context"
	"errors"
	"fmt"
	"time"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

// DefaultGrantExpiryBatchLimit bounds one expiry pass. A later pass can pick
// up remaining rows; no caller may turn the candidate scan into an unbounded
// read by supplying an oversized limit.
const DefaultGrantExpiryBatchLimit int64 = 64

// GrantExpiryCandidate is the immutable, narrow snapshot needed to invoke the
// existing ExpireGrant CAS. It deliberately does not copy authorization data.
type GrantExpiryCandidate struct {
	GrantID          string
	ExpectedRevision int64
	ExpiresAt        time.Time
}

// ListDueGrantCandidates enumerates active grants due at securityNow using a
// bounded sqlc query. securityNow must be supplied by the external security-
// time owner; this method never reads the process wall clock.
// ListDueGrantCandidates 使用安全时间快照读取有界的到期授权候选。
func (s *Store) ListDueGrantCandidates(ctx context.Context, securityNow time.Time, limit int64) ([]GrantExpiryCandidate, error) {
	if err := s.ready(ctx); err != nil {
		return nil, err
	}
	now, err := validateSecurityExpiryTime(securityNow)
	if err != nil {
		return nil, err
	}
	if err := validateGrantExpiryLimit(limit); err != nil {
		return nil, err
	}
	rows, err := s.queries.ListOrchestrationAccessGrantExpiryCandidates(ctx, storedb.ListOrchestrationAccessGrantExpiryCandidatesParams{
		NowAt: utcText(now), Limit: limit,
	})
	if err != nil {
		return nil, classifyDBError("list access grant expiry candidates", err)
	}
	candidates := make([]GrantExpiryCandidate, 0, len(rows))
	for _, row := range rows {
		expiresAt, parseErr := parseUTC("grant expiry candidate expires_at", row.ExpiresAt)
		if parseErr != nil {
			return nil, fmt.Errorf("grant %q expiry candidate: %w", row.GrantID, parseErr)
		}
		if row.GrantID == "" || row.Revision <= 0 || expiresAt.After(now) {
			return nil, fmt.Errorf("grant %q expiry candidate is invalid", row.GrantID)
		}
		candidates = append(candidates, GrantExpiryCandidate{
			GrantID: row.GrantID, ExpectedRevision: row.Revision, ExpiresAt: expiresAt,
		})
	}
	return candidates, nil
}

// ExpireDueGrants performs one bounded expiry pass. Each candidate is fed to
// the existing ExpireGrant owner with the exact durable revision and expiry
// timestamp. A competing revoke/expiry loses its CAS and is harmless; any
// other error stops the pass and returns the successful count so far.
// ExpireDueGrants 使用候选快照执行一次有界 CAS 到期收敛。
func (s *Store) ExpireDueGrants(ctx context.Context, securityNow time.Time, limit int64) (int64, error) {
	if err := s.ready(ctx); err != nil {
		return 0, err
	}
	now, err := validateSecurityExpiryTime(securityNow)
	if err != nil {
		return 0, err
	}
	if err := validateGrantExpiryLimit(limit); err != nil {
		return 0, err
	}
	candidates, err := s.ListDueGrantCandidates(ctx, now, limit)
	if err != nil {
		return 0, err
	}

	// ExpireGrant validates request.ExpiredAt against Store.now. Use a copy
	// bound to this same security-time snapshot so a Store constructed with a
	// legacy wall-clock default cannot accidentally authorize this candidate.
	securityStore := *s
	securityStore.now = func() time.Time { return now }
	var expired int64
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return expired, err
		}
		won, expireErr := expireDueGrantCandidate(ctx, &securityStore, s, candidate)
		if expireErr != nil {
			return expired, expireErr
		}
		if won {
			expired++
		}
	}
	return expired, nil
}

// expireDueGrantCandidate 以同一安全时间快照尝试单个候选，并将可预期的
// revoke/expiry CAS 竞争转换为无副作用的跳过。
func expireDueGrantCandidate(ctx context.Context, securityStore, readStore *Store, candidate GrantExpiryCandidate) (bool, error) {
	_, expireErr := securityStore.ExpireGrant(ctx, ExpireGrantRequest{
		GrantID: candidate.GrantID, ExpectedRevision: candidate.ExpectedRevision,
		ExpiredAt: candidate.ExpiresAt,
	})
	if expireErr == nil {
		return true, nil
	}
	if errors.Is(expireErr, context.Canceled) || errors.Is(expireErr, context.DeadlineExceeded) {
		return false, expireErr
	}
	lost, raceErr := readStore.expiryCandidateCASLost(ctx, candidate, expireErr)
	if raceErr != nil {
		return false, raceErr
	}
	if lost {
		return false, nil
	}
	return false, expireErr
}

// expiryCandidateCASLost distinguishes an expected stale candidate from a
// real expiry failure. It performs a read-only recheck and never retries a
// lifecycle write, preventing repeated side effects after a CAS race.
// expiryCandidateCASLost 只读复核候选是否因生命周期 CAS 竞争而失效。
func (s *Store) expiryCandidateCASLost(ctx context.Context, candidate GrantExpiryCandidate, cause error) (bool, error) {
	if !errors.Is(cause, ErrAccessConflict) && !errors.Is(cause, ErrAccessExpired) && !errors.Is(cause, ErrAccessRevoked) {
		return false, nil
	}
	latest, err := s.GetGrant(ctx, candidate.GrantID)
	if err != nil {
		if errors.Is(err, ErrAccessNotFound) {
			return true, nil
		}
		return false, err
	}
	if latest.Status != "active" || latest.Revision != candidate.ExpectedRevision {
		return true, nil
	}
	return false, nil
}

func validateSecurityExpiryTime(value time.Time) (time.Time, error) {
	if value.IsZero() || value.Location() != time.UTC {
		return time.Time{}, errors.New("access grant expiry requires an injected UTC security time")
	}
	return value.UTC(), nil
}

func validateGrantExpiryLimit(limit int64) error {
	if limit <= 0 || limit > DefaultGrantExpiryBatchLimit {
		return fmt.Errorf("access grant expiry limit must be in 1..%d", DefaultGrantExpiryBatchLimit)
	}
	return nil
}
