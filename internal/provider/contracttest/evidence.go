package contracttest

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

type promptEvidenceOrigin string

const (
	promptOriginProviderCarrier  promptEvidenceOrigin = "provider_carrier"
	promptOriginExpectedSnapshot promptEvidenceOrigin = "expected_snapshot"
)

// PromptParityFields 是 provider prompt parity 必须证明的字段集合。
type PromptParityFields struct {
	BaseInstructions      string `json:"baseInstructions"`
	DeveloperInstructions string `json:"developerInstructions"`
	PrefixHash            string `json:"prefixHash"`
	Boundary              string `json:"boundary"`
	SectionSnapshot       string `json:"sectionSnapshot"`
}

// PromptParityEvidence 保存 prompt parity 证据及不可伪造的来源元数据。
type PromptParityEvidence struct {
	origin             promptEvidenceOrigin
	evidenceID         string
	fields             PromptParityFields
	loadedFromSnapshot bool
}

type eventEvidenceOrigin string

const (
	eventOriginProviderTranslator eventEvidenceOrigin = "provider_translator"
	eventOriginExpectedSnapshot   eventEvidenceOrigin = "expected_event_snapshot"
)

// EventTranslationEvidence 保存 event translation 证据及不可伪造的来源元数据。
type EventTranslationEvidence struct {
	origin             eventEvidenceOrigin
	evidenceID         string
	canonicalJSON      []byte
	loadedFromSnapshot bool
	captured           bool
}

// EventMatrixEvidence 记录关键 provider event 类别的 snapshot 或 typed unsupported 证据。
type EventMatrixEvidence struct {
	Provider   string
	Categories []EventMatrixCategoryEvidence
}

// EventMatrixCategoryEvidence 是 event matrix 中一个类别的覆盖证明。
type EventMatrixCategoryEvidence struct {
	Category     string
	SnapshotIDs  []string
	Unsupported  *UnsupportedOutcomeEvidence
	Reason       string
	TranslatorID string
}

// ExpectedPromptSnapshot 是 LoadExpectedPromptSnapshot 读取出的独立 prompt golden。
type ExpectedPromptSnapshot struct {
	snapshotID         string
	fields             PromptParityFields
	loadedFromSnapshot bool
}

// ExpectedEventSnapshot 是 LoadExpectedEventSnapshot 读取出的独立 event golden。
type ExpectedEventSnapshot struct {
	snapshotID         string
	canonicalJSON      []byte
	loadedFromSnapshot bool
}

// ResumeIdentityEvidence 证明 resume 使用 provider thread id。
type ResumeIdentityEvidence struct {
	PublicThreadID   string
	ProviderThreadID string
	ResumedThreadID  string
}

// RuntimeReportEvidence 证明 runtime report 至少包含可诊断 carrier。
type RuntimeReportEvidence struct {
	AgentID        string
	Provider       string
	SessionURLPort string
	StdioMode      string
	DeferredReason string
}

// DynamicToolResponderEvidence 证明 provider 已处理动态工具调用，或以 typed unsupported 阻断。
type DynamicToolResponderEvidence struct {
	ToolName               string
	CallID                 string
	ResponseID             string
	ResponsePayload        string
	ExpectedDependencyName string
	Unsupported            *UnsupportedOutcomeEvidence
}

// CaseEvidence 记录单个 contract case 的强类型证据。
type CaseEvidence struct {
	assertions map[EvidenceKey]string
	invalid    []string
}

// NewProviderPromptEvidence 创建 provider runtime carrier 来源的 prompt parity 证据。
func NewProviderPromptEvidence(captureID string, fields PromptParityFields) PromptParityEvidence {
	return PromptParityEvidence{origin: promptOriginProviderCarrier, evidenceID: strings.TrimSpace(captureID), fields: fields}
}

// NewExpectedPromptEvidence 创建 expected snapshot 来源的 prompt parity 证据。
func NewExpectedPromptEvidence(snapshot ExpectedPromptSnapshot) PromptParityEvidence {
	return PromptParityEvidence{
		origin:             promptOriginExpectedSnapshot,
		evidenceID:         strings.TrimSpace(snapshot.snapshotID),
		fields:             snapshot.fields,
		loadedFromSnapshot: snapshot.loadedFromSnapshot,
	}
}

// CaptureProviderEventTranslation 通过真实 translator callback 捕获 provider event 证据。
func CaptureProviderEventTranslation(
	t testing.TB,
	captureID string,
	raw dto.RawProviderEvent,
	translate func(dto.RawProviderEvent, func(any)),
) EventTranslationEvidence {
	t.Helper()
	if translate == nil {
		t.Fatal("event translator capture requires a translator function")
	}
	captured := captureTranslatedEvents(raw, translate)
	if len(captured) != 1 {
		t.Fatalf("event translator capture produced %d events, want exactly 1", len(captured))
	}
	return newProviderEventEvidence(t, captureID, captured[0])
}

// NewExpectedEventEvidence 创建 expected event snapshot 来源的证据。
func NewExpectedEventEvidence(snapshot ExpectedEventSnapshot) EventTranslationEvidence {
	return EventTranslationEvidence{
		origin:             eventOriginExpectedSnapshot,
		evidenceID:         strings.TrimSpace(snapshot.snapshotID),
		canonicalJSON:      snapshot.canonicalJSON,
		loadedFromSnapshot: snapshot.loadedFromSnapshot,
	}
}

// NewEvidence 创建空证据收集器。
func NewEvidence() *CaseEvidence {
	return &CaseEvidence{assertions: map[EvidenceKey]string{}}
}

// AssertEqual 记录 supplemental equality 证据，不能满足 reserved evidence key。
func (e *CaseEvidence) AssertEqual(t *testing.T, key EvidenceKey, got, want any) {
	t.Helper()
	if key == "" {
		t.Fatal("provider contract evidence key is required")
	}
	if reservedEvidenceKeys[key] {
		e.invalid = append(e.invalid, fmt.Sprintf("%s must be recorded through a typed evidence helper", key))
		return
	}
	if isTautologicalEvidence(got, want) {
		e.invalid = append(e.invalid, fmt.Sprintf("%s used tautological evidence", key))
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s got %#v, want %#v", key, got, want)
	}
	e.assertions[key] = fmt.Sprintf("%#v", got)
}

// AssertNoError 记录 supplemental no-error 证据，不能满足 reserved evidence key。
func (e *CaseEvidence) AssertNoError(t *testing.T, key EvidenceKey, err error) {
	t.Helper()
	if key == "" {
		t.Fatal("provider contract evidence key is required")
	}
	if reservedEvidenceKeys[key] {
		e.invalid = append(e.invalid, fmt.Sprintf("%s must be recorded through a typed evidence helper", key))
		return
	}
	if err != nil {
		t.Fatalf("%s error = %v", key, err)
	}
	e.assertions[key] = "ok"
}

// RecordEventTranslation 记录真实 translator 捕获值与独立 snapshot 的 event 对比证据。
func (e *CaseEvidence) RecordEventTranslation(t *testing.T, name string, got, want EventTranslationEvidence) {
	t.Helper()
	if strings.TrimSpace(name) == "" {
		t.Fatal("event translation evidence name is required")
	}
	if err := validateEventTranslationEvidence(got, want); err != nil {
		e.invalid = append(e.invalid, err.Error())
		return
	}
	if !bytes.Equal(got.canonicalJSON, want.canonicalJSON) {
		t.Fatalf("event translation %s got %s, want %s", name, got.canonicalJSON, want.canonicalJSON)
	}
	e.assertions[EvidenceEventTranslated] = fmt.Sprintf("%s/%s/%s", name, got.evidenceID, want.evidenceID)
}

// RecordEventMatrix 记录关键 event 类别的 snapshot manifest；无法覆盖的类别必须给 typed unsupported。
func (e *CaseEvidence) RecordEventMatrix(t *testing.T, matrix EventMatrixEvidence) {
	t.Helper()
	if err := validateEventMatrixEvidence(matrix); err != nil {
		e.invalid = append(e.invalid, err.Error())
		return
	}
	parts := make([]string, 0, len(matrix.Categories))
	for _, category := range matrix.Categories {
		if category.Unsupported != nil {
			parts = append(parts, strings.TrimSpace(category.Category)+":unsupported:"+category.Unsupported.dependencyName)
			continue
		}
		parts = append(parts, strings.TrimSpace(category.Category)+":"+strings.Join(trimmedNonEmptyStrings(category.SnapshotIDs...), "+"))
	}
	e.assertions[EvidenceEventMatrixManifest] = strings.TrimSpace(matrix.Provider) + "/" + strings.Join(parts, ";")
}

// RecordPromptParity 记录 provider carrier 与独立 snapshot 字段相等的 prompt parity 证据。
func (e *CaseEvidence) RecordPromptParity(t *testing.T, got, want PromptParityEvidence) {
	t.Helper()
	if err := validatePromptEvidenceOrigins(got, want); err != nil {
		e.invalid = append(e.invalid, err.Error())
		return
	}
	e.recordPromptField(t, EvidencePromptBaseInstructions, got.fields.BaseInstructions, want.fields.BaseInstructions)
	e.recordPromptField(t, EvidencePromptDeveloperInstructions, got.fields.DeveloperInstructions, want.fields.DeveloperInstructions)
	e.recordPromptField(t, EvidencePromptPrefixHash, got.fields.PrefixHash, want.fields.PrefixHash)
	e.recordPromptField(t, EvidencePromptBoundary, got.fields.Boundary, want.fields.Boundary)
	e.recordPromptField(t, EvidencePromptSectionSnapshot, got.fields.SectionSnapshot, want.fields.SectionSnapshot)
}

// RecordPromptMaterializedCarrier 只记录 provider RPC payload 中实际存在的 materialized prompt 字段。
func (e *CaseEvidence) RecordPromptMaterializedCarrier(t *testing.T, got, want PromptParityEvidence) {
	t.Helper()
	if err := validatePromptEvidenceOrigins(got, want); err != nil {
		e.invalid = append(e.invalid, err.Error())
		return
	}
	e.recordPromptField(t, EvidencePromptBaseInstructions, got.fields.BaseInstructions, want.fields.BaseInstructions)
	e.recordPromptField(t, EvidencePromptDeveloperInstructions, got.fields.DeveloperInstructions, want.fields.DeveloperInstructions)
	e.assertions[EvidencePromptMaterializedCarrier] = got.evidenceID + "/" + want.evidenceID
}

// RecordResumeIdentity 记录 resume identity 证据。
func (e *CaseEvidence) RecordResumeIdentity(t *testing.T, identity ResumeIdentityEvidence) {
	t.Helper()
	if strings.TrimSpace(identity.PublicThreadID) == "" ||
		strings.TrimSpace(identity.ProviderThreadID) == "" ||
		strings.TrimSpace(identity.ResumedThreadID) == "" {
		t.Fatal("resume identity evidence requires public, provider, and resumed thread ids")
	}
	if identity.ResumedThreadID != identity.ProviderThreadID {
		t.Fatalf("resume used thread id %q, want provider thread id %q", identity.ResumedThreadID, identity.ProviderThreadID)
	}
	if identity.ResumedThreadID == identity.PublicThreadID {
		t.Fatalf("resume reinvented public thread id %q instead of provider thread id", identity.PublicThreadID)
	}
	e.assertions[EvidenceResumeIdentity] = identity.PublicThreadID + "/" + identity.ProviderThreadID + "/" + identity.ResumedThreadID
}

// RecordRuntimeReport 记录 runtime report 证据。
func (e *CaseEvidence) RecordRuntimeReport(t *testing.T, report RuntimeReportEvidence) {
	t.Helper()
	if strings.TrimSpace(report.AgentID) == "" || strings.TrimSpace(report.Provider) == "" {
		t.Fatal("runtime report evidence requires agent id and provider")
	}
	if strings.TrimSpace(report.SessionURLPort) == "" &&
		strings.TrimSpace(report.StdioMode) == "" &&
		strings.TrimSpace(report.DeferredReason) == "" {
		t.Fatal("runtime report evidence requires session URL port, stdio mode, or deferred reason")
	}
	e.assertions[EvidenceRuntimeReportPayload] = strings.Join(
		[]string{report.AgentID, report.Provider, report.SessionURLPort, report.StdioMode, report.DeferredReason},
		"/",
	)
}

// RecordDynamicToolResponder 记录动态工具响应链路证据。
func (e *CaseEvidence) RecordDynamicToolResponder(t *testing.T, responder DynamicToolResponderEvidence) {
	t.Helper()
	if responder.Unsupported != nil {
		if err := validateUnsupportedOutcome(responder.Unsupported); err != nil {
			e.invalid = append(e.invalid, fmt.Sprintf("dynamic tool responder unsupported evidence: %v", err))
			return
		}
		if strings.TrimSpace(responder.ExpectedDependencyName) == "" {
			e.invalid = append(e.invalid, "dynamic tool responder unsupported evidence requires expected dependency name")
			return
		}
		if responder.ExpectedDependencyName != responder.Unsupported.dependencyName {
			e.invalid = append(e.invalid, fmt.Sprintf(
				"dynamic tool responder unsupported dependency = %s, want %s",
				responder.Unsupported.dependencyName,
				responder.ExpectedDependencyName,
			))
			return
		}
		e.assertions[EvidenceDynamicToolResponder] = responder.Unsupported.operationID + "/" + responder.Unsupported.dependencyName + "/" + string(responder.Unsupported.profile)
		return
	}
	if strings.TrimSpace(responder.ToolName) == "" ||
		strings.TrimSpace(responder.CallID) == "" ||
		strings.TrimSpace(responder.ResponseID) == "" ||
		strings.TrimSpace(responder.ResponsePayload) == "" {
		e.invalid = append(e.invalid, "dynamic tool responder evidence requires tool name, call id, response id, and response payload")
		return
	}
	e.assertions[EvidenceDynamicToolResponder] = strings.Join(
		[]string{responder.ToolName, responder.CallID, responder.ResponseID, responder.ResponsePayload},
		"/",
	)
}

// Validate 校验当前 case 是否记录了所有 required evidence。
func (e *CaseEvidence) Validate(caseName string, required []EvidenceKey) error {
	if e == nil {
		return fmt.Errorf("provider contract case %s returned no evidence", caseName)
	}
	if len(e.invalid) > 0 {
		return fmt.Errorf("provider contract case %s returned invalid evidence: %s", caseName, strings.Join(e.invalid, ", "))
	}
	if len(e.assertions) == 0 {
		return fmt.Errorf("provider contract case %s returned no evidence", caseName)
	}
	for _, key := range required {
		if _, ok := e.assertions[key]; !ok {
			return fmt.Errorf("provider contract case %s missing evidence key %s", caseName, key)
		}
	}
	return nil
}

func captureTranslatedEvents(raw dto.RawProviderEvent, translate func(dto.RawProviderEvent, func(any))) []any {
	var captured []any
	translate(raw, func(event any) {
		captured = append(captured, event)
	})
	return captured
}

func newProviderEventEvidence(t testing.TB, captureID string, event any) EventTranslationEvidence {
	t.Helper()
	canonical, err := canonicalEventJSON(event)
	if err != nil {
		t.Fatalf("canonicalize provider event evidence: %v", err)
	}
	return EventTranslationEvidence{
		origin:        eventOriginProviderTranslator,
		evidenceID:    strings.TrimSpace(captureID),
		canonicalJSON: canonical,
		captured:      true,
	}
}

// validateEventTranslationEvidence 校验 event evidence 的来源、snapshot 独立性和非空内容。
func validateEventTranslationEvidence(got, want EventTranslationEvidence) error {
	switch {
	case got.origin != eventOriginProviderTranslator || want.origin != eventOriginExpectedSnapshot:
		return fmt.Errorf("event translation evidence must compare provider_translator to expected_event_snapshot")
	case !got.captured:
		return fmt.Errorf("event translation observed evidence must come from translator capture API")
	case !want.loadedFromSnapshot:
		return fmt.Errorf("event translation expectation must be loaded from an independent checked-in snapshot")
	case got.evidenceID == "" || want.evidenceID == "" || got.evidenceID == want.evidenceID:
		return fmt.Errorf("event translation evidence must use distinct capture and expected snapshot ids")
	case len(got.canonicalJSON) == 0 || len(want.canonicalJSON) == 0:
		return fmt.Errorf("event translation evidence must include observed and expected events")
	default:
		return nil
	}
}

// validateEventMatrixEvidence 校验 matrix 覆盖了 contract 关心的关键 event 类别。
func validateEventMatrixEvidence(matrix EventMatrixEvidence) error {
	if strings.TrimSpace(matrix.Provider) == "" {
		return fmt.Errorf("event matrix evidence requires provider")
	}
	seen := map[string]bool{}
	for _, category := range matrix.Categories {
		name := strings.TrimSpace(category.Category)
		if name == "" {
			return fmt.Errorf("event matrix category is required")
		}
		if seen[name] {
			return fmt.Errorf("event matrix category %s is duplicated", name)
		}
		seen[name] = true
		if err := validateEventMatrixCategory(category); err != nil {
			return err
		}
	}
	for _, required := range []string{"interrupt", "tool_end", "failed_or_status", "approval_or_tool_diff"} {
		if !seen[required] {
			return fmt.Errorf("event matrix missing category %s", required)
		}
	}
	return nil
}

// validateEventMatrixCategory 校验单个 event 类别有 snapshot 或 typed unsupported 证明。
func validateEventMatrixCategory(category EventMatrixCategoryEvidence) error {
	if category.Unsupported != nil {
		if err := validateUnsupportedOutcome(category.Unsupported); err != nil {
			return fmt.Errorf("event matrix category %s unsupported evidence: %w", category.Category, err)
		}
		if strings.TrimSpace(category.Reason) == "" {
			return fmt.Errorf("event matrix category %s unsupported evidence requires reason", category.Category)
		}
		return nil
	}
	if strings.TrimSpace(category.TranslatorID) == "" {
		return fmt.Errorf("event matrix category %s requires translator id", category.Category)
	}
	if len(trimmedNonEmptyStrings(category.SnapshotIDs...)) == 0 {
		return fmt.Errorf("event matrix category %s requires snapshot id or typed unsupported", category.Category)
	}
	return nil
}

func trimmedNonEmptyStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// validatePromptEvidenceOrigins 校验 prompt evidence 的来源、独立 snapshot 和不同证据 ID。
func validatePromptEvidenceOrigins(got, want PromptParityEvidence) error {
	switch {
	case got == want:
		return fmt.Errorf("prompt parity evidence must compare captured provider_carrier to an independent expected_snapshot")
	case got.origin != promptOriginProviderCarrier || want.origin != promptOriginExpectedSnapshot:
		return fmt.Errorf("prompt parity evidence must compare provider_carrier to expected_snapshot")
	case !want.loadedFromSnapshot:
		return fmt.Errorf("prompt parity evidence must compare captured provider_carrier to an independent expected_snapshot")
	case got.evidenceID == "" || want.evidenceID == "" || got.evidenceID == want.evidenceID:
		return fmt.Errorf("prompt parity evidence must compare captured provider_carrier to an independent expected_snapshot")
	default:
		return nil
	}
}

func (e *CaseEvidence) recordPromptField(t *testing.T, key EvidenceKey, got, want string) {
	t.Helper()
	if strings.TrimSpace(got) == "" || strings.TrimSpace(want) == "" {
		e.invalid = append(e.invalid, fmt.Sprintf("%s prompt evidence is blank", key))
		return
	}
	if got != want {
		t.Fatalf("%s got %q, want %q", key, got, want)
	}
	e.assertions[key] = got
}
