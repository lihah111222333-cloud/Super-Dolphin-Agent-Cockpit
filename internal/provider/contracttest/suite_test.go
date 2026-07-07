package contracttest

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func TestSuiteRejectsEmptyProviderName(t *testing.T) {
	result := ValidateSpec(Spec{})
	if result == nil {
		t.Fatal("ValidateSpec() error = nil, want empty provider name error")
	}
}

func TestSuiteRejectsMissingRequiredCases(t *testing.T) {
	spec := CompleteFixtureSpec("fixture")
	delete(spec.RequiredCases, CaseRuntimeReport)
	if err := ValidateSpec(spec); err == nil {
		t.Fatal("ValidateSpec() error = nil, want missing required case error")
	}
}

func TestSuiteRejectsRequiredCaseWithoutEvidence(t *testing.T) {
	spec := CompleteFixtureSpec("fixture")
	spec.RequiredCases[CaseRuntimeReport] = Case{Name: "runtime report", Run: func(*testing.T, *CaseEvidence) {}}
	if err := RunSpecForTest(t, spec); err == nil {
		t.Fatal("RunSpecForTest() error = nil, want missing evidence error")
	}
}

func TestSuiteRejectsRequiredCaseWithoutRecordedAssertion(t *testing.T) {
	spec := CompleteFixtureSpec("fixture")
	spec.RequiredCases[CaseRuntimeReport] = Case{Name: "runtime report", Run: func(t *testing.T, e *CaseEvidence) {
		t.Helper()
		_ = RuntimeReportEvidence{AgentID: "agent-contract", Provider: "fixture", StdioMode: "stdio"}
	}}
	if err := RunSpecForTest(t, spec); err == nil {
		t.Fatal("RunSpecForTest() error = nil, want missing recorded assertion")
	}
}

func TestSuiteRejectsTautologicalRequiredCaseEvidence(t *testing.T) {
	spec := CompleteFixtureSpec("fixture")
	spec.RequiredCases[CaseRuntimeReport] = Case{Name: "runtime report", Run: func(t *testing.T, e *CaseEvidence) {
		t.Helper()
		e.AssertEqual(t, EvidenceKey("supplemental.runtime_report_shape"), true, true)
	}}
	err := RunSpecForTest(t, spec)
	if err == nil || !strings.Contains(err.Error(), "tautological") {
		t.Fatalf("RunSpecForTest() error = %v, want tautological evidence failure", err)
	}
}

func TestSuiteRejectsGenericReservedEvidenceKey(t *testing.T) {
	spec := CompleteFixtureSpec("fixture")
	spec.RequiredCases[CaseRuntimeReport] = Case{Name: "runtime report", Run: func(t *testing.T, e *CaseEvidence) {
		t.Helper()
		e.AssertNoError(t, EvidenceRuntimeReportPayload, nil)
	}}
	err := RunSpecForTest(t, spec)
	if err == nil || !strings.Contains(err.Error(), "typed evidence helper") {
		t.Fatalf("RunSpecForTest() error = %v, want reserved evidence helper failure", err)
	}
}

func TestSuiteRejectsPromptParitySelfEvidence(t *testing.T) {
	spec := CompleteFixtureSpec("fixture")
	fields := completePromptParityFields()
	evidence := NewProviderPromptEvidence("capture-1", fields)
	spec.RequiredCases[CasePromptParity] = Case{Name: "prompt parity", Run: func(t *testing.T, e *CaseEvidence) {
		t.Helper()
		e.RecordPromptParity(t, evidence, evidence)
	}}
	err := RunSpecForTest(t, spec)
	if err == nil || !strings.Contains(err.Error(), "expected_snapshot") {
		t.Fatalf("RunSpecForTest() error = %v, want prompt parity self-evidence failure", err)
	}
}

func TestSuiteRejectsPromptParityCopiedExpectedEvidence(t *testing.T) {
	spec := CompleteFixtureSpec("fixture")
	got := NewProviderPromptEvidence("capture-1", completePromptParityFields())
	want := got
	spec.RequiredCases[CasePromptParity] = Case{Name: "prompt parity", Run: func(t *testing.T, e *CaseEvidence) {
		t.Helper()
		e.RecordPromptParity(t, got, want)
	}}
	err := RunSpecForTest(t, spec)
	if err == nil || !strings.Contains(err.Error(), "independent expected_snapshot") {
		t.Fatalf("RunSpecForTest() error = %v, want copied expected evidence failure", err)
	}
}

func TestSuiteRejectsPromptParityExpectedFieldsCopiedFromProvider(t *testing.T) {
	spec := CompleteFixtureSpec("fixture")
	fields := completePromptParityFields()
	got := NewProviderPromptEvidence("capture-1", fields)
	want := expectedPromptEvidenceForTest("snapshot-1", fields)
	spec.RequiredCases[CasePromptParity] = Case{Name: "prompt parity", Run: func(t *testing.T, e *CaseEvidence) {
		t.Helper()
		e.RecordPromptParity(t, got, want)
	}}
	err := RunSpecForTest(t, spec)
	if err == nil || !strings.Contains(err.Error(), "independent expected_snapshot") {
		t.Fatalf("RunSpecForTest() error = %v, want copied expected fields failure", err)
	}
}

func TestSuiteRejectsPromptParitySharedEvidenceID(t *testing.T) {
	spec := CompleteFixtureSpec("fixture")
	fields := completePromptParityFields()
	got := NewProviderPromptEvidence("same-origin", fields)
	want := NewExpectedPromptEvidence(ExpectedPromptSnapshot{
		snapshotID:         "same-origin",
		fields:             fields,
		loadedFromSnapshot: true,
	})
	spec.RequiredCases[CasePromptParity] = Case{Name: "prompt parity", Run: func(t *testing.T, e *CaseEvidence) {
		t.Helper()
		e.RecordPromptParity(t, got, want)
	}}
	err := RunSpecForTest(t, spec)
	if err == nil || !strings.Contains(err.Error(), "independent expected_snapshot") {
		t.Fatalf("RunSpecForTest() error = %v, want shared evidence id failure", err)
	}
}

func TestSuiteRejectsPromptParityDirectFieldEvidence(t *testing.T) {
	spec := CompleteFixtureSpec("fixture")
	spec.RequiredCases[CasePromptParity] = Case{Name: "prompt parity", Run: func(t *testing.T, e *CaseEvidence) {
		t.Helper()
		fields := completePromptParityFields()
		e.AssertEqual(t, EvidencePromptBaseInstructions, fields.BaseInstructions, fields.BaseInstructions)
		e.AssertEqual(t, EvidencePromptDeveloperInstructions, fields.DeveloperInstructions, fields.DeveloperInstructions)
		e.AssertEqual(t, EvidencePromptPrefixHash, fields.PrefixHash, fields.PrefixHash)
		e.AssertEqual(t, EvidencePromptBoundary, fields.Boundary, fields.Boundary)
		e.AssertEqual(t, EvidencePromptSectionSnapshot, fields.SectionSnapshot, fields.SectionSnapshot)
	}}
	err := RunSpecForTest(t, spec)
	if err == nil || !strings.Contains(err.Error(), "typed evidence helper") {
		t.Fatalf("RunSpecForTest() error = %v, want direct prompt evidence failure", err)
	}
}

func TestSuiteRejectsResumeWithoutIdentityEvidence(t *testing.T) {
	spec := CompleteFixtureSpec("fixture")
	delete(spec.RequiredCases, CaseResume)
	if err := ValidateSpec(spec); err == nil {
		t.Fatal("ValidateSpec() error = nil, want missing resume identity case")
	}

	spec = CompleteFixtureSpec("fixture")
	spec.RequiredCases[CaseResume] = Case{Name: "resume identity", Run: func(t *testing.T, e *CaseEvidence) {
		t.Helper()
	}}
	if err := RunSpecForTest(t, spec); err == nil {
		t.Fatal("RunSpecForTest() error = nil, want missing resume identity evidence")
	}
}

func TestSuiteRunsCompleteProvider(t *testing.T) {
	spec := CompleteFixtureSpec("fixture")
	Run(t, spec)
}

func TestLoadExpectedPromptSnapshotRejectsMissingSnapshot(t *testing.T) {
	_, err := loadExpectedPromptSnapshotFields("does-not-exist")
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("loadExpectedPromptSnapshotFields() error = %v, want missing snapshot", err)
	}
}

func TestLoadExpectedPromptSnapshotRejectsEmptySnapshot(t *testing.T) {
	_, err := loadExpectedPromptSnapshotFields("empty")
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("loadExpectedPromptSnapshotFields() error = %v, want empty snapshot", err)
	}
}

func TestLoadExpectedPromptSnapshotRejectsUntrackedSnapshot(t *testing.T) {
	path := filepath.Join("testdata", "prompt_snapshots", "untracked_contracttest.json")
	if err := os.WriteFile(path, []byte(`{"baseInstructions":"base"}`), 0o600); err != nil {
		t.Fatalf("write untracked snapshot: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })

	_, err := loadExpectedPromptSnapshotFields("untracked_contracttest")
	if err == nil || !strings.Contains(err.Error(), "tracked golden data") {
		t.Fatalf("loadExpectedPromptSnapshotFields() error = %v, want tracked golden data", err)
	}
}

func TestLoadExpectedPromptSnapshotRejectsDirtyTrackedSnapshot(t *testing.T) {
	path := filepath.Join("testdata", "prompt_snapshots", "valid.json")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read tracked snapshot: %v", err)
	}
	t.Cleanup(func() {
		if err := os.WriteFile(path, original, 0o600); err != nil {
			t.Errorf("restore tracked snapshot: %v", err)
		}
	})
	if err := os.WriteFile(path, append([]byte(nil), append(original, []byte("\n ")...)...), 0o600); err != nil {
		t.Fatalf("dirty tracked snapshot: %v", err)
	}

	_, err = loadExpectedPromptSnapshotFields("valid")
	if err == nil || !strings.Contains(err.Error(), "unstaged working-tree changes") {
		t.Fatalf("loadExpectedPromptSnapshotFields() error = %v, want dirty tracked snapshot", err)
	}
}

func TestLoadExpectedPromptSnapshotRejectsGeneratedMarker(t *testing.T) {
	_, err := loadExpectedPromptSnapshotFields("generated_marker")
	if err == nil || !strings.Contains(err.Error(), "generated during the test") {
		t.Fatalf("loadExpectedPromptSnapshotFields() error = %v, want generated marker", err)
	}
}

func TestRecordEventTranslationRejectsCopiedExpectedEvent(t *testing.T) {
	spec := CompleteFixtureSpec("fixture")
	got := NewProviderEventEvidenceForTest("capture-1", map[string]string{"type": "ok"})
	spec.EventCases = []Case{{Name: "copied expected event", Run: func(t *testing.T, e *CaseEvidence) {
		t.Helper()
		e.RecordEventTranslation(t, "copied", got, got)
	}}}
	err := RunSpecForTest(t, spec)
	if err == nil || !strings.Contains(err.Error(), "expected_event_snapshot") {
		t.Fatalf("RunSpecForTest() error = %v, want independent snapshot error", err)
	}
}

func TestRecordEventTranslationRejectsSameCaptureAndSnapshotID(t *testing.T) {
	spec := CompleteFixtureSpec("fixture")
	got := NewProviderEventEvidenceForTest("same", map[string]string{"type": "ok"})
	want := NewExpectedEventEvidence(ExpectedEventSnapshot{
		snapshotID:         "same",
		canonicalJSON:      mustCanonicalEventJSON(t, map[string]string{"type": "ok"}),
		loadedFromSnapshot: true,
	})
	spec.EventCases = []Case{{Name: "same id", Run: func(t *testing.T, e *CaseEvidence) {
		t.Helper()
		e.RecordEventTranslation(t, "same", got, want)
	}}}
	err := RunSpecForTest(t, spec)
	if err == nil || !strings.Contains(err.Error(), "distinct capture and expected snapshot ids") {
		t.Fatalf("RunSpecForTest() error = %v, want distinct id error", err)
	}
}

func TestRecordEventTranslationRejectsCopiedSnapshotLiteral(t *testing.T) {
	spec := CompleteFixtureSpec("fixture")
	got := EventTranslationEvidence{
		origin:        eventOriginProviderTranslator,
		evidenceID:    "literal",
		canonicalJSON: mustCanonicalEventJSON(t, map[string]string{"event": "translated"}),
	}
	want := NewExpectedEventEvidence(LoadExpectedEventSnapshot(t, "valid"))
	spec.EventCases = []Case{{Name: "literal observed", Run: func(t *testing.T, e *CaseEvidence) {
		t.Helper()
		e.RecordEventTranslation(t, "literal", got, want)
	}}}
	err := RunSpecForTest(t, spec)
	if err == nil || !strings.Contains(err.Error(), "translator capture API") {
		t.Fatalf("RunSpecForTest() error = %v, want translator capture API error", err)
	}
}

func TestRecordEventTranslationRejectsMissingGoldenEvent(t *testing.T) {
	_, err := loadExpectedEventSnapshot("does-not-exist")
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("loadExpectedEventSnapshot() error = %v, want missing snapshot", err)
	}
}

func TestLoadExpectedEventSnapshotExposesNoDecoderCallback(t *testing.T) {
	fnType := reflect.TypeOf(LoadExpectedEventSnapshot)
	if fnType.NumIn() != 2 {
		t.Fatalf("LoadExpectedEventSnapshot inputs = %d, want t and snapshot id only", fnType.NumIn())
	}
}

func TestLoadExpectedEventSnapshotRejectsDirtyTrackedSnapshot(t *testing.T) {
	path := filepath.Join("testdata", "event_snapshots", "valid.json")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read tracked event snapshot: %v", err)
	}
	t.Cleanup(func() {
		if err := os.WriteFile(path, original, 0o600); err != nil {
			t.Errorf("restore tracked event snapshot: %v", err)
		}
	})
	if err := os.WriteFile(path, append([]byte(nil), append(original, []byte("\n ")...)...), 0o600); err != nil {
		t.Fatalf("dirty tracked event snapshot: %v", err)
	}

	_, err = loadExpectedEventSnapshot("valid")
	if err == nil || !strings.Contains(err.Error(), "unstaged working-tree changes") {
		t.Fatalf("loadExpectedEventSnapshot() error = %v, want dirty tracked snapshot", err)
	}
}

func TestRecordOutcomeRejectsFreeFormApproval(t *testing.T) {
	err := runOutcomeCase(t, EvidenceApprovalOutcome, OutcomeEvidence{StateAfter: "approved"})
	if err == nil || !strings.Contains(err.Error(), "observed action id or typed unsupported result") {
		t.Fatalf("RecordOutcome() error = %v, want typed outcome evidence error", err)
	}
}

func TestRecordOutcomeRejectsBooleanUnsupported(t *testing.T) {
	err := runOutcomeCase(t, EvidenceApprovalOutcome, OutcomeEvidence{
		StateAfter: "unsupported",
		Unsupported: &UnsupportedOutcomeEvidence{
			operationID:    "op-boolean",
			dependencyName: "approval",
			profile:        contract.DependencyProfileTest,
			booleanOnly:    true,
		},
		ExpectedDependencyName: "approval",
	})
	if err == nil || !strings.Contains(err.Error(), "typed dependency-mode error") {
		t.Fatalf("RecordOutcome() error = %v, want boolean unsupported rejection", err)
	}
}

func TestRecordOutcomeRejectsSyntheticUnsupportedWithoutObservedProviderResult(t *testing.T) {
	err := runOutcomeCase(t, EvidenceApprovalOutcome, OutcomeEvidence{
		StateAfter: "unsupported",
		Unsupported: &UnsupportedOutcomeEvidence{
			err:            contract.NewDependencyModeError(contract.ErrUnsupportedDependencyMode, "approval", contract.DependencyProfileTest),
			dependencyName: "approval",
			profile:        contract.DependencyProfileTest,
			operationID:    "inline",
		},
		ExpectedDependencyName: "approval",
	})
	if err == nil || !strings.Contains(err.Error(), "observed provider operation") {
		t.Fatalf("RecordOutcome() error = %v, want synthetic unsupported rejection", err)
	}
}

func TestRecordOutcomeRejectsWrongUnsupportedDependencyForCase(t *testing.T) {
	unsupported := CaptureUnsupportedOutcome(t, "approval-op", "approval", contract.DependencyProfileTest, func() error {
		return contract.NewDependencyModeError(contract.ErrUnsupportedDependencyMode, "approval", contract.DependencyProfileTest)
	})
	err := runOutcomeCase(t, EvidenceApprovalOutcome, OutcomeEvidence{
		StateAfter:             "unsupported",
		Unsupported:            unsupported,
		ExpectedDependencyName: "other-dependency",
	})
	if err == nil || !strings.Contains(err.Error(), "want other-dependency") {
		t.Fatalf("RecordOutcome() error = %v, want key-specific dependency error", err)
	}
}

func TestRecordOutcomeRejectsToolbridgeWithoutDependencyProfile(t *testing.T) {
	err := runOutcomeCase(t, EvidenceToolbridgeDependency, OutcomeEvidence{
		ObservedActionID: "toolbridge-call",
		StateAfter:       "completed",
	})
	if err == nil || !strings.Contains(err.Error(), "dependency name and profile") {
		t.Fatalf("RecordOutcome() error = %v, want dependency/profile evidence error", err)
	}
}

func TestRecordOutcomeRejectsCustomAsSyntheticUnsupported(t *testing.T) {
	err := runOutcomeCase(t, EvidenceApprovalOutcome, OutcomeEvidence{
		StateAfter: "unsupported",
		Unsupported: &UnsupportedOutcomeEvidence{
			err:            customAsDependencyModeError{},
			dependencyName: "approval",
			profile:        contract.DependencyProfileTest,
			operationID:    "custom-as",
			captured:       true,
		},
		ExpectedDependencyName: "approval",
	})
	if err == nil || !strings.Contains(err.Error(), "concrete dependency mode error") {
		t.Fatalf("RecordOutcome() error = %v, want concrete dependency-mode error requirement", err)
	}
}

func TestFixtureSessionCapabilityContracts(t *testing.T) {
	session := NewFixtureSession("fixture", "thread-contract", dto.CapabilitySet{})
	if _, err := session.ListThreads(t.Context()); !isCapabilityError(err, dto.CapThreadList) {
		t.Fatalf("ListThreads() error = %v, want thread list CapabilityError", err)
	}
	if _, err := session.ForkThread(t.Context(), dto.ForkRequest{ThreadID: session.ThreadID()}); !isCapabilityError(err, dto.CapThreadFork) {
		t.Fatalf("ForkThread() error = %v, want thread fork CapabilityError", err)
	}
}

func completePromptParityFields() PromptParityFields {
	return PromptParityFields{
		BaseInstructions:      "base contract instructions",
		DeveloperInstructions: "developer contract instructions",
		PrefixHash:            "hash-contract",
		Boundary:              `{"cachedPrefix":"base contract instructions","uncachedTail":"runtime context"}`,
		SectionSnapshot:       `{"developer":"developer contract instructions","system":"base contract instructions"}`,
	}
}

func runOutcomeCase(t *testing.T, key EvidenceKey, outcome OutcomeEvidence) error {
	t.Helper()
	required := []EvidenceKey{key}
	switch key {
	case EvidenceInterruptOutcome:
	case EvidenceForceCompleteOutcome:
	case EvidenceToolbridgeDependency:
	default:
	}
	evidence := NewEvidence()
	evidence.RecordOutcome(t, key, outcome)
	return evidence.Validate(string(key), required)
}

type customAsDependencyModeError struct{}

func (customAsDependencyModeError) Error() string { return "custom as dependency mode error" }

func (customAsDependencyModeError) As(target any) bool {
	modeErr, ok := target.(*contract.DependencyModeError)
	if !ok {
		return false
	}
	*modeErr = contract.DependencyModeError{
		Err:     contract.ErrUnsupportedDependencyMode,
		Name:    "approval",
		Profile: contract.DependencyProfileTest,
	}
	return true
}

func mustCanonicalEventJSON(t testing.TB, event any) []byte {
	t.Helper()
	canonical, err := canonicalEventJSON(event)
	if err != nil {
		t.Fatalf("canonical event JSON: %v", err)
	}
	return canonical
}

func TestRunSpecForTestWithTestingTDoesNotUseNilRuntime(t *testing.T) {
	spec := CompleteFixtureSpec("fixture")
	if err := RunSpecForTest(t, spec); err != nil {
		t.Fatalf("RunSpecForTest() error = %v", err)
	}
}

func ExampleLoadExpectedEventSnapshot_signature() {
	fmt.Println(reflect.TypeOf(LoadExpectedEventSnapshot).NumIn())
	// Output: 2
}
