package content

import (
	"context"
	"database/sql"
	"errors"
	"time"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
)

// BindOperatorSource 在立即事务中绑定当前operator来源，不创建task身份。
func (s *Store) BindOperatorSource(ctx context.Context, id string, b OperatorSourceBinding) (SourceResolution, error) {
	if err := s.ready(ctx); err != nil {
		return SourceResolution{}, err
	}
	if !validContentText(id, 128) || !validOperatorBinding(b) {
		return SourceResolution{}, ErrInvalidRequest
	}
	var out SourceResolution
	err := sqlctx.WithImmediateTx(ctx, s.db, s.queries, func(q *storedb.Queries, _ storedb.DBTX) error {
		if err := requireReservation(ctx, q, id); err != nil {
			return err
		}
		existing, err := q.ResolveSource(ctx, storedb.ResolveSourceParams{IngestReservationID: id})
		if err == nil {
			return replayOperatorBinding(existing, id, b, &out)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		_, err = q.BindOperatorSource(ctx, storedb.BindOperatorSourceParams{
			IngestReservationID: id, OperatorSourceID: &b.SourceID, OperatorSourceFenceRef: &b.FenceRef,
			OperatorPrincipalRef: &b.PrincipalRef, OperatorScopeRef: &b.ScopeRef, OperatorAuthContext: &b.AuthContext,
			OperatorGeneration: &b.Generation, CreatedAt: time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z"),
		})
		if err == nil {
			out = SourceResolution{id, b}
		}
		return err
	})
	return out, err
}

// ResolveSource 在同一立即事务内重查union和138当前来源，旧代立即失效。
func (s *Store) ResolveSource(ctx context.Context, id string) (SourceResolution, error) {
	if err := s.ready(ctx); err != nil {
		return SourceResolution{}, err
	}
	if !validContentText(id, 128) {
		return SourceResolution{}, ErrInvalidRequest
	}
	var out SourceResolution
	err := sqlctx.WithImmediateTx(ctx, s.db, s.queries, func(q *storedb.Queries, _ storedb.DBTX) error {
		row, err := q.ResolveSource(ctx, storedb.ResolveSourceParams{IngestReservationID: id})
		if errors.Is(err, sql.ErrNoRows) {
			return ErrContentUnavailable
		}
		if err != nil {
			return err
		}
		binding, err := operatorBindingFromRow(row)
		if err != nil {
			return err
		}
		if err := verifyCurrentOperatorSource(ctx, q, binding); err != nil {
			return err
		}
		out = SourceResolution{id, binding}
		return nil
	})
	return out, err
}

// validOperatorBinding 校验operator selector的有界文本和代际。
func validOperatorBinding(b OperatorSourceBinding) bool {
	return validContentText(b.SourceID, 512) && validContentText(b.FenceRef, 512) &&
		validContentText(b.PrincipalRef, 512) && validContentText(b.ScopeRef, 512) &&
		validContentText(b.AuthContext, 512) && b.Generation > 0
}

func requireReservation(ctx context.Context, q *storedb.Queries, id string) error {
	_, err := q.GetContentIngestReservationByID(ctx, storedb.GetContentIngestReservationByIDParams{IngestReservationID: id})
	if errors.Is(err, sql.ErrNoRows) {
		return ErrIngestNotFound
	}
	return err
}

func replayOperatorBinding(row storedb.OrchestrationContentSourceUnion, id string, b OperatorSourceBinding, out *SourceResolution) error {
	got, err := operatorBindingFromRow(row)
	if err != nil {
		return err
	}
	if got != b {
		return ErrReplayConflict
	}
	*out = SourceResolution{id, b}
	return nil
}

// operatorBindingFromRow 将可空sqlc字段转换为完整operator selector。
func operatorBindingFromRow(row storedb.OrchestrationContentSourceUnion) (OperatorSourceBinding, error) {
	if row.SourceKind != "operator" || row.OperatorSourceID == nil || row.OperatorSourceFenceRef == nil ||
		row.OperatorPrincipalRef == nil || row.OperatorScopeRef == nil || row.OperatorAuthContext == nil || row.OperatorGeneration == nil {
		return OperatorSourceBinding{}, ErrContentUnavailable
	}
	return OperatorSourceBinding{*row.OperatorSourceID, *row.OperatorSourceFenceRef, *row.OperatorPrincipalRef, *row.OperatorScopeRef, *row.OperatorAuthContext, *row.OperatorGeneration}, nil
}

// verifyCurrentOperatorSource 重新读取138当前source，拒绝撤销或旧代。
func verifyCurrentOperatorSource(ctx context.Context, q *storedb.Queries, b OperatorSourceBinding) error {
	source, err := q.GetOperatorSourceID(ctx, storedb.GetOperatorSourceIDParams{SourceID: b.SourceID})
	if err != nil || !currentOperatorSourceMatches(source, b) {
		return ErrContentUnavailable
	}
	latest, err := q.GetLatestOperatorSource(ctx)
	if err != nil || latest.SourceID != b.SourceID || latest.Generation != b.Generation || latest.State != "active" {
		return ErrContentUnavailable
	}
	return nil
}

// currentOperatorSourceMatches 比较operator selector的全部持久字段。
func currentOperatorSourceMatches(source storedb.OrchestrationOperatorSource, b OperatorSourceBinding) bool {
	return source.State == "active" && source.SourceFenceRef == b.FenceRef && source.PrincipalRef == b.PrincipalRef &&
		source.ScopeRef == b.ScopeRef && source.AuthContext == b.AuthContext && source.Generation == b.Generation
}
