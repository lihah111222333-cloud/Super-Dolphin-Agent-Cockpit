package orchestration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sharedfile"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/kelindar/event"
	"go.uber.org/fx"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	sharedto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
)

func TestHookConsumerAfter_StateChangeMirrorsAgentState(t *testing.T) {
	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	agent := addHookTestAgent(t, svc, "agent-1")
	agent.state = agentdto.StateTurnStarting
	agent.threadID = "thread-1"
	agent.remoteThreadID = "thread-1"
	agent.activeTurnID = "turn-1"

	consumer := newHookConsumer(svc, silentLogger())
	stateChanged := agentdto.StateChanged{
		AgentSessionHeader: sharedto.AgentSessionHeader{
			AgentHeader: sharedto.AgentHeader{
				ThreadHeader: sharedto.ThreadHeader{
					EventHeader: sharedto.EventHeader{Timestamp: time.Unix(10, 0).UTC()},
					ThreadID:    "thread-1",
				},
				AgentID: "agent-1",
			},
		},
		NewState: string(agentdto.StateTurnRunning),
	}
	if _, err := consumer.After(context.Background(), hookPayload(t, hookTopicStateChange, hookRelayKindStateChanged, stateChanged)); err != nil {
		t.Fatalf("After(state running) error = %v", err)
	}

	snapshot, err := svc.Snapshot(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.State != string(agentdto.StateTurnRunning) {
		t.Fatalf("snapshot.State = %q, want %q", snapshot.State, agentdto.StateTurnRunning)
	}
	if snapshot.ActiveTurnID != "turn-1" {
		t.Fatalf("snapshot.ActiveTurnID = %q, want turn-1", snapshot.ActiveTurnID)
	}

	stateChanged.NewState = string(agentdto.StateIdle)
	stateChanged.Timestamp = time.Unix(11, 0).UTC()
	if _, err := consumer.After(context.Background(), hookPayload(t, hookTopicStateChange, hookRelayKindStateChanged, stateChanged)); err != nil {
		t.Fatalf("After(state idle) error = %v", err)
	}

	snapshot, err = svc.Snapshot(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("Snapshot() after idle error = %v", err)
	}
	if snapshot.State != string(agentdto.StateIdle) {
		t.Fatalf("snapshot.State after idle = %q, want %q", snapshot.State, agentdto.StateIdle)
	}
	if snapshot.ActiveTurnID != "" {
		t.Fatalf("snapshot.ActiveTurnID after idle = %q, want empty", snapshot.ActiveTurnID)
	}
}

// TestHookConsumerAfter_DefersIdleDuringProvisioning verifies the
// single-writer guard: when the launcher subprocess reports idle while
// the main flow is still mid-launch (state=provisioning), the hook
// consumer must defer to commitLaunchSuccessLocked and leave the agent's
// state untouched. Without this guard the state machine would fire
// launch_succeeded twice (once via hookSyncFireLocked, once via the main
// flow), producing an illegal-state-transition error and aborting launch.
func TestHookConsumerAfter_DefersIdleDuringProvisioning(t *testing.T) {
	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	agent := addHookTestAgent(t, svc, "agent-1")
	agent.state = agentdto.StateProvisioning
	agent.threadID = "thread-launch-rpc"
	agent.remoteThreadID = "thread-launch-rpc"

	consumer := newHookConsumer(svc, silentLogger())
	stateChanged := agentdto.StateChanged{
		AgentSessionHeader: sharedto.AgentSessionHeader{
			AgentHeader: sharedto.AgentHeader{
				ThreadHeader: sharedto.ThreadHeader{
					EventHeader: sharedto.EventHeader{Timestamp: time.Unix(10, 0).UTC()},
					ThreadID:    "thread-from-hook",
				},
				AgentID: "agent-1",
			},
		},
		NewState: string(agentdto.StateIdle),
	}
	if _, err := consumer.After(context.Background(), hookPayload(t, hookTopicStateChange, hookRelayKindStateChanged, stateChanged)); err != nil {
		t.Fatalf("After(provisioning idle hook) error = %v", err)
	}

	snapshot, err := svc.Snapshot(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.State != string(agentdto.StateProvisioning) {
		t.Fatalf("snapshot.State = %q, want %q (hook idle must be deferred during launch)",
			snapshot.State, agentdto.StateProvisioning)
	}
	if snapshot.ThreadID != "thread-launch-rpc" {
		t.Fatalf("snapshot.ThreadID = %q, want %q (main flow owns thread_id during launch)",
			snapshot.ThreadID, "thread-launch-rpc")
	}
}

// TestHookConsumerAfter_DefersIdleDuringRecovering covers the symmetric
// recover path: state machine has Recovering->launch_succeeded->Idle, so
// the same race exists when commitLaunchSuccessLocked is fired during a
// recover cycle. The guard must apply here too.
func TestHookConsumerAfter_DefersIdleDuringRecovering(t *testing.T) {
	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	agent := addHookTestAgent(t, svc, "agent-1")
	agent.state = agentdto.StateRecovering

	consumer := newHookConsumer(svc, silentLogger())
	stateChanged := agentdto.StateChanged{
		AgentSessionHeader: sharedto.AgentSessionHeader{
			AgentHeader: sharedto.AgentHeader{
				ThreadHeader: sharedto.ThreadHeader{
					EventHeader: sharedto.EventHeader{Timestamp: time.Unix(10, 0).UTC()},
					ThreadID:    "thread-1",
				},
				AgentID: "agent-1",
			},
		},
		NewState: string(agentdto.StateIdle),
	}
	if _, err := consumer.After(context.Background(), hookPayload(t, hookTopicStateChange, hookRelayKindStateChanged, stateChanged)); err != nil {
		t.Fatalf("After(recovering idle hook) error = %v", err)
	}

	snapshot, err := svc.Snapshot(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.State != string(agentdto.StateRecovering) {
		t.Fatalf("snapshot.State = %q, want %q (hook idle must be deferred during recover)",
			snapshot.State, agentdto.StateRecovering)
	}
}

// TestHookConsumerAfter_FailedDuringProvisioning makes sure the guard is
// scoped narrowly to NewState=idle. A failed/stopped hook during launch
// represents a real subprocess crash and must still drive the state
// machine so the main flow can observe the failure.
func TestHookConsumerAfter_FailedDuringProvisioning(t *testing.T) {
	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	agent := addHookTestAgent(t, svc, "agent-1")
	agent.state = agentdto.StateProvisioning

	consumer := newHookConsumer(svc, silentLogger())
	stateChanged := agentdto.StateChanged{
		AgentSessionHeader: sharedto.AgentSessionHeader{
			AgentHeader: sharedto.AgentHeader{
				ThreadHeader: sharedto.ThreadHeader{
					EventHeader: sharedto.EventHeader{Timestamp: time.Unix(10, 0).UTC()},
					ThreadID:    "thread-1",
				},
				AgentID: "agent-1",
			},
		},
		NewState: string(agentdto.StateFailed),
	}
	if _, err := consumer.After(context.Background(), hookPayload(t, hookTopicStateChange, hookRelayKindStateChanged, stateChanged)); err != nil {
		t.Fatalf("After(provisioning failed hook) error = %v", err)
	}

	snapshot, err := svc.Snapshot(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.State != string(agentdto.StateFailed) {
		t.Fatalf("snapshot.State = %q, want %q (failed hook must still drive sm during launch)",
			snapshot.State, agentdto.StateFailed)
	}
}

func TestHookConsumerAfter_ProcessExitMarksStopped(t *testing.T) {
	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	agent := addHookTestAgent(t, svc, "agent-1")
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-1"
	agent.remoteThreadID = "thread-1"
	agent.activeTurnID = "turn-1"

	consumer := newHookConsumer(svc, silentLogger())
	stopped := threaddto.Stopped{
		EventHeader: sharedto.EventHeader{Timestamp: time.Unix(20, 0).UTC()},
		ThreadID:    "thread-1",
		AgentID:     "agent-1",
		Reason:      "stopped",
	}
	if _, err := consumer.After(context.Background(), hookPayload(t, hookTopicProcessExit, hookRelayKindThreadStopped, stopped)); err != nil {
		t.Fatalf("After(process exit) error = %v", err)
	}

	snapshot, err := svc.Snapshot(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.State != string(agentdto.StateStopped) {
		t.Fatalf("snapshot.State = %q, want %q", snapshot.State, agentdto.StateStopped)
	}
	if snapshot.ActiveTurnID != "" {
		t.Fatalf("snapshot.ActiveTurnID = %q, want empty", snapshot.ActiveTurnID)
	}
}

func TestHookConsumerAfter_TurnCompletedMarksIdleAndPersistsReport(t *testing.T) {
	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	agent := addHookTestAgent(t, svc, "agent-1")
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-1"
	agent.remoteThreadID = "thread-1"
	agent.activeTurnID = "turn-1"

	consumer := newHookConsumer(svc, silentLogger())
	completed := turndto.TurnCompleted{
		TurnHeader: sharedto.TurnHeader{
			AgentHeader: sharedto.AgentHeader{
				ThreadHeader: sharedto.ThreadHeader{
					EventHeader: sharedto.EventHeader{Timestamp: time.Unix(21, 0).UTC()},
					ThreadID:    "thread-1",
				},
				AgentID: "agent-1",
			},
			TurnIDHeader: sharedto.TurnIDHeader{TurnID: "turn-1"},
		},
		Success: true,
		Message: "ORCH_OK",
	}
	if _, err := consumer.After(context.Background(), hookPayload(t, hookTopicTurnAfter, hookRelayKindTurnCompleted, completed)); err != nil {
		t.Fatalf("After(turn completed) error = %v", err)
	}

	snapshot, err := svc.Snapshot(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.State != string(agentdto.StateIdle) {
		t.Fatalf("snapshot.State = %q, want %q", snapshot.State, agentdto.StateIdle)
	}
	if snapshot.ActiveTurnID != "" {
		t.Fatalf("snapshot.ActiveTurnID = %q, want empty", snapshot.ActiveTurnID)
	}
	if snapshot.LastReport != "ORCH_OK" {
		t.Fatalf("snapshot.LastReport = %q, want ORCH_OK", snapshot.LastReport)
	}
}

func TestHookConsumerAfter_TurnCompletedAdvancesDAGNodeFromHook(t *testing.T) {
	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	agent := addHookTestAgent(t, svc, "agent-1")
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-1"
	agent.remoteThreadID = "thread-1"
	agent.activeTurnID = "turn-1"

	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{
		DagKey:   "dag-1",
		NodeKey:  "node-1",
		NodeType: "agent",
		Status:   "running",
		Config:   testRawConfig(t, `{"exec":{"agent_key":"source_monitor","cwd":"/tmp/node-cwd"},"outputs":{"to_node_result":true}}`),
	}}}
	flow := &dagSubscriberFlowSpy{}
	events := []string{}
	agentExec := newTestAgentExecutor(&stubAgentLauncher{}, nodeexec.WithHooks(recordingLifecycleHooks(&events)))
	consumer := newHookConsumerInternal(
		svc,
		silentLogger(),
		nil,
		lookup,
		flow,
		withHookTurnCompletedDAGDeps(DAGSubscriberDeps{
			LookupStore: lookup,
			FlowStore:   flow,
			NodeRouter:  NewNodeExecutorRouter(&stubRouterStore{}, agentExec, nil, nil, nil, nil),
		}),
	)
	completed := turndto.TurnCompleted{
		TurnHeader: sharedto.TurnHeader{
			AgentHeader: sharedto.AgentHeader{
				ThreadHeader: sharedto.ThreadHeader{
					EventHeader: sharedto.EventHeader{Timestamp: time.Unix(21, 0).UTC()},
					ThreadID:    "thread-1",
				},
				AgentID: "agent-1",
			},
			TurnIDHeader: sharedto.TurnIDHeader{TurnID: "turn-1"},
		},
		Success: true,
		Result:  `{"summary":"ok"}`,
	}
	if _, err := consumer.After(context.Background(), hookPayload(t, hookTopicTurnAfter, hookRelayKindTurnCompleted, completed)); err != nil {
		t.Fatalf("After(turn completed) error = %v", err)
	}

	if lookup.lastThread != "thread-1" {
		t.Fatalf("lookup thread = %q, want thread-1", lookup.lastThread)
	}
	if len(flow.completeCalls) != 1 {
		t.Fatalf("CompleteNode calls = %d, want 1", len(flow.completeCalls))
	}
	got := flow.completeCalls[0]
	if got.DagKey != "dag-1" || got.NodeKey != "node-1" || got.Status != "done" {
		t.Fatalf("CompleteNode input = %+v, want dag-1/node-1 done", got)
	}
	if string(got.Result) != `{"summary":"ok"}` {
		t.Fatalf("CompleteNode result = %s, want turn result", got.Result)
	}
	if want := []string{"on_state_change:node-1:done"}; strings.Join(events, "|") != strings.Join(want, "|") {
		t.Fatalf("lifecycle events = %v, want %v", events, want)
	}
}

func TestHookConsumerAfter_ArtifactTargetUsesFxInjectedImporter(t *testing.T) {
	sourcePath := "/tmp/video-with-audio/final.mp4"
	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	agent := addHookTestAgent(t, svc, "agent-1")
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-artifact"
	agent.remoteThreadID = "thread-artifact"
	agent.activeTurnID = "turn-1"

	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{
		DagKey:   "dag-video",
		NodeKey:  "generate_video_mp4",
		NodeType: "agent",
		Status:   "running",
		RunID:    dagSubscriberTestRunID(42),
		Config: testRawConfig(t, `{
			"exec":{"agent_key":"video","cwd":"/tmp/node-cwd"},
			"outputs":{
				"to_node_result":false,
				"to_artifact":{
					"source_tool":"video_with_audio",
					"source_path_field":"output_path",
					"path_template":"dag/douyin/daily-video/{{run_id}}/final.mp4",
					"content_type":"video/mp4",
					"allowed_extensions":[".mp4"],
					"allowed_source_roots":["/tmp/video-with-audio"],
					"max_bytes":524288000,
					"overwrite":"fail"
				}
			}
		}`),
	}}}
	flow, importer := &dagSubscriberFlowSpy{}, &dagSubscriberArtifactImporterSpy{}
	var handler contract.BootstrapHookAfterHandler
	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			fx.Annotate(
				func() *service { return svc },
				fx.As(fx.Self()),
				fx.As(new(HookConsumerRuntime)),
				fx.As(new(HookReportPort)),
			),
			func() taskdag.NodeSpawningThreadLookup { return lookup },
			func() taskdag.NodeFlowStore { return flow },
			func() sharedfile.Importer { return importer },
			func() HookSuppressionLookup { return svc.registry },
			ProvideHookAfterHandler,
		),
		fx.Populate(&handler),
	)
	if err := app.Err(); err != nil {
		t.Fatalf("fx.New() error = %v", err)
	}
	if handler == nil {
		t.Fatal("BootstrapHookAfterHandler was not populated")
	}

	completed := turndto.TurnCompleted{
		TurnHeader: sharedto.TurnHeader{
			AgentHeader: sharedto.AgentHeader{
				ThreadHeader: sharedto.ThreadHeader{
					EventHeader: sharedto.EventHeader{Timestamp: time.Unix(21, 0).UTC()},
					ThreadID:    "thread-artifact",
				},
				AgentID: "agent-1",
			},
			TurnIDHeader: sharedto.TurnIDHeader{TurnID: "turn-1"},
		},
		Success: true,
		Result:  `{"success":true,"output_path":"` + sourcePath + `"}`,
	}
	if _, err := handler(context.Background(), hookPayload(t, hookTopicTurnAfter, hookRelayKindTurnCompleted, completed)); err != nil {
		t.Fatalf("After(turn completed) error = %v", err)
	}

	wantTarget := "dag/douyin/daily-video/42/final.mp4"
	assertArtifactImportCall(t, importer, sourcePath, wantTarget)
	assertArtifactCompletion(t, flow, &dagSubscriberSharedFileWriterSpy{}, wantTarget)
	if len(flow.failCalls) != 0 {
		t.Fatalf("failCalls = %d, want 0; first reason=%q", len(flow.failCalls), flow.failCalls[0].Reason)
	}
}

func TestHookConsumerAfter_TurnInterruptedAdvancesDAGNodeFromHook(t *testing.T) {
	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	agent := addHookTestAgent(t, svc, "agent-1")
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-1"
	agent.remoteThreadID = "thread-1"
	agent.activeTurnID = "turn-1"

	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{
		DagKey:   "dag-1",
		NodeKey:  "node-1",
		NodeType: "agent",
		Status:   "running",
		Config:   testRawConfig(t, `{"exec":{"agent_key":"source_monitor","cwd":"/tmp/node-cwd"},"outputs":{"to_node_result":true}}`),
	}}}
	flow := &dagSubscriberFlowSpy{}
	events := []string{}
	agentExec := newTestAgentExecutor(&stubAgentLauncher{}, nodeexec.WithHooks(recordingLifecycleHooks(&events)))
	consumer := newHookConsumerInternal(
		svc,
		silentLogger(),
		nil,
		lookup,
		flow,
		withHookTurnCompletedDAGDeps(DAGSubscriberDeps{
			LookupStore: lookup,
			FlowStore:   flow,
			NodeRouter:  NewNodeExecutorRouter(&stubRouterStore{}, agentExec, nil, nil, nil, nil),
		}),
	)
	interrupted := turndto.TurnInterrupted{
		TurnHeader: sharedto.TurnHeader{
			AgentHeader: sharedto.AgentHeader{
				ThreadHeader: sharedto.ThreadHeader{
					EventHeader: sharedto.EventHeader{Timestamp: time.Unix(21, 0).UTC()},
					ThreadID:    "thread-1",
				},
				AgentID: "agent-1",
			},
			TurnIDHeader: sharedto.TurnIDHeader{TurnID: "turn-1"},
		},
		Reason: "provider disconnected",
	}
	if _, err := consumer.After(context.Background(), hookPayload(t, hookTopicTurnFailed, hookRelayKindTurnInterrupted, interrupted)); err != nil {
		t.Fatalf("After(turn interrupted) error = %v", err)
	}

	if lookup.lastThread != "thread-1" {
		t.Fatalf("lookup thread = %q, want thread-1", lookup.lastThread)
	}
	if len(flow.failCalls) != 1 {
		t.Fatalf("FailNode calls = %d, want 1", len(flow.failCalls))
	}
	got := flow.failCalls[0]
	if got.DagKey != "dag-1" || got.NodeKey != "node-1" || got.Reason != "provider disconnected" {
		t.Fatalf("FailNode input = %+v, want dag-1/node-1 provider disconnected", got)
	}
	if len(flow.completeCalls) != 0 {
		t.Fatalf("CompleteNode calls = %d, want 0", len(flow.completeCalls))
	}
	want := []string{
		"on_state_change:node-1:failed",
		"on_failure:node-1:failed",
	}
	if strings.Join(events, "|") != strings.Join(want, "|") {
		t.Fatalf("lifecycle events = %v, want %v", events, want)
	}
}

func TestHookConsumerAfter_FinalAnswerItemPersistsReport(t *testing.T) {
	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	agent := addHookTestAgent(t, svc, "agent-1")
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-1"
	agent.remoteThreadID = "thread-1"
	agent.activeTurnID = "turn-1"

	consumer := newHookConsumer(svc, silentLogger())
	payload := testRawConfig(t, `{"item":{"type":"agentMessage","phase":"final_answer","text":"ORCH_OK"}}`)
	item := turndto.ItemCompleted{
		TurnHeader: sharedto.TurnHeader{
			AgentHeader: sharedto.AgentHeader{
				ThreadHeader: sharedto.ThreadHeader{
					EventHeader: sharedto.EventHeader{Timestamp: time.Unix(22, 0).UTC()},
					ThreadID:    "thread-1",
				},
				AgentID: "agent-1",
			},
			TurnIDHeader: sharedto.TurnIDHeader{TurnID: "turn-1"},
		},
		RawType:  "item/completed",
		ItemType: "agentMessage",
		Success:  true,
		Payload:  payload,
	}
	if _, err := consumer.After(context.Background(), hookPayload(t, hookTopicTurnProgress, hookRelayKindTurnItemCompleted, item)); err != nil {
		t.Fatalf("After(turn item completed) error = %v", err)
	}

	report, err := svc.GetReport(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("GetReport() error = %v", err)
	}
	if report.Report != "ORCH_OK" {
		t.Fatalf("report.Report = %q, want ORCH_OK", report.Report)
	}
	if report.State != string(agentdto.StateTurnRunning) {
		t.Fatalf("report.State = %q, want %q", report.State, agentdto.StateTurnRunning)
	}
}

func TestHookConsumerAfter_UnknownAgentIsIgnored(t *testing.T) {
	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	consumer := newHookConsumer(svc, silentLogger())

	stateChanged := agentdto.StateChanged{
		AgentSessionHeader: sharedto.AgentSessionHeader{
			AgentHeader: sharedto.AgentHeader{
				ThreadHeader: sharedto.ThreadHeader{
					EventHeader: sharedto.EventHeader{Timestamp: time.Unix(30, 0).UTC()},
					ThreadID:    "thread-missing",
				},
				AgentID: "missing-agent",
			},
		},
		NewState: string(agentdto.StateIdle),
	}
	if _, err := consumer.After(context.Background(), hookPayload(t, hookTopicStateChange, hookRelayKindStateChanged, stateChanged)); err != nil {
		t.Fatalf("After(unknown agent) error = %v", err)
	}
}

func addHookTestAgent(t *testing.T, svc *service, agentID string) *agentRuntime {
	t.Helper()

	svc.registry.mu.Lock()
	defer svc.registry.mu.Unlock()

	agent := svc.newAgentLocked(agentID)
	svc.registry.agents[agentID] = agent
	return agent
}

func hookPayload(t *testing.T, topic, kind string, event any) mcp.HookPayload {
	t.Helper()

	eventRaw, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("json.Marshal(event) error = %v", err)
	}
	contextRaw, err := json.Marshal(hookContextEnvelope{
		Kind:  kind,
		Event: eventRaw,
	})
	if err != nil {
		t.Fatalf("json.Marshal(context) error = %v", err)
	}
	return mcp.HookPayload{
		AgentID: "agent-1",
		Topic:   topic,
		Context: contextRaw,
	}
}

// TestHookConsumerTurnFailedReportsErrorToParent locks the report回传
// path for a failed child-agent turn: the turn carries its detail in
// TurnCompleted.Error (result/summary/message are empty), so without
// the error fallback the parent agent's get_agent_report sees an empty
// report / "not found" and the failure is silently lost.
func TestHookConsumerTurnFailedReportsErrorToParent(t *testing.T) {
	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	agent := addHookTestAgent(t, svc, "agent-1")
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-1"
	agent.remoteThreadID = "thread-1"
	agent.activeTurnID = "turn-1"

	consumer := newHookConsumer(svc, silentLogger())
	consumer.handleTurnCompleted(context.Background(), turndto.TurnCompleted{
		TurnHeader: sharedto.TurnHeader{
			AgentHeader:  sharedto.AgentHeader{ThreadHeader: sharedto.ThreadHeader{ThreadID: "thread-1"}, AgentID: "agent-1"},
			TurnIDHeader: sharedto.TurnIDHeader{TurnID: "turn-1"},
		},
		Success: false,
		Error:   "child agent denied tool permission",
	})

	report, err := svc.GetReport(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("GetReport() error = %v", err)
	}
	if !strings.Contains(report.Report, "child agent denied tool permission") {
		t.Fatalf("report.Report = %q, want it to carry the failed turn's error detail", report.Report)
	}
}
