package contracttest

import (
	"fmt"
	"os"
	"os/exec"
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

func TestSuiteRejectsMissingDynamicToolResponderCase(t *testing.T) {
	spec := CompleteFixtureSpec("fixture")
	delete(spec.RequiredCases, CaseDynamicToolResponder)
	if err := ValidateSpec(spec); err == nil {
		t.Fatal("ValidateSpec() error = nil, want missing dynamic tool responder case")
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

func TestSuiteRejectsGenericDynamicToolResponderEvidenceKey(t *testing.T) {
	spec := CompleteFixtureSpec("fixture")
	spec.RequiredCases[CaseDynamicToolResponder] = Case{Name: "dynamic tool responder", Run: func(t *testing.T, e *CaseEvidence) {
		t.Helper()
		e.AssertNoError(t, EvidenceDynamicToolResponder, nil)
	}}
	err := RunSpecForTest(t, spec)
	if err == nil || !strings.Contains(err.Error(), "typed evidence helper") {
		t.Fatalf("RunSpecForTest() error = %v, want reserved dynamic tool responder evidence helper failure", err)
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

func TestRecordDynamicToolResponderRejectsMissingObservedResponse(t *testing.T) {
	spec := CompleteFixtureSpec("fixture")
	spec.RequiredCases[CaseDynamicToolResponder] = Case{Name: "dynamic tool responder", Run: func(t *testing.T, e *CaseEvidence) {
		t.Helper()
		e.RecordDynamicToolResponder(t, DynamicToolResponderEvidence{ToolName: "fixture_echo"})
	}}

	err := RunSpecForTest(t, spec)
	if err == nil || !strings.Contains(err.Error(), "response id") {
		t.Fatalf("RunSpecForTest() error = %v, want missing dynamic tool response failure", err)
	}
}

func TestRecordDynamicToolResponderRejectsBooleanUnsupported(t *testing.T) {
	spec := CompleteFixtureSpec("fixture")
	spec.RequiredCases[CaseDynamicToolResponder] = Case{Name: "dynamic tool responder", Run: func(t *testing.T, e *CaseEvidence) {
		t.Helper()
		e.RecordDynamicToolResponder(t, DynamicToolResponderEvidence{
			ExpectedDependencyName: "dynamic_tools",
			Unsupported: &UnsupportedOutcomeEvidence{
				operationID:    "dynamic-tool-op",
				dependencyName: "dynamic_tools",
				profile:        contract.DependencyProfileTest,
				booleanOnly:    true,
			},
		})
	}}

	err := RunSpecForTest(t, spec)
	if err == nil || !strings.Contains(err.Error(), "typed dependency-mode error") {
		t.Fatalf("RunSpecForTest() error = %v, want typed unsupported failure", err)
	}
}

func TestRecordDynamicToolResponderAcceptsCapturedUnsupported(t *testing.T) {
	spec := CompleteFixtureSpec("fixture")
	spec.RequiredCases[CaseDynamicToolResponder] = Case{Name: "dynamic tool responder", Run: func(t *testing.T, e *CaseEvidence) {
		t.Helper()
		unsupported := CaptureUnsupportedOutcome(t, "dynamic-tool-op", "dynamic_tools", contract.DependencyProfileTest, func() error {
			return contract.NewDependencyModeError(contract.ErrUnsupportedDependencyMode, "dynamic_tools", contract.DependencyProfileTest)
		})
		e.RecordDynamicToolResponder(t, DynamicToolResponderEvidence{
			ExpectedDependencyName: "dynamic_tools",
			Unsupported:            unsupported,
		})
	}}

	if err := RunSpecForTest(t, spec); err != nil {
		t.Fatalf("RunSpecForTest() error = %v, want captured dynamic tool unsupported accepted", err)
	}
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
	path := dirtyTrackedSnapshotRepo(t, filepath.Join("testdata", "prompt_snapshots", "valid.json"), []byte(`{"baseInstructions":"base"}`), []byte("{\"baseInstructions\":\"base\"}\n "))
	raw, repoPath, err := readTrackedSnapshot("prompt", path)
	if err != nil {
		t.Fatalf("read tracked snapshot: %v", err)
	}
	if repoPath != path {
		t.Fatalf("tracked snapshot repo path = %q, want %q", repoPath, path)
	}
	err = validateSnapshotIndex("prompt", path, repoPath, raw)
	if err == nil || !strings.Contains(err.Error(), "unstaged working-tree changes") {
		t.Fatalf("validateSnapshotIndex() error = %v, want dirty tracked snapshot", err)
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
	path := dirtyTrackedSnapshotRepo(t, filepath.Join("testdata", "event_snapshots", "valid.json"), []byte(`{"event":"original"}`), []byte("{\"event\":\"original\"}\n "))
	raw, repoPath, err := readTrackedSnapshot("event", path)
	if err != nil {
		t.Fatalf("read tracked event snapshot: %v", err)
	}
	if repoPath != path {
		t.Fatalf("tracked event snapshot repo path = %q, want %q", repoPath, path)
	}
	err = validateSnapshotIndex("event", path, repoPath, raw)
	if err == nil || !strings.Contains(err.Error(), "unstaged working-tree changes") {
		t.Fatalf("validateSnapshotIndex() error = %v, want dirty tracked snapshot", err)
	}
}

func dirtyTrackedSnapshotRepo(t *testing.T, relPath string, tracked, dirty []byte) string {
	t.Helper()
	repoRoot := t.TempDir()
	fullPath := filepath.Join(repoRoot, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("create tracked snapshot dir: %v", err)
	}
	if err := os.WriteFile(fullPath, tracked, 0o600); err != nil {
		t.Fatalf("write tracked snapshot: %v", err)
	}
	runGitCommand(t, repoRoot, "init", "-q")
	runGitCommand(t, repoRoot, "add", relPath)
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	if err := os.Chdir(repoRoot); err != nil {
		t.Fatalf("chdir dirty snapshot repo: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWD); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
	t.Setenv("GIT_DIR", filepath.Join(repoRoot, ".git"))
	t.Setenv("GIT_WORK_TREE", repoRoot)
	if err := os.WriteFile(fullPath, dirty, 0o600); err != nil {
		t.Fatalf("dirty tracked snapshot: %v", err)
	}
	return relPath
}

func runGitCommand(t *testing.T, workDir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = workDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, strings.TrimSpace(string(out)))
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
