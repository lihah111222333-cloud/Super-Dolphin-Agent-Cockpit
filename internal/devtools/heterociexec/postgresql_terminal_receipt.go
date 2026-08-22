package heterociexec

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"
)

const (
	// PostgreSQLTerminalReceiptSchema 是 remote-ci terminal receipt 的冻结 wire schema。
	PostgreSQLTerminalReceiptSchema = "remote-ci-result/v1"
	// PostgreSQLReceiptEventSchema 是 remote-ci observable event 的冻结 wire schema。
	PostgreSQLReceiptEventSchema = "remote-ci-event/v1"

	// PostgreSQLReceiptStatusSucceeded 表示 workload 以 exit code 0 完成。
	PostgreSQLReceiptStatusSucceeded = "succeeded"
	// PostgreSQLReceiptStatusFailed 表示 workload 以非零 exit code 终止。
	PostgreSQLReceiptStatusFailed = "failed"
	// PostgreSQLReceiptStatusBlocked 表示容量或契约阻断且没有执行。
	PostgreSQLReceiptStatusBlocked = "blocked"

	// PostgreSQLReceiptCleanupVerified 表示清理、释放和 worktree 状态均已证明。
	PostgreSQLReceiptCleanupVerified = "verified"
	// PostgreSQLReceiptCleanupIncomplete 表示清理证明缺失，不能发布 terminal receipt。
	PostgreSQLReceiptCleanupIncomplete = "incomplete"
)

// PostgreSQLReceiptEventStage 是 observable event 的固定 lifecycle stage。
type PostgreSQLReceiptEventStage string

const (
	PostgreSQLReceiptEventStageRequestValidation PostgreSQLReceiptEventStage = "request_validation"
	PostgreSQLReceiptEventStageManifestBinding   PostgreSQLReceiptEventStage = "manifest_binding"
	PostgreSQLReceiptEventStageCapacitySnapshot  PostgreSQLReceiptEventStage = "capacity_snapshot"
	PostgreSQLReceiptEventStageReservation       PostgreSQLReceiptEventStage = "reservation"
	PostgreSQLReceiptEventStageMaterialize       PostgreSQLReceiptEventStage = "materialize"
	PostgreSQLReceiptEventStageExecute           PostgreSQLReceiptEventStage = "execute"
	PostgreSQLReceiptEventStageCleanup           PostgreSQLReceiptEventStage = "cleanup"
	PostgreSQLReceiptEventStageTerminal          PostgreSQLReceiptEventStage = "terminal"
)

// PostgreSQLReceiptEventState 是 observable event 的固定 state 集合。
type PostgreSQLReceiptEventState string

const (
	PostgreSQLReceiptEventStateStarted   PostgreSQLReceiptEventState = "started"
	PostgreSQLReceiptEventStateSucceeded PostgreSQLReceiptEventState = "succeeded"
	PostgreSQLReceiptEventStateCompleted PostgreSQLReceiptEventState = "completed"
	PostgreSQLReceiptEventStateFailed    PostgreSQLReceiptEventState = "failed"
	PostgreSQLReceiptEventStateBlocked   PostgreSQLReceiptEventState = "blocked"
	PostgreSQLReceiptEventStateSkipped   PostgreSQLReceiptEventState = "skipped"
)

// PostgreSQLReceiptEvent 是一条可重放、不可变的 remote-ci event。
type PostgreSQLReceiptEvent struct {
	SchemaVersion       string                      `json:"schema_version"`
	OperationID         string                      `json:"operation_id"`
	ManifestDigest      string                      `json:"manifest_digest"`
	PlanDigest          string                      `json:"plan_digest"`
	NodeID              string                      `json:"node_id"`
	NodeIDDigest        string                      `json:"node_id_digest"`
	ExecutorKind        string                      `json:"executor_kind"`
	AuthorityGeneration int64                       `json:"authority_generation"`
	Seq                 int64                       `json:"seq"`
	OccurredAt          time.Time                   `json:"occurred_at"`
	Stage               PostgreSQLReceiptEventStage `json:"stage"`
	State               PostgreSQLReceiptEventState `json:"state"`
	Code                string                      `json:"code"`
	EventDigest         string                      `json:"-"`
}

// PostgreSQLTerminalReceipt 是一个 operation 与其唯一 bound executor/node 的 terminal receipt；多节点运行必须由上层派生不同 operation_id 后在外部聚合，receipt 本身不承载 aggregate。
type PostgreSQLTerminalReceipt struct {
	SchemaVersion       string    `json:"schema_version"`
	OperationID         string    `json:"operation_id"`
	RequestDigest       string    `json:"request_digest"`
	ManifestDigest      string    `json:"manifest_digest"`
	PlanDigest          string    `json:"plan_digest"`
	NodeID              string    `json:"node_id"`
	ExecutorKind        string    `json:"executor_kind"`
	AuthorityGeneration int64     `json:"authority_generation"`
	StartedAt           time.Time `json:"started_at"`
	FinishedAt          time.Time `json:"finished_at"`
	ExitCode            int       `json:"exit_code"`
	CleanupState        string    `json:"cleanup_state"`
	ErrorCode           string    `json:"error_code"`
	CapacityCode        string    `json:"capacity_code"`
	Status              string    `json:"status"`
	EventSeq            int64     `json:"event_seq"`
	EventDigest         string    `json:"event_digest"`
	ReceiptDigest       string    `json:"-"`
}

// Validate 校验 event 的身份、固定 stage/state、时间和 bounded code。
func (event PostgreSQLReceiptEvent) Validate() error {
	if err := validatePostgreSQLReceiptEventIdentity(event); err != nil {
		return err
	}
	if err := validatePostgreSQLReceiptEventBinding(event); err != nil {
		return err
	}
	if !validPostgreSQLReceiptStage(event.Stage) || !validPostgreSQLReceiptState(event.State) || !validTerminalCode(event.Code) {
		return ErrPostgreSQLReceiptStore
	}
	if event.EventDigest != "" && !validTerminalDigest(event.EventDigest) {
		return ErrPostgreSQLReceiptStore
	}
	return nil
}

// Validate 校验 terminal receipt 的完整发布条件。
func (receipt PostgreSQLTerminalReceipt) Validate() error {
	if err := validatePostgreSQLTerminalReceiptIdentity(receipt); err != nil {
		return err
	}
	if err := validatePostgreSQLTerminalReceiptTiming(receipt); err != nil {
		return err
	}
	return validatePostgreSQLTerminalReceiptOutcome(receipt)
}

func validatePostgreSQLReceiptEventIdentity(event PostgreSQLReceiptEvent) error {
	if event.SchemaVersion != PostgreSQLReceiptEventSchema || !validTerminalOperationID(event.OperationID) || !validTerminalDigest(event.ManifestDigest) || !validTerminalDigest(event.PlanDigest) {
		return ErrPostgreSQLReceiptStore
	}
	return nil
}

func validatePostgreSQLReceiptEventBinding(event PostgreSQLReceiptEvent) error {
	if !validTerminalIdentity(event.NodeID) || !validTerminalIdentity(event.ExecutorKind) || event.AuthorityGeneration <= 0 || event.Seq <= 0 || !validPostgreSQLReceiptTime(event.OccurredAt) {
		return ErrPostgreSQLReceiptStore
	}
	if event.NodeIDDigest != digestPostgreSQLReceiptBytes([]byte(event.NodeID)) || !validTerminalDigest(event.NodeIDDigest) {
		return ErrPostgreSQLReceiptStore
	}
	return nil
}

func validatePostgreSQLTerminalReceiptIdentity(receipt PostgreSQLTerminalReceipt) error {
	if !validPostgreSQLTerminalReceiptHeader(receipt) || !validPostgreSQLTerminalReceiptBinding(receipt) {
		return ErrPostgreSQLReceiptStore
	}
	return nil
}

func validPostgreSQLTerminalReceiptHeader(receipt PostgreSQLTerminalReceipt) bool {
	return receipt.SchemaVersion == PostgreSQLTerminalReceiptSchema && validTerminalOperationID(receipt.OperationID) && validTerminalDigest(receipt.RequestDigest) && validTerminalDigest(receipt.ManifestDigest) && validTerminalDigest(receipt.PlanDigest)
}

func validPostgreSQLTerminalReceiptBinding(receipt PostgreSQLTerminalReceipt) bool {
	return validTerminalIdentity(receipt.NodeID) && validTerminalIdentity(receipt.ExecutorKind) && receipt.AuthorityGeneration > 0 && receipt.EventSeq > 0 && validTerminalDigest(receipt.EventDigest)
}

func validatePostgreSQLTerminalReceiptTiming(receipt PostgreSQLTerminalReceipt) error {
	if !validPostgreSQLReceiptTime(receipt.StartedAt) || !validPostgreSQLReceiptTime(receipt.FinishedAt) || receipt.FinishedAt.Before(receipt.StartedAt) || receipt.CleanupState != PostgreSQLReceiptCleanupVerified {
		return ErrPostgreSQLReceiptStore
	}
	return nil
}

func validatePostgreSQLTerminalReceiptOutcome(receipt PostgreSQLTerminalReceipt) error {
	if receipt.ExitCode < -1 || receipt.ExitCode > 255 || !validTerminalCode(receipt.ErrorCode) || !validTerminalCode(receipt.CapacityCode) {
		return ErrPostgreSQLReceiptStore
	}
	switch receipt.Status {
	case PostgreSQLReceiptStatusSucceeded:
		return validatePostgreSQLSuccessReceipt(receipt)
	case PostgreSQLReceiptStatusFailed:
		return validatePostgreSQLFailedReceipt(receipt)
	case PostgreSQLReceiptStatusBlocked:
		return validatePostgreSQLBlockedReceipt(receipt)
	default:
		return ErrPostgreSQLReceiptStore
	}
}

func validatePostgreSQLSuccessReceipt(receipt PostgreSQLTerminalReceipt) error {
	if receipt.ExitCode != 0 || receipt.ErrorCode != "" || receipt.CapacityCode != "" {
		return ErrPostgreSQLReceiptStore
	}
	return nil
}

func validatePostgreSQLFailedReceipt(receipt PostgreSQLTerminalReceipt) error {
	if receipt.ExitCode == 0 || receipt.ErrorCode == "" || receipt.CapacityCode != "" {
		return ErrPostgreSQLReceiptStore
	}
	return nil
}

func validatePostgreSQLBlockedReceipt(receipt PostgreSQLTerminalReceipt) error {
	if receipt.ExitCode != -1 || receipt.CapacityCode == "" || receipt.ErrorCode != "" {
		return ErrPostgreSQLReceiptStore
	}
	return nil
}

// EncodePostgreSQLReceiptEvent 返回 canonical event bytes 与 SHA-256 digest。
func EncodePostgreSQLReceiptEvent(event PostgreSQLReceiptEvent) ([]byte, string, error) {
	return canonicalPostgreSQLReceiptEvent(event)
}

// EncodePostgreSQLTerminalReceipt 返回 canonical terminal receipt bytes 与 SHA-256 digest。
func EncodePostgreSQLTerminalReceipt(receipt PostgreSQLTerminalReceipt) ([]byte, string, error) {
	return canonicalPostgreSQLTerminalReceipt(receipt)
}

func canonicalPostgreSQLReceiptEvent(event PostgreSQLReceiptEvent) ([]byte, string, error) {
	suppliedDigest := event.EventDigest
	event.EventDigest = ""
	if err := event.Validate(); err != nil {
		return nil, "", err
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		return nil, "", fmt.Errorf("%w: encode receipt event: %w", ErrPostgreSQLReceiptStore, err)
	}
	digest := digestPostgreSQLReceiptBytes(encoded)
	if suppliedDigest != "" && suppliedDigest != digest {
		return nil, "", ErrPostgreSQLReceiptConflict
	}
	return encoded, digest, nil
}

func canonicalPostgreSQLTerminalReceipt(receipt PostgreSQLTerminalReceipt) ([]byte, string, error) {
	suppliedDigest := receipt.ReceiptDigest
	receipt.ReceiptDigest = ""
	if err := receipt.Validate(); err != nil {
		return nil, "", err
	}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		return nil, "", fmt.Errorf("%w: encode terminal receipt: %w", ErrPostgreSQLReceiptStore, err)
	}
	digest := digestPostgreSQLReceiptBytes(encoded)
	if suppliedDigest != "" && suppliedDigest != digest {
		return nil, "", ErrPostgreSQLReceiptConflict
	}
	return encoded, digest, nil
}

func decodePostgreSQLReceiptEvent(raw []byte, digest string) (PostgreSQLReceiptEvent, error) {
	if len(raw) == 0 || !validTerminalDigest(digest) || digestPostgreSQLReceiptBytes(raw) != digest {
		return PostgreSQLReceiptEvent{}, ErrPostgreSQLReceiptStore
	}
	var event PostgreSQLReceiptEvent
	if err := decodeStrictPostgreSQLReceiptJSON(raw, &event); err != nil {
		return PostgreSQLReceiptEvent{}, fmt.Errorf("%w: decode receipt event: %w", ErrPostgreSQLReceiptStore, err)
	}
	event.EventDigest = digest
	if err := event.Validate(); err != nil {
		return PostgreSQLReceiptEvent{}, err
	}
	return event, nil
}

func decodePostgreSQLTerminalReceipt(raw []byte, digest string) (PostgreSQLTerminalReceipt, error) {
	if len(raw) == 0 || !validTerminalDigest(digest) || digestPostgreSQLReceiptBytes(raw) != digest {
		return PostgreSQLTerminalReceipt{}, ErrPostgreSQLReceiptStore
	}
	var receipt PostgreSQLTerminalReceipt
	if err := decodeStrictPostgreSQLReceiptJSON(raw, &receipt); err != nil {
		return PostgreSQLTerminalReceipt{}, fmt.Errorf("%w: decode terminal receipt: %w", ErrPostgreSQLReceiptStore, err)
	}
	receipt.ReceiptDigest = digest
	if err := receipt.Validate(); err != nil {
		return PostgreSQLTerminalReceipt{}, err
	}
	return receipt, nil
}

func decodeStrictPostgreSQLReceiptJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("trailing JSON is invalid")
	}
	canonical, err := json.Marshal(target)
	if err != nil {
		return err
	}
	if !bytes.Equal(raw, canonical) {
		return fmt.Errorf("JSON is not canonical")
	}
	return nil
}

func validPostgreSQLReceiptStage(stage PostgreSQLReceiptEventStage) bool {
	switch stage {
	case PostgreSQLReceiptEventStageRequestValidation, PostgreSQLReceiptEventStageManifestBinding, PostgreSQLReceiptEventStageCapacitySnapshot, PostgreSQLReceiptEventStageReservation, PostgreSQLReceiptEventStageMaterialize, PostgreSQLReceiptEventStageExecute, PostgreSQLReceiptEventStageCleanup, PostgreSQLReceiptEventStageTerminal:
		return true
	default:
		return false
	}
}

func validPostgreSQLReceiptState(state PostgreSQLReceiptEventState) bool {
	switch state {
	case PostgreSQLReceiptEventStateStarted, PostgreSQLReceiptEventStateSucceeded, PostgreSQLReceiptEventStateCompleted, PostgreSQLReceiptEventStateFailed, PostgreSQLReceiptEventStateBlocked, PostgreSQLReceiptEventStateSkipped:
		return true
	default:
		return false
	}
}

func validPostgreSQLReceiptStateTransition(previous, next PostgreSQLReceiptEventState) bool {
	switch previous {
	case PostgreSQLReceiptEventStateStarted:
		return next == PostgreSQLReceiptEventStateSucceeded || next == PostgreSQLReceiptEventStateCompleted || next == PostgreSQLReceiptEventStateFailed || next == PostgreSQLReceiptEventStateBlocked || next == PostgreSQLReceiptEventStateSkipped
	case PostgreSQLReceiptEventStateSucceeded:
		return next == PostgreSQLReceiptEventStateCompleted
	default:
		return false
	}
}

func validTerminalOperationID(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func validTerminalDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func validTerminalIdentity(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && len(value) <= 256 && strings.IndexFunc(value, unicode.IsControl) < 0 && !strings.ContainsAny(value, `/\\`)
}

func validPostgreSQLReceiptTime(value time.Time) bool {
	if value.IsZero() || value.Location() != time.UTC || value != value.Round(0) {
		return false
	}
	encoded := value.Format(time.RFC3339Nano)
	parsed, err := time.Parse(time.RFC3339Nano, encoded)
	return err == nil && parsed.Location() == time.UTC && parsed.Equal(value) && parsed.Format(time.RFC3339Nano) == encoded
}

func parsePostgreSQLReceiptTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || !validPostgreSQLReceiptTime(parsed) || parsed.Format(time.RFC3339Nano) != value {
		return time.Time{}, ErrPostgreSQLReceiptStore
	}
	return parsed, nil
}

func validTerminalCode(value string) bool {
	if len(value) > 128 || strings.TrimSpace(value) != value || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && !strings.ContainsRune("._-", character) {
			return false
		}
	}
	return true
}

func digestPostgreSQLReceiptBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}
