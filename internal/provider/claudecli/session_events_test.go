package claudecli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/kelindar/event"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

func TestForceStopRawFailedEventUsesSafeMetadata(t *testing.T) {
	t.Parallel()

	bus := event.NewDispatcher()
	defer func() { _ = bus.Close() }()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher)

	got := make(chan dto.BusRawProviderEvent, 1)
	cancel := event.Subscribe(bus, func(ev dto.BusRawProviderEvent) {
		if ev.Event.EventType == "agent:failed" {
			got <- ev
		}
	})
	defer cancel()

	stderrBuf := newLimitedBuffer(1024)
	_, _ = stderrBuf.Write([]byte("Error: authentication failed\n"))

	// done already closed so Kill() won't block; cmd nil so signal is a no-op.
	done := make(chan struct{})
	close(done)

	s := &session{
		agentID:         "agent-1",
		threadID:        "thread-1",
		sessionID:       "thread-1",
		transport:       &transport{stderr: stderrBuf, done: done},
		eventDispatcher: dispatcher,
	}

	_ = s.stop(true)

	select {
	case ev := <-got:
		data, _ := ev.Event.Data.(map[string]any)
		encoded, _ := json.Marshal(data)
		text := string(encoded)
		if strings.Contains(text, "authentication failed") || strings.Contains(text, "stderr") {
			t.Fatalf("agent:failed raw event leaked stderr: %s", text)
		}
		for _, want := range []string{"payload_sha256", "payload_size_bytes", "thread_id", "agent_id"} {
			if !strings.Contains(text, want) {
				t.Fatalf("agent:failed safe metadata = %s, want %q", text, want)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("agent:failed event was not published")
	}
}

func TestBaseDataUsesPublicThreadIDAndSeparateSessionID(t *testing.T) {
	t.Parallel()

	got := baseData(rawBase{
		AgentID:  "agent-1",
		ThreadID: "thread-public",
		TurnID:   "turn-1",
	}, "session-123", "2026-03-26T00:00:00Z")

	if got["thread_id"] != "thread-public" {
		t.Fatalf("thread_id = %v, want thread-public", got["thread_id"])
	}
	if got["session_id"] != "session-123" {
		t.Fatalf("session_id = %v, want session-123", got["session_id"])
	}
}

func TestDecodeClaudeLineLogsOnlySafePayloadMetadata(t *testing.T) {
	var buf bytes.Buffer
	old := pkglogger.Get()
	pkglogger.InitWithConsoleWriter(&buf)
	t.Cleanup(func() { pkglogger.SetForTest(old) })

	_, err := decodeClaudeLine([]byte(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"call-1","content":"token=sk-live-secret"}]}}`), rawBase{AgentID: "agent-1"})
	if err != nil {
		t.Fatalf("decodeClaudeLine(user) error = %v", err)
	}
	_ = decodeResultEvent(streamEvent{
		Type:           "result",
		IsError:        true,
		TerminalReason: "provider_error",
		Error:          json.RawMessage(`{"message":"sk-live-secret"}`),
		Message:        json.RawMessage(`"token=sk-live-secret"`),
		Errors:         []string{"api_key=sk-live-secret"},
	}, rawBase{AgentID: "agent-1"})

	output := buf.String()
	for _, forbidden := range []string{"sk-live-secret", "api_key", "token="} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("claude log leaked %q: %q", forbidden, output)
		}
	}
	for _, want := range []string{"payload_sha256", "payload_size_bytes"} {
		if !strings.Contains(output, want) {
			t.Fatalf("claude log = %q, want %q metadata", output, want)
		}
	}
}

func TestHandleReceiveExitEOFCompletesActiveTurn(t *testing.T) {
	t.Parallel()

	bus := event.NewDispatcher()
	defer func() { _ = bus.Close() }()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher)

	got := make(chan turndto.TurnCompleted, 1)
	cancel := event.Subscribe(bus, func(ev turndto.TurnCompleted) {
		got <- ev
	})
	defer cancel()

	tr := &transport{}
	handle := newTurnHandle("local-1", "turn-1")
	s := &session{
		agentID:         "agent-1",
		threadID:        "thread-1",
		sessionID:       "thread-1",
		transport:       tr,
		activeTurn:      handle,
		eventDispatcher: dispatcher,
	}

	s.handleReceiveExit(tr, io.EOF)

	select {
	case <-handle.Done():
	case <-time.After(time.Second):
		t.Fatal("handle was not completed on EOF")
	}
	if !errors.Is(handle.Err(), io.EOF) {
		t.Fatalf("handle.Err() = %v, want EOF", handle.Err())
	}
	if s.activeTurn != nil {
		t.Fatal("activeTurn was not cleared")
	}

	select {
	case ev := <-got:
		if ev.Success {
			t.Fatal("TurnCompleted.Success = true, want false")
		}
		if ev.Error != io.EOF.Error() {
			t.Fatalf("TurnCompleted.Error = %q, want %q", ev.Error, io.EOF.Error())
		}
		if ev.TurnID != "turn-1" {
			t.Fatalf("TurnCompleted.TurnID = %q, want turn-1", ev.TurnID)
		}
	case <-time.After(time.Second):
		t.Fatal("TurnCompleted event was not published")
	}
}

func TestDriverResumeSessionDoesNotWaitForSystemInit(t *testing.T) {
	resumedUUID := "11111111-2222-3333-4444-555555555555"
	next := newScriptedTransport()
	defer next.finish()
	launchFn := overrideLaunchCLI(t, func(_, _, _, _ string, _ cliLaunchConfig, _ dto.MCPManifest, resumeID string) (*transport, func(), error) {
		if resumeID != resumedUUID {
			t.Fatalf("resumeID = %q, want %s", resumeID, resumedUUID)
		}
		return next.tr, nil, nil
	})

	d := &driver{
		mirror:     &recordingMirrorReconciler{},
		launchCLI:  launchFn,
		authStatus: loggedInClaudeAuthStatus,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resumed, err := d.ResumeSession(ctx, dto.ResumeSessionRequest{
		Provider:         "claude",
		AgentID:          "agent-1",
		ThreadID:         "thread-public",
		ProviderThreadID: resumedUUID,
		CWD:              t.TempDir(),
		PromptSnapshot:   validResumePromptSnapshotForTest(),
	})
	if err != nil {
		t.Fatalf("ResumeSession() error = %v", err)
	}
	assertResumedSessionIDs(t, resumed, resumedUUID, "thread-public")
}

func TestDriverResumeSessionPublishesPublicThreadID(t *testing.T) {
	bus := event.NewDispatcher()
	defer func() { _ = bus.Close() }()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher)

	got := make(chan agentdto.AgentLaunched, 4)
	cancel := event.Subscribe(bus, func(ev agentdto.AgentLaunched) {
		got <- ev
	})
	defer cancel()

	next := newBufferedTransport(t, "provider-thread-1")
	launchFn := overrideLaunchCLI(t, func(_, _, _, instructions string, cfg cliLaunchConfig, _ dto.MCPManifest, resumeID string) (*transport, func(), error) {
		assertPublicResumeLaunchConfig(t, instructions, cfg, resumeID)
		return next.tr, nil, nil
	})

	d := &driver{
		eventDispatcher: dispatcher,
		mirror:          &recordingMirrorReconciler{},
		launchCLI:       launchFn,
		authStatus:      loggedInClaudeAuthStatus,
	}
	resumed, err := d.ResumeSession(context.Background(), dto.ResumeSessionRequest{
		Provider:         "claude",
		AgentID:          "agent-1",
		ThreadID:         "thread-public",
		ProviderThreadID: "provider-thread-1",
		CWD:              t.TempDir(),
		PromptSnapshot: dto.PromptAssemblySnapshot{
			DisplayName:           "thread-public",
			BaseInstructions:      "stored base",
			DeveloperInstructions: "stored dev",
			Provider:              "claude",
			Version:               contract.PromptAssemblySnapshotVersion,
			Hash:                  "snapshot-hash",
		},
	})
	if err != nil {
		t.Fatalf("ResumeSession() error = %v", err)
	}
	assertResumedSessionIDs(t, resumed, "provider-thread-1", "thread-public")
	assertAgentLaunchedEvents(t, got, 2, "thread-public", "provider-thread-1")
}

func TestDriverResumeSessionAppliesRuntimeToolSafetyConfig(t *testing.T) {
	next := newBufferedTransport(t, "provider-thread-tools")
	var launchedConfig cliLaunchConfig
	launchFn := overrideLaunchCLI(t, func(_, _, _ string, _ string, cfg cliLaunchConfig, _ dto.MCPManifest, _ string) (*transport, func(), error) {
		launchedConfig = cfg
		return next.tr, nil, nil
	})

	d := &driver{
		mirror:     &recordingMirrorReconciler{},
		launchCLI:  launchFn,
		authStatus: loggedInClaudeAuthStatus,
	}
	_, err := d.ResumeSession(context.Background(), dto.ResumeSessionRequest{
		Provider:         "claude",
		AgentID:          "agent-1",
		ThreadID:         "thread-public",
		ProviderThreadID: "provider-thread-tools",
		CWD:              t.TempDir(),
		PromptSnapshot:   validResumePromptSnapshotForTest(),
		Config: map[string]any{
			"builtinTools":                []any{"Read", "Skill", "Task"},
			"disallowedTools":             []any{"Bash"},
			"additionalDisallowedTools":   []any{"WebFetch"},
			"disableProviderNativeSkills": true,
			"developerInstructions":       "runtime dev",
			"providerNativeSkillsIgnored": "keeps unrelated keys harmless",
		},
	})
	if err != nil {
		t.Fatalf("ResumeSession() error = %v", err)
	}
	if !launchedConfig.DisableProviderNativeSkills {
		t.Fatal("DisableProviderNativeSkills = false, want true")
	}
	args := mustBuildCLIArgs(t, "claude-sonnet", "system", "", launchedConfig)
	if values := flagValues(args, "--tools"); len(values) != 1 || values[0] != "Read,Task" {
		t.Fatalf("--tools = %#v from resumed config, want [Read,Task]", values)
	}
	if values := flagValues(args, "--disallowedTools"); len(values) != 0 {
		t.Fatalf("--disallowedTools = %#v, want --tools allowlist path", values)
	}
	if strings.Join(launchedConfig.DisallowedTools, ",") != "Bash" {
		t.Fatalf("DisallowedTools = %#v, want [Bash]", launchedConfig.DisallowedTools)
	}
	if strings.Join(launchedConfig.AdditionalDisallowedTools, ",") != "WebFetch" {
		t.Fatalf("AdditionalDisallowedTools = %#v, want [WebFetch]", launchedConfig.AdditionalDisallowedTools)
	}
	if launchedConfig.DeveloperInstructions != "runtime dev" {
		t.Fatalf("DeveloperInstructions = %q, want runtime dev", launchedConfig.DeveloperInstructions)
	}
}

func TestDriverResumeSessionRehydratesClaudeOverrideState(t *testing.T) {
	next := newBufferedTransport(t, "provider-thread-override")
	model := "claude-sonnet-4-20250514[1m]"
	effectiveEffort := "high"
	overrideEffort := "max"
	launchFn := overrideLaunchCLI(t, func(_, _, passedModel, _ string, cfg cliLaunchConfig, _ dto.MCPManifest, resumeID string) (*transport, func(), error) {
		assertClaudeOverrideLaunchConfig(t, passedModel, cfg, resumeID, model, effectiveEffort, "provider-thread-override")
		return next.tr, nil, nil
	})

	d := &driver{
		mirror:     &recordingMirrorReconciler{},
		launchCLI:  launchFn,
		authStatus: loggedInClaudeAuthStatus,
	}
	resumed, err := d.ResumeSession(context.Background(), dto.ResumeSessionRequest{
		Provider:         "claude",
		AgentID:          "agent-1",
		ThreadID:         "thread-public",
		ProviderThreadID: "provider-thread-override",
		CWD:              t.TempDir(),
		Model:            model,
		Effort:           effectiveEffort,
		PromptSnapshot:   validResumePromptSnapshotForTest(),
		ConfigOverride: dto.ThreadConfigPatch{
			Model:  &model,
			Effort: &overrideEffort,
		},
	})
	if err != nil {
		t.Fatalf("ResumeSession() error = %v", err)
	}
	s := requireResumedSession(t, resumed)
	assertClaudeOverrideState(t, s, model, overrideEffort)
	cfg := readClaudeSessionConfig(t, s)
	assertClaudeConfigState(t, cfg, model, overrideEffort, model, effectiveEffort)
}

func TestDriverResumeSessionPreservesExplicitClearOverrideState(t *testing.T) {
	next := newBufferedTransport(t, "provider-thread-clear")
	empty := ""
	effectiveModel := "sonnet"
	effectiveEffort := "high"
	launchFn := overrideLaunchCLI(t, func(_, _, passedModel, _ string, cfg cliLaunchConfig, _ dto.MCPManifest, resumeID string) (*transport, func(), error) {
		assertClaudeOverrideLaunchConfig(t, passedModel, cfg, resumeID, effectiveModel, effectiveEffort, "provider-thread-clear")
		return next.tr, nil, nil
	})

	d := &driver{
		mirror:     &recordingMirrorReconciler{},
		launchCLI:  launchFn,
		authStatus: loggedInClaudeAuthStatus,
	}
	resumed, err := d.ResumeSession(context.Background(), dto.ResumeSessionRequest{
		Provider:         "claude",
		AgentID:          "agent-1",
		ThreadID:         "thread-public",
		ProviderThreadID: "provider-thread-clear",
		CWD:              t.TempDir(),
		Model:            effectiveModel,
		Effort:           effectiveEffort,
		PromptSnapshot:   validResumePromptSnapshotForTest(),
		ConfigOverride: dto.ThreadConfigPatch{
			Model:  &empty,
			Effort: &empty,
		},
	})
	if err != nil {
		t.Fatalf("ResumeSession() error = %v", err)
	}
	s := requireResumedSession(t, resumed)
	assertClaudeOverrideState(t, s, "", "")
	cfg := readClaudeSessionConfig(t, s)
	assertClaudeConfigState(t, cfg, "", "", effectiveModel, effectiveEffort)
}

func requireResumedSession(t *testing.T, resumed contract.Session) *session {
	t.Helper()

	s, ok := resumed.(*session)
	if !ok {
		t.Fatalf("ResumeSession() type = %T, want *session", resumed)
	}
	return s
}

func assertResumedSessionIDs(t *testing.T, resumed contract.Session, providerThreadID, publicThreadID string) *session {
	t.Helper()

	s := requireResumedSession(t, resumed)
	if s.ThreadID() != providerThreadID {
		t.Fatalf("ThreadID() = %q, want %s", s.ThreadID(), providerThreadID)
	}
	if s.EventThreadID() != publicThreadID {
		t.Fatalf("EventThreadID() = %q, want %s", s.EventThreadID(), publicThreadID)
	}
	return s
}

func assertPublicResumeLaunchConfig(t *testing.T, instructions string, cfg cliLaunchConfig, resumeID string) {
	t.Helper()

	if resumeID != "provider-thread-1" {
		t.Fatalf("resumeID = %q, want provider-thread-1", resumeID)
	}
	if instructions != "stored base" {
		t.Fatalf("instructions = %q, want stored base", instructions)
	}
	if cfg.PromptSnapshot.BaseInstructions != "stored base" || cfg.PromptSnapshot.DeveloperInstructions != "stored dev" {
		t.Fatalf("PromptSnapshot = %#v, want stored snapshot", cfg.PromptSnapshot)
	}
}

func assertAgentLaunchedEvents(t *testing.T, got <-chan agentdto.AgentLaunched, count int, threadID, sessionID string) {
	t.Helper()

	for i := 0; i < count; i++ {
		select {
		case ev := <-got:
			assertAgentLaunchedEvent(t, ev, threadID, sessionID)
		case <-time.After(time.Second):
			t.Fatal("AgentLaunched event was not published")
		}
	}
}

func assertAgentLaunchedEvent(t *testing.T, ev agentdto.AgentLaunched, threadID, sessionID string) {
	t.Helper()

	if ev.ThreadID != threadID {
		t.Fatalf("AgentLaunched.ThreadID = %q, want %s", ev.ThreadID, threadID)
	}
	if ev.SessionID != sessionID {
		t.Fatalf("AgentLaunched.SessionID = %q, want %s", ev.SessionID, sessionID)
	}
}

func assertClaudeOverrideLaunchConfig(t *testing.T, passedModel string, cfg cliLaunchConfig, resumeID, wantModel, wantEffort, wantResumeID string) {
	t.Helper()

	if resumeID != wantResumeID {
		t.Fatalf("resumeID = %q, want %s", resumeID, wantResumeID)
	}
	if passedModel != wantModel {
		t.Fatalf("launch model = %q, want %q", passedModel, wantModel)
	}
	if cfg.Effort != wantEffort {
		t.Fatalf("launch effort = %q, want %q", cfg.Effort, wantEffort)
	}
}

func assertClaudeOverrideState(t *testing.T, s *session, model, effort string) {
	t.Helper()

	if !s.overrideModelSet || s.overrideModel != model {
		t.Fatalf("override model state = (%v, %q), want true/%q", s.overrideModelSet, s.overrideModel, model)
	}
	if !s.overrideEffortSet || s.overrideEffort != effort {
		t.Fatalf("override effort state = (%v, %q), want true/%q", s.overrideEffortSet, s.overrideEffort, effort)
	}
}

func readClaudeSessionConfig(t *testing.T, s *session) dto.ThreadConfig {
	t.Helper()

	cfg, err := s.ReadConfig(context.Background(), "")
	if err != nil {
		t.Fatalf("ReadConfig() error = %v", err)
	}
	return cfg
}

func assertClaudeConfigState(t *testing.T, cfg dto.ThreadConfig, overrideModel, overrideEffort, effectiveModel, effectiveEffort string) {
	t.Helper()

	if cfg.Override.Model != overrideModel || cfg.Override.Effort != overrideEffort {
		t.Fatalf("Override = %#v, want model=%q effort=%q", cfg.Override, overrideModel, overrideEffort)
	}
	if cfg.Effective.Model != effectiveModel || cfg.Effective.Effort != effectiveEffort {
		t.Fatalf("Effective = %#v, want model=%q effort=%q", cfg.Effective, effectiveModel, effectiveEffort)
	}
}
