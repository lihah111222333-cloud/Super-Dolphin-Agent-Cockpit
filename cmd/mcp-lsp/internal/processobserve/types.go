// Package processobserve records conservative, no-signal process observations
// in a bounded in-memory projection. Persistent receipt integration is owned by
// the caller and is intentionally outside this package's authority boundary.
package processobserve

import (
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/processprobe"
)

const (
	// MaxObservationBytes bounds the in-memory event projection.
	MaxObservationBytes uint64 = 8 * 1024 * 1024
	// MaxObservationBuckets bounds observations without a complete lifecycle identity.
	MaxObservationBuckets = 1_024
)

var (
	// ErrCapacity means that the bounded in-memory projection cannot admit more evidence.
	ErrCapacity = errors.New("process observation memory capacity exhausted")
	// ErrDurabilityHandoff marks the intentionally unimplemented persistent integration.
	ErrDurabilityHandoff = errors.New("persistent process receipt integration is an explicit handoff")
)

// DecisionStatus describes one in-memory candidate/blocked projection pair.
type DecisionStatus string

const (
	DecisionPersisted   DecisionStatus = "persisted"
	DecisionPairPending DecisionStatus = "pair_pending"
)

// ProjectionKind identifies one half of the no-signal event pair.
type ProjectionKind string

const (
	ProjectionCandidate ProjectionKind = "candidate"
	ProjectionBlocked   ProjectionKind = "blocked"
)

// Projection is an immutable view of one no-signal projection.
type Projection struct {
	id         string
	eventID    string
	kind       ProjectionKind
	event      string
	reason     processprobe.ObservationReason
	signalSent bool
	acked      bool
}

// ID returns the projection identifier.
func (p Projection) ID() string { return p.id }

// EventID returns the parent observation event identifier.
func (p Projection) EventID() string { return p.eventID }

// Kind returns candidate or blocked.
func (p Projection) Kind() ProjectionKind { return p.kind }

// Event returns the redaction-safe event name.
func (p Projection) Event() string { return p.event }

// Reason returns the conservative blocked reason.
func (p Projection) Reason() processprobe.ObservationReason { return p.reason }

// SignalSent is permanently false for observation projections.
func (p Projection) SignalSent() bool { return false }

// Acked reports whether this projection is present in the in-memory pair.
func (p Projection) Acked() bool { return p.acked }

type decisionIdentity struct {
	eventID      string
	operationID  string
	lifecycleKey string
	dedupKey     string
	bucketKey    string
}

type decisionEvidence struct {
	reason             processprobe.ObservationReason
	status             DecisionStatus
	firstSeen          time.Time
	lastSeen           time.Time
	seenCount          uint64
	droppedCount       uint64
	missingFields      []string
	latestEvidenceHash string
}

type decisionPair struct {
	candidate Projection
	blocked   Projection
}

// Decision is an immutable view of one bounded observation bucket.
//
// A decision with an incomplete lifecycle identity has an empty LifecycleKey
// and DedupKey. Its BucketKey is only an aggregation key; it is not proof of
// process ownership or a reclaim authorization.
type Decision struct {
	decisionIdentity
	decisionEvidence
	decisionPair
}

// EventID returns the stable observation event identifier.
func (d decisionIdentity) EventID() string { return d.eventID }

// OperationID returns the unique identifier for this observation attempt.
func (d decisionIdentity) OperationID() string { return d.operationID }

// LifecycleKey returns a lifecycle correlation key only when one is admitted.
func (d decisionIdentity) LifecycleKey() string { return d.lifecycleKey }

// DedupKey returns a complete-identity key, or an empty string for buckets.
func (d decisionIdentity) DedupKey() string { return d.dedupKey }

// BucketKey returns the bounded incomplete-identity aggregation key.
func (d decisionIdentity) BucketKey() string { return d.bucketKey }

// Reason returns the conservative blocked reason.
func (d decisionEvidence) Reason() processprobe.ObservationReason { return d.reason }

// Status returns persisted or pair_pending.
func (d decisionEvidence) Status() DecisionStatus { return d.status }

// FirstSeen returns when this observation bucket was first observed.
func (d decisionEvidence) FirstSeen() time.Time { return d.firstSeen }

// LastSeen returns the most recent observation time.
func (d decisionEvidence) LastSeen() time.Time { return d.lastSeen }

// SeenCount returns observations merged into this bucket.
func (d decisionEvidence) SeenCount() uint64 { return d.seenCount }

// DroppedCount returns observations merged into the overflow bucket.
func (d decisionEvidence) DroppedCount() uint64 { return d.droppedCount }

// MissingFields returns a copy of missing lifecycle-association fields.
func (d decisionEvidence) MissingFields() []string { return append([]string(nil), d.missingFields...) }

// LatestEvidenceDigest returns the most recent redacted evidence digest.
func (d decisionEvidence) LatestEvidenceDigest() string { return d.latestEvidenceHash }

// CandidateProjection returns the ghost-candidate projection.
func (d decisionPair) CandidateProjection() Projection { return d.candidate }

// BlockedProjection returns the reclaim-blocked projection.
func (d decisionPair) BlockedProjection() Projection { return d.blocked }

// SignalSent is permanently false for observation decisions.
func (d Decision) SignalSent() bool { return false }

// Stats is a bounded in-memory projection accounting snapshot.
type Stats struct {
	TotalBytes       uint64
	ObservationBytes uint64
	BucketCount      int
	DedupCount       int
	SignalCount      int
}

// Event is the minimal logger payload emitted by Observer.
type Event struct {
	Name          string
	EventID       string
	OperationID   string
	Reason        processprobe.ObservationReason
	SignalSent    bool
	MissingFields []string
	SeenCount     uint64
	DroppedCount  uint64
}

// Logger receives redaction-safe observation events.
type Logger interface {
	Record(Event) error
}

func unionFields(left, right []string) []string {
	seen := make(map[string]struct{}, len(left)+len(right))
	result := make([]string, 0, len(left)+len(right))
	for _, fields := range [][]string{left, right} {
		for _, field := range fields {
			field = strings.TrimSpace(field)
			if field == "" {
				continue
			}
			if _, exists := seen[field]; exists {
				continue
			}
			seen[field] = struct{}{}
			result = append(result, field)
		}
	}
	sort.Strings(result)
	return result
}
