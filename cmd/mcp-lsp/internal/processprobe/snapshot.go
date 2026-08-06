// Package processprobe provides a sealed, read-only process identity snapshot.
// It deliberately exposes no process-control capability.
package processprobe

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// AuthorityDecision describes the only decision a process probe can make.
type AuthorityDecision string

const (
	// AuthorityNoSignal keeps observation permanently outside the process-control authority.
	AuthorityNoSignal AuthorityDecision = "no_signal"
)

// ObservationReason identifies why a snapshot is incomplete or blocked.
type ObservationReason string

const (
	ReasonNoAuthoritativeOwner ObservationReason = "no_authoritative_owner"
	ReasonPermissionDenied     ObservationReason = "permission_denied"
	ReasonIdentityMismatch     ObservationReason = "identity_mismatch"
	ReasonPIDReuse             ObservationReason = "pid_reuse"
	ReasonProbeFailed          ObservationReason = "probe_failed"
	ReasonUnknown              ObservationReason = "unknown"
)

// lifecycleAssociationFields 返回 lifecycle 归属证明所需字段的独立快照。
// probe 不能制造这些字段的权威值，故每次 Snapshot 都明确报告其缺失。
func lifecycleAssociationFields() []string {
	return []string{
		"receipt_id",
		"owner_instance_id",
		"workspace_hash",
		"generation",
		"client_start",
		"binary_digest",
	}
}

type snapshotCore struct {
	valid      bool
	pid        int
	alive      bool
	platform   string
	reason     ObservationReason
	observedAt time.Time
}

type snapshotIdentity struct {
	parentPID      int
	processGroupID string
	sessionID      string
	uid            string
	startIdentity  string
	executable     string
}

type snapshotSafety struct {
	missingFields  []string
	evidenceDigest string
	redactedError  string
}

// Snapshot is an immutable value produced only by this package's platform probe.
// Its fields are deliberately private: callers cannot manufacture platform proof
// with a struct literal or mutate a previously observed identity.
type Snapshot struct {
	snapshotCore
	snapshotIdentity
	snapshotSafety
}

// Valid reports whether the platform supplied a complete, parseable snapshot.
func (s snapshotCore) Valid() bool { return s.valid }

// PID returns the observed process identifier, or zero for an invalid snapshot.
func (s snapshotCore) PID() int { return s.pid }

// ParentPID returns the observed parent identifier when the platform exposed it.
func (s snapshotIdentity) ParentPID() int { return s.parentPID }

// ProcessGroupID returns the observed process-group identifier, if available.
func (s snapshotIdentity) ProcessGroupID() string { return s.processGroupID }

// SessionID returns the observed session identifier, if available.
func (s snapshotIdentity) SessionID() string { return s.sessionID }

// UID returns the observed owner identifier, if available.
func (s snapshotIdentity) UID() string { return s.uid }

// StartIdentity returns the native process-start token used to distinguish PID reuse.
func (s snapshotIdentity) StartIdentity() string { return s.startIdentity }

// Executable returns a redacted executable basename suitable for logs.
func (s snapshotIdentity) Executable() string { return s.executable }

// Alive reports the result of the read-only liveness check.
func (s snapshotCore) Alive() bool { return s.alive }

// Platform returns the platform identifier that produced the snapshot.
func (s snapshotCore) Platform() string { return s.platform }

// Reason returns the conservative observation reason for an incomplete snapshot.
func (s snapshotCore) Reason() ObservationReason { return s.reason }

// MissingFields returns a copy of fields that could not be observed.
func (s snapshotSafety) MissingFields() []string {
	return append([]string(nil), s.missingFields...)
}

// EvidenceDigest returns a stable digest of the redacted observation evidence.
func (s snapshotSafety) EvidenceDigest() string { return s.evidenceDigest }

// ObservedAt returns the platform observation time.
func (s snapshotCore) ObservedAt() time.Time { return s.observedAt }

// RedactedError returns a bounded, path-free diagnostic, when one exists.
func (s snapshotSafety) RedactedError() string { return s.redactedError }

// IdentityComplete reports whether this snapshot carries lifecycle admission
// proof. Platform PID/start evidence alone is deliberately insufficient, so
// snapshots produced by this lane always use bounded observation buckets.
func (s Snapshot) IdentityComplete() bool {
	return false
}

// AuthorityDecision always returns no_signal; a probe has no destructive capability.
func (s Snapshot) AuthorityDecision() AuthorityDecision { return AuthorityNoSignal }

// SignalSent is permanently false for a read-only snapshot.
func (s Snapshot) SignalSent() bool { return false }

// MarshalJSON exposes only redacted observation data, never a caller-controlled proof.
func (s Snapshot) MarshalJSON() ([]byte, error) {
	type wireSnapshot struct {
		Valid          bool              `json:"valid"`
		PID            int               `json:"pid"`
		ParentPID      int               `json:"parent_pid,omitempty"`
		ProcessGroupID string            `json:"process_group_id,omitempty"`
		SessionID      string            `json:"session_id,omitempty"`
		UID            string            `json:"uid,omitempty"`
		StartIdentity  string            `json:"start_identity,omitempty"`
		Executable     string            `json:"executable,omitempty"`
		Alive          bool              `json:"alive"`
		Platform       string            `json:"platform"`
		Reason         ObservationReason `json:"reason,omitempty"`
		MissingFields  []string          `json:"missing_fields,omitempty"`
		EvidenceDigest string            `json:"evidence_digest,omitempty"`
		ObservedAt     time.Time         `json:"observed_at"`
		RedactedError  string            `json:"error,omitempty"`
		Authority      AuthorityDecision `json:"authority_decision"`
		SignalSent     bool              `json:"signal_sent"`
	}
	return json.Marshal(wireSnapshot{
		Valid:          s.valid,
		PID:            s.pid,
		ParentPID:      s.parentPID,
		ProcessGroupID: s.processGroupID,
		SessionID:      s.sessionID,
		UID:            s.uid,
		StartIdentity:  s.startIdentity,
		Executable:     s.executable,
		Alive:          s.alive,
		Platform:       s.platform,
		Reason:         s.reason,
		MissingFields:  s.MissingFields(),
		EvidenceDigest: s.evidenceDigest,
		ObservedAt:     s.observedAt,
		RedactedError:  s.redactedError,
		Authority:      AuthorityNoSignal,
		SignalSent:     false,
	})
}

func newSnapshot(
	pid int,
	parentPID int,
	processGroupID string,
	sessionID string,
	uid string,
	startIdentity string,
	executable string,
	alive bool,
	platform string,
	reason ObservationReason,
	missingFields []string,
	redactedError string,
) Snapshot {
	associationMissing := lifecycleAssociationFields()
	normalizedMissing := normalizeMissingFields(append(missingFields, associationMissing...))
	snapshot := Snapshot{
		snapshotCore: snapshotCore{
			valid:      reason == "" && pid > 1,
			pid:        pid,
			alive:      alive,
			platform:   strings.TrimSpace(platform),
			reason:     reason,
			observedAt: time.Now().UTC(),
		},
		snapshotIdentity: snapshotIdentity{
			parentPID:      parentPID,
			processGroupID: strings.TrimSpace(processGroupID),
			sessionID:      strings.TrimSpace(sessionID),
			uid:            strings.TrimSpace(uid),
			startIdentity:  strings.TrimSpace(startIdentity),
			executable:     strings.TrimSpace(executable),
		},
		snapshotSafety: snapshotSafety{
			missingFields: normalizedMissing,
			redactedError: boundError(redactedError),
		},
	}
	snapshot.evidenceDigest = digestSnapshot(snapshot)
	return snapshot
}

func normalizeMissingFields(fields []string) []string {
	if len(fields) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(fields))
	result := make([]string, 0, len(fields))
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
	return result
}

func boundError(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > 256 {
		return value[:256]
	}
	return value
}

func digestSnapshot(snapshot Snapshot) string {
	value := fmt.Sprintf(
		"v1|%s|%d|%d|%s|%s|%s|%s|%s|%t|%s|%s|%s",
		snapshot.platform,
		snapshot.pid,
		snapshot.parentPID,
		snapshot.processGroupID,
		snapshot.sessionID,
		snapshot.uid,
		snapshot.startIdentity,
		snapshot.executable,
		snapshot.alive,
		snapshot.reason,
		strings.Join(snapshot.missingFields, ","),
		snapshot.redactedError,
	)
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

func unsupportedSnapshot(platform string) (Snapshot, error) {
	snapshot := newSnapshot(0, 0, "", "", "", "", "", false, platform, ReasonProbeFailed, []string{"platform"}, "platform is unsupported")
	return snapshot, errors.New("process probe is unsupported on this platform")
}
