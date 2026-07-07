// Package contracttest 提供 provider 共享契约测试入口和强类型证据校验。
package contracttest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

// Spec 描述一个 provider 必须满足的共享契约测试面。
type Spec struct {
	Name          string
	Start         func(context.Context, dto.StartSessionRequest) (contract.Session, error)
	Resume        func(context.Context, dto.ResumeSessionRequest) (contract.Session, error)
	EventCases    []Case
	RequiredCases map[CaseKey]Case
}

// CaseKey 标识必须覆盖的 provider 行为。
type CaseKey string

const (
	// CaseEventMatrix 校验 provider 关键 event 类别有 snapshot 或 typed unsupported 证据。
	CaseEventMatrix CaseKey = "event_matrix"
	// CasePromptParity 校验 provider prompt carrier 与独立 snapshot 一致。
	CasePromptParity CaseKey = "prompt_parity"
	// CasePromptMaterializedCarrier 校验只暴露 materialized prompt 字段的 provider RPC carrier。
	CasePromptMaterializedCarrier CaseKey = "prompt_materialized_carrier"
	// CaseApproval 校验 approval 行为证据。
	CaseApproval CaseKey = "approval"
	// CaseInterrupt 校验 interrupt 行为证据。
	CaseInterrupt CaseKey = "interrupt"
	// CaseForceComplete 校验 force-complete 行为证据。
	CaseForceComplete CaseKey = "force_complete"
	// CaseResume 校验 resume 使用 provider identity。
	CaseResume CaseKey = "resume"
	// CaseToolbridge 校验 toolbridge 行为或 typed unsupported 结果。
	CaseToolbridge CaseKey = "toolbridge"
	// CaseRuntimeReport 校验 runtime report 行为证据。
	CaseRuntimeReport CaseKey = "runtime_report"
)

// Case 是单个 provider 契约用例。
type Case struct {
	Name string
	Run  func(*testing.T, *CaseEvidence)
}

// EvidenceKey 标识证据记录键。
type EvidenceKey string

const (
	// EvidenceEventTranslated 由 RecordEventTranslation 写入。
	EvidenceEventTranslated EvidenceKey = "event.translated"
	// EvidenceEventMatrixManifest 由 RecordEventMatrix 写入。
	EvidenceEventMatrixManifest EvidenceKey = "event.matrix_manifest"
	// EvidencePromptBaseInstructions 由 RecordPromptParity 写入。
	EvidencePromptBaseInstructions EvidenceKey = "prompt.base_instructions"
	// EvidencePromptDeveloperInstructions 由 RecordPromptParity 写入。
	EvidencePromptDeveloperInstructions EvidenceKey = "prompt.developer_instructions"
	// EvidencePromptPrefixHash 由 RecordPromptParity 写入。
	EvidencePromptPrefixHash EvidenceKey = "prompt.prefix_hash"
	// EvidencePromptBoundary 由 RecordPromptParity 写入。
	EvidencePromptBoundary EvidenceKey = "prompt.boundary"
	// EvidencePromptSectionSnapshot 由 RecordPromptParity 写入。
	EvidencePromptSectionSnapshot EvidenceKey = "prompt.section_snapshot"
	// EvidencePromptMaterializedCarrier 由 RecordPromptMaterializedCarrier 写入。
	EvidencePromptMaterializedCarrier EvidenceKey = "prompt.materialized_carrier"
	// EvidenceApprovalOutcome 由 RecordOutcome 写入。
	EvidenceApprovalOutcome EvidenceKey = "approval.outcome"
	// EvidenceInterruptOutcome 由 RecordOutcome 写入。
	EvidenceInterruptOutcome EvidenceKey = "interrupt.outcome"
	// EvidenceForceCompleteOutcome 由 RecordOutcome 写入。
	EvidenceForceCompleteOutcome EvidenceKey = "force_complete.outcome"
	// EvidenceResumeIdentity 由 RecordResumeIdentity 写入。
	EvidenceResumeIdentity EvidenceKey = "resume.identity"
	// EvidenceToolbridgeDependency 由 RecordOutcome 写入。
	EvidenceToolbridgeDependency EvidenceKey = "toolbridge.dependency"
	// EvidenceRuntimeReportPayload 由 RecordRuntimeReport 写入。
	EvidenceRuntimeReportPayload EvidenceKey = "runtime_report.payload"
)

var requiredEvidenceByCase = map[CaseKey][]EvidenceKey{
	CaseEventMatrix: {EvidenceEventMatrixManifest},
	CasePromptParity: {
		EvidencePromptBaseInstructions,
		EvidencePromptDeveloperInstructions,
		EvidencePromptPrefixHash,
		EvidencePromptBoundary,
		EvidencePromptSectionSnapshot,
	},
	CasePromptMaterializedCarrier: {
		EvidencePromptBaseInstructions,
		EvidencePromptDeveloperInstructions,
		EvidencePromptMaterializedCarrier,
	},
	CaseApproval:      {EvidenceApprovalOutcome},
	CaseInterrupt:     {EvidenceInterruptOutcome},
	CaseForceComplete: {EvidenceForceCompleteOutcome},
	CaseResume:        {EvidenceResumeIdentity},
	CaseToolbridge:    {EvidenceToolbridgeDependency},
	CaseRuntimeReport: {EvidenceRuntimeReportPayload},
}

var requiredCaseOrder = []CaseKey{
	CaseEventMatrix,
	CaseApproval,
	CaseInterrupt,
	CaseForceComplete,
	CaseResume,
	CaseToolbridge,
	CaseRuntimeReport,
}

var promptCaseAlternatives = []CaseKey{
	CasePromptParity,
	CasePromptMaterializedCarrier,
}

var reservedEvidenceKeys = map[EvidenceKey]bool{
	EvidenceEventTranslated:             true,
	EvidenceEventMatrixManifest:         true,
	EvidencePromptBaseInstructions:      true,
	EvidencePromptDeveloperInstructions: true,
	EvidencePromptPrefixHash:            true,
	EvidencePromptBoundary:              true,
	EvidencePromptSectionSnapshot:       true,
	EvidencePromptMaterializedCarrier:   true,
	EvidenceApprovalOutcome:             true,
	EvidenceInterruptOutcome:            true,
	EvidenceForceCompleteOutcome:        true,
	EvidenceResumeIdentity:              true,
	EvidenceToolbridgeDependency:        true,
	EvidenceRuntimeReportPayload:        true,
}

// ValidateSpec 校验 provider 契约规格是否完整。
func ValidateSpec(spec Spec) error {
	if err := validateSpecEntrypoints(spec); err != nil {
		return err
	}
	if err := validateEventCases(spec.EventCases); err != nil {
		return err
	}
	if err := validatePromptCaseAlternative(spec.RequiredCases); err != nil {
		return err
	}
	return validateRequiredCaseSet(spec.RequiredCases)
}

// validateSpecEntrypoints 校验 provider contract suite 的入口函数和事件用例。
func validateSpecEntrypoints(spec Spec) error {
	if strings.TrimSpace(spec.Name) == "" {
		return errors.New("provider contract spec name is required")
	}
	if spec.Start == nil {
		return errors.New("provider contract Start function is required")
	}
	if spec.Resume == nil {
		return errors.New("provider contract Resume function is required")
	}
	if len(spec.EventCases) == 0 {
		return errors.New("provider contract event cases are required")
	}
	return nil
}

// validateRequiredCaseSet 校验除 prompt alternative 之外的必需契约用例。
func validateRequiredCaseSet(cases map[CaseKey]Case) error {
	for _, key := range requiredCaseOrder {
		c, ok := cases[key]
		if !ok || strings.TrimSpace(c.Name) == "" || c.Run == nil {
			return fmt.Errorf("provider contract case %s is required", key)
		}
	}
	return nil
}

// validatePromptCaseAlternative 校验 provider 至少声明一种 prompt carrier 证据。
func validatePromptCaseAlternative(cases map[CaseKey]Case) error {
	present := false
	for _, key := range promptCaseAlternatives {
		c, ok := cases[key]
		if !ok {
			continue
		}
		if strings.TrimSpace(c.Name) == "" || c.Run == nil {
			return fmt.Errorf("provider contract case %s is incomplete", key)
		}
		present = true
	}
	if !present {
		return fmt.Errorf("provider contract requires %s or %s", CasePromptParity, CasePromptMaterializedCarrier)
	}
	return nil
}

// Run 执行 provider 契约测试，发现违规时直接失败。
func Run(t *testing.T, spec Spec) {
	t.Helper()
	if err := RunSpecForTest(t, spec); err != nil {
		t.Fatal(err)
	}
}

// RunSpecForTest 执行 provider 契约测试并返回违规，供 harness 自测断言。
func RunSpecForTest(t *testing.T, spec Spec) error {
	t.Helper()
	if err := ValidateAcceptanceSpec(spec); err != nil {
		return err
	}
	if err := ValidateSpec(spec); err != nil {
		return err
	}
	if err := runStartResumeSmoke(t, spec); err != nil {
		return err
	}
	if err := runEventCases(t, spec.EventCases); err != nil {
		return err
	}
	return runRequiredCases(t, spec.RequiredCases)
}

func validateEventCases(cases []Case) error {
	for _, c := range cases {
		if strings.TrimSpace(c.Name) == "" || c.Run == nil {
			return errors.New("provider contract event case is incomplete")
		}
	}
	return nil
}

func runStartResumeSmoke(t *testing.T, spec Spec) error {
	t.Helper()
	request := contractStartRequest(spec.Name)
	session, err := spec.Start(context.Background(), request)
	if err != nil {
		return fmt.Errorf("Start() error = %w", err)
	}
	if err := assertSessionContract(t, session); err != nil {
		return err
	}
	return runResumeSmoke(t, spec, request.StartAssembly.Snapshot)
}

// contractStartRequest 构造包含 Boundary 和 SectionSnapshot 的最小 start 请求。
func contractStartRequest(provider string) dto.StartSessionRequest {
	boundary := &dto.PromptAssemblyBoundary{
		CachedPrefix: "base contract instructions",
		UncachedTail: "runtime context",
	}
	sections := map[string]string{
		"developer": "developer contract instructions",
		"system":    "base contract instructions",
	}
	return dto.StartSessionRequest{
		Provider: provider,
		AgentID:  "public-thread-contract",
		CWD:      os.TempDir(),
		StartAssembly: dto.StartAssembly{
			DisplayName:           "contract",
			BaseInstructions:      "base contract instructions",
			DeveloperInstructions: "developer contract instructions",
			Boundary:              boundary,
			PrefixShape:           dto.PrefixShape{Hash: "hash-contract"},
			Snapshot: dto.PromptAssemblySnapshot{
				DisplayName:           "contract",
				BaseInstructions:      "base contract instructions",
				DeveloperInstructions: "developer contract instructions",
				Boundary:              boundary,
				Provider:              provider,
				Version:               contract.PromptAssemblySnapshotVersion,
				Hash:                  "hash-contract",
				SectionSnapshot:       sections,
				Generation:            1,
			},
		},
	}
}

func runResumeSmoke(t *testing.T, spec Spec, snapshot dto.PromptAssemblySnapshot) error {
	t.Helper()
	resumed, err := spec.Resume(context.Background(), dto.ResumeSessionRequest{
		Provider:         spec.Name,
		AgentID:          "agent-contract",
		ThreadID:         "public-thread-contract",
		ProviderThreadID: "provider-thread-contract",
		CWD:              os.TempDir(),
		PromptSnapshot:   snapshot,
	})
	if err != nil {
		return fmt.Errorf("Resume() error = %w", err)
	}
	return assertSessionContract(t, resumed)
}

func runEventCases(t *testing.T, cases []Case) error {
	t.Helper()
	for _, c := range cases {
		if err := runCase(t, "event/"+c.Name, c, []EvidenceKey{EvidenceEventTranslated}); err != nil {
			return err
		}
	}
	return nil
}

// runRequiredCases 先运行 prompt alternative，再运行所有非 prompt 必需契约。
func runRequiredCases(t *testing.T, cases map[CaseKey]Case) error {
	t.Helper()
	for _, key := range promptCaseAlternatives {
		c, ok := cases[key]
		if !ok {
			continue
		}
		if err := runCase(t, string(key)+"/"+c.Name, c, requiredEvidenceByCase[key]); err != nil {
			return err
		}
	}
	for _, key := range requiredCaseOrder {
		c := cases[key]
		if err := runCase(t, string(key)+"/"+c.Name, c, requiredEvidenceByCase[key]); err != nil {
			return err
		}
	}
	return nil
}

func runCase(t *testing.T, name string, c Case, required []EvidenceKey) error {
	t.Helper()
	evidence := NewEvidence()
	passed := t.Run(name, func(t *testing.T) {
		t.Helper()
		c.Run(t, evidence)
	})
	if !passed {
		return fmt.Errorf("provider contract case %s failed", name)
	}
	return evidence.Validate(name, required)
}

func assertSessionContract(t *testing.T, session contract.Session) error {
	t.Helper()
	if err := assertSessionIdentity(session); err != nil {
		return err
	}
	if err := assertStartTurnContract(t, session); err != nil {
		return err
	}
	if err := assertSessionOperations(session); err != nil {
		return err
	}
	return assertSessionShutdown(session)
}

func assertSessionIdentity(session contract.Session) error {
	if session == nil {
		return errors.New("session is nil")
	}
	if strings.TrimSpace(session.ThreadID()) == "" {
		return errors.New("session ThreadID is empty")
	}
	if rolloutPath := strings.TrimSpace(session.RolloutPath()); rolloutPath != "" && strings.TrimSpace(session.ThreadID()) == "" {
		return errors.New("session RolloutPath is set while ThreadID is empty")
	}
	return nil
}

func assertStartTurnContract(t *testing.T, session contract.Session) error {
	t.Helper()
	handle, err := session.StartTurn(context.Background(), dto.TurnRequest{
		LocalID:  "turn-contract",
		ThreadID: session.ThreadID(),
		Inputs:   []dto.InputItem{{Type: "text", Content: "contract turn"}},
	})
	if err != nil {
		return fmt.Errorf("StartTurn() error = %w", err)
	}
	return assertTurnHandle(handle)
}

// assertTurnHandle 校验 turn handle 已携带本地和 provider id。
func assertTurnHandle(handle contract.TurnHandle) error {
	if handle == nil || strings.TrimSpace(handle.LocalID()) == "" || strings.TrimSpace(handle.ProviderID()) == "" {
		return fmt.Errorf("StartTurn() handle = %#v, want local and provider ids", handle)
	}
	return nil
}

// assertSessionOperations 校验 session 常用操作和 capability error 契约。
func assertSessionOperations(session contract.Session) error {
	caps := session.Capabilities()
	if err := assertThreadListContract(session, caps); err != nil {
		return err
	}
	if _, err := session.ReadHistory(context.Background(), session.ThreadID(), 10); err != nil {
		return fmt.Errorf("ReadHistory() error = %w", err)
	}
	if err := session.Configure(context.Background(), dto.ThreadConfigPatch{}); err != nil {
		return fmt.Errorf("Configure() error = %w", err)
	}
	if err := session.Interrupt(context.Background(), dto.InterruptRequest{ThreadID: session.ThreadID()}); err != nil {
		return fmt.Errorf("Interrupt() error = %w", err)
	}
	if err := session.ForceComplete(context.Background(), dto.ForceCompleteRequest{ThreadID: session.ThreadID()}); err != nil {
		return fmt.Errorf("ForceComplete() error = %w", err)
	}
	return assertThreadForkContract(session, caps)
}

func assertSessionShutdown(session contract.Session) error {
	if err := session.Close(context.Background()); err != nil {
		return fmt.Errorf("Close() error = %w", err)
	}
	if err := session.ForceStop(); err != nil {
		return fmt.Errorf("ForceStop() error = %w", err)
	}
	return nil
}

func assertThreadListContract(session contract.Session, caps dto.CapabilitySet) error {
	_, err := session.ListThreads(context.Background())
	if caps.Has(dto.CapThreadList) && err != nil {
		return fmt.Errorf("ListThreads() error = %w", err)
	}
	if !caps.Has(dto.CapThreadList) && !isCapabilityError(err, dto.CapThreadList) {
		return fmt.Errorf("ListThreads() error = %v, want thread list CapabilityError", err)
	}
	return nil
}

func assertThreadForkContract(session contract.Session, caps dto.CapabilitySet) error {
	_, err := session.ForkThread(context.Background(), dto.ForkRequest{ThreadID: session.ThreadID()})
	if caps.Has(dto.CapThreadFork) && err != nil {
		return fmt.Errorf("ForkThread() error = %w", err)
	}
	if !caps.Has(dto.CapThreadFork) && !isCapabilityError(err, dto.CapThreadFork) {
		return fmt.Errorf("ForkThread() error = %v, want thread fork CapabilityError", err)
	}
	return nil
}

func isCapabilityError(err error, capability string) bool {
	var capErr *contract.CapabilityError
	return errors.As(err, &capErr) && capErr.Capability == capability
}
