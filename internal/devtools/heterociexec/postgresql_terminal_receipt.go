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
	SchemaVersion         string                        `json:"schema_version"`
	OperationID           string                        `json:"operation_id"`
	ManifestDigest        string                        `json:"manifest_digest"`
	PlanDigest            string                        `json:"plan_digest"`
	ExecutorBindingDigest string                        `json:"executor_binding_digest"`
	NodeSetDigest         string                        `json:"node_set_digest"`
	AuthorityGeneration   int64                         `json:"authority_generation"`
	Seq                   int64                         `json:"seq"`
	OccurredAt            time.Time                     `json:"occurred_at"`
	Stage                 PostgreSQLReceiptEventStage   `json:"stage"`
	State                 PostgreSQLReceiptEventState   `json:"state"`
	Code                  string                        `json:"code"`
	Shards                []PostgreSQLReceiptEventShard `json:"shards"`
	EventDigest           string                        `json:"-"`
}

// PostgreSQLReceiptEventShard 是一条 lifecycle event 对实际节点分片的排序投影。
type PostgreSQLReceiptEventShard struct {
	NodeID         string                      `json:"node_id"`
	NodeIDDigest   string                      `json:"node_id_digest"`
	ExecutorKind   string                      `json:"executor_kind"`
	ShardDigest    string                      `json:"shard_digest"`
	BatchSetDigest string                      `json:"batch_set_digest"`
	UnitSetDigest  string                      `json:"unit_set_digest"`
	State          PostgreSQLReceiptEventState `json:"state"`
	Code           string                      `json:"code"`
}

// PostgreSQLTerminalShard 是 request terminal receipt 中一个实际节点的不可变终态。
type PostgreSQLTerminalShard struct {
	ChildOperationID string    `json:"child_operation_id"`
	NodeID           string    `json:"node_id"`
	NodeIDDigest     string    `json:"node_id_digest"`
	ExecutorKind     string    `json:"executor_kind"`
	ShardDigest      string    `json:"shard_digest"`
	BatchSetDigest   string    `json:"batch_set_digest"`
	UnitSetDigest    string    `json:"unit_set_digest"`
	StartedAt        time.Time `json:"started_at"`
	FinishedAt       time.Time `json:"finished_at"`
	ExitCode         int       `json:"exit_code"`
	CleanupState     string    `json:"cleanup_state"`
	ErrorCode        string    `json:"error_code"`
	CapacityCode     string    `json:"capacity_code"`
	Status           string    `json:"status"`
}

// PostgreSQLTerminalReceipt 是一个 request operation 的唯一多节点 aggregate terminal receipt。
type PostgreSQLTerminalReceipt struct {
	SchemaVersion         string                    `json:"schema_version"`
	OperationID           string                    `json:"operation_id"`
	RequestDigest         string                    `json:"request_digest"`
	ManifestDigest        string                    `json:"manifest_digest"`
	PlanDigest            string                    `json:"plan_digest"`
	ExecutorBindingDigest string                    `json:"executor_binding_digest"`
	NodeSetDigest         string                    `json:"node_set_digest"`
	AuthorityGeneration   int64                     `json:"authority_generation"`
	StartedAt             time.Time                 `json:"started_at"`
	FinishedAt            time.Time                 `json:"finished_at"`
	ExitCode              int                       `json:"exit_code"`
	CleanupState          string                    `json:"cleanup_state"`
	ErrorCode             string                    `json:"error_code"`
	CapacityCode          string                    `json:"capacity_code"`
	Status                string                    `json:"status"`
	Shards                []PostgreSQLTerminalShard `json:"shards"`
	EventSeq              int64                     `json:"event_seq"`
	EventDigest           string                    `json:"event_digest"`
	ReceiptDigest         string                    `json:"-"`
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
	if event.SchemaVersion != PostgreSQLReceiptEventSchema || !validTerminalOperationID(event.OperationID) || !validTerminalDigest(event.ManifestDigest) || !validTerminalDigest(event.PlanDigest) || !validTerminalDigest(event.ExecutorBindingDigest) || !validTerminalDigest(event.NodeSetDigest) {
		return ErrPostgreSQLReceiptStore
	}
	return nil
}

func validatePostgreSQLReceiptEventBinding(event PostgreSQLReceiptEvent) error {
	if event.AuthorityGeneration <= 0 || event.Seq <= 0 || !validPostgreSQLReceiptTime(event.OccurredAt) {
		return ErrPostgreSQLReceiptStore
	}
	want, err := postgreSQLReceiptEventNodeSetDigest(event.Shards)
	if err != nil || event.NodeSetDigest != want {
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
	return receipt.SchemaVersion == PostgreSQLTerminalReceiptSchema && validTerminalOperationID(receipt.OperationID) && validTerminalDigest(receipt.RequestDigest) && validTerminalDigest(receipt.ManifestDigest) && validTerminalDigest(receipt.PlanDigest) && validTerminalDigest(receipt.ExecutorBindingDigest) && validTerminalDigest(receipt.NodeSetDigest)
}

func validPostgreSQLTerminalReceiptBinding(receipt PostgreSQLTerminalReceipt) bool {
	if receipt.AuthorityGeneration <= 0 || receipt.EventSeq <= 0 || receipt.EventSeq > 8 || !validTerminalDigest(receipt.EventDigest) {
		return false
	}
	want, err := PostgreSQLTerminalNodeSetDigest(receipt.Shards)
	return err == nil && receipt.NodeSetDigest == want
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
	if err := validatePostgreSQLTerminalShards(receipt.Shards); err != nil {
		return err
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
	if receipt.ExitCode != 0 || receipt.ErrorCode != "" || receipt.CapacityCode != "" || len(receipt.Shards) == 0 {
		return ErrPostgreSQLReceiptStore
	}
	for _, shard := range receipt.Shards {
		if shard.Status != PostgreSQLReceiptStatusSucceeded {
			return ErrPostgreSQLReceiptStore
		}
	}
	return nil
}

func validatePostgreSQLFailedReceipt(receipt PostgreSQLTerminalReceipt) error {
	if receipt.ExitCode <= 0 || receipt.ErrorCode == "" || receipt.CapacityCode != "" || len(receipt.Shards) == 0 {
		return ErrPostgreSQLReceiptStore
	}
	found := false
	for _, shard := range receipt.Shards {
		found = found || shard.Status == PostgreSQLReceiptStatusFailed
	}
	if !found {
		return ErrPostgreSQLReceiptStore
	}
	return nil
}

func validatePostgreSQLBlockedReceipt(receipt PostgreSQLTerminalReceipt) error {
	if receipt.ExitCode != -1 || receipt.CapacityCode == "" || receipt.ErrorCode != "" || len(receipt.Shards) != 0 {
		return ErrPostgreSQLReceiptStore
	}
	return nil
}

func validatePostgreSQLTerminalShards(shards []PostgreSQLTerminalShard) error {
	if shards == nil {
		return ErrPostgreSQLReceiptStore
	}
	for index, shard := range shards {
		if index > 0 && shards[index-1].NodeID >= shard.NodeID {
			return ErrPostgreSQLReceiptStore
		}
		if err := shard.validate(); err != nil {
			return err
		}
	}
	return nil
}

func (shard PostgreSQLTerminalShard) validate() error {
	for _, validate := range []func(PostgreSQLTerminalShard) error{
		validatePostgreSQLTerminalShardIdentity,
		validatePostgreSQLTerminalShardDigests,
		validatePostgreSQLTerminalShardTiming,
		validatePostgreSQLTerminalShardOutcome,
	} {
		if err := validate(shard); err != nil {
			return err
		}
	}
	return nil
}

func validatePostgreSQLTerminalShardIdentity(shard PostgreSQLTerminalShard) error {
	if !validTerminalOperationID(shard.ChildOperationID) || !validTerminalIdentity(shard.NodeID) || !validTerminalIdentity(shard.ExecutorKind) {
		return ErrPostgreSQLReceiptStore
	}
	return nil
}

func validatePostgreSQLTerminalShardDigests(shard PostgreSQLTerminalShard) error {
	if shard.NodeIDDigest != digestPostgreSQLReceiptBytes([]byte(shard.NodeID)) || !validTerminalDigest(shard.ShardDigest) || !validTerminalDigest(shard.BatchSetDigest) || !validTerminalDigest(shard.UnitSetDigest) {
		return ErrPostgreSQLReceiptStore
	}
	return nil
}

func validatePostgreSQLTerminalShardTiming(shard PostgreSQLTerminalShard) error {
	if !validPostgreSQLReceiptTime(shard.StartedAt) || !validPostgreSQLReceiptTime(shard.FinishedAt) || shard.FinishedAt.Before(shard.StartedAt) || shard.CleanupState != PostgreSQLReceiptCleanupVerified {
		return ErrPostgreSQLReceiptStore
	}
	return nil
}

func validatePostgreSQLTerminalShardOutcome(shard PostgreSQLTerminalShard) error {
	if err := validatePostgreSQLTerminalShardExitFields(shard); err != nil {
		return err
	}
	switch shard.Status {
	case PostgreSQLReceiptStatusSucceeded:
		if shard.ExitCode != 0 || shard.ErrorCode != "" {
			return ErrPostgreSQLReceiptStore
		}
	case PostgreSQLReceiptStatusFailed:
		if shard.ExitCode <= 0 || shard.ErrorCode == "" {
			return ErrPostgreSQLReceiptStore
		}
	default:
		return ErrPostgreSQLReceiptStore
	}
	return nil
}

func validatePostgreSQLTerminalShardExitFields(shard PostgreSQLTerminalShard) error {
	if shard.ExitCode < 0 || shard.ExitCode > 255 || !validTerminalCode(shard.ErrorCode) || shard.CapacityCode != "" {
		return ErrPostgreSQLReceiptStore
	}
	return nil
}

// PostgreSQLTerminalNodeSetDigest 计算按 node_id 排序的 terminal shard binding 摘要。
func PostgreSQLTerminalNodeSetDigest(shards []PostgreSQLTerminalShard) (string, error) {
	if err := validatePostgreSQLTerminalShardBindings(shards); err != nil {
		return "", err
	}
	projection := make([]PostgreSQLReceiptEventShard, len(shards))
	for index, shard := range shards {
		projection[index] = PostgreSQLReceiptEventShard{NodeID: shard.NodeID, NodeIDDigest: shard.NodeIDDigest, ExecutorKind: shard.ExecutorKind, ShardDigest: shard.ShardDigest, BatchSetDigest: shard.BatchSetDigest, UnitSetDigest: shard.UnitSetDigest, State: PostgreSQLReceiptEventStateCompleted}
	}
	return postgreSQLReceiptEventNodeSetDigest(projection)
}

func validatePostgreSQLTerminalShardBindings(shards []PostgreSQLTerminalShard) error {
	if shards == nil {
		return ErrPostgreSQLReceiptStore
	}
	for index := range shards {
		if err := validatePostgreSQLTerminalShardBinding(index, shards); err != nil {
			return err
		}
	}
	return nil
}

func validatePostgreSQLTerminalShardBinding(index int, shards []PostgreSQLTerminalShard) error {
	if index > 0 && shards[index-1].NodeID >= shards[index].NodeID {
		return ErrPostgreSQLReceiptStore
	}
	if err := validatePostgreSQLTerminalShardIdentity(shards[index]); err != nil {
		return err
	}
	return validatePostgreSQLTerminalShardDigests(shards[index])
}

func postgreSQLReceiptEventNodeSetDigest(shards []PostgreSQLReceiptEventShard) (string, error) {
	if shards == nil {
		return "", ErrPostgreSQLReceiptStore
	}
	type binding struct {
		NodeID         string `json:"node_id"`
		NodeIDDigest   string `json:"node_id_digest"`
		ExecutorKind   string `json:"executor_kind"`
		ShardDigest    string `json:"shard_digest"`
		BatchSetDigest string `json:"batch_set_digest"`
		UnitSetDigest  string `json:"unit_set_digest"`
	}
	bindings := make([]binding, len(shards))
	for index, shard := range shards {
		if err := validatePostgreSQLReceiptEventShard(index, shards); err != nil {
			return "", err
		}
		bindings[index] = binding{shard.NodeID, shard.NodeIDDigest, shard.ExecutorKind, shard.ShardDigest, shard.BatchSetDigest, shard.UnitSetDigest}
	}
	encoded, err := json.Marshal(bindings)
	if err != nil {
		return "", ErrPostgreSQLReceiptStore
	}
	return digestPostgreSQLReceiptBytes(append([]byte("remote-ci-node-set/v1\x00"), encoded...)), nil
}

func validatePostgreSQLReceiptEventShard(index int, shards []PostgreSQLReceiptEventShard) error {
	shard := shards[index]
	if index > 0 && shards[index-1].NodeID >= shard.NodeID {
		return ErrPostgreSQLReceiptStore
	}
	if err := validatePostgreSQLReceiptEventShardIdentity(shard); err != nil {
		return err
	}
	return validatePostgreSQLReceiptEventShardDigests(shard)
}

func validatePostgreSQLReceiptEventShardIdentity(shard PostgreSQLReceiptEventShard) error {
	if !validTerminalIdentity(shard.NodeID) || !validTerminalIdentity(shard.ExecutorKind) || !validPostgreSQLReceiptState(shard.State) || !validTerminalCode(shard.Code) {
		return ErrPostgreSQLReceiptStore
	}
	return nil
}

func validatePostgreSQLReceiptEventShardDigests(shard PostgreSQLReceiptEventShard) error {
	if shard.NodeIDDigest != digestPostgreSQLReceiptBytes([]byte(shard.NodeID)) || !validTerminalDigest(shard.ShardDigest) || !validTerminalDigest(shard.BatchSetDigest) || !validTerminalDigest(shard.UnitSetDigest) {
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
