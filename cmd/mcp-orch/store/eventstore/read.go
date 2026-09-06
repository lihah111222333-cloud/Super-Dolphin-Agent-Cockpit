package eventstore

import (
	"context"
	"errors"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	modernsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// GetSourceState 读取 source/lane 的最新水位；缺失时不猜测初始状态。
func (s *Store) GetSourceState(ctx context.Context, sourceID, sourceEpoch string, lane Lane) (SourceState, error) {
	if err := validateSourceKey(sourceID, sourceEpoch, lane); err != nil {
		return SourceState{}, err
	}
	if err := s.ready(ctx); err != nil {
		return SourceState{}, err
	}
	row, err := s.queries.GetOrchestrationEventSourceState(ctx, storedb.GetOrchestrationEventSourceStateParams{
		SourceID: sourceID, SourceEpoch: sourceEpoch, Lane: string(lane),
	})
	if err != nil {
		return SourceState{}, classifyEventDBError("get event source state", err)
	}
	return sourceStateFromRow(row)
}

// GetReceipt 按 source tuple 读取接收事实。
func (s *Store) GetReceipt(ctx context.Context, sourceID, sourceEpoch string, lane Lane, sequence int64) (IngressReceipt, error) {
	if err := validateSourceKey(sourceID, sourceEpoch, lane); err != nil {
		return IngressReceipt{}, err
	}
	if sequence <= 0 {
		return IngressReceipt{}, invalidEvent("source_sequence must be positive")
	}
	if err := s.ready(ctx); err != nil {
		return IngressReceipt{}, err
	}
	row, err := s.queries.GetOrchestrationEventIngressReceipt(ctx, storedb.GetOrchestrationEventIngressReceiptParams{
		SourceID: sourceID, SourceEpoch: sourceEpoch, Lane: string(lane), SourceSequence: sequence,
	})
	if err != nil {
		return IngressReceipt{}, classifyEventDBError("get event ingress receipt", err)
	}
	return ingressReceiptFromRow(row)
}

// GetEvent 按 event_id 读取已 materialize 的 canonical event。
func (s *Store) GetEvent(ctx context.Context, eventID string) (AgentEvent, error) {
	if err := validateText("event_id", eventID, 128, false); err != nil {
		return AgentEvent{}, err
	}
	if err := s.ready(ctx); err != nil {
		return AgentEvent{}, err
	}
	row, err := s.queries.GetOrchestrationAgentEvent(ctx, storedb.GetOrchestrationAgentEventParams{EventID: eventID})
	if err != nil {
		return AgentEvent{}, classifyEventDBError("get canonical event", err)
	}
	return agentEventFromRow(row)
}

// GetEventTx 按 event_id 在调用方事务中读取 canonical event。
func (s *Store) GetEventTx(ctx context.Context, dbtx storedb.DBTX, eventID string) (AgentEvent, error) {
	if err := s.validateTransaction(ctx, dbtx); err != nil {
		return AgentEvent{}, err
	}
	if err := validateText("event_id", eventID, 128, false); err != nil {
		return AgentEvent{}, err
	}
	row, err := storedb.New(dbtx).GetOrchestrationAgentEvent(ctx, storedb.GetOrchestrationAgentEventParams{EventID: eventID})
	if err != nil {
		return AgentEvent{}, classifyEventDBError("get canonical event in transaction", err)
	}
	return agentEventFromRow(row)
}

// ListEvents 按 source sequence 升序返回有界 canonical 事件。
func (s *Store) ListEvents(ctx context.Context, sourceID, sourceEpoch string, lane Lane, afterSequence, limit int64) ([]AgentEvent, error) {
	if err := validateSourceKey(sourceID, sourceEpoch, lane); err != nil {
		return nil, err
	}
	if afterSequence < 0 || limit < 1 || limit > 200 {
		return nil, invalidEvent("event list requires after_sequence >= 0 and limit 1..200")
	}
	if err := s.ready(ctx); err != nil {
		return nil, err
	}
	return listEvents(ctx, s.queries, sourceID, sourceEpoch, lane, afterSequence, limit)
}

// ListEventsTx 按 source sequence 在调用方事务中读取有界 canonical events。
// 调用方可用它把 source 状态、事件 tuple 和后续 CAS 绑定到同一 SQLite 连接。
func (s *Store) ListEventsTx(ctx context.Context, dbtx storedb.DBTX, sourceID, sourceEpoch string, lane Lane, afterSequence, limit int64) ([]AgentEvent, error) {
	if err := s.validateTransaction(ctx, dbtx); err != nil {
		return nil, err
	}
	if err := validateSourceKey(sourceID, sourceEpoch, lane); err != nil {
		return nil, err
	}
	if afterSequence < 0 || limit < 1 || limit > 200 {
		return nil, invalidEvent("event list requires after_sequence >= 0 and limit 1..200")
	}
	return listEvents(ctx, storedb.New(dbtx), sourceID, sourceEpoch, lane, afterSequence, limit)
}

func listEvents(ctx context.Context, q *storedb.Queries, sourceID, sourceEpoch string, lane Lane, afterSequence, limit int64) ([]AgentEvent, error) {
	rows, err := q.ListOrchestrationAgentEvents(ctx, storedb.ListOrchestrationAgentEventsParams{
		SourceID: sourceID, SourceEpoch: sourceEpoch, Lane: string(lane), AfterSequence: afterSequence, Limit: limit,
	})
	if err != nil {
		return nil, classifyEventDBError("list canonical events", err)
	}
	result := make([]AgentEvent, 0, len(rows))
	for _, row := range rows {
		event, mapErr := agentEventFromRow(row)
		if mapErr != nil {
			return nil, mapErr
		}
		result = append(result, event)
	}
	return result, nil
}

func validateSourceKey(sourceID, sourceEpoch string, lane Lane) error {
	if err := validateText("source_id", sourceID, 256, false); err != nil {
		return err
	}
	if err := validateText("source_epoch", sourceEpoch, 128, false); err != nil {
		return err
	}
	if !validLane(lane) {
		return invalidEvent("lane is unsupported")
	}
	return nil
}

func isEventUniqueError(err error) bool {
	var sqliteErr *modernsqlite.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	return sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE || sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY
}
