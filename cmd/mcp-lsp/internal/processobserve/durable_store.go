package processobserve

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/processprobe"
)

const (
	durableSchemaVersion = 1
	maxDurableRecordSize = MaxObservationBytes
)

// DurableOptions controls the explicit durable observation store constructor.
// TestOnly documents that a temporary root is being used by a test. Production
// wiring must pass the canonical cache root from trusted configuration and must
// fail closed if that root cannot be opened.
type DurableOptions struct {
	TestOnly bool
}

var (
	// ErrDurableStoreClosed is returned when a durable facade was closed.
	ErrDurableStoreClosed = errors.New("durable process observation store is closed")
	// ErrDurableRootMismatch is returned when the configured root was replaced.
	ErrDurableRootMismatch = errors.New("durable process observation root identity changed")
	// ErrDurablePlatformNotVerified is returned when a platform contract is not verified.
	ErrDurablePlatformNotVerified = errors.New("durable process observation store platform contract is not verified")
)

// durableBackend is deliberately unexported. It carries only a root path and
// an immutable filesystem identity; it never contains a process handle or a
// capability that can send a signal.
type durableBackend struct {
	root string
	dev  uint64
	ino  uint64
}

type durableRecord struct {
	SchemaVersion int               `json:"schema_version"`
	EventID       string            `json:"event_id"`
	OperationID   string            `json:"operation_id"`
	LifecycleKey  string            `json:"lifecycle_key,omitempty"`
	DedupKey      string            `json:"ghost_dedup_key,omitempty"`
	BucketKey     string            `json:"ghost_observation_bucket_key,omitempty"`
	Reason        string            `json:"reason"`
	Status        string            `json:"status"`
	FirstSeen     time.Time         `json:"first_seen"`
	LastSeen      time.Time         `json:"last_seen"`
	SeenCount     uint64            `json:"seen_count"`
	DroppedCount  uint64            `json:"dropped_count"`
	MissingFields []string          `json:"missing_fields,omitempty"`
	EvidenceHash  string            `json:"latest_evidence_digest,omitempty"`
	Candidate     durableProjection `json:"candidate"`
	Blocked       durableProjection `json:"blocked"`
}

type durableProjection struct {
	ID          string `json:"projection_id"`
	EventID     string `json:"event_id"`
	OperationID string `json:"operation_id"`
	Kind        string `json:"projection_kind"`
	Event       string `json:"event"`
	Reason      string `json:"reason"`
	SignalSent  bool   `json:"signal_sent"`
	Acked       bool   `json:"acked"`
}

type loadedDurableRecord struct {
	record durableRecord
	size   uint64
}

// OpenDurableStore opens a crash-safe observation store at the explicit root.
// No environment variable, home-directory guess, or memory fallback is used.
func OpenDurableStore(root string, _ DurableOptions) (*Store, error) {
	secure, err := openDurableRoot(root)
	if err != nil {
		return nil, err
	}
	dev, ino := secure.identity()
	if err := secure.withStoreLock(context.Background(), func(*secureRoot) error { return nil }); err != nil {
		_ = secure.close()
		return nil, err
	}
	if err := secure.close(); err != nil {
		return nil, err
	}
	return &Store{durable: &durableBackend{root: root, dev: dev, ino: ino}}, nil
}

func (b *durableBackend) list(ctx context.Context) ([]Decision, Stats, error) {
	var decisions []Decision
	var stats Stats
	err := b.withLockedRoot(ctx, func(root *secureRoot) error {
		loaded, err := root.readDurableRecords()
		if err != nil {
			return err
		}
		decisions = make([]Decision, 0, len(loaded))
		for _, item := range loaded {
			decision, err := item.record.toDecision()
			if err != nil {
				return err
			}
			decisions = append(decisions, decision)
		}
		sort.Slice(decisions, func(i, j int) bool { return decisions[i].EventID() < decisions[j].EventID() })
		stats = durableStats(loaded)
		return nil
	})
	return decisions, stats, err
}

func (b *durableBackend) record(ctx context.Context, snapshot processprobe.Snapshot, failOnce *bool) (Decision, error) {
	var result Decision
	err := b.withLockedRoot(ctx, func(root *secureRoot) error {
		var err error
		result, err = recordDurableLocked(root, snapshot, failOnce)
		return err
	})
	return result, err
}

// recordBatch 在单个可信根锁内批量持久化快照并复用已加载记录。
func (b *durableBackend) recordBatch(ctx context.Context, snapshots []processprobe.Snapshot, failOnce *bool) ([]Decision, error) {
	decisions := make([]Decision, 0, len(snapshots))
	err := b.withLockedRoot(ctx, func(root *secureRoot) error {
		loaded, err := root.readDurableRecords()
		if err != nil {
			return err
		}
		for _, snapshot := range snapshots {
			if err := validateContext(ctx); err != nil {
				return err
			}
			if !isNoSignalSnapshot(snapshot) {
				return errors.New("process observation snapshot has signal authority")
			}
			key, decision, record, err := prepareDurableRecord(loaded, snapshot)
			if err != nil {
				return err
			}
			if err := writeDurableRecordWithCapacity(root, loaded, key, record); err != nil {
				return err
			}
			if consumeDurableFailure(failOnce) {
				return errors.New("injected durable projection fan-out failure")
			}
			if err := fanoutDurableRecord(root, loaded, key, &record); err != nil {
				return err
			}
			decision, err = record.toDecision()
			if err != nil {
				return err
			}
			decisions = append(decisions, decision)
		}
		return nil
	})
	return decisions, err
}

func recordDurableLocked(root *secureRoot, snapshot processprobe.Snapshot, failOnce *bool) (Decision, error) {
	loaded, err := root.readDurableRecords()
	if err != nil {
		return Decision{}, err
	}
	key, decision, record, err := prepareDurableRecord(loaded, snapshot)
	if err != nil {
		return Decision{}, err
	}
	if err := writeDurableRecordWithCapacity(root, loaded, key, record); err != nil {
		return Decision{}, err
	}
	if consumeDurableFailure(failOnce) {
		return decision, errors.New("injected durable projection fan-out failure")
	}
	if err := fanoutDurableRecord(root, loaded, key, &record); err != nil {
		return decision, err
	}
	return record.toDecision()
}

func prepareDurableRecord(loaded map[string]loadedDurableRecord, snapshot processprobe.Snapshot) (string, Decision, durableRecord, error) {
	reason := observationReason(snapshot)
	key, dropped, err := durableBucketChoice(loaded, snapshot, reason)
	if err != nil {
		return "", Decision{}, durableRecord{}, err
	}
	decision, record, encoded, err := buildDurableRecord(loaded, key, snapshot, reason, dropped)
	if err != nil {
		return "", Decision{}, durableRecord{}, err
	}
	if durableBucketNeedsOverflow(loaded, key, snapshot, reason, len(encoded)) {
		key = overflowBucketKey(snapshot, reason)
		decision, record, _, err = buildDurableRecord(loaded, key, snapshot, reason, dropped+1)
		if err != nil {
			return "", Decision{}, durableRecord{}, err
		}
	}
	return key, decision, record, nil
}

func durableBucketChoice(loaded map[string]loadedDurableRecord, snapshot processprobe.Snapshot, reason processprobe.ObservationReason) (string, uint64, error) {
	dedupKey, bucketKey := ghostKeys(snapshot, reason, snapshot.IdentityComplete())
	if dedupKey != "" {
		return "", 0, errors.New("complete lifecycle identity requires integration handoff")
	}
	if bucketKey == "" {
		return "", 0, errors.New("observation bucket key is empty")
	}
	if _, exists := loaded[bucketKey]; exists || durableBucketCount(loaded) < MaxObservationBuckets-1 {
		return bucketKey, 0, nil
	}
	return overflowBucketKey(snapshot, reason), 1, nil
}

func buildDurableRecord(loaded map[string]loadedDurableRecord, key string, snapshot processprobe.Snapshot, reason processprobe.ObservationReason, dropped uint64) (Decision, durableRecord, []byte, error) {
	current, exists := loaded[key]
	decision, err := updateDurableDecision(current.record, exists, snapshot, reason, key, dropped)
	if err != nil {
		return Decision{}, durableRecord{}, nil, err
	}
	decision.candidate = projectionFor(decision, ProjectionCandidate, false)
	decision.blocked = projectionFor(decision, ProjectionBlocked, false)
	decision.status = DecisionPairPending
	record := durableRecordFromDecision(decision)
	encoded, err := encodeDurableRecord(record)
	if err != nil {
		return Decision{}, durableRecord{}, nil, err
	}
	return decision, record, encoded, nil
}

func durableBucketNeedsOverflow(loaded map[string]loadedDurableRecord, key string, snapshot processprobe.Snapshot, reason processprobe.ObservationReason, encodedSize int) bool {
	overflow := overflowBucketKey(snapshot, reason)
	if key == overflow {
		return false
	}
	oldSize := uint64(0)
	if old, ok := loaded[key]; ok {
		oldSize = old.size
	}
	return durableBucketBytes(loaded, key)-oldSize+uint64(encodedSize) > MaxObservationBucketBytes
}

func consumeDurableFailure(failOnce *bool) bool {
	if failOnce == nil || !*failOnce {
		return false
	}
	*failOnce = false
	return true
}

func (b *durableBackend) retry(ctx context.Context, failOnce *bool) (int, error) {
	count := 0
	err := b.withLockedRoot(ctx, func(root *secureRoot) error {
		loaded, err := root.readDurableRecords()
		if err != nil {
			return err
		}
		keys := make([]string, 0, len(loaded))
		for key, item := range loaded {
			if item.record.Status == string(DecisionPairPending) {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			item := loaded[key]
			if failOnce != nil && *failOnce {
				*failOnce = false
				return errors.New("injected durable projection fan-out failure")
			}
			record := item.record
			if err := fanoutDurableRecord(root, loaded, key, &record); err != nil {
				return err
			}
			count++
		}
		return nil
	})
	return count, err
}

func (b *durableBackend) withLockedRoot(ctx context.Context, fn func(*secureRoot) error) error {
	if b == nil {
		return ErrDurableStoreClosed
	}
	if err := validateContext(ctx); err != nil {
		return err
	}
	root, err := openDurableRoot(b.root)
	if err != nil {
		return err
	}
	defer func() { _ = root.close() }()
	dev, ino := root.identity()
	if dev != b.dev || ino != b.ino {
		return ErrDurableRootMismatch
	}
	return root.withStoreLock(ctx, fn)
}

func updateDurableDecision(record durableRecord, exists bool, snapshot processprobe.Snapshot, reason processprobe.ObservationReason, key string, dropped uint64) (Decision, error) {
	if !exists {
		decision, err := updateDecision(Decision{}, false, snapshot, reason, key, dropped)
		if err != nil {
			return Decision{}, err
		}
		return decision, nil
	}
	decision, err := record.toDecision()
	if err != nil {
		return Decision{}, err
	}
	operationID, err := randomID()
	if err != nil {
		return Decision{}, err
	}
	now := snapshot.ObservedAt()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	decision.operationID = operationID
	decision.lastSeen = now
	decision.seenCount++
	decision.droppedCount += dropped
	decision.missingFields = unionFields(decision.missingFields, snapshot.MissingFields())
	decision.latestEvidenceHash = snapshot.EvidenceDigest()
	decision.reason = reason
	decision.status = DecisionPairPending
	return decision, nil
}

func pruneDurableOldest(root *secureRoot, loaded map[string]loadedDurableRecord, neededBytes uint64, excludeKey string) {
	for neededBytes > 0 {
		var oldestKey string
		var oldestRecord loadedDurableRecord
		found := false
		for k, item := range loaded {
			if k == excludeKey {
				continue
			}
			if !found || item.record.LastSeen.Before(oldestRecord.record.LastSeen) {
				oldestKey = k
				oldestRecord = item
				found = true
			}
		}
		if !found {
			break
		}
		_ = root.deleteDurableRecord(oldestRecord.record.EventID)
		delete(loaded, oldestKey)
		if oldestRecord.size >= neededBytes {
			neededBytes = 0
		} else {
			neededBytes -= oldestRecord.size
		}
	}
}

func writeDurableRecordWithCapacity(root *secureRoot, loaded map[string]loadedDurableRecord, key string, record durableRecord) error {
	raw, err := encodeDurableRecord(record)
	if err != nil {
		return err
	}
	var oldSize uint64
	if old, ok := loaded[key]; ok {
		oldSize = old.size
	}
	projected := durableStatsBytes(loaded) - oldSize + uint64(len(raw))
	if projected > MaxObservationBytes {
		pruneDurableOldest(root, loaded, projected-MaxObservationBytes, key)
		projected = durableStatsBytes(loaded) - oldSize + uint64(len(raw))
		if projected > MaxObservationBytes {
			return ErrCapacity
		}
	}
	if err := root.publishDurableRecord(record.EventID, raw); err != nil {
		return err
	}
	loaded[key] = loadedDurableRecord{record: record, size: uint64(len(raw))}
	return nil
}

func fanoutDurableRecord(root *secureRoot, loaded map[string]loadedDurableRecord, key string, record *durableRecord) error {
	if record == nil {
		return errors.New("durable incident is nil")
	}
	record.Candidate.Acked = true
	record.Status = string(DecisionPairPending)
	if err := publishDurableProjection(root, loaded, key, record); err != nil {
		return err
	}
	record.Blocked.Acked = true
	record.Status = string(DecisionPersisted)
	return publishDurableProjection(root, loaded, key, record)
}

func publishDurableProjection(root *secureRoot, loaded map[string]loadedDurableRecord, key string, record *durableRecord) error {
	raw, err := encodeDurableRecord(*record)
	if err != nil {
		return err
	}
	projected := durableStatsBytes(loaded) - loaded[key].size + uint64(len(raw))
	if projected > MaxObservationBytes {
		pruneDurableOldest(root, loaded, projected-MaxObservationBytes, key)
		projected = durableStatsBytes(loaded) - loaded[key].size + uint64(len(raw))
		if projected > MaxObservationBytes {
			return ErrCapacity
		}
	}
	if err := root.publishDurableRecord(record.EventID, raw); err != nil {
		return err
	}
	loaded[key] = loadedDurableRecord{record: *record, size: uint64(len(raw))}
	return nil
}

func durableStats(loaded map[string]loadedDurableRecord) Stats {
	stats := Stats{TotalBytes: durableStatsBytes(loaded), ObservationBytes: durableStatsBytes(loaded), BucketCount: durableBucketCount(loaded)}
	for _, item := range loaded {
		if item.record.DedupKey != "" {
			stats.DedupCount++
		}
		if item.record.Candidate.SignalSent || item.record.Blocked.SignalSent {
			stats.SignalCount++
		}
	}
	return stats
}

func durableStatsBytes(loaded map[string]loadedDurableRecord) uint64 {
	var total uint64
	for _, item := range loaded {
		total += item.size
	}
	return total
}

func durableBucketCount(loaded map[string]loadedDurableRecord) int {
	count := 0
	for _, item := range loaded {
		if item.record.BucketKey != "" {
			count++
		}
	}
	return count
}

func durableBucketBytes(loaded map[string]loadedDurableRecord, key string) uint64 {
	var total uint64
	for _, item := range loaded {
		itemKey := item.record.BucketKey
		if itemKey == "" {
			itemKey = item.record.DedupKey
		}
		if itemKey == key {
			total += item.size
		}
	}
	return total
}

func durableRecordFromDecision(decision Decision) durableRecord {
	return durableRecord{
		SchemaVersion: durableSchemaVersion,
		EventID:       decision.EventID(), OperationID: decision.OperationID(),
		LifecycleKey: decision.LifecycleKey(), DedupKey: decision.DedupKey(), BucketKey: decision.BucketKey(),
		Reason: string(decision.Reason()), Status: string(decision.Status()),
		FirstSeen: decision.FirstSeen(), LastSeen: decision.LastSeen(), SeenCount: decision.SeenCount(), DroppedCount: decision.DroppedCount(),
		MissingFields: decision.MissingFields(), EvidenceHash: decision.LatestEvidenceDigest(),
		Candidate: durableProjectionFrom(decision.CandidateProjection()), Blocked: durableProjectionFrom(decision.BlockedProjection()),
	}
}

func durableProjectionFrom(projection Projection) durableProjection {
	return durableProjection{ID: projection.ID(), EventID: projection.EventID(), OperationID: projection.OperationID(), Kind: string(projection.Kind()), Event: projection.Event(), Reason: string(projection.Reason()), SignalSent: false, Acked: projection.Acked()}
}

func (record durableRecord) toDecision() (Decision, error) {
	if err := validateDurableRecord(record); err != nil {
		return Decision{}, err
	}
	return Decision{
		decisionIdentity: decisionIdentity{eventID: record.EventID, operationID: record.OperationID, lifecycleKey: record.LifecycleKey, dedupKey: record.DedupKey, bucketKey: record.BucketKey},
		decisionEvidence: decisionEvidence{reason: processprobe.ObservationReason(record.Reason), status: DecisionStatus(record.Status), firstSeen: record.FirstSeen, lastSeen: record.LastSeen, seenCount: record.SeenCount, droppedCount: record.DroppedCount, missingFields: append([]string(nil), record.MissingFields...), latestEvidenceHash: record.EvidenceHash},
		decisionPair:     decisionPair{candidate: projectionFromDurable(record.Candidate), blocked: projectionFromDurable(record.Blocked)},
	}, nil
}

func projectionFromDurable(value durableProjection) Projection {
	return Projection{id: value.ID, eventID: value.EventID, operationID: value.OperationID, kind: ProjectionKind(value.Kind), event: value.Event, reason: processprobe.ObservationReason(value.Reason), signalSent: false, acked: value.Acked}
}

func encodeDurableRecord(record durableRecord) ([]byte, error) {
	if err := validateDurableRecord(record); err != nil {
		return nil, err
	}
	return json.Marshal(record)
}

func decodeDurableRecord(raw []byte) (durableRecord, error) {
	var record durableRecord
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return durableRecord{}, fmt.Errorf("decode durable process observation: %w", err)
	}
	if err := validateDurableRecord(record); err != nil {
		return durableRecord{}, err
	}
	return record, nil
}

func validateDurableRecord(record durableRecord) error {
	validators := []func(durableRecord) error{
		validateDurableIdentity,
		validateDurableKeys,
		validateDurableProjectionIdentity,
		validateDurableProjectionValues,
		validateDurableMissingFields,
	}
	for _, validator := range validators {
		if err := validator(record); err != nil {
			return err
		}
	}
	return nil
}

func validateDurableIdentity(record durableRecord) error {
	if record.SchemaVersion != durableSchemaVersion || !validID(record.EventID) || !validID(record.OperationID) {
		return errors.New("invalid durable process observation identity")
	}
	if !validDurableReason(record.Reason) {
		return errors.New("invalid durable process observation reason")
	}
	if record.Status != string(DecisionPersisted) && record.Status != string(DecisionPairPending) {
		return errors.New("invalid durable process observation status")
	}
	if invalidDurableEvidence(record) {
		return errors.New("invalid durable process observation evidence")
	}
	return nil
}

func invalidDurableEvidence(record durableRecord) bool {
	return record.Reason == "" || record.FirstSeen.IsZero() || record.LastSeen.IsZero() || record.SeenCount == 0
}

func validateDurableKeys(record durableRecord) error {
	if record.BucketKey == "" && record.DedupKey == "" {
		return errors.New("durable process observation has no bounded key")
	}
	for _, key := range []string{record.BucketKey, record.DedupKey} {
		if key != "" && !validHash(key) {
			return errors.New("durable process observation key is not a digest")
		}
	}
	return nil
}

func validateDurableProjectionIdentity(record durableRecord) error {
	if record.Candidate.ID != record.EventID+"|"+string(ProjectionCandidate) || record.Blocked.ID != record.EventID+"|"+string(ProjectionBlocked) {
		return errors.New("durable process observation projection identity is invalid")
	}
	if record.Candidate.EventID != record.EventID || record.Blocked.EventID != record.EventID {
		return errors.New("durable process observation projection event is inconsistent")
	}
	if record.Candidate.OperationID != record.OperationID || record.Blocked.OperationID != record.OperationID {
		return errors.New("durable process observation projection operation is inconsistent")
	}
	if record.Candidate.SignalSent || record.Blocked.SignalSent {
		return errors.New("durable process observation projection has signal authority")
	}
	return nil
}

func validateDurableProjectionValues(record durableRecord) error {
	if record.Candidate.Kind != string(ProjectionCandidate) || record.Blocked.Kind != string(ProjectionBlocked) {
		return errors.New("durable process observation projection kind is invalid")
	}
	if record.Candidate.Event != "lsp_ghost_candidate_observed" || record.Blocked.Event != "lsp_reclaim_blocked" {
		return errors.New("durable process observation projection event is invalid")
	}
	if record.Candidate.Reason != record.Reason || record.Blocked.Reason != record.Reason {
		return errors.New("durable process observation projection reason is inconsistent")
	}
	if record.Status == string(DecisionPersisted) && (!record.Candidate.Acked || !record.Blocked.Acked) {
		return errors.New("persisted durable process observation has an unacknowledged projection")
	}
	return nil
}

func validateDurableMissingFields(record durableRecord) error {
	for _, field := range record.MissingFields {
		if field == "" || strings.ContainsAny(field, "/\\\t\r\n") {
			return errors.New("durable process observation missing field is unsafe")
		}
	}
	return nil
}

func validDurableReason(value string) bool {
	switch processprobe.ObservationReason(value) {
	case processprobe.ReasonNoAuthoritativeOwner, processprobe.ReasonPermissionDenied, processprobe.ReasonIdentityMismatch, processprobe.ReasonPIDReuse, processprobe.ReasonProbeFailed, processprobe.ReasonUnknown:
		return true
	default:
		return false
	}
}

func validID(value string) bool {
	if len(value) != 32 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
