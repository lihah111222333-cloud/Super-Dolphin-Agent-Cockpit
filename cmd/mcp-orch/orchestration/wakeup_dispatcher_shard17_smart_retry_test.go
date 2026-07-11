package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/stretchr/testify/require"
)

func TestDispatcherSmartRetryCapabilityEscalatesModelAndRetries(t *testing.T) {
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	store := newAgentFailureClassStore(t, "capability-escalate", 1, now)
	store.nodesReply[0].Config = testRawConfig(t, `{
		"exec":{
			"agent_key":"alpha",
			"cwd":"/tmp/node-cwd",
			"model":"sonnet",
			"on_failure":{
				"by_class":{"capability":"escalate_model"},
				"max_attempts":2,
				"escalation_chain":["sonnet","opus"]
			}
		},
		"first_turn":"go"
	}`)
	d := newAgentFailureClassDispatcher(t, store, errors.New("model lacks capability for this task"))

	if _, err := d.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("ProcessBatch err = %v", err)
	}
	require.Len(t, store.retryCalls, 1)
	require.Len(t, store.patchConfigCalls, 1)
	var cfg nodeexec.AgentNodeConfig
	require.NoError(t, json.Unmarshal(store.patchConfigCalls[0].Config, &cfg))
	require.Equal(t, "opus", cfg.Exec.Model)
	require.Equal(t, string(store.nodesReply[0].Config), string(store.patchConfigCalls[0].PreviousConfig))
	require.Contains(t, store.retryCalls[0].LastError, "strategy=escalate_model")
	require.Contains(t, store.retryCalls[0].LastError, "model=opus")
	require.Empty(t, store.failCalls)
	require.Empty(t, store.failNodeCalls)
}

func TestDispatcherSmartRetryValidationFailsFastWithoutPromptPatch(t *testing.T) {
	now := time.Date(2026, 5, 14, 10, 15, 0, 0, time.UTC)
	store := newAgentFailureClassStore(t, "validation-append", 1, now)
	store.nodesReply[0].Config = testRawConfig(t, `{
		"exec":{
			"agent_key":"alpha",
			"cwd":"/tmp/validation-cwd",
			"on_failure":{
				"by_class":{"validation":"append_error"},
				"max_attempts":2
			}
		},
		"inputs":{"from_nodes":["missing-upstream"]},
		"first_turn":"produce valid json"
	}`)
	d := newAgentFailureClassDispatcher(t, store, errors.New("launcher should not be called"))

	if _, err := d.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("ProcessBatch err = %v", err)
	}
	require.Empty(t, store.retryCalls)
	require.Empty(t, store.patchConfigCalls)
	require.Len(t, store.failCalls, 1)
	require.Len(t, store.failNodeCalls, 1)
	require.Contains(t, store.failCalls[0].LastError, "missing-upstream")
	require.True(t, store.failNodeCalls[0].FailFast)
}

func TestDispatcherSmartRetryLaunchCWDValidationFailsFastWithoutRetry(t *testing.T) {
	now := time.Date(2026, 5, 14, 10, 17, 0, 0, time.UTC)
	store := newAgentFailureClassStore(t, "launch-cwd-validation", 1, now)
	store.nodesReply[0].Config = testRawConfig(t, `{
		"exec":{
			"agent_key":"alpha",
			"on_failure":{
				"by_class":{"validation":"append_error"},
				"default":"replan",
				"max_attempts":2
			}
		},
		"first_turn":"produce valid json"
	}`)
	launcher := &stubAgentLauncher{err: errors.New("launcher should not be called")}
	agentExec := newTestAgentExecutor(launcher)
	router := NewNodeExecutorRouter(store, agentExec, nil, nil, nil, nil)
	plannerLauncher := &dispatcherStubLauncher{}
	d, err := NewWakeupDispatcher(store, plannerLauncher, nil, WakeupDispatcherConfig{})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher err = %v", err)
	}
	d.WithNodeRouter(router)

	if _, err := d.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("ProcessBatch err = %v", err)
	}
	require.Empty(t, launcher.calls)
	require.Empty(t, plannerLauncher.calls)
	require.Empty(t, store.retryCalls)
	require.Empty(t, store.patchConfigCalls)
	require.Len(t, store.failCalls, 1)
	require.Len(t, store.failNodeCalls, 1)
	require.Contains(t, store.failCalls[0].LastError, "launch cwd is required")
	require.True(t, store.failNodeCalls[0].FailFast)
}

func TestDispatcherSmartRetryReplanSpawnsPlannerAgent(t *testing.T) {
	now := time.Date(2026, 5, 14, 10, 30, 0, 0, time.UTC)
	store := newAgentFailureClassStore(t, "replan", 1, now)
	store.nodesReply[0].Config = testRawConfig(t, `{
		"exec":{
			"agent_key":"alpha",
			"cwd":"/tmp/replan-cwd",
			"on_failure":{
				"default":"replan",
				"max_attempts":2
			}
		},
		"first_turn":"go"
	}`)
	routerLauncher := &stubAgentLauncher{err: errors.New("model lacks capability for this graph")}
	agentExec := newTestAgentExecutor(routerLauncher)
	router := NewNodeExecutorRouter(store, agentExec, nil, nil, nil, nil)
	plannerLauncher := &dispatcherStubLauncher{}
	d, err := NewWakeupDispatcher(store, plannerLauncher, nil, WakeupDispatcherConfig{})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher err = %v", err)
	}
	d.WithNodeRouter(router)

	if _, err := d.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("ProcessBatch err = %v", err)
	}
	require.Len(t, plannerLauncher.calls, 1)
	call := plannerLauncher.calls[0]
	require.Equal(t, "dag_designer", call.AgentKey)
	require.Equal(t, testCWD(t, "replan-cwd"), call.Cwd)
	require.Contains(t, call.Prompt, "dag-f14-replan")
	require.Contains(t, call.Prompt, "agent-replan")
	require.Contains(t, call.Prompt, "task_dag_apply_ops")
	require.Len(t, store.markSentCalls, 1)
	require.Empty(t, store.retryCalls)
	require.Empty(t, store.failCalls)
	require.Empty(t, store.failNodeCalls)
}

func TestDispatcherSmartRetryReplanFenceMissIsNotHandled(t *testing.T) {
	now := time.Date(2026, 5, 14, 10, 32, 0, 0, time.UTC)
	store := newAgentFailureClassStore(t, "replan-fence-miss", 1, now)
	store.markSentRowsSet = true
	store.markSentRows = 0
	store.nodesReply[0].Config = testRawConfig(t, `{
		"exec":{
			"agent_key":"alpha",
			"cwd":"/tmp/replan-cwd",
			"on_failure":{"default":"replan","max_attempts":2}
		},
		"first_turn":"go"
	}`)
	routerLauncher := &stubAgentLauncher{err: errors.New("model lacks capability for this graph")}
	agentExec := newTestAgentExecutor(routerLauncher)
	router := NewNodeExecutorRouter(store, agentExec, nil, nil, nil, nil)
	plannerLauncher := &dispatcherStubLauncher{}
	d, err := NewWakeupDispatcher(store, plannerLauncher, nil, WakeupDispatcherConfig{})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher err = %v", err)
	}
	d.WithNodeRouter(router)

	n, err := d.ProcessBatch(context.Background())
	if err != nil {
		t.Fatalf("ProcessBatch err = %v", err)
	}
	require.Equal(t, 0, n)
	require.Len(t, plannerLauncher.calls, 1)
	require.Len(t, store.markSentCalls, 1)
}

func TestDispatcherSmartRetryFailFastFenceMissIsNotHandled(t *testing.T) {
	now := time.Date(2026, 5, 14, 10, 33, 0, 0, time.UTC)
	store := newAgentFailureClassStore(t, "failfast-fence-miss", 1, now)
	store.failRowsSet = true
	store.failRows = 0
	store.nodesReply[0].Config = testRawConfig(t, `{
		"exec":{
			"agent_key":"alpha",
			"cwd":"/tmp/replan-cwd",
			"on_failure":{"default":"fail_fast","max_attempts":2}
		},
		"first_turn":"go"
	}`)
	d := newAgentFailureClassDispatcher(t, store, errors.New("model lacks capability for this graph"))

	n, err := d.ProcessBatch(context.Background())
	if err != nil {
		t.Fatalf("ProcessBatch err = %v", err)
	}
	require.Equal(t, 0, n)
	require.Len(t, store.failCalls, 1)
	require.Empty(t, store.failNodeCalls)
}

func TestDispatcherSmartRetryReplanPlannerFailureFailsClosed(t *testing.T) {
	now := time.Date(2026, 5, 14, 10, 35, 0, 0, time.UTC)
	tests := []struct {
		name            string
		plannerLauncher WakeupLauncher
	}{
		{name: "unavailable", plannerLauncher: nil},
		{name: "launch_error", plannerLauncher: &dispatcherStubLauncher{errs: []error{errors.New("planner offline")}}},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			store := newAgentFailureClassStore(t, "replan-planner-"+tt.name, 1, now)
			store.nodesReply[0].Config = testRawConfig(t, `{
				"exec":{
					"agent_key":"alpha",
					"cwd":"/tmp/replan-cwd",
					"on_failure":{
						"default":"replan",
						"max_attempts":2
					}
				},
				"first_turn":"go"
			}`)
			d, err := NewWakeupDispatcher(store, tt.plannerLauncher, nil, WakeupDispatcherConfig{})
			if err != nil {
				t.Fatalf("NewWakeupDispatcher err = %v", err)
			}
			w := store.claimReply[0]

			d.handleFailedRouterOutcome(context.Background(), &w, extractFence(&w), nodeexec.NodeOutcome{
				Status:       nodeexec.NodeStatusFailed,
				FailureClass: nodeexec.FailureClassCapability,
				ErrorSummary: "model lacks capability",
			})

			if len(store.retryCalls) != 0 {
				t.Fatalf("retryCalls = %d, want 0 when replan planner cannot start", len(store.retryCalls))
			}
			if len(store.markSentCalls) != 0 {
				t.Fatalf("markSentCalls = %d, want 0 when replan planner cannot start", len(store.markSentCalls))
			}
			if len(store.failCalls) != 1 || len(store.failNodeCalls) != 1 {
				t.Fatalf("fail calls = wakeup %d node %d, want 1/1", len(store.failCalls), len(store.failNodeCalls))
			}
			if !strings.Contains(store.failCalls[0].LastError, "smart retry prepare failed") {
				t.Fatalf("FailWakeup LastError = %q, want smart retry prepare failure", store.failCalls[0].LastError)
			}
			if !store.failNodeCalls[0].FailFast {
				t.Fatalf("FailFast = false, want DAG policy fail_fast on replan planner failure")
			}
		})
	}
}

func TestDispatcherSmartRetryPermanentReplanPlannerFailureDoesNotRetryOriginalNode(t *testing.T) {
	now := time.Date(2026, 5, 14, 10, 40, 0, 0, time.UTC)
	store := newAgentFailureClassStore(t, "needs-human-replan-planner-fail", 1, now)
	store.nodesReply[0].Config = testRawConfig(t, `{
		"exec":{
			"agent_key":"alpha",
			"cwd":"/tmp/replan-cwd",
			"on_failure":{
				"by_class":{"needs_human":"replan"},
				"max_attempts":2
			}
		},
		"first_turn":"go"
	}`)
	plannerLauncher := &dispatcherStubLauncher{errs: []error{errors.New("planner offline")}}
	d, err := NewWakeupDispatcher(store, plannerLauncher, nil, WakeupDispatcherConfig{})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher err = %v", err)
	}
	w := store.claimReply[0]

	d.handleFailedRouterOutcome(context.Background(), &w, extractFence(&w), nodeexec.NodeOutcome{
		Status:       nodeexec.NodeStatusFailed,
		FailureClass: nodeexec.FailureClassNeedsHuman,
		ErrorSummary: "requires operator decision",
	})

	if len(plannerLauncher.calls) != 1 {
		t.Fatalf("planner calls = %d, want 1 explicit replan attempt", len(plannerLauncher.calls))
	}
	if plannerLauncher.calls[0].Cwd != testCWD(t, "replan-cwd") {
		t.Fatalf("planner Cwd = %q, want failed node cwd", plannerLauncher.calls[0].Cwd)
	}
	if len(store.retryCalls) != 0 {
		t.Fatalf("retryCalls = %d, want 0 for needs_human planner failure", len(store.retryCalls))
	}
	if len(store.failCalls) != 1 || len(store.failNodeCalls) != 1 {
		t.Fatalf("fail calls = wakeup %d node %d, want 1/1", len(store.failCalls), len(store.failNodeCalls))
	}
}

func TestDispatcherSmartRetryReplanMissingCWDDoesNotLaunchPlanner(t *testing.T) {
	now := time.Date(2026, 5, 14, 10, 43, 0, 0, time.UTC)
	store := newAgentFailureClassStore(t, "replan-missing-cwd", 1, now)
	store.nodesReply[0].Config = testRawConfig(t, `{
		"exec":{
			"agent_key":"alpha",
			"on_failure":{
				"default":"replan",
				"max_attempts":2
			}
		},
		"first_turn":"go"
	}`)
	plannerLauncher := &dispatcherStubLauncher{}
	d, err := NewWakeupDispatcher(store, plannerLauncher, nil, WakeupDispatcherConfig{})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher err = %v", err)
	}
	w := store.claimReply[0]

	d.handleFailedRouterOutcome(context.Background(), &w, extractFence(&w), nodeexec.NodeOutcome{
		Status:       nodeexec.NodeStatusFailed,
		FailureClass: nodeexec.FailureClassTransient,
		ErrorSummary: "connection refused",
	})

	require.Empty(t, plannerLauncher.calls)
	require.Empty(t, store.retryCalls)
	require.Empty(t, store.markSentCalls)
	require.Len(t, store.failCalls, 1)
	require.Len(t, store.failNodeCalls, 1)
	require.Contains(t, store.failCalls[0].LastError, "replan planner cwd unavailable")
	require.Contains(t, store.failCalls[0].LastError, "launch cwd is required")
}

func TestDispatcherSmartRetryPermanentFailureWithoutClassMappingDoesNotRetry(t *testing.T) {
	now := time.Date(2026, 5, 14, 10, 45, 0, 0, time.UTC)
	store := newAgentFailureClassStore(t, "hard-unmapped", 1, now)
	store.nodesReply[0].Config = testRawConfig(t, `{
		"exec":{
			"agent_key":"alpha",
			"cwd":"/tmp/node-cwd",
			"on_failure":{"max_attempts":2}
		},
		"first_turn":"go"
	}`)
	d, err := NewWakeupDispatcher(store, &dispatcherStubLauncher{}, nil, WakeupDispatcherConfig{})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher err = %v", err)
	}
	w := store.claimReply[0]

	d.handleFailedRouterOutcome(context.Background(), &w, extractFence(&w), nodeexec.NodeOutcome{
		Status:       nodeexec.NodeStatusFailed,
		FailureClass: nodeexec.FailureClassHard,
		ErrorSummary: "disabled command card",
	})

	if len(store.retryCalls) != 0 {
		t.Fatalf("retryCalls = %d, want 0 for unmapped hard failure", len(store.retryCalls))
	}
	if len(store.failCalls) != 1 || len(store.failNodeCalls) != 1 {
		t.Fatalf("fail calls = wakeup %d node %d, want 1/1", len(store.failCalls), len(store.failNodeCalls))
	}
	if !store.failNodeCalls[0].FailFast {
		t.Fatalf("FailFast = false, want DAG policy fail_fast for permanent hard failure")
	}
}

func TestDispatcherSmartRetryPermanentFailureDoesNotUseDefaultReplan(t *testing.T) {
	now := time.Date(2026, 5, 14, 10, 47, 0, 0, time.UTC)
	tests := []struct {
		name  string
		class nodeexec.FailureClass
	}{
		{name: "hard", class: nodeexec.FailureClassHard},
		{name: "needs_human", class: nodeexec.FailureClassNeedsHuman},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			store := newAgentFailureClassStore(t, "permanent-default-replan-"+tt.name, 1, now)
			store.nodesReply[0].Config = testRawConfig(t, `{
				"exec":{
					"agent_key":"alpha",
					"cwd":"/tmp/node-cwd",
					"on_failure":{"default":"replan","max_attempts":2}
				},
				"first_turn":"go"
			}`)
			plannerLauncher := &dispatcherStubLauncher{}
			d, err := NewWakeupDispatcher(store, plannerLauncher, nil, WakeupDispatcherConfig{})
			if err != nil {
				t.Fatalf("NewWakeupDispatcher err = %v", err)
			}
			w := store.claimReply[0]

			d.handleFailedRouterOutcome(context.Background(), &w, extractFence(&w), nodeexec.NodeOutcome{
				Status:       nodeexec.NodeStatusFailed,
				FailureClass: tt.class,
				ErrorSummary: "requires operator decision",
			})

			if len(plannerLauncher.calls) != 0 {
				t.Fatalf("planner calls = %d, want 0 for unmapped permanent failure", len(plannerLauncher.calls))
			}
			if len(store.retryCalls) != 0 || len(store.markSentCalls) != 0 {
				t.Fatalf("unexpected retry/mark-sent calls: retry=%d markSent=%d", len(store.retryCalls), len(store.markSentCalls))
			}
			if len(store.failCalls) != 1 || len(store.failNodeCalls) != 1 {
				t.Fatalf("fail calls = wakeup %d node %d, want 1/1", len(store.failCalls), len(store.failNodeCalls))
			}
		})
	}
}

func TestDispatcherSmartRetryReplanRespectsMaxAttempts(t *testing.T) {
	now := time.Date(2026, 5, 14, 10, 50, 0, 0, time.UTC)
	store := newAgentFailureClassStore(t, "replan-max", 1, now)
	store.nodesReply[0].Config = testRawConfig(t, `{
		"exec":{
			"agent_key":"alpha",
			"cwd":"/tmp/replan-cwd",
			"on_failure":{"default":"replan","max_attempts":1}
		},
		"first_turn":"go"
	}`)
	plannerLauncher := &dispatcherStubLauncher{}
	d, err := NewWakeupDispatcher(store, plannerLauncher, nil, WakeupDispatcherConfig{})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher err = %v", err)
	}
	w := store.claimReply[0]

	d.handleFailedRouterOutcome(context.Background(), &w, extractFence(&w), nodeexec.NodeOutcome{
		Status:       nodeexec.NodeStatusFailed,
		FailureClass: nodeexec.FailureClassTransient,
		ErrorSummary: "connection refused",
	})

	if len(plannerLauncher.calls) != 0 {
		t.Fatalf("planner calls = %d, want 0 when max_attempts reached", len(plannerLauncher.calls))
	}
	if len(store.retryCalls) != 0 {
		t.Fatalf("retryCalls = %d, want 0 when max_attempts reached", len(store.retryCalls))
	}
	if len(store.failCalls) != 1 || len(store.failNodeCalls) != 1 {
		t.Fatalf("fail calls = wakeup %d node %d, want 1/1", len(store.failCalls), len(store.failNodeCalls))
	}
	if !store.failNodeCalls[0].FailFast {
		t.Fatalf("FailFast = false, want DAG policy fail_fast on smart retry exhaustion")
	}
}

func TestDispatcherSmartRetryUnsupportedStrategiesFailClosed(t *testing.T) {
	now := time.Date(2026, 5, 14, 10, 55, 0, 0, time.UTC)
	tests := []struct {
		name     string
		strategy nodeexec.OnFailureStrategy
	}{
		{name: "skip", strategy: nodeexec.OnFailureSkip},
		{name: "ask_human", strategy: nodeexec.OnFailureAskHuman},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			store := newAgentFailureClassStore(t, tt.name, 1, now)
			store.nodesReply[0].Config = testRawConfig(t, `{
				"exec":{
					"agent_key":"alpha",
					"cwd":"/tmp/node-cwd",
					"on_failure":{"default":"`+string(tt.strategy)+`","max_attempts":2}
				},
				"first_turn":"go"
			}`)
			d, err := NewWakeupDispatcher(store, &dispatcherStubLauncher{}, nil, WakeupDispatcherConfig{})
			if err != nil {
				t.Fatalf("NewWakeupDispatcher err = %v", err)
			}
			w := store.claimReply[0]

			d.handleFailedRouterOutcome(context.Background(), &w, extractFence(&w), nodeexec.NodeOutcome{
				Status:       nodeexec.NodeStatusFailed,
				FailureClass: nodeexec.FailureClassTransient,
				ErrorSummary: "connection refused",
			})

			if len(store.retryCalls) != 0 {
				t.Fatalf("retryCalls = %d, want 0 for unsupported strategy %s", len(store.retryCalls), tt.strategy)
			}
			if len(store.markSentCalls) != 0 {
				t.Fatalf("markSentCalls = %d, want 0 for unsupported strategy %s", len(store.markSentCalls), tt.strategy)
			}
			if len(store.failCalls) != 1 || len(store.failNodeCalls) != 1 {
				t.Fatalf("fail calls = wakeup %d node %d, want 1/1", len(store.failCalls), len(store.failNodeCalls))
			}
			if !strings.Contains(store.failCalls[0].LastError, "unsupported smart retry strategy") {
				t.Fatalf("FailWakeup LastError = %q, want unsupported strategy reason", store.failCalls[0].LastError)
			}
			if !store.failNodeCalls[0].FailFast {
				t.Fatalf("FailFast = false, want DAG policy fail_fast for unsupported strategy")
			}
		})
	}
}
