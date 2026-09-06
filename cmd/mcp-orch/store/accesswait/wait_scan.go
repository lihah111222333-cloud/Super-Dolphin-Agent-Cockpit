package accesswait

import (
	"context"
	"errors"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

const maxWaitScanPages = 64

func (s *Store) findWaitEventTx(ctx context.Context, dbtx storedb.DBTX, handle WaitHandle) (*eventstore.AgentEvent, error) {
	after := handle.AfterSequence
	for page := 0; page < maxWaitScanPages; page++ {
		batch, err := s.eventsTx.ListEventsTx(ctx, dbtx, handle.Selector.SourceID, handle.Selector.SourceEpoch, handle.Selector.Lane, after, maxWaitPollEvents)
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			return nil, nil
		}
		matched, selectErr := selectWaitEvent(batch, handle)
		if selectErr != nil || matched != nil {
			return matched, selectErr
		}
		next, ok := lastSequenceAfter(batch, after)
		if !ok {
			return nil, ErrWaitGap
		}
		after = next
		if int64(len(batch)) < maxWaitPollEvents {
			return nil, nil
		}
	}
	return nil, ErrWaitScanLimit
}

func (s *Store) ensureSourceReadableTx(ctx context.Context, dbtx storedb.DBTX, selector EventSelector, afterSequence int64) error {
	state, err := s.eventsTx.GetSourceStateTx(ctx, dbtx, selector.SourceID, selector.SourceEpoch, selector.Lane)
	if err != nil {
		if errors.Is(err, eventstore.ErrEventNotFound) {
			return ErrWaitNotFound
		}
		return err
	}
	return ensureSourceStateReadable(state, afterSequence)
}

func ensureSourceStateReadable(state eventstore.SourceState, afterSequence int64) error {
	if err := ensureSourceStateStatus(state); err != nil {
		return err
	}
	if state.Status == "active" {
		if afterSequence < state.DedupFloor {
			return ErrWaitReplayExpired
		}
		if afterSequence > state.MaterializedSequence {
			return ErrWaitCursorAhead
		}
	}
	return nil
}

func ensureSourceStateStatus(state eventstore.SourceState) error {
	switch state.Status {
	case "gap":
		return ErrWaitGap
	case "quarantined", "closed":
		return ErrWaitQuarantined
	case "active":
		return nil
	default:
		return ErrWaitQuarantined
	}
}

func lastSequenceAfter(batch []eventstore.AgentEvent, after int64) (int64, bool) {
	next := after
	for _, item := range batch {
		if item.SourceSequence <= next {
			return 0, false
		}
		next = item.SourceSequence
	}
	return next, true
}
