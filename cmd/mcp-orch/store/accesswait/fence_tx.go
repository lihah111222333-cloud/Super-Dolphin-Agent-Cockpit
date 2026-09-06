package accesswait

import (
	"context"
	"errors"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

type transactionalIdentityReader interface {
	GetCurrentWithDBTX(context.Context, storedb.DBTX, string) (identity.CurrentBinding, error)
}

type transactionalEventReader interface {
	EventReader
	EventSourceStatusReader
	ListEventsTx(context.Context, storedb.DBTX, string, string, eventstore.Lane, int64, int64) ([]eventstore.AgentEvent, error)
	GetEventTx(context.Context, storedb.DBTX, string) (eventstore.AgentEvent, error)
	GetSourceStateTx(context.Context, storedb.DBTX, string, string, eventstore.Lane) (eventstore.SourceState, error)
}

// verifyRequestedFencesTx 在写事务使用的同一 SQLite 连接上复核 current binding。
// 构造器已拒绝不支持该窄口的 identity reader，不能退回外层预检。
func (s *Store) verifyRequestedFencesTx(ctx context.Context, dbtx storedb.DBTX, source, target FenceSnapshot) error {
	reader, ok := s.identities.(transactionalIdentityReader)
	if !ok {
		return errors.New("access wait transactional identity reader is unavailable")
	}
	for _, snapshot := range []FenceSnapshot{source, target} {
		binding, err := reader.GetCurrentWithDBTX(ctx, dbtx, snapshot.AgentID)
		if err != nil || binding.State != "active" || !equalFence(snapshot, fenceFromBinding(binding)) {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			return ErrAccessDenied
		}
	}
	return nil
}
