package unified

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/contracttest"
	"github.com/kelindar/event"
)

func TestUnifiedProviderContract(t *testing.T) {
	contracttest.Run(t, CompleteUnifiedContractSpec())
}

func CompleteUnifiedContractSpec() contracttest.Spec {
	return contracttest.Spec{
		Name:       unifiedContractProvider,
		Start:      newUnifiedContractEnv().start,
		Resume:     newUnifiedContractEnv().resume,
		EventCases: []contracttest.Case{unifiedEventTranslationCase()},
		RequiredCases: map[contracttest.CaseKey]contracttest.Case{
			contracttest.CasePromptParity:  unifiedPromptParityCase(),
			contracttest.CaseApproval:      unifiedApprovalCase(),
			contracttest.CaseInterrupt:     unifiedInterruptCase(),
			contracttest.CaseForceComplete: unifiedForceCompleteCase(),
			contracttest.CaseResume:        unifiedResumeIdentityCase(),
			contracttest.CaseToolbridge:    unifiedToolbridgeCase(),
			contracttest.CaseRuntimeReport: unifiedRuntimeReportCase(),
		},
	}
}

const (
	unifiedContractProvider         = "unified-contract"
	unifiedContractMissingProvider  = "missing-unified-contract"
	unifiedContractDependency       = "provider.driver_factory"
	unifiedContractPromptSnapshotID = "unified_prompt_parity"
	unifiedContractEventSnapshotID  = "unified_common_plan_delta"
)

type unifiedContractEnv struct {
	driver   *unifiedContractDriver
	registry *Registry
	sessions *SessionManager
	client   *Client
}

func newUnifiedContractEnv() *unifiedContractEnv {
	driver := newUnifiedContractDriver(unifiedContractProvider)
	registry := NewRegistry(RegistryParams{Drivers: []contract.DriverFactory{{
		Name:   unifiedContractProvider,
		Create: func() contract.Driver { return driver },
		NativeTools: []contract.NativeToolDescriptor{
			{
				ID:          "unified-native-read",
				Label:       "Unified Read",
				Description: "contract native read tool",
				Provider:    unifiedContractProvider,
				FilterMode:  contract.NativeToolFilterModeHard,
			},
			{
				ID:          "unified-native-shell",
				Label:       "Unified Shell",
				Description: "contract native shell tool",
				Provider:    unifiedContractProvider,
				FilterMode:  contract.NativeToolFilterModeSoft,
			},
		},
	}}})
	sessions := NewSessionManager(nil)
	return &unifiedContractEnv{
		driver:   driver,
		registry: registry,
		sessions: sessions,
		client:   NewClient(registry, sessions, nil),
	}
}

func (e *unifiedContractEnv) start(ctx context.Context, req dto.StartSessionRequest) (contract.Session, error) {
	return e.client.StartSession(ctx, req)
}

func (e *unifiedContractEnv) resume(ctx context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
	return e.client.ResumeSession(ctx, req)
}

type unifiedContractDriver struct {
	name       string
	startReq   dto.StartSessionRequest
	resumeReq  dto.ResumeSessionRequest
	started    int
	resumed    int
	approvals  []unifiedContractApproval
	sessionSeq int
}

type unifiedContractApproval struct {
	callID   string
	decision contract.ApprovalDecision
}

func newUnifiedContractDriver(name string) *unifiedContractDriver {
	return &unifiedContractDriver{name: strings.TrimSpace(name)}
}

func (d *unifiedContractDriver) Name() string { return d.name }

func (d *unifiedContractDriver) StartSession(_ context.Context, req dto.StartSessionRequest) (contract.Session, error) {
	d.started++
	d.startReq = req
	d.sessionSeq++
	threadID := firstNonEmptyContractString(req.AgentID, fmt.Sprintf("start-thread-%d", d.sessionSeq))
	return newUnifiedContractSession(threadID, d.name), nil
}

func (d *unifiedContractDriver) ResumeSession(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
	d.resumed++
	d.resumeReq = req
	d.sessionSeq++
	threadID := firstNonEmptyContractString(req.ProviderThreadID, req.ThreadID, fmt.Sprintf("resume-thread-%d", d.sessionSeq))
	return newUnifiedContractSession(threadID, d.name), nil
}

func (d *unifiedContractDriver) RespondApproval(_ context.Context, callID string, decision contract.ApprovalDecision) error {
	d.approvals = append(d.approvals, unifiedContractApproval{
		callID:   strings.TrimSpace(callID),
		decision: decision,
	})
	return nil
}

type unifiedContractApprovalDriver interface {
	RespondApproval(ctx context.Context, callID string, decision contract.ApprovalDecision) error
}

type unifiedContractSession struct {
	*unifiedContractSessionIdentity
	*unifiedContractSessionTurnOps
	*unifiedContractSessionHistoryOps
	*unifiedContractSessionLifecycle
	*unifiedContractSessionRuntime
}

func newUnifiedContractSession(threadID, provider string) *unifiedContractSession {
	identity := &unifiedContractSessionIdentity{
		threadID:    strings.TrimSpace(threadID),
		rolloutPath: "/tmp/unified-contract-rollout.jsonl",
		provider:    strings.TrimSpace(provider),
	}
	return &unifiedContractSession{
		unifiedContractSessionIdentity:   identity,
		unifiedContractSessionTurnOps:    &unifiedContractSessionTurnOps{identity: identity},
		unifiedContractSessionHistoryOps: &unifiedContractSessionHistoryOps{identity: identity},
		unifiedContractSessionLifecycle:  &unifiedContractSessionLifecycle{},
		unifiedContractSessionRuntime: &unifiedContractSessionRuntime{runtime: map[string]any{
			"provider":       strings.TrimSpace(provider),
			"sessionURL":     "ws://127.0.0.1:4579/session",
			"stdioMode":      "stdio-jsonl",
			"deferredReason": "desktop-host-deferred",
		}},
	}
}

type unifiedContractSessionIdentity struct {
	threadID    string
	rolloutPath string
	provider    string
}

func (s *unifiedContractSessionIdentity) ThreadID() string    { return s.threadID }
func (s *unifiedContractSessionIdentity) RolloutPath() string { return s.rolloutPath }
func (*unifiedContractSessionIdentity) Capabilities() dto.CapabilitySet {
	return dto.CapabilitySet{}
}

type unifiedContractSessionTurnOps struct {
	identity      *unifiedContractSessionIdentity
	lastTurn      dto.TurnRequest
	lastInterrupt dto.InterruptRequest
	lastForce     dto.ForceCompleteRequest
}

func (s *unifiedContractSessionTurnOps) StartTurn(_ context.Context, req dto.TurnRequest) (contract.TurnHandle, error) {
	s.lastTurn = req
	return newUnifiedContractTurnHandle(req.LocalID, "provider-"+firstNonEmptyContractString(req.ThreadID, s.identity.threadID)), nil
}

func (s *unifiedContractSessionTurnOps) Interrupt(_ context.Context, req dto.InterruptRequest) error {
	s.lastInterrupt = req
	return nil
}

func (s *unifiedContractSessionTurnOps) ForceComplete(_ context.Context, req dto.ForceCompleteRequest) error {
	s.lastForce = req
	return nil
}

type unifiedContractSessionHistoryOps struct {
	identity *unifiedContractSessionIdentity
}

func (s *unifiedContractSessionHistoryOps) ListThreads(context.Context) ([]dto.ThreadRef, error) {
	return nil, contract.NewCapabilityError(dto.CapThreadList, s.identity.provider)
}

func (s *unifiedContractSessionHistoryOps) ForkThread(context.Context, dto.ForkRequest) (dto.ForkResult, error) {
	return dto.ForkResult{}, contract.NewCapabilityError(dto.CapThreadFork, s.identity.provider)
}

func (*unifiedContractSessionHistoryOps) ReadHistory(context.Context, string, int) ([]dto.Message, error) {
	return []dto.Message{{Role: "assistant", Content: "contract history", Timestamp: time.Unix(1, 0)}}, nil
}

type unifiedContractSessionLifecycle struct {
	closed       bool
	forceStopped bool
}

func (*unifiedContractSessionLifecycle) Configure(context.Context, dto.ThreadConfigPatch) error {
	return nil
}

func (s *unifiedContractSessionLifecycle) Close(context.Context) error {
	s.closed = true
	return nil
}

func (s *unifiedContractSessionLifecycle) ForceStop() error {
	s.forceStopped = true
	return nil
}

type unifiedContractSessionRuntime struct {
	runtime map[string]any
}

func (s *unifiedContractSessionRuntime) RuntimeConfigSnapshot() map[string]any {
	out := make(map[string]any, len(s.runtime))
	maps.Copy(out, s.runtime)
	return out
}

type unifiedContractTurnHandle struct {
	localID    string
	providerID string
	done       chan struct{}
}

func newUnifiedContractTurnHandle(localID, providerID string) *unifiedContractTurnHandle {
	done := make(chan struct{})
	close(done)
	return &unifiedContractTurnHandle{
		localID:    strings.TrimSpace(localID),
		providerID: strings.TrimSpace(providerID),
		done:       done,
	}
}

func (h *unifiedContractTurnHandle) LocalID() string       { return h.localID }
func (h *unifiedContractTurnHandle) ProviderID() string    { return h.providerID }
func (h *unifiedContractTurnHandle) Done() <-chan struct{} { return h.done }
func (h *unifiedContractTurnHandle) Err() error            { return nil }

func unifiedEventTranslationCase() contracttest.Case {
	return contracttest.Case{
		Name: "common dispatch sanitizes raw and publishes typed plan delta",
		Run: func(t *testing.T, e *contracttest.CaseEvidence) {
			raw := unifiedCommonPlanDeltaRawEvent()
			got := contracttest.CaptureProviderEventTranslation(t, "unified-common-plan-delta-capture", raw, translateCommonRawEvent)
			want := contracttest.NewExpectedEventEvidence(contracttest.LoadExpectedEventSnapshot(t, unifiedContractEventSnapshotID))
			e.RecordEventTranslation(t, "unified common plan delta", got, want)
			assertUnifiedDispatcherPublishesSanitizedRawAndTypedPlan(t, raw)
			assertUnifiedDispatcherRejectsUnsupportedTypedEvent(t)
		},
	}
}

func unifiedPromptParityCase() contracttest.Case {
	return contracttest.Case{
		Name: "routes selected driver prompt request without rewriting",
		Run: func(t *testing.T, e *contracttest.CaseEvidence) {
			env := newUnifiedContractEnv()
			_, err := env.client.StartSession(context.Background(), unifiedPromptParityStartRequest())
			if err != nil {
				t.Fatalf("StartSession() error = %v", err)
			}
			got := contracttest.NewProviderPromptEvidence("unified-provider-request", promptFieldsFromStartRequest(env.driver.startReq))
			want := contracttest.NewExpectedPromptEvidence(contracttest.LoadExpectedPromptSnapshot(t, unifiedContractPromptSnapshotID))
			e.RecordPromptParity(t, got, want)
		},
	}
}

func unifiedApprovalCase() contracttest.Case {
	return contracttest.Case{
		Name: "delegates approval to resolved driver and fails unknown provider",
		Run: func(t *testing.T, e *contracttest.CaseEvidence) {
			env := newUnifiedContractEnv()
			approved := true
			decision := contract.ApprovalDecision{Approved: &approved, Reason: "contract approved"}
			if err := respondUnifiedContractApproval(context.Background(), env.registry, unifiedContractProvider, "call-unified", decision); err != nil {
				t.Fatalf("approval delegate error = %v", err)
			}
			missingErr := respondUnifiedContractApproval(context.Background(), env.registry, unifiedContractMissingProvider, "call-missing", decision)
			if missingErr == nil || !strings.Contains(missingErr.Error(), "unknown provider") {
				t.Fatalf("missing provider approval error = %v, want unknown-provider error", missingErr)
			}
			if len(env.driver.approvals) != 1 || env.driver.approvals[0].callID != "call-unified" {
				t.Fatalf("driver approvals = %#v", env.driver.approvals)
			}
			e.RecordOutcome(t, contracttest.EvidenceApprovalOutcome, contracttest.OutcomeEvidence{
				ObservedActionID: "approval delegated call-unified",
				StateBefore:      "pending",
				StateAfter:       "approved:call-unified;missing-provider:error",
			})
		},
	}
}

func unifiedInterruptCase() contracttest.Case {
	return contracttest.Case{
		Name: "forwards interrupt to active session and fails missing session",
		Run: func(t *testing.T, e *contracttest.CaseEvidence) {
			session := newUnifiedContractSession("thread-interrupt", unifiedContractProvider)
			manager := NewSessionManager(nil)
			manager.Register("agent-interrupt", session)
			if err := interruptUnifiedContractSession(context.Background(), manager, "agent-interrupt", dto.InterruptRequest{ThreadID: "thread-interrupt", Source: "contract"}); err != nil {
				t.Fatalf("interrupt error = %v", err)
			}
			missingErr := interruptUnifiedContractSession(context.Background(), manager, "missing-agent", dto.InterruptRequest{ThreadID: "missing"})
			if !errors.Is(missingErr, contract.ErrSessionNotFound) {
				t.Fatalf("missing interrupt error = %v, want ErrSessionNotFound", missingErr)
			}
			e.RecordOutcome(t, contracttest.EvidenceInterruptOutcome, contracttest.OutcomeEvidence{
				ObservedActionID: "interrupt forwarded thread-interrupt",
				StateBefore:      "active",
				StateAfter:       "source:" + session.lastInterrupt.Source + ";missing-session:ErrSessionNotFound",
			})
		},
	}
}

func unifiedForceCompleteCase() contracttest.Case {
	return contracttest.Case{
		Name: "forwards force complete to active session and fails missing session",
		Run: func(t *testing.T, e *contracttest.CaseEvidence) {
			session := newUnifiedContractSession("thread-force", unifiedContractProvider)
			manager := NewSessionManager(nil)
			manager.Register("agent-force", session)
			if err := forceCompleteUnifiedContractSession(context.Background(), manager, "agent-force", dto.ForceCompleteRequest{ThreadID: "thread-force", ProviderID: "provider-turn-force"}); err != nil {
				t.Fatalf("force complete error = %v", err)
			}
			missingErr := forceCompleteUnifiedContractSession(context.Background(), manager, "missing-agent", dto.ForceCompleteRequest{ThreadID: "missing"})
			if !errors.Is(missingErr, contract.ErrSessionNotFound) {
				t.Fatalf("missing force complete error = %v, want ErrSessionNotFound", missingErr)
			}
			e.RecordOutcome(t, contracttest.EvidenceForceCompleteOutcome, contracttest.OutcomeEvidence{
				ObservedActionID: "force-complete forwarded thread-force",
				StateBefore:      "running",
				StateAfter:       "provider-turn:" + session.lastForce.ProviderID + ";missing-session:ErrSessionNotFound",
			})
		},
	}
}

func unifiedResumeIdentityCase() contracttest.Case {
	return contracttest.Case{
		Name: "auto resume uses provider thread identity",
		Run: func(t *testing.T, e *contracttest.CaseEvidence) {
			env := newUnifiedContractEnv()
			providerThreadID := "77777777-aaaa-bbbb-cccc-777777777777"
			resolver := NewSessionResolver(sessionResolverParams{
				Registry: env.registry,
				Sessions: env.sessions,
			}).(*sessionResolver)
			binding := &contract.SessionBinding{
				AgentID:          "agent-resume",
				Provider:         unifiedContractProvider,
				ProviderThreadID: providerThreadID,
				CodexThreadID:    "public-thread-should-not-be-used",
				Cwd:              t.TempDir(),
				RolloutPath:      writeExistingProviderHistoryFile(t),
			}
			plan, err := resolver.buildAutoResumePlan(binding, map[string]any{"provider": unifiedContractProvider}, unifiedContractPromptSnapshot(), "public-thread-resume")
			if err != nil {
				t.Fatalf("buildAutoResumePlan() error = %v", err)
			}
			session, err := plan.driver.ResumeSession(context.Background(), plan.req)
			if err != nil {
				t.Fatalf("ResumeSession() error = %v", err)
			}
			e.RecordResumeIdentity(t, contracttest.ResumeIdentityEvidence{
				PublicThreadID:   plan.req.ThreadID,
				ProviderThreadID: plan.req.ProviderThreadID,
				ResumedThreadID:  session.ThreadID(),
			})
		},
	}
}

func unifiedToolbridgeCase() contracttest.Case {
	return contracttest.Case{
		Name: "aggregates native tools from factories and fails missing provider",
		Run: func(t *testing.T, e *contracttest.CaseEvidence) {
			env := newUnifiedContractEnv()
			tools := env.registry.NativeTools()
			if len(tools) != 2 {
				t.Fatalf("NativeTools() len = %d, want 2", len(tools))
			}
			if _, err := env.registry.Resolve(unifiedContractMissingProvider); err == nil || !strings.Contains(err.Error(), "unknown provider") {
				t.Fatalf("Resolve(missing) error = %v, want unknown provider", err)
			}
			e.RecordOutcome(t, contracttest.EvidenceToolbridgeDependency, contracttest.OutcomeEvidence{
				ObservedActionID: "native tools resolved from registered factories",
				StateBefore:      "registry:registered-factories",
				StateAfter:       fmt.Sprintf("native-tools:%d;missing-provider:error", len(tools)),
				DependencyName:   unifiedContractDependency,
				Profile:          contract.DependencyProfileTest,
			})
		},
	}
}

func unifiedRuntimeReportCase() contracttest.Case {
	return contracttest.Case{
		Name: "preserves provider runtime payload fields",
		Run: func(t *testing.T, e *contracttest.CaseEvidence) {
			env := newUnifiedContractEnv()
			session, err := env.client.StartSession(context.Background(), dto.StartSessionRequest{
				Provider: unifiedContractProvider,
				AgentID:  "agent-runtime",
				CWD:      "/tmp/unified-runtime",
			})
			if err != nil {
				t.Fatalf("StartSession() error = %v", err)
			}
			runtimeReader, ok := session.(interface{ RuntimeConfigSnapshot() map[string]any })
			if !ok {
				t.Fatalf("session %T does not expose RuntimeConfigSnapshot", session)
			}
			runtime := runtimeReader.RuntimeConfigSnapshot()
			port := runtimeSessionURLPort(t, runtime)
			stdioMode := runtimeString(t, runtime, "stdioMode")
			deferredReason := runtimeString(t, runtime, "deferredReason")
			if got := runtimeString(t, runtime, "provider"); got != unifiedContractProvider {
				t.Fatalf("runtime provider = %q, want %q", got, unifiedContractProvider)
			}
			e.RecordRuntimeReport(t, contracttest.RuntimeReportEvidence{
				AgentID:        "agent-runtime",
				Provider:       unifiedContractProvider,
				SessionURLPort: port,
				StdioMode:      stdioMode,
				DeferredReason: deferredReason,
			})
		},
	}
}

func respondUnifiedContractApproval(ctx context.Context, registry *Registry, provider, callID string, decision contract.ApprovalDecision) error {
	driver, err := registry.Resolve(provider)
	if err != nil {
		return err
	}
	approvalDriver, ok := driver.(unifiedContractApprovalDriver)
	if !ok {
		return fmt.Errorf("provider %q does not support approval response", provider)
	}
	return approvalDriver.RespondApproval(ctx, callID, decision)
}

func interruptUnifiedContractSession(ctx context.Context, manager *SessionManager, agentID string, req dto.InterruptRequest) error {
	session, err := manager.Get(agentID)
	if err != nil {
		return err
	}
	return session.Interrupt(ctx, req)
}

func forceCompleteUnifiedContractSession(ctx context.Context, manager *SessionManager, agentID string, req dto.ForceCompleteRequest) error {
	session, err := manager.Get(agentID)
	if err != nil {
		return err
	}
	return session.ForceComplete(ctx, req)
}

func unifiedPromptParityStartRequest() dto.StartSessionRequest {
	snapshot := unifiedContractPromptSnapshot()
	return dto.StartSessionRequest{
		Provider: unifiedContractProvider,
		AgentID:  "agent-prompt",
		CWD:      "/tmp/unified-prompt",
		StartAssembly: dto.StartAssembly{
			DisplayName:           snapshot.DisplayName,
			BaseInstructions:      snapshot.BaseInstructions,
			DeveloperInstructions: snapshot.DeveloperInstructions,
			Boundary:              snapshot.Boundary,
			PrefixShape:           dto.PrefixShape{Hash: snapshot.Hash},
			Snapshot:              snapshot,
		},
	}
}

func unifiedContractPromptSnapshot() dto.PromptAssemblySnapshot {
	return dto.PromptAssemblySnapshot{
		DisplayName:           "unified contract prompt",
		BaseInstructions:      "unified base instructions",
		DeveloperInstructions: "unified developer instructions",
		Boundary: &dto.PromptAssemblyBoundary{
			CachedPrefix: "unified cached prefix",
			UncachedTail: "unified uncached tail",
		},
		Provider: unifiedContractProvider,
		Version:  contract.PromptAssemblySnapshotVersion,
		Hash:     "unified-prefix-hash",
		SectionSnapshot: map[string]string{
			"developer": "unified developer instructions",
			"system":    "unified base instructions",
			"tools":     "unified tool bridge instructions",
		},
		Generation: 4,
	}
}

func promptFieldsFromStartRequest(req dto.StartSessionRequest) contracttest.PromptParityFields {
	return contracttest.PromptParityFields{
		BaseInstructions:      req.StartAssembly.BaseInstructions,
		DeveloperInstructions: req.StartAssembly.DeveloperInstructions,
		PrefixHash:            req.StartAssembly.PrefixShape.Hash,
		Boundary:              promptBoundaryEvidence(req.StartAssembly.Boundary),
		SectionSnapshot:       promptSectionSnapshotEvidence(req.StartAssembly.Snapshot.SectionSnapshot),
	}
}

func promptBoundaryEvidence(boundary *dto.PromptAssemblyBoundary) string {
	if boundary == nil {
		return ""
	}
	return "cached=" + strings.TrimSpace(boundary.CachedPrefix) + "\nuncached=" + strings.TrimSpace(boundary.UncachedTail)
}

func promptSectionSnapshotEvidence(snapshot map[string]string) string {
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return ""
	}
	return string(raw)
}

func unifiedCommonPlanDeltaRawEvent() dto.RawProviderEvent {
	return dto.RawProviderEvent{
		EventType: "item/plan/delta",
		Data: map[string]any{
			"agentId":   "agent-contract",
			"threadId":  "thread-contract",
			"turnId":    "turn-contract",
			"delta":     "contract plan step",
			"timestamp": "2026-07-07T00:00:00Z",
			"token":     "secret-provider-token",
		},
	}
}

func assertUnifiedDispatcherPublishesSanitizedRawAndTypedPlan(t *testing.T, raw dto.RawProviderEvent) {
	t.Helper()
	bus := event.NewDispatcher()
	defer func() { _ = bus.Close() }()
	dispatcher := NewEventDispatcher(bus, nil)
	rawEvents := make(chan dto.BusRawProviderEvent, 1)
	planEvents := make(chan turndto.PlanDelta, 1)
	cancelRaw := event.Subscribe(bus, func(ev dto.BusRawProviderEvent) { rawEvents <- ev })
	defer cancelRaw()
	cancelPlan := event.Subscribe(bus, func(ev turndto.PlanDelta) { planEvents <- ev })
	defer cancelPlan()

	dispatcher.Dispatch(raw)

	select {
	case ev := <-rawEvents:
		assertUnifiedSanitizedRawEvent(t, ev)
	case <-time.After(time.Second):
		t.Fatal("raw provider event was not published")
	}

	select {
	case ev := <-planEvents:
		assertUnifiedPlanDeltaEvent(t, ev)
	case <-time.After(time.Second):
		t.Fatal("typed plan delta was not published")
	}
}

func assertUnifiedSanitizedRawEvent(t *testing.T, ev dto.BusRawProviderEvent) {
	t.Helper()
	encoded, _ := json.Marshal(ev.Event.Data)
	text := string(encoded)
	assertStringAbsent(t, text, "secret-provider-token")
	assertStringAbsent(t, text, "token")
	assertStringPresent(t, text, "payload_sha256")
	assertStringPresent(t, text, "payload_size_bytes")
}

func assertUnifiedPlanDeltaEvent(t *testing.T, ev turndto.PlanDelta) {
	t.Helper()
	if ev.AgentID != "agent-contract" {
		t.Fatalf("plan AgentID = %q", ev.AgentID)
	}
	if ev.ThreadID != "thread-contract" {
		t.Fatalf("plan ThreadID = %q", ev.ThreadID)
	}
	if ev.TurnID != "turn-contract" {
		t.Fatalf("plan TurnID = %q", ev.TurnID)
	}
	if ev.Delta != "contract plan step" {
		t.Fatalf("plan Delta = %q", ev.Delta)
	}
}

func assertUnifiedDispatcherRejectsUnsupportedTypedEvent(t *testing.T) {
	t.Helper()
	type unsupportedProviderEvent struct{}
	if publishTypedEvent(event.NewDispatcher(), unsupportedProviderEvent{}) {
		t.Fatal("unsupported typed event was reported as published")
	}
}

func runtimeString(t *testing.T, runtime map[string]any, key string) string {
	t.Helper()
	value, ok := runtime[key].(string)
	if !ok || strings.TrimSpace(value) == "" {
		t.Fatalf("runtime[%s] = %#v, want non-empty string", key, runtime[key])
	}
	return strings.TrimSpace(value)
}

func runtimeSessionURLPort(t *testing.T, runtime map[string]any) string {
	t.Helper()
	parsed, err := url.Parse(runtimeString(t, runtime, "sessionURL"))
	if err != nil {
		t.Fatalf("parse sessionURL: %v", err)
	}
	port := parsed.Port()
	if port == "" {
		t.Fatalf("sessionURL %q has no port", parsed.String())
	}
	return port
}

func assertStringPresent(t *testing.T, text, want string) {
	t.Helper()
	if !strings.Contains(text, want) {
		t.Fatalf("text missing %q: %s", want, text)
	}
}

func assertStringAbsent(t *testing.T, text, forbidden string) {
	t.Helper()
	if strings.Contains(text, forbidden) {
		t.Fatalf("text contained %q: %s", forbidden, text)
	}
}

func firstNonEmptyContractString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
