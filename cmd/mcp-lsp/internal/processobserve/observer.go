package processobserve

import (
	"context"
	"errors"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/processprobe"
)

// Observer consumes sealed snapshots and never receives a probe implementation or signal capability.
type Observer struct {
	store  *Store
	logger Logger
}

// NewObserver constructs an observer over a bounded no-signal store.
func NewObserver(store *Store) (*Observer, error) {
	return NewObserverWithLogger(store, nil)
}

// NewObserverWithLogger adds an optional redaction-safe event logger.
func NewObserverWithLogger(store *Store, logger Logger) (*Observer, error) {
	if store == nil {
		return nil, errors.New("observer store is required")
	}
	return &Observer{store: store, logger: logger}, nil
}

// Observe projects candidate and blocked events for each snapshot.
func (o *Observer) Observe(ctx context.Context, snapshots []processprobe.Snapshot) ([]Decision, error) {
	if err := o.validateObserveContext(ctx); err != nil {
		return nil, err
	}
	if err := validateNoSignalSnapshots(snapshots); err != nil {
		return nil, err
	}
	decisions, err := o.store.RecordGhostBatch(ctx, snapshots)
	if err != nil {
		return nil, err
	}
	return o.emitDecisions(decisions)
}

func (o *Observer) validateObserveContext(ctx context.Context) error {
	if o == nil || o.store == nil {
		return errors.New("observer is not initialized")
	}
	if ctx == nil {
		return errors.New("observer context is nil")
	}
	return ctx.Err()
}

func validateNoSignalSnapshots(snapshots []processprobe.Snapshot) error {
	for _, snapshot := range snapshots {
		if snapshot.AuthorityDecision() != processprobe.AuthorityNoSignal || snapshot.SignalSent() {
			return errors.New("observer received a signal-capable snapshot")
		}
	}
	return nil
}

func (o *Observer) emitDecisions(decisions []Decision) ([]Decision, error) {
	for _, decision := range decisions {
		if o.logger != nil {
			if err := o.logDecision(decision); err != nil {
				return nil, err
			}
		}
	}
	return decisions, nil
}

// ObservePID performs one platform probe and preserves its error alongside the blocked record.
func (o *Observer) ObservePID(ctx context.Context, pid int) ([]Decision, error) {
	snapshot, probeErr := processprobe.Probe(ctx, pid)
	decisions, observeErr := o.Observe(ctx, []processprobe.Snapshot{snapshot})
	return decisions, errors.Join(probeErr, observeErr)
}

func (o *Observer) logDecision(decision Decision) error {
	missing := decision.MissingFields()
	if err := o.logger.Record(Event{
		Name:          decision.CandidateProjection().Event(),
		EventID:       decision.EventID(),
		OperationID:   decision.OperationID(),
		Reason:        decision.Reason(),
		SignalSent:    false,
		MissingFields: missing,
		SeenCount:     decision.SeenCount(),
		DroppedCount:  decision.DroppedCount(),
	}); err != nil {
		return err
	}
	return o.logger.Record(Event{
		Name:          decision.BlockedProjection().Event(),
		EventID:       decision.EventID(),
		OperationID:   decision.OperationID(),
		Reason:        decision.Reason(),
		SignalSent:    false,
		MissingFields: missing,
		SeenCount:     decision.SeenCount(),
		DroppedCount:  decision.DroppedCount(),
	})
}
