// Package legacywakeupmigration contains an opt-in, one-shot planner for
// untagged task_dag_wakeups payloads. It deliberately has no orchestration,
// provider, Fx, RecoveryCustody, or PostgreSQL dependency.
package legacywakeupmigration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

const (
	SchemaVersion            = int64(1)
	KindLegacyLaunch         = "legacy_launch"
	KindDAGDownstream        = "dag_downstream"
	WakeupKindStart          = "start"
	WakeupKindNodeStart      = "node_start"
	WakeupKindManualDispatch = "manual_dispatch"
	maxPayloadBytes          = 256 << 10
	maxAgentIDBytes          = 256
	maxPromptBytes           = 128 << 10
	maxPathBytes             = 4096
	maxUpstreamRefs          = 64
)

var ErrInvalidSource = errors.New("invalid legacy wakeup source")

// Row is the transport-neutral projection needed by the planner. UpdatedAt
// and the planner-computed raw payload digest form the optimistic CAS fence;
// PromptPayloadDigest is retained only as an untrusted source projection.
type Row struct {
	ID                  int64
	DagKey              string
	NodeKey             string
	RunID               *int64
	WakeupKind          string
	TargetAgentID       string
	PromptPayload       []byte
	PromptPayloadDigest string
	IdempotencyKey      string
	Status              string
	UpdatedAt           int64
	// SourcePayloadSchemaVersion is the persisted discriminator for the source
	// row: 0 means an unversioned legacy payload and 1 means a canonical
	// versioned payload. The planner never infers this value from JSON shape.
	SourcePayloadSchemaVersion int64
}

// Disposition 表示一次 legacy wakeup 规划动作的处理类别。
type Disposition string

const (
	DispositionMigrate    = Disposition("migrate")
	DispositionQuarantine = Disposition("quarantine")
	DispositionUnchanged  = Disposition("unchanged")
)

// QuarantineReason 表示无法安全迁移时写入审核记录的稳定原因。
type QuarantineReason string

const (
	ReasonMalformedJSON        = QuarantineReason("malformed_json")
	ReasonMissingDiscriminator = QuarantineReason("missing_discriminator")
	ReasonUnknownFields        = QuarantineReason("unknown_fields")
	ReasonMixedUnion           = QuarantineReason("mixed_union")
	ReasonUnknownWakeupKind    = QuarantineReason("unknown_wakeup_kind")
	ReasonMissingRequiredField = QuarantineReason("missing_required_field")
	ReasonTargetMismatch       = QuarantineReason("target_mismatch")
	ReasonDAGIdentityMismatch  = QuarantineReason("dag_identity_mismatch")
	ReasonUnsupportedVersion   = QuarantineReason("unsupported_version")
)

// Mutation is an in-place update candidate. An applier MUST execute the
// mutation and its idempotency record in one transaction and require both
// expected values in its WHERE clause; zero rows is a stale/CAS conflict.
type Mutation struct {
	RowID                   int64
	ExpectedUpdatedAt       int64
	ExpectedPayloadDigest   string
	IdempotencyKey          string
	NewPayload              []byte
	NewPayloadSchemaVersion int64
	NewKind                 string
}

// QuarantineRecord 保存待操作员审核的原始行和分类原因，不触发任何执行副作用。
type QuarantineRecord struct {
	RowID                      int64
	Reason                     QuarantineReason
	Detail                     string
	SourcePayloadSchemaVersion int64
	SourceDigest               string
	TargetAgentID              string
	Payload                    []byte
	IdempotencyKey             string
}

// Action 是规划器为一条 wakeup 行生成的不可变动作。
type Action struct {
	Disposition Disposition
	Mutation    *Mutation
	Quarantine  *QuarantineRecord
	RowID       int64
}

// Plan 是按稳定行顺序生成、可重复审核和应用的一次迁移计划。
type Plan struct {
	Actions   []Action
	RunDigest string
}

// Applier is transport-neutral. Implementations must bind CAS predicates,
// the mutation, and a unique idempotency record in one SQLite transaction.
// Quarantine recording must atomically mark the source failed/quarantined.
type Applier interface {
	ApplyMutation(context.Context, Mutation) error
	RecordQuarantine(context.Context, QuarantineRecord) error
}

// ApplySummary 汇总一次已审核计划的迁移、隔离和无变化行数。
type ApplySummary struct {
	Migrated    int
	Quarantined int
	Unchanged   int
}

// Apply 执行已由操作员审核的计划，首个应用错误即停止且不做兜底重试。
// Apply executes an operator-reviewed plan and stops on the first applier
// error. No retry or fallback is performed here.
func (p Plan) Apply(ctx context.Context, applier Applier) (ApplySummary, error) {
	if applier == nil {
		return ApplySummary{}, errors.New("legacy wakeup migration applier is required")
	}
	var summary ApplySummary
	for _, action := range p.Actions {
		delta, err := applyAction(ctx, applier, action)
		if err != nil {
			return summary, err
		}
		summary.Migrated += delta.Migrated
		summary.Quarantined += delta.Quarantined
		summary.Unchanged += delta.Unchanged
	}
	return summary, nil
}

func applyAction(ctx context.Context, applier Applier, action Action) (ApplySummary, error) {
	switch action.Disposition {
	case DispositionMigrate:
		return applyMigrationAction(ctx, applier, action)
	case DispositionQuarantine:
		return applyQuarantineAction(ctx, applier, action)
	case DispositionUnchanged:
		return ApplySummary{Unchanged: 1}, nil
	default:
		return ApplySummary{}, fmt.Errorf("row %d: unknown disposition %q", action.RowID, action.Disposition)
	}
}

func applyMigrationAction(ctx context.Context, applier Applier, action Action) (ApplySummary, error) {
	if action.Mutation == nil {
		return ApplySummary{}, fmt.Errorf("row %d: migrate action missing mutation", action.RowID)
	}
	if action.Mutation.NewPayloadSchemaVersion != SchemaVersion {
		return ApplySummary{}, fmt.Errorf("row %d: migration payload schema version = %d, want %d", action.RowID, action.Mutation.NewPayloadSchemaVersion, SchemaVersion)
	}
	if err := applier.ApplyMutation(ctx, *action.Mutation); err != nil {
		return ApplySummary{}, fmt.Errorf("row %d: apply migration mutation: %w", action.RowID, err)
	}
	return ApplySummary{Migrated: 1}, nil
}

func applyQuarantineAction(ctx context.Context, applier Applier, action Action) (ApplySummary, error) {
	if action.Quarantine == nil {
		return ApplySummary{}, fmt.Errorf("row %d: quarantine action missing record", action.RowID)
	}
	if err := validateQuarantineRecord(action.RowID, *action.Quarantine); err != nil {
		return ApplySummary{}, fmt.Errorf("row %d: invalid quarantine record: %w", action.RowID, err)
	}
	if err := applier.RecordQuarantine(ctx, *action.Quarantine); err != nil {
		return ApplySummary{}, fmt.Errorf("row %d: record quarantine: %w", action.RowID, err)
	}
	return ApplySummary{Quarantined: 1}, nil
}

// BuildPlan 按稳定行 ID 生成可重复计划，绝不从载荷形状猜测分支。
// BuildPlan deterministically classifies rows. It never infers a launch/DAG
// branch from payload shape: WakeupKind and row identity select the branch.
// Already-versioned valid rows are unchanged, making reruns safe.
func BuildPlan(rows []Row) Plan {
	ordered := append([]Row(nil), rows...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	actions := make([]Action, 0, len(ordered))
	h := sha256.New()
	for _, row := range ordered {
		action := planRow(row)
		actions = append(actions, action)
		entry := runDigestEntry(row, action)
		encoded, _ := json.Marshal(entry)
		_, _ = h.Write(encoded)
		_, _ = h.Write([]byte{'\n'})
	}
	return Plan{Actions: actions, RunDigest: hex.EncodeToString(h.Sum(nil))}
}

type digestRecord struct {
	RowID                      int64            `json:"row_id"`
	DagKey                     string           `json:"dag_key"`
	NodeKey                    string           `json:"node_key"`
	RunID                      *int64           `json:"run_id"`
	WakeupKind                 string           `json:"wakeup_kind"`
	TargetAgentID              string           `json:"target_agent_id"`
	Status                     string           `json:"status"`
	SourceIdempotencyKey       string           `json:"source_idempotency_key"`
	SourcePayloadSchemaVersion int64            `json:"source_payload_schema_version"`
	UpdatedAt                  int64            `json:"updated_at"`
	SourcePayloadDigest        string           `json:"source_payload_digest"`
	ExpectedUpdatedAt          int64            `json:"expected_updated_at"`
	ExpectedPayloadDigest      string           `json:"expected_payload_digest"`
	Disposition                Disposition      `json:"disposition"`
	QuarantineReason           QuarantineReason `json:"quarantine_reason,omitempty"`
	NewKind                    string           `json:"new_kind,omitempty"`
	NewPayloadDigest           string           `json:"new_payload_digest,omitempty"`
	NewPayloadSchemaVersion    int64            `json:"new_payload_schema_version"`
}

func runDigestEntry(row Row, action Action) digestRecord {
	digest := rowDigest(row)
	entry := digestRecord{
		RowID: row.ID, DagKey: row.DagKey, NodeKey: row.NodeKey, RunID: cloneRunID(row.RunID),
		WakeupKind: row.WakeupKind, TargetAgentID: row.TargetAgentID, Status: row.Status,
		SourceIdempotencyKey: row.IdempotencyKey, SourcePayloadSchemaVersion: row.SourcePayloadSchemaVersion,
		UpdatedAt: row.UpdatedAt, SourcePayloadDigest: digest,
		Disposition: action.Disposition,
	}
	entry.ExpectedUpdatedAt, entry.ExpectedPayloadDigest = row.UpdatedAt, digest
	if action.Mutation != nil {
		entry.ExpectedUpdatedAt = action.Mutation.ExpectedUpdatedAt
		entry.ExpectedPayloadDigest = action.Mutation.ExpectedPayloadDigest
		entry.NewKind = action.Mutation.NewKind
		entry.NewPayloadDigest = payloadDigest(action.Mutation.NewPayload)
		entry.NewPayloadSchemaVersion = action.Mutation.NewPayloadSchemaVersion
	}
	if action.Quarantine != nil {
		entry.QuarantineReason = action.Quarantine.Reason
	}
	if action.Disposition == DispositionUnchanged {
		entry.NewKind = taggedKind(row.PromptPayload)
		entry.NewPayloadDigest = digest
		entry.NewPayloadSchemaVersion = SchemaVersion
	}
	return entry
}

func cloneRunID(runID *int64) *int64 {
	if runID == nil {
		return nil
	}
	run := *runID
	return &run
}

func actionPayloadSchemaVersion(a Action) int64 {
	if a.Mutation == nil {
		return 0
	}
	return a.Mutation.NewPayloadSchemaVersion
}

func actionReason(a Action) string {
	if a.Quarantine == nil {
		return ""
	}
	return string(a.Quarantine.Reason)
}

// planRow 对单行执行 fail-fast 分类。
func planRow(row Row) Action {
	digest := rowDigest(row)
	if row.SourcePayloadSchemaVersion != 0 && row.SourcePayloadSchemaVersion != SchemaVersion {
		return quarantine(row, ReasonUnsupportedVersion, fmt.Sprintf("source payload schema version %d is not supported", row.SourcePayloadSchemaVersion), digest)
	}
	if action, stop := invalidBase(row, digest); stop {
		return action
	}
	if row.SourcePayloadSchemaVersion == SchemaVersion {
		if action, tagged := planTagged(row, digest); tagged {
			return action
		}
		return quarantine(row, ReasonMissingDiscriminator, "source schema version 1 requires a versioned payload", digest)
	}
	// A source-version-0 row is legacy by contract. A canonical envelope in
	// such a row is evidence of a mismatched source marker, never a reason to
	// silently treat it as legacy.
	if payloadHasTag(row.PromptPayload) {
		return quarantine(row, ReasonUnsupportedVersion, "versioned payload requires source schema version 1", digest)
	}
	wakeupKind := strings.TrimSpace(row.WakeupKind)
	if wakeupKind == WakeupKindStart {
		return planLegacy(row, digest)
	}
	if wakeupKind == WakeupKindNodeStart || wakeupKind == WakeupKindManualDispatch {
		return planDownstream(row, digest)
	}
	return quarantine(row, ReasonUnknownWakeupKind, fmt.Sprintf("wakeup_kind %q is not migratable", wakeupKind), digest)
}

func invalidBase(row Row, digest string) (Action, bool) {
	if row.ID <= 0 || strings.TrimSpace(row.IdempotencyKey) == "" {
		return quarantine(row, ReasonMissingRequiredField, "id and idempotency_key are required", digest), true
	}
	if !canonicalAgentID(row.TargetAgentID) {
		return quarantine(row, ReasonMissingRequiredField, "target_agent_id is required", digest), true
	}
	return Action{}, false
}

// planTagged 校验已版本化行并确认 wakeup_kind 与 DAG 身份一致。
func planTagged(row Row, digest string) (Action, bool) {
	tagged, valid, reason, agentID := inspectTagged(row.PromptPayload)
	if !tagged {
		return Action{}, false
	}
	if !valid {
		return quarantine(row, reason, "versioned payload is not valid", digest), true
	}
	if !canonicalAgentID(agentID) || agentID != row.TargetAgentID {
		return quarantine(row, ReasonTargetMismatch, "versioned payload agent_id differs from target_agent_id", digest), true
	}
	kind := taggedKind(row.PromptPayload)
	if !taggedWakeupKindMatches(row, kind) {
		return quarantine(row, ReasonMixedUnion, "payload kind disagrees with wakeup_kind", digest), true
	}
	if !taggedIdentityMatches(row, kind) {
		return quarantine(row, ReasonDAGIdentityMismatch, "versioned payload identity disagrees with row", digest), true
	}
	return Action{Disposition: DispositionUnchanged, RowID: row.ID}, true
}

// taggedWakeupKindMatches 确认版本分支没有被错误 wakeup_kind 复用。
func taggedWakeupKindMatches(row Row, kind string) bool {
	if kind == KindLegacyLaunch {
		return strings.TrimSpace(row.WakeupKind) == WakeupKindStart
	}
	return strings.TrimSpace(row.WakeupKind) == WakeupKindNodeStart || strings.TrimSpace(row.WakeupKind) == WakeupKindManualDispatch
}

// taggedIdentityMatches 确认载荷分支与行上的 DAG 身份互斥/完整。
func taggedIdentityMatches(row Row, kind string) bool {
	if kind == KindLegacyLaunch {
		return strings.TrimSpace(row.DagKey) == "" && strings.TrimSpace(row.NodeKey) == "" && row.RunID == nil
	}
	return strings.TrimSpace(row.DagKey) != "" && strings.TrimSpace(row.NodeKey) != "" && row.RunID != nil && *row.RunID > 0
}

// planLegacy 把明确 start 行转换成 versioned legacy_launch。
func planLegacy(row Row, digest string) Action {
	if strings.TrimSpace(row.DagKey) != "" || strings.TrimSpace(row.NodeKey) != "" || row.RunID != nil {
		return quarantine(row, ReasonMixedUnion, "start wakeup cannot carry DAG identity", digest)
	}
	legacy, reason, detail := decodeLegacy(row.PromptPayload)
	if reason != "" {
		return quarantine(row, reason, detail, digest)
	}
	if !canonicalAgentID(legacy.AgentID) || legacy.AgentID != row.TargetAgentID {
		return quarantine(row, ReasonTargetMismatch, "payload agent_id differs from target_agent_id", digest)
	}
	payload, err := marshalLegacy(legacy)
	if err != nil {
		return quarantine(row, ReasonMalformedJSON, err.Error(), digest)
	}
	return migrateAction(row, payload, KindLegacyLaunch, digest)
}

// planDownstream 把明确 DAG 行转换成 versioned dag_downstream。
func planDownstream(row Row, digest string) Action {
	if strings.TrimSpace(row.DagKey) == "" || strings.TrimSpace(row.NodeKey) == "" || row.RunID == nil || *row.RunID <= 0 {
		return quarantine(row, ReasonDAGIdentityMismatch, "DAG wakeup requires dag_key, node_key and positive run_id", digest)
	}
	downstream, reason, detail := decodeDownstream(row.PromptPayload)
	if reason != "" {
		return quarantine(row, reason, detail, digest)
	}
	if !canonicalAgentID(downstream.AgentID) || downstream.AgentID != row.TargetAgentID {
		return quarantine(row, ReasonTargetMismatch, "payload agent_id differs from target_agent_id", digest)
	}
	payload, err := marshalDownstream(downstream)
	if err != nil {
		return quarantine(row, ReasonMalformedJSON, err.Error(), digest)
	}
	return migrateAction(row, payload, KindDAGDownstream, digest)
}

func taggedKind(raw []byte) string {
	var fields struct {
		Kind string `json:"wakeup_payload_kind"`
	}
	_ = json.Unmarshal(raw, &fields)
	return fields.Kind
}

func payloadHasTag(raw []byte) bool {
	fields, reason, ok := readTaggedFields(raw)
	return ok && reason == "" && hasTag(fields)
}

func migrateAction(row Row, payload []byte, kind, digest string) Action {
	return Action{Disposition: DispositionMigrate, RowID: row.ID, Mutation: &Mutation{
		RowID: row.ID, ExpectedUpdatedAt: row.UpdatedAt, ExpectedPayloadDigest: digest,
		IdempotencyKey: migrationKey(row.ID, digest),
		NewPayload:     payload, NewPayloadSchemaVersion: SchemaVersion, NewKind: kind,
	}}
}

func quarantine(row Row, reason QuarantineReason, detail, digest string) Action {
	return Action{Disposition: DispositionQuarantine, RowID: row.ID, Quarantine: &QuarantineRecord{
		RowID: row.ID, Reason: reason, Detail: detail, SourcePayloadSchemaVersion: row.SourcePayloadSchemaVersion,
		SourceDigest: digest, TargetAgentID: row.TargetAgentID,
		Payload: append([]byte(nil), row.PromptPayload...), IdempotencyKey: migrationKey(row.ID, digest),
	}}
}

func migrationKey(id int64, digest string) string {
	return fmt.Sprintf("legacy-wakeup-migration:v1:%d:%s", id, digest)
}

func rowDigest(row Row) string {
	return payloadDigest(row.PromptPayload)
}

func payloadDigest(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func validateQuarantineRecord(rowID int64, record QuarantineRecord) error {
	if record.RowID != rowID {
		return fmt.Errorf("row id %d does not match action row id %d", record.RowID, rowID)
	}
	// Migration 134's quarantine table is intentionally legacy-only
	// (source_schema_version = 0). Versioned/unknown rows remain reviewable in
	// the plan but must not be sent to an incompatible applier.
	if record.SourcePayloadSchemaVersion != 0 {
		return fmt.Errorf("source payload schema version %d cannot be quarantined by the legacy-only applier", record.SourcePayloadSchemaVersion)
	}
	digest := payloadDigest(record.Payload)
	if record.SourceDigest != digest {
		return fmt.Errorf("source digest does not match raw payload")
	}
	if record.IdempotencyKey != migrationKey(record.RowID, record.SourceDigest) {
		return fmt.Errorf("idempotency key is not bound to row and source digest")
	}
	return nil
}

// inspectTagged 严格识别版本载荷并提取 agent_id。
func inspectTagged(raw []byte) (tagged, valid bool, reason QuarantineReason, agentID string) {
	fields, parseReason, ok := readTaggedFields(raw)
	if !ok {
		return true, false, parseReason, ""
	}
	if !hasTag(fields) {
		return false, false, "", ""
	}
	kind, headerReason, ok := readTaggedHeader(fields)
	if !ok {
		return true, false, headerReason, ""
	}
	if !strictCanonical(raw, kind) {
		return true, false, ReasonUnknownFields, ""
	}
	var envelope struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return true, false, ReasonMalformedJSON, ""
	}
	return true, true, "", envelope.AgentID
}

func readTaggedFields(raw []byte) (map[string]json.RawMessage, QuarantineReason, bool) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, ReasonMalformedJSON, false
	}
	return fields, "", true
}

func hasTag(fields map[string]json.RawMessage) bool {
	_, kind := fields["wakeup_payload_kind"]
	_, version := fields["schema_version"]
	return kind || version
}

// readTaggedHeader 严格读取版本和分支字段。
func readTaggedHeader(fields map[string]json.RawMessage) (string, QuarantineReason, bool) {
	if fields["wakeup_payload_kind"] == nil {
		return "", ReasonMissingDiscriminator, false
	}
	var kind string
	if err := json.Unmarshal(fields["wakeup_payload_kind"], &kind); err != nil || strings.TrimSpace(kind) == "" {
		return "", ReasonMissingDiscriminator, false
	}
	var version int64
	if err := json.Unmarshal(fields["schema_version"], &version); err != nil || version != SchemaVersion {
		return "", ReasonUnsupportedVersion, false
	}
	if kind != KindLegacyLaunch && kind != KindDAGDownstream {
		return "", ReasonUnknownFields, false
	}
	return kind, "", true
}

// strictCanonical 校验版本载荷字段、重复键、null 和 JSON 尾随值。
func strictCanonical(raw []byte, kind string) bool {
	if len(raw) > maxPayloadBytes || rejectDuplicateKeys(raw) != nil {
		return false
	}
	if canonicalNullField(raw, kind) {
		return false
	}
	if kind == KindLegacyLaunch {
		var target canonicalLegacy
		return decodeCanonical(raw, &target) && legacyBounds(target)
	}
	var target canonicalDownstream
	if !decodeCanonical(raw, &target) {
		return false
	}
	reason, _ := downstreamBounds(target)
	return canonicalAgentID(target.AgentID) && reason == ""
}

// decodeCanonical 严格解码单个版本化对象并拒绝尾随 JSON 值。
func decodeCanonical(raw []byte, target any) bool {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return false
	}
	var extra any
	return dec.Decode(&extra) == io.EOF
}

// legacyBounds 校验已版本化 launch 分支的必填字段和正文上限。
func legacyBounds(value canonicalLegacy) bool {
	return canonicalAgentID(value.AgentID) && len(value.AgentID) <= maxAgentIDBytes && len(value.Prompt) <= maxPromptBytes
}

func canonicalAgentID(value string) bool {
	return value != "" && strings.TrimSpace(value) == value
}

// canonicalNullField 拒绝生产 strict decoder 不接受的 null 判别字段。
func canonicalNullField(raw []byte, kind string) bool {
	_ = kind // all null values are invalid in a canonical payload, including optional fields.
	return containsNullValue(raw)
}

type canonicalLegacy struct {
	WakeupPayloadKind string   `json:"wakeup_payload_kind"`
	SchemaVersion     int64    `json:"schema_version"`
	AgentID           string   `json:"agent_id"`
	Name              string   `json:"name,omitempty"`
	Prompt            string   `json:"prompt,omitempty"`
	Instructions      string   `json:"instructions,omitempty"`
	ParentID          string   `json:"parent_id,omitempty"`
	ParentThreadID    string   `json:"parent_thread_id,omitempty"`
	ContextMode       string   `json:"context_mode,omitempty"`
	AgentType         string   `json:"agent_type,omitempty"`
	AgentKey          string   `json:"agent_key,omitempty"`
	PromptKey         string   `json:"prompt_key,omitempty"`
	MemoryScope       string   `json:"memory_scope,omitempty"`
	Cwd               string   `json:"cwd,omitempty"`
	Language          string   `json:"language,omitempty"`
	Command           []string `json:"command,omitempty"`
	Env               []string `json:"env,omitempty"`
}
type canonicalDownstream struct {
	WakeupPayloadKind string      `json:"wakeup_payload_kind"`
	SchemaVersion     int64       `json:"schema_version"`
	AgentID           string      `json:"agent_id"`
	UpstreamOutputs   []legacyRef `json:"upstream_outputs,omitempty"`
}
type legacyRef struct {
	NodeKey string `json:"node_key"`
	Path    string `json:"path"`
}

func decodeLegacy(raw []byte) (canonicalLegacy, QuarantineReason, string) {
	var v canonicalLegacy
	return decodeLegacyObject(raw, &v)
}
func decodeDownstream(raw []byte) (canonicalDownstream, QuarantineReason, string) {
	var v canonicalDownstream
	return decodeDownstreamObject(raw, &v)
}

// decodeLegacyObject 解析历史 launch JSON 并执行字段边界检查。
func decodeLegacyObject(raw []byte, out *canonicalLegacy) (canonicalLegacy, QuarantineReason, string) {
	if len(raw) > maxPayloadBytes {
		return *out, ReasonMalformedJSON, "payload exceeds size limit"
	}
	if json.Valid(raw) && containsNullValue(raw) {
		return *out, ReasonMissingRequiredField, "legacy fields must not be null"
	}
	if err := decodeAliases(raw, out, false); err != nil {
		return *out, classify(err), err.Error()
	}
	if !canonicalAgentID(out.AgentID) {
		return *out, ReasonMissingRequiredField, "agent_id is required"
	}
	if len(out.AgentID) > maxAgentIDBytes || len(out.Prompt) > maxPromptBytes {
		return *out, ReasonMissingRequiredField, "legacy field exceeds size limit"
	}
	return *out, "", ""
}

// decodeDownstreamObject 解析历史 DAG JSON 并执行引用边界检查。
func decodeDownstreamObject(raw []byte, out *canonicalDownstream) (canonicalDownstream, QuarantineReason, string) {
	if len(raw) > maxPayloadBytes {
		return *out, ReasonMalformedJSON, "payload exceeds size limit"
	}
	if json.Valid(raw) && containsNullValue(raw) {
		return *out, ReasonMissingRequiredField, "DAG fields must not be null"
	}
	if err := decodeAliases(raw, out, true); err != nil {
		return *out, classify(err), err.Error()
	}
	if !canonicalAgentID(out.AgentID) {
		return *out, ReasonMissingRequiredField, "agent_id is required"
	}
	if reason, detail := downstreamBounds(*out); reason != "" {
		return *out, reason, detail
	}
	return *out, "", ""
}

func containsNullValue(raw []byte) bool {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return true
	}
	return containsNull(value)
}

// containsNull 递归检查解码后的 JSON 值，拒绝任意层级的 null。
func containsNull(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case []any:
		for _, item := range typed {
			if containsNull(item) {
				return true
			}
		}
	case map[string]any:
		for _, item := range typed {
			if containsNull(item) {
				return true
			}
		}
	}
	return false
}

// downstreamBounds 校验 DAG agent、引用数量及路径长度边界。
func downstreamBounds(v canonicalDownstream) (QuarantineReason, string) {
	if len(v.AgentID) > maxAgentIDBytes || len(v.UpstreamOutputs) > maxUpstreamRefs {
		return ReasonMissingRequiredField, "downstream field exceeds size limit"
	}
	for _, ref := range v.UpstreamOutputs {
		if strings.TrimSpace(ref.NodeKey) == "" || strings.TrimSpace(ref.Path) == "" {
			return ReasonMissingRequiredField, "upstream_outputs entries require node_key and path"
		}
		if len(ref.NodeKey) > maxAgentIDBytes || len(ref.Path) > maxPathBytes {
			return ReasonMissingRequiredField, "upstream_outputs entry exceeds size limit"
		}
	}
	return "", ""
}

func classify(err error) QuarantineReason {
	if errors.Is(err, errMixed) {
		return ReasonMixedUnion
	}
	if errors.Is(err, errUnknown) {
		return ReasonUnknownFields
	}
	return ReasonMalformedJSON
}

var errMixed = errors.New("mixed union")
var errUnknown = errors.New("unknown field")

// decodeAliases accepts the three historical Go JSON spellings while rejecting
// aliases duplicated in one object. This is explicit compatibility, not shape guessing.
// decodeAliases 规范化历史别名后使用严格 decoder 解析对象。
func decodeAliases(raw []byte, out any, downstream bool) error {
	fields, err := parseAliasFields(raw)
	if err != nil {
		return err
	}
	canonical, err := canonicalizeAliases(fields, downstream)
	if err != nil {
		return err
	}
	return decodeCanonicalObject(canonical, out)
}

func parseAliasFields(raw []byte) (map[string]json.RawMessage, error) {
	if err := rejectDuplicateKeys(raw); err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	return fields, nil
}

// canonicalizeAliases 将已知历史字段别名转换为规范键并规范化嵌套引用。
func canonicalizeAliases(fields map[string]json.RawMessage, downstream bool) (map[string]json.RawMessage, error) {
	canonical := make(map[string]json.RawMessage, len(fields))
	for key := range fields {
		if err := validateAliasKey(key, downstream); err != nil {
			return nil, err
		}
	}
	for key, value := range fields {
		name := aliasName(key)
		if _, ok := canonical[name]; ok {
			return nil, fmt.Errorf("%w: duplicate alias %s", errMixed, name)
		}
		if downstream && name == "upstream_outputs" {
			var err error
			value, err = normalizeRefs(value)
			if err != nil {
				return nil, err
			}
		}
		canonical[name] = value
	}
	return canonical, nil
}

// validateAliasKey 阻止跨分支字段和未知字段进入严格 decoder。
func validateAliasKey(key string, downstream bool) error {
	if key == "wakeup_payload_kind" || key == "schema_version" {
		return errMixed
	}
	n := aliasName(key)
	if !downstream && (n == "upstream_outputs" || n == "node_key" || n == "path") {
		return fmt.Errorf("%w: DAG field %s in launch payload", errMixed, key)
	}
	if downstream && isLaunchOnlyField(n) {
		return fmt.Errorf("%w: launch field %s in DAG payload", errMixed, key)
	}
	if !knownAlias(key, downstream) {
		return fmt.Errorf("%w: %s", errUnknown, key)
	}
	return nil
}

func isLaunchOnlyField(name string) bool {
	return name != "agent_id" && isLaunchPayloadField(name)
}

func isLaunchPayloadField(name string) bool {
	switch name {
	case "agent_id", "name", "prompt", "instructions", "parent_id", "parent_thread_id",
		"context_mode", "agent_type", "agent_key", "prompt_key", "memory_scope", "cwd",
		"language", "command", "env":
		return true
	default:
		return false
	}
}

func normalizeRefs(value []byte) ([]byte, error) {
	var refs []map[string]json.RawMessage
	if err := json.Unmarshal(value, &refs); err != nil {
		return nil, err
	}
	for i, ref := range refs {
		normalized, err := normalizeRef(i, ref)
		if err != nil {
			return nil, err
		}
		refs[i] = normalized
	}
	return json.Marshal(refs)
}

func normalizeRef(i int, ref map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	normalized := make(map[string]json.RawMessage, len(ref))
	for k, v := range ref {
		n := aliasName(k)
		if n != "node_key" && n != "path" {
			return nil, fmt.Errorf("%w: upstream_outputs[%d].%s", errUnknown, i, k)
		}
		if _, exists := normalized[n]; exists {
			return nil, fmt.Errorf("%w: duplicate upstream_outputs[%d].%s", errMixed, i, n)
		}
		normalized[n] = v
	}
	return normalized, nil
}

func decodeCanonicalObject(canonical map[string]json.RawMessage, out any) error {
	b, _ := json.Marshal(canonical)
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	return dec.Decode(out)
}

func rejectDuplicateKeys(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := scanJSON(dec); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

// scanJSON 递归扫描 JSON 值以阻止重复键覆盖。
func scanJSON(dec *json.Decoder) error {
	token, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := token.(json.Delim); ok {
		switch d {
		case '{':
			return scanJSONObject(dec)
		case '[':
			for dec.More() {
				if err := scanJSON(dec); err != nil {
					return err
				}
			}
			_, err = dec.Token()
			return err
		}
	}
	return nil
}

// scanJSONObject 扫描对象并拒绝重复字段。
func scanJSONObject(dec *json.Decoder) error {
	seen := map[string]struct{}{}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := t.(string)
		if !ok {
			return errors.New("object key must be string")
		}
		if _, ok := seen[key]; ok {
			return fmt.Errorf("%w: duplicate JSON field %q", errMixed, key)
		}
		seen[key] = struct{}{}
		if err := scanJSON(dec); err != nil {
			return err
		}
	}
	_, err := dec.Token()
	return err
}

// aliasName 将历史 camelCase/Go 字段映射到规范 snake_case 名称。
func aliasName(key string) string {
	normalized := strings.ToLower(strings.ReplaceAll(key, "_", ""))
	aliases := map[string]string{
		"agentid": "agent_id", "upstreamoutputs": "upstream_outputs", "nodekey": "node_key", "path": "path",
		"parentid": "parent_id", "parentthreadid": "parent_thread_id", "contextmode": "context_mode",
		"agenttype": "agent_type", "agentkey": "agent_key", "promptkey": "prompt_key", "memoryscope": "memory_scope",
		"cwd": "cwd", "language": "language", "command": "command", "env": "env", "name": "name", "prompt": "prompt", "instructions": "instructions",
	}
	if name, ok := aliases[normalized]; ok {
		return name
	}
	return key
}

// knownAlias 判断别名是否属于当前 launch 或 DAG 分支。
func knownAlias(key string, downstream bool) bool {
	n := aliasName(key)
	if downstream {
		return n == "agent_id" || n == "upstream_outputs" || n == "node_key" || n == "path"
	}
	return isLaunchPayloadField(n)
}
func marshalLegacy(v canonicalLegacy) ([]byte, error) {
	v.WakeupPayloadKind, v.SchemaVersion = KindLegacyLaunch, SchemaVersion
	return json.Marshal(v)
}
func marshalDownstream(v canonicalDownstream) ([]byte, error) {
	v.WakeupPayloadKind, v.SchemaVersion = KindDAGDownstream, SchemaVersion
	return json.Marshal(v)
}
