package contracttest

import (
	"context"
	"errors"
	"maps"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func expectedPromptEvidenceForTest(snapshotID string, fields PromptParityFields) PromptParityEvidence {
	return PromptParityEvidence{origin: promptOriginExpectedSnapshot, evidenceID: strings.TrimSpace(snapshotID), fields: fields}
}

func newProviderEventEvidenceForTest(captureID string, event any, captured bool) EventTranslationEvidence {
	canonical, _ := canonicalEventJSON(event)
	return EventTranslationEvidence{
		origin:        eventOriginProviderTranslator,
		evidenceID:    strings.TrimSpace(captureID),
		canonicalJSON: canonical,
		captured:      captured,
	}
}

func NewProviderEventEvidenceForTest(captureID string, event any) EventTranslationEvidence {
	return newProviderEventEvidenceForTest(captureID, event, true)
}

func NewFixtureSession(provider, threadID string, caps dto.CapabilitySet) contract.Session {
	copiedCaps := dto.CapabilitySet{}
	maps.Copy(copiedCaps, caps)
	state := &fixtureSessionState{
		provider: strings.TrimSpace(provider),
		threadID: strings.TrimSpace(threadID),
		caps:     copiedCaps,
	}
	return &fixtureSession{
		fixtureSessionState:     state,
		fixtureTurnOperations:   fixtureTurnOperations{},
		fixtureThreadOperations: fixtureThreadOperations{state: state},
		fixtureLifecycle:        fixtureLifecycle{state: state},
	}
}

func CompleteFixtureSpec(provider string) Spec {
	name := strings.TrimSpace(provider)
	if name == "" {
		name = "fixture"
	}
	return Spec{
		Name:       name,
		Start:      fixtureStart(name),
		Resume:     fixtureResume(name),
		EventCases: []Case{fixtureEventCase()},
		RequiredCases: map[CaseKey]Case{
			CaseEventMatrix:          fixtureEventMatrixCase(name),
			CasePromptParity:         fixturePromptParityCase(),
			CaseApproval:             fixtureOutcomeCase("approval", EvidenceApprovalOutcome),
			CaseInterrupt:            fixtureOutcomeCase("interrupt", EvidenceInterruptOutcome),
			CaseForceComplete:        fixtureOutcomeCase("force_complete", EvidenceForceCompleteOutcome),
			CaseResume:               fixtureResumeIdentityCase(),
			CaseToolbridge:           fixtureToolbridgeCase(),
			CaseDynamicToolResponder: fixtureDynamicToolResponderCase(),
			CaseRuntimeReport:        fixtureRuntimeReportCase(name),
		},
	}
}

func fixtureEventMatrixCase(provider string) Case {
	return Case{Name: "event matrix", Run: func(t *testing.T, e *CaseEvidence) {
		t.Helper()
		e.RecordEventMatrix(t, EventMatrixEvidence{
			Provider: provider,
			Categories: []EventMatrixCategoryEvidence{
				{Category: "interrupt", SnapshotIDs: []string{"valid"}, TranslatorID: "fixtureTranslateEvent"},
				{Category: "tool_end", SnapshotIDs: []string{"valid"}, TranslatorID: "fixtureTranslateEvent"},
				{Category: "failed_or_status", SnapshotIDs: []string{"valid"}, TranslatorID: "fixtureTranslateEvent"},
				{Category: "approval_or_tool_diff", SnapshotIDs: []string{"valid"}, TranslatorID: "fixtureTranslateEvent"},
			},
		})
	}}
}

func fixtureStart(provider string) func(context.Context, dto.StartSessionRequest) (contract.Session, error) {
	return func(_ context.Context, req dto.StartSessionRequest) (contract.Session, error) {
		threadID := strings.TrimSpace(req.AgentID)
		if threadID == "" {
			threadID = "public-thread-contract"
		}
		return NewFixtureSession(provider, threadID, dto.CapabilitySet{}), nil
	}
}

func fixtureResume(provider string) func(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
	return func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
		threadID := strings.TrimSpace(req.ProviderThreadID)
		if threadID == "" {
			return nil, errors.New("provider thread id is required")
		}
		return NewFixtureSession(provider, threadID, dto.CapabilitySet{}), nil
	}
}

func fixtureEventCase() Case {
	return Case{Name: "translated event", Run: func(t *testing.T, e *CaseEvidence) {
		t.Helper()
		raw := dto.RawProviderEvent{EventType: "fixture", Data: map[string]any{"event": "translated"}}
		got := CaptureProviderEventTranslation(t, "fixture-event-capture", raw, fixtureTranslateEvent)
		want := NewExpectedEventEvidence(LoadExpectedEventSnapshot(t, "valid"))
		e.RecordEventTranslation(t, "fixture event", got, want)
	}}
}

func fixturePromptParityCase() Case {
	return Case{Name: "prompt parity", Run: func(t *testing.T, e *CaseEvidence) {
		t.Helper()
		got := NewProviderPromptEvidence("fixture-prompt-capture", FixturePromptParityFields())
		want := NewExpectedPromptEvidence(LoadExpectedPromptSnapshot(t, "valid"))
		e.RecordPromptParity(t, got, want)
	}}
}

func fixtureOutcomeCase(action string, key EvidenceKey) Case {
	return Case{Name: action, Run: func(t *testing.T, e *CaseEvidence) {
		t.Helper()
		e.RecordOutcome(t, key, OutcomeEvidence{
			ObservedActionID: action + "-fixture",
			StateBefore:      "running",
			StateAfter:       "completed",
		})
	}}
}

func fixtureResumeIdentityCase() Case {
	return Case{Name: "resume identity", Run: func(t *testing.T, e *CaseEvidence) {
		t.Helper()
		e.RecordResumeIdentity(t, ResumeIdentityEvidence{
			PublicThreadID:   "public-thread-contract",
			ProviderThreadID: "provider-thread-contract",
			ResumedThreadID:  "provider-thread-contract",
		})
	}}
}

func fixtureToolbridgeCase() Case {
	return Case{Name: "toolbridge dependency", Run: func(t *testing.T, e *CaseEvidence) {
		t.Helper()
		e.RecordOutcome(t, EvidenceToolbridgeDependency, OutcomeEvidence{
			ObservedActionID: "toolbridge-fixture",
			StateAfter:       "completed",
			DependencyName:   "toolbridge",
			Profile:          contract.DependencyProfileTest,
		})
	}}
}

func fixtureDynamicToolResponderCase() Case {
	return Case{Name: "dynamic tool responder", Run: func(t *testing.T, e *CaseEvidence) {
		t.Helper()
		e.RecordDynamicToolResponder(t, DynamicToolResponderEvidence{
			ToolName:        "fixture_echo",
			CallID:          "call-fixture",
			ResponseID:      "response-fixture",
			ResponsePayload: `{"ok":true}`,
		})
	}}
}

func fixtureRuntimeReportCase(provider string) Case {
	return Case{Name: "runtime report", Run: func(t *testing.T, e *CaseEvidence) {
		t.Helper()
		e.RecordRuntimeReport(t, RuntimeReportEvidence{
			AgentID:   "agent-contract",
			Provider:  provider,
			StdioMode: "fixture",
		})
	}}
}

func FixturePromptParityFields() PromptParityFields {
	return PromptParityFields{
		BaseInstructions:      "base contract instructions",
		DeveloperInstructions: "developer contract instructions",
		PrefixHash:            "hash-contract",
		Boundary:              `{"cachedPrefix":"base contract instructions","uncachedTail":"runtime context"}`,
		SectionSnapshot:       `{"developer":"developer contract instructions","system":"base contract instructions"}`,
	}
}

func FixtureStartRequest(provider string) dto.StartSessionRequest {
	fields := FixturePromptParityFields()
	boundary := &dto.PromptAssemblyBoundary{
		CachedPrefix: "base contract instructions",
		UncachedTail: "runtime context",
	}
	sectionSnapshot := map[string]string{
		"developer": "developer contract instructions",
		"system":    "base contract instructions",
	}
	return dto.StartSessionRequest{
		Provider: provider,
		AgentID:  "public-thread-contract",
		CWD:      "/tmp/contracttest",
		StartAssembly: dto.StartAssembly{
			DisplayName:           "contract",
			BaseInstructions:      fields.BaseInstructions,
			DeveloperInstructions: fields.DeveloperInstructions,
			Boundary:              boundary,
			PrefixShape:           dto.PrefixShape{Hash: fields.PrefixHash},
			Snapshot: dto.PromptAssemblySnapshot{
				DisplayName:           "contract",
				BaseInstructions:      fields.BaseInstructions,
				DeveloperInstructions: fields.DeveloperInstructions,
				Boundary:              boundary,
				Provider:              provider,
				Hash:                  fields.PrefixHash,
				SectionSnapshot:       sectionSnapshot,
				Generation:            1,
			},
		},
	}
}

func fixtureTranslateEvent(_ dto.RawProviderEvent, publish func(any)) {
	publish(map[string]string{"event": "translated"})
}

type fixtureSession struct {
	*fixtureSessionState
	fixtureTurnOperations
	fixtureThreadOperations
	fixtureLifecycle
}

type fixtureSessionState struct {
	provider string
	threadID string
	caps     dto.CapabilitySet
	closed   bool
}

func (s *fixtureSessionState) ThreadID() string { return s.threadID }

func (s *fixtureSessionState) RolloutPath() string { return "/tmp/contracttest-rollout.jsonl" }

func (s *fixtureSessionState) Capabilities() dto.CapabilitySet { return s.caps }

type fixtureTurnOperations struct{}

func (fixtureTurnOperations) StartTurn(_ context.Context, req dto.TurnRequest) (contract.TurnHandle, error) {
	localID := strings.TrimSpace(req.LocalID)
	if localID == "" {
		localID = "turn-contract"
	}
	return newFixtureTurnHandle(localID, "provider-"+localID), nil
}

func (fixtureTurnOperations) Interrupt(context.Context, dto.InterruptRequest) error { return nil }

func (fixtureTurnOperations) ForceComplete(context.Context, dto.ForceCompleteRequest) error {
	return nil
}

func (fixtureTurnOperations) ReadHistory(context.Context, string, int) ([]dto.Message, error) {
	return []dto.Message{}, nil
}

func (fixtureTurnOperations) Configure(context.Context, dto.ThreadConfigPatch) error { return nil }

type fixtureThreadOperations struct {
	state *fixtureSessionState
}

func (o fixtureThreadOperations) ListThreads(context.Context) ([]dto.ThreadRef, error) {
	if !o.state.caps.Has(dto.CapThreadList) {
		return nil, contract.NewCapabilityError(dto.CapThreadList, o.state.provider)
	}
	return []dto.ThreadRef{{ID: o.state.threadID, Name: "fixture"}}, nil
}

func (o fixtureThreadOperations) ForkThread(context.Context, dto.ForkRequest) (dto.ForkResult, error) {
	if !o.state.caps.Has(dto.CapThreadFork) {
		return dto.ForkResult{}, contract.NewCapabilityError(dto.CapThreadFork, o.state.provider)
	}
	return dto.ForkResult{NewThreadID: o.state.threadID + "-fork"}, nil
}

type fixtureLifecycle struct {
	state *fixtureSessionState
}

func (l fixtureLifecycle) Close(context.Context) error {
	l.state.closed = true
	return nil
}

func (l fixtureLifecycle) ForceStop() error {
	l.state.closed = true
	return nil
}

type fixtureTurnHandle struct {
	localID    string
	providerID string
	done       chan struct{}
}

func newFixtureTurnHandle(localID, providerID string) *fixtureTurnHandle {
	done := make(chan struct{})
	close(done)
	return &fixtureTurnHandle{localID: localID, providerID: providerID, done: done}
}

func (h *fixtureTurnHandle) LocalID() string { return h.localID }

func (h *fixtureTurnHandle) ProviderID() string { return h.providerID }

func (h *fixtureTurnHandle) Done() <-chan struct{} { return h.done }

func (h *fixtureTurnHandle) Err() error { return nil }
