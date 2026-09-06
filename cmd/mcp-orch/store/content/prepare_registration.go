package content

import (
	"context"
	"errors"
	"time"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
)

// startPreparation 在外部key/object I/O前持久化唯一operation和开始时刻。
func (s *Store) startPreparation(ctx context.Context, ticketID, operationID string) (IngestReservation, error) {
	expected, err := StablePrepareOperationID(ticketID)
	if err != nil {
		return IngestReservation{}, err
	}
	if operationID != expected {
		return IngestReservation{}, ErrReplayConflict
	}
	var result IngestReservation
	err = sqlctx.WithImmediateTx(ctx, s.db, s.queries, func(q *storedb.Queries, _ storedb.DBTX) error {
		current, err := loadReservationByTicket(ctx, q, ticketID)
		if err != nil {
			return err
		}
		if current.PrepareOperationID != "" {
			if current.PrepareOperationID != operationID {
				return ErrReplayConflict
			}
			result = current
			return nil
		}
		if current.State != "reserved" {
			return ErrContentCorrupt
		}
		now := s.now().UTC()
		if !current.AssemblyDeadline.After(now) {
			return ErrIngestExpired
		}
		affected, err := q.StartContentPreparation(ctx, storedb.StartContentPreparationParams{
			PrepareOperationID: operationID, PrepareStartedAt: now.Format(time.RFC3339Nano),
			UpdatedAt: now.Format(time.RFC3339Nano), IngestReservationID: current.ReservationID,
		})
		if err != nil || affected != 1 {
			return errors.Join(ErrInvalidTransition, err)
		}
		result, err = loadReservationByTicket(ctx, q, ticketID)
		return err
	})
	return result, err
}

func loadReservationByTicket(ctx context.Context, q *storedb.Queries, ticketID string) (IngestReservation, error) {
	row, err := q.GetContentIngestReservationByTicket(ctx, storedb.GetContentIngestReservationByTicketParams{
		IngestTicketID: ticketID,
	})
	if err != nil {
		return IngestReservation{}, classifyContentReadError(err)
	}
	return reservationFromRow(row)
}
