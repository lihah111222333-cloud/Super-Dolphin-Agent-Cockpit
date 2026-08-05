package processobserve

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/processprobe"
)

// Store is a bounded no-signal observation projection. NewMemoryStore is
// process-local; OpenDurableStore attaches the sealed, crash-safe backend.
// Neither mode carries process ownership or signal authority.
type Store struct {
	mu                    sync.Mutex
	decisions             map[string]Decision
	decisionSizes         map[string]uint64
	projectionFailureOnce bool
	observationBytes      uint64
	durable               *durableBackend
	closed                bool
}

// NewMemoryStore constructs an empty bounded in-memory projection.
func NewMemoryStore() *Store {
	return &Store{
		decisions:     make(map[string]Decision),
		decisionSizes: make(map[string]uint64),
	}
}

// NewMemoryStoreForTest is an explicit test constructor with no path access.
func NewMemoryStoreForTest() *Store { return NewMemoryStore() }

// Close releases durable resources. Memory stores have no resources.
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return nil
}

// Stats returns current bounded observation accounting.
func (s *Store) Stats() (Stats, error) {
	if s == nil {
		return Stats{}, errors.New("observation memory store is nil")
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return Stats{}, ErrDurableStoreClosed
	}
	if s.durable != nil {
		durable := s.durable
		s.mu.Unlock()
		_, stats, err := durable.list(context.Background())
		return stats, err
	}
	defer s.mu.Unlock()
	return s.statsLocked(), nil
}

// ListDecisions returns a stable, redaction-safe copy of current decisions.
func (s *Store) ListDecisions(ctx context.Context) ([]Decision, error) {
	if s == nil {
		return nil, errors.New("observation memory store is nil")
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrDurableStoreClosed
	}
	if err := validateContext(ctx); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	if s.durable != nil {
		durable := s.durable
		s.mu.Unlock()
		decisions, _, err := durable.list(ctx)
		return decisions, err
	}
	defer s.mu.Unlock()
	decisions := make([]Decision, 0, len(s.decisions))
	for _, decision := range s.decisions {
		decisions = append(decisions, cloneDecision(decision))
	}
	sort.Slice(decisions, func(i, j int) bool {
		return decisions[i].EventID() < decisions[j].EventID()
	})
	return decisions, nil
}

// RecordGhost records both no-signal projections for one observation attempt.
func (s *Store) RecordGhost(ctx context.Context, snapshot processprobe.Snapshot) (Decision, error) {
	if s == nil {
		return Decision{}, errors.New("observation memory store is nil")
	}
	if err := validateContext(ctx); err != nil {
		return Decision{}, err
	}
	if !isNoSignalSnapshot(snapshot) {
		return Decision{}, errors.New("process observation snapshot has signal authority")
	}
	if s.durable != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.closed {
			return Decision{}, ErrDurableStoreClosed
		}
		return s.durable.record(ctx, snapshot, &s.projectionFailureOnce)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Decision{}, ErrDurableStoreClosed
	}
	return s.recordGhostLocked(snapshot)
}

// RecordGhostBatch records a bounded scan under one lock. Repeated snapshots
// in one bucket are compacted without creating unbounded per-observation data.
func (s *Store) RecordGhostBatch(ctx context.Context, snapshots []processprobe.Snapshot) ([]Decision, error) {
	if s == nil {
		return nil, errors.New("observation memory store is nil")
	}
	if err := validateContext(ctx); err != nil {
		return nil, err
	}
	if s.durable != nil {
		return s.recordDurableBatch(ctx, snapshots)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrDurableStoreClosed
	}
	return s.recordMemoryBatchLocked(snapshots)
}

func (s *Store) recordDurableBatch(ctx context.Context, snapshots []processprobe.Snapshot) ([]Decision, error) {
	decisions := make([]Decision, 0, len(snapshots))
	for _, snapshot := range snapshots {
		decision, err := s.RecordGhost(ctx, snapshot)
		if err != nil {
			return decisions, err
		}
		decisions = append(decisions, decision)
	}
	return decisions, nil
}

func (s *Store) recordMemoryBatchLocked(snapshots []processprobe.Snapshot) ([]Decision, error) {
	groups, order, err := groupBatchSnapshots(snapshots)
	if err != nil {
		return nil, err
	}
	return s.persistBatchGroupsLocked(groups, order)
}

func groupBatchSnapshots(snapshots []processprobe.Snapshot) (map[string][]processprobe.Snapshot, []string, error) {
	groups := make(map[string][]processprobe.Snapshot, len(snapshots))
	order := make([]string, 0, len(snapshots))
	for _, snapshot := range snapshots {
		if !isNoSignalSnapshot(snapshot) {
			return nil, nil, errors.New("process observation snapshot has signal authority")
		}
		key := batchObservationKey(snapshot)
		if _, exists := groups[key]; !exists {
			order = append(order, key)
		}
		groups[key] = append(groups[key], snapshot)
	}
	return groups, order, nil
}

func (s *Store) persistBatchGroupsLocked(groups map[string][]processprobe.Snapshot, order []string) ([]Decision, error) {
	decisions := make([]Decision, 0, len(order))
	for _, key := range order {
		group := groups[key]
		decision, err := s.recordGhostLocked(group[0])
		if err != nil {
			return nil, err
		}
		if len(group) > 1 {
			decision, err = s.mergeBatchObservationsLocked(decision, group[1:])
			if err != nil {
				return nil, err
			}
		}
		decisions = append(decisions, cloneDecision(decision))
	}
	return decisions, nil
}

// RetryPending completes a pair whose projection fan-out was interrupted.
// IDs and the event/operation relationship remain unchanged.
func (s *Store) RetryPending(ctx context.Context) (int, error) {
	if s == nil {
		return 0, errors.New("observation memory store is nil")
	}
	if err := validateContext(ctx); err != nil {
		return 0, err
	}
	if s.durable != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.closed {
			return 0, ErrDurableStoreClosed
		}
		return s.durable.retry(ctx, &s.projectionFailureOnce)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, ErrDurableStoreClosed
	}
	keys := make([]string, 0, len(s.decisions))
	for key, decision := range s.decisions {
		if decision.Status() == DecisionPairPending {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	count := 0
	for _, key := range keys {
		decision := s.decisions[key]
		if err := s.projectDecisionLocked(&decision); err != nil {
			return count, err
		}
		s.replaceDecisionLocked(key, decision)
		count++
	}
	return count, nil
}

// InjectProjectionFailureOnceForTest simulates an interrupted in-memory pair.
func (s *Store) InjectProjectionFailureOnceForTest() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.projectionFailureOnce = true
	s.mu.Unlock()
}

func (s *Store) recordGhostLocked(snapshot processprobe.Snapshot) (Decision, error) {
	reason := observationReason(snapshot)
	// This lane never receives receipt/generation/binary-admission proof. The
	// incomplete path is therefore the only valid path and never emits a
	// lifecycle or complete-identity key.
	dedupKey, bucketKey := ghostKeys(snapshot, reason, snapshot.IdentityComplete())
	if dedupKey != "" {
		return Decision{}, errors.New("complete lifecycle identity requires integration handoff")
	}
	key, decision, exists, dropped := s.admitBucketLocked(bucketKey, snapshot, reason)
	if key == "" {
		return Decision{}, errors.New("observation bucket key is empty")
	}
	decision, err := updateDecision(decision, exists, snapshot, reason, key, dropped)
	if err != nil {
		return decision, err
	}
	decision.candidate = projectionFor(decision, ProjectionCandidate, false)
	decision.blocked = projectionFor(decision, ProjectionBlocked, false)
	if err := s.admitDecisionLocked(key, decision); err != nil {
		return decision, err
	}
	if err := s.projectDecisionLocked(&decision); err != nil {
		s.replaceDecisionLocked(key, decision)
		return cloneDecision(decision), err
	}
	s.replaceDecisionLocked(key, decision)
	return cloneDecision(decision), nil
}

func (s *Store) admitBucketLocked(bucketKey string, snapshot processprobe.Snapshot, reason processprobe.ObservationReason) (string, Decision, bool, uint64) {
	decision, exists := s.decisions[bucketKey]
	if exists || s.bucketCountLocked() < MaxObservationBuckets-1 {
		return bucketKey, decision, exists, 0
	}
	overflowKey := overflowBucketKey(snapshot, reason)
	decision, exists = s.decisions[overflowKey]
	return overflowKey, decision, exists, 1
}

func updateDecision(decision Decision, exists bool, snapshot processprobe.Snapshot, reason processprobe.ObservationReason, bucketKey string, dropped uint64) (Decision, error) {
	now := snapshot.ObservedAt()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if !exists {
		eventID, err := randomID()
		if err != nil {
			return Decision{}, err
		}
		operationID, err := randomID()
		if err != nil {
			return Decision{}, err
		}
		return Decision{
			decisionIdentity: decisionIdentity{eventID: eventID, operationID: operationID, bucketKey: bucketKey},
			decisionEvidence: decisionEvidence{
				reason: reason, status: DecisionPairPending, firstSeen: now, lastSeen: now,
				seenCount: 1, droppedCount: dropped, missingFields: snapshot.MissingFields(), latestEvidenceHash: snapshot.EvidenceDigest(),
			},
		}, nil
	}
	operationID, err := randomID()
	if err != nil {
		return Decision{}, err
	}
	decision.operationID = operationID
	decision.lastSeen = now
	decision.seenCount++
	decision.droppedCount += dropped
	decision.missingFields = unionFields(decision.missingFields, snapshot.MissingFields())
	decision.latestEvidenceHash = snapshot.EvidenceDigest()
	decision.status = DecisionPairPending
	return decision, nil
}

func (s *Store) mergeBatchObservationsLocked(decision Decision, snapshots []processprobe.Snapshot) (Decision, error) {
	for _, snapshot := range snapshots {
		now := snapshot.ObservedAt()
		if now.IsZero() {
			now = time.Now().UTC()
		}
		decision.lastSeen = now
		decision.seenCount++
		decision.missingFields = unionFields(decision.missingFields, snapshot.MissingFields())
		decision.latestEvidenceHash = snapshot.EvidenceDigest()
	}
	key := decision.BucketKey()
	if key == "" {
		return Decision{}, errors.New("batch observation bucket key is empty")
	}
	if err := s.admitDecisionLocked(key, decision); err != nil {
		return decision, err
	}
	s.replaceDecisionLocked(key, decision)
	return cloneDecision(decision), nil
}

func (s *Store) admitDecisionLocked(key string, decision Decision) error {
	newSize := decisionSize(decision)
	oldSize := s.decisionSizes[key]
	projected := s.observationBytes - oldSize + newSize
	if projected > MaxObservationBytes {
		return ErrCapacity
	}
	s.observationBytes = projected
	return nil
}

func (s *Store) replaceDecisionLocked(key string, decision Decision) {
	s.decisions[key] = cloneDecision(decision)
	s.decisionSizes[key] = decisionSize(decision)
}

func (s *Store) projectDecisionLocked(decision *Decision) error {
	if decision == nil {
		return errors.New("observation decision is nil")
	}
	if s.projectionFailureOnce {
		s.projectionFailureOnce = false
		return errors.New("injected projection write failure")
	}
	decision.candidate = projectionFor(*decision, ProjectionCandidate, true)
	decision.blocked = projectionFor(*decision, ProjectionBlocked, true)
	decision.status = DecisionPersisted
	return nil
}

func (s *Store) bucketCountLocked() int {
	count := 0
	for _, decision := range s.decisions {
		if decision.BucketKey() != "" {
			count++
		}
	}
	return count
}

func (s *Store) statsLocked() Stats {
	stats := Stats{
		TotalBytes:       s.observationBytes,
		ObservationBytes: s.observationBytes,
		BucketCount:      s.bucketCountLocked(),
	}
	for _, decision := range s.decisions {
		if decision.DedupKey() != "" {
			stats.DedupCount++
		}
	}
	// No method in this package can ever send a signal.
	stats.SignalCount = 0
	return stats
}

func batchObservationKey(snapshot processprobe.Snapshot) string {
	_, bucketKey := ghostKeys(snapshot, observationReason(snapshot), snapshot.IdentityComplete())
	return bucketKey
}

func observationReason(snapshot processprobe.Snapshot) processprobe.ObservationReason {
	if snapshot.Reason() != "" {
		return snapshot.Reason()
	}
	return processprobe.ReasonNoAuthoritativeOwner
}

func isNoSignalSnapshot(snapshot processprobe.Snapshot) bool {
	return snapshot.AuthorityDecision() == processprobe.AuthorityNoSignal && !snapshot.SignalSent()
}

func ghostKeys(snapshot processprobe.Snapshot, reason processprobe.ObservationReason, complete bool) (string, string) {
	if complete {
		// Complete lifecycle admission is intentionally owned by the integration
		// layer; this package cannot manufacture a dedup proof from PID evidence.
		return "", ""
	}
	return "", hashKey(strings.Join([]string{
		"bucket-v1",
		snapshot.Platform(),
		"mcp-lsp",
		string(reason),
		strconv.Itoa(snapshot.PID()),
		snapshot.Executable(),
		evidenceClass(snapshot),
	}, "|"))
}

func overflowBucketKey(snapshot processprobe.Snapshot, reason processprobe.ObservationReason) string {
	return hashKey(strings.Join([]string{"overflow-v1", snapshot.Platform(), "mcp-lsp", string(reason)}, "|"))
}

func evidenceClass(snapshot processprobe.Snapshot) string {
	digest := snapshot.EvidenceDigest()
	if len(digest) < 8 {
		return "missing"
	}
	return digest[:8]
}

func decisionSize(decision Decision) uint64 {
	size := uint64(384)
	for _, value := range []string{
		decision.EventID(), decision.OperationID(), decision.LifecycleKey(), decision.DedupKey(),
		decision.BucketKey(), string(decision.Reason()), string(decision.Status()),
		decision.LatestEvidenceDigest(), decision.CandidateProjection().ID(), decision.BlockedProjection().ID(),
	} {
		size += uint64(len(value))
	}
	for _, field := range decision.MissingFields() {
		size += uint64(len(field))
	}
	return size
}

func cloneDecision(decision Decision) Decision {
	decision.missingFields = append([]string(nil), decision.missingFields...)
	return decision
}

func projectionFor(decision Decision, kind ProjectionKind, acked bool) Projection {
	event := "lsp_ghost_candidate_observed"
	if kind == ProjectionBlocked {
		event = "lsp_reclaim_blocked"
	}
	return Projection{
		id:          decision.EventID() + "|" + string(kind),
		eventID:     decision.EventID(),
		operationID: decision.OperationID(),
		kind:        kind,
		event:       event,
		reason:      decision.Reason(),
		signalSent:  false,
		acked:       acked,
	}
}

func validateContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("observation context is nil")
	}
	return ctx.Err()
}

func randomID() (string, error) {
	var data [16]byte
	if _, err := io.ReadFull(rand.Reader, data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}

func hashKey(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}
