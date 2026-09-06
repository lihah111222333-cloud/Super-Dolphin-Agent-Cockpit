package resourceadmission

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
	modernsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// PublishEffectivePolicy 在IMMEDIATE事务内发布连续、同owner lineage的policy revision。
func (s *Store) PublishEffectivePolicy(
	ctx context.Context,
	request PublishEffectivePolicyRequest,
) (PolicySnapshot, error) {
	if err := s.ready(ctx); err != nil {
		return PolicySnapshot{}, err
	}
	if err := request.Validate(); err != nil {
		return PolicySnapshot{}, err
	}
	var winner PolicySnapshot
	err := sqlctx.WithImmediateTx(ctx, s.db, s.queries, func(q *storedb.Queries, _ storedb.DBTX) error {
		var err error
		winner, err = s.publishEffectivePolicyTx(ctx, q, request)
		return err
	})
	if err != nil {
		return PolicySnapshot{}, err
	}
	return winner, nil
}

// publishEffectivePolicyTx 在持有SQLite写锁时先冻结幂等winner，再校验前驱并插入fresh snapshot。
func (s *Store) publishEffectivePolicyTx(
	ctx context.Context,
	q *storedb.Queries,
	request PublishEffectivePolicyRequest,
) (PolicySnapshot, error) {
	existing, found, err := getPolicyByKey(ctx, q, request.IdempotencyNamespace, request.IdempotencyKeyDigest)
	if err != nil {
		return PolicySnapshot{}, err
	}
	if found {
		if existing.PublishRequestDigest != request.PublishRequestDigest {
			return PolicySnapshot{}, ErrReplayConflict
		}
		return existing, nil
	}
	candidate, err := newPolicySnapshot(request, s.now())
	if err != nil {
		return PolicySnapshot{}, err
	}
	if err := validatePolicyPredecessor(ctx, q, candidate); err != nil {
		return PolicySnapshot{}, err
	}
	row, err := q.CreateResourceEffectivePolicy(ctx, policyCreateParams(candidate))
	if err != nil {
		return PolicySnapshot{}, classifyPolicyDBError("create effective policy", err)
	}
	winner, err := policyFromRow(row)
	if err != nil {
		return PolicySnapshot{}, err
	}
	if winner.PolicySnapshotDigest != candidate.PolicySnapshotDigest {
		return PolicySnapshot{}, ErrSnapshotCorrupt
	}
	return winner, nil
}

// getPolicyByKey 读取幂等winner并严格验证持久化projection。
func getPolicyByKey(
	ctx context.Context,
	q *storedb.Queries,
	namespace string,
	keyDigest string,
) (PolicySnapshot, bool, error) {
	row, err := q.GetResourceEffectivePolicyByKey(ctx, storedb.GetResourceEffectivePolicyByKeyParams{
		IdempotencyNamespace: namespace,
		IdempotencyKeyDigest: keyDigest,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return PolicySnapshot{}, false, nil
	}
	if err != nil {
		return PolicySnapshot{}, false, fmt.Errorf("get effective policy by key: %w", err)
	}
	snapshot, err := policyFromRow(row)
	if err != nil {
		return PolicySnapshot{}, false, err
	}
	return snapshot, true, nil
}

// validatePolicyPredecessor 拒绝revision gap、跨scope lineage和未经证明的owner接管。
func validatePolicyPredecessor(ctx context.Context, q *storedb.Queries, candidate PolicySnapshot) error {
	if candidate.Revision == 1 {
		return nil
	}
	row, err := q.GetResourceEffectivePolicyByLineageRevision(ctx, storedb.GetResourceEffectivePolicyByLineageRevisionParams{
		PolicyLineageID: candidate.LineageID,
		PolicyRevision:  candidate.Revision - 1,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPolicyStale
	}
	if err != nil {
		return fmt.Errorf("get effective policy predecessor: %w", err)
	}
	predecessor, err := policyFromRow(row)
	if err != nil {
		return err
	}
	if !samePolicySelector(predecessor, candidate) {
		return ErrPolicyConflict
	}
	if predecessor.PolicyOwner != candidate.PolicyOwner {
		return ErrWriterFence
	}
	if candidate.EffectiveAt.Before(predecessor.EffectiveAt) {
		return ErrPolicyStale
	}
	return nil
}

// samePolicySelector 保证同lineage revision不会切换调用方、scope或kind。
func samePolicySelector(predecessor PolicySnapshot, candidate PolicySnapshot) bool {
	return predecessor.RequestedKind == candidate.RequestedKind &&
		predecessor.CallerPrincipalRef == candidate.CallerPrincipalRef &&
		predecessor.ScopeRef == candidate.ScopeRef &&
		predecessor.PolicyApplicabilityDigest == candidate.PolicyApplicabilityDigest
}

// classifyPolicyDBError 将SQLite约束失败收敛为稳定policy领域错误。
func classifyPolicyDBError(operation string, err error) error {
	var sqliteErr *modernsqlite.Error
	if errors.As(err, &sqliteErr) {
		switch sqliteErr.Code() {
		case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY:
			return errors.Join(ErrPolicyConflict, err)
		case sqlite3.SQLITE_CONSTRAINT_CHECK, sqlite3.SQLITE_CONSTRAINT_TRIGGER, sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY:
			return errors.Join(ErrSnapshotCorrupt, err)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
