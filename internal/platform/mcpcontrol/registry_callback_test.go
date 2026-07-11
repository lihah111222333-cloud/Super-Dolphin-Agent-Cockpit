package mcpcontrol

import (
	"context"
	"fmt"
	"slices"
	"testing"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
)

func TestCallbackBefore(t *testing.T) {
	t.Parallel()

	var captured dto.HookPayload
	registry := NewRegistry()
	lease := dto.LeaseKey{InstanceID: "instance-before", Generation: 1}
	registry.instances[lease] = &ToolInstance{
		Lease:  lease,
		Peer:   &stubCallbackPeer{},
		Status: dto.StatusActive,
	}
	registry.instances[lease].Peer = &stubCallbackPeer{
		callbackFn: func(_ context.Context, method string, params any, result any) error {
			if method != dto.MethodHookBefore {
				return fmt.Errorf("method = %q, want %q", method, dto.MethodHookBefore)
			}
			payload, ok := params.(dto.HookPayload)
			if !ok {
				return fmt.Errorf("params type = %T, want dto.HookPayload", params)
			}
			decision, ok := result.(*dto.BeforeDecision)
			if !ok {
				return fmt.Errorf("result type = %T, want *dto.BeforeDecision", result)
			}
			captured = payload
			*decision = dto.BeforeDecision{Decision: dto.HookDecisionAllow, AllowedTools: []string{"shell"}}
			return nil
		},
	}

	decision, err := registry.CallbackBefore(context.Background(), lease, dto.HookPayload{
		HookCallID: "call-before",
		AgentID:    "agent-before",
	})
	if err != nil {
		t.Fatalf("CallbackBefore() error = %v", err)
	}
	if decision.Decision != dto.HookDecisionAllow {
		t.Fatalf("CallbackBefore() decision = %q, want %q", decision.Decision, dto.HookDecisionAllow)
	}
	if !slices.Equal(decision.AllowedTools, []string{"shell"}) {
		t.Fatalf("CallbackBefore() allowed tools = %#v, want %#v", decision.AllowedTools, []string{"shell"})
	}
	if captured.HookCallID != "call-before" {
		t.Fatalf("captured HookCallID = %q, want %q", captured.HookCallID, "call-before")
	}
}

func TestCallbackCheck(t *testing.T) {
	t.Parallel()

	var captured dto.HookPayload
	registry := NewRegistry()
	lease := dto.LeaseKey{InstanceID: "instance-check", Generation: 1}
	registry.instances[lease] = &ToolInstance{
		Lease:  lease,
		Peer:   &stubCallbackPeer{},
		Status: dto.StatusActive,
	}
	registry.instances[lease].Peer = &stubCallbackPeer{
		callbackFn: func(_ context.Context, method string, params any, result any) error {
			if method != dto.MethodHookCheck {
				return fmt.Errorf("method = %q, want %q", method, dto.MethodHookCheck)
			}
			payload, ok := params.(dto.HookPayload)
			if !ok {
				return fmt.Errorf("params type = %T, want dto.HookPayload", params)
			}
			decision, ok := result.(*dto.CheckDecision)
			if !ok {
				return fmt.Errorf("result type = %T, want *dto.CheckDecision", result)
			}
			captured = payload
			*decision = dto.CheckDecision{Decision: dto.HookDecisionWarn, Severity: "high", Reason: "policy"}
			return nil
		},
	}

	decision, err := registry.CallbackCheck(context.Background(), lease, dto.HookPayload{
		HookCallID: "call-check",
		ThreadID:   "thread-check",
	})
	if err != nil {
		t.Fatalf("CallbackCheck() error = %v", err)
	}
	if decision.Decision != dto.HookDecisionWarn {
		t.Fatalf("CallbackCheck() decision = %q, want %q", decision.Decision, dto.HookDecisionWarn)
	}
	if decision.Severity != "high" {
		t.Fatalf("CallbackCheck() severity = %q, want %q", decision.Severity, "high")
	}
	if captured.HookCallID != "call-check" {
		t.Fatalf("captured HookCallID = %q, want %q", captured.HookCallID, "call-check")
	}
}

func TestCallbackAfter(t *testing.T) {
	t.Parallel()

	var captured dto.HookPayload
	registry := NewRegistry()
	lease := dto.LeaseKey{InstanceID: "instance-after", Generation: 1}
	registry.instances[lease] = &ToolInstance{
		Lease:  lease,
		Peer:   &stubCallbackPeer{},
		Status: dto.StatusActive,
	}
	registry.instances[lease].Peer = &stubCallbackPeer{
		callbackFn: func(_ context.Context, method string, params any, result any) error {
			if method != dto.MethodHookAfter {
				return fmt.Errorf("method = %q, want %q", method, dto.MethodHookAfter)
			}
			payload, ok := params.(dto.HookPayload)
			if !ok {
				return fmt.Errorf("params type = %T, want dto.HookPayload", params)
			}
			decision, ok := result.(*dto.AfterDecision)
			if !ok {
				return fmt.Errorf("result type = %T, want *dto.AfterDecision", result)
			}
			captured = payload
			*decision = dto.AfterDecision{Decision: dto.HookDecisionApprove, Reason: "done"}
			return nil
		},
	}

	decision, err := registry.CallbackAfter(context.Background(), lease, dto.HookPayload{
		HookCallID: "call-after",
		TurnID:     "turn-after",
	})
	if err != nil {
		t.Fatalf("CallbackAfter() error = %v", err)
	}
	if decision.Decision != dto.HookDecisionApprove {
		t.Fatalf("CallbackAfter() decision = %q, want %q", decision.Decision, dto.HookDecisionApprove)
	}
	if decision.Reason != "done" {
		t.Fatalf("CallbackAfter() reason = %q, want %q", decision.Reason, "done")
	}
	if captured.HookCallID != "call-after" {
		t.Fatalf("captured HookCallID = %q, want %q", captured.HookCallID, "call-after")
	}
}

func TestCallbackHookBefore(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	lease := dto.LeaseKey{InstanceID: "instance-hook-before", Generation: 1}
	var captured dto.HookPayload
	addIndexedInstance(registry, &ToolInstance{
		Lease:         lease,
		Subscriptions: []string{"topic.hook.before"},
		Peer: &stubCallbackPeer{
			callbackFn: func(_ context.Context, method string, params any, result any) error {
				if method != dto.MethodHookBefore {
					return fmt.Errorf("method = %q, want %q", method, dto.MethodHookBefore)
				}
				if result != nil {
					return fmt.Errorf("result = %T, want nil", result)
				}
				payload, ok := params.(dto.HookPayload)
				if !ok {
					return fmt.Errorf("params type = %T, want dto.HookPayload", params)
				}
				captured = payload
				return nil
			},
		},
	})

	err := registry.CallbackHookBefore(context.Background(), "topic.hook.before", dto.HookPayload{HookCallID: "call-hook-before"})
	if err != nil {
		t.Fatalf("CallbackHookBefore() error = %v", err)
	}
	if captured.Topic != "topic.hook.before" {
		t.Fatalf("CallbackHookBefore() payload.Topic = %q, want %q", captured.Topic, "topic.hook.before")
	}
	if captured.HookCallID != "call-hook-before" {
		t.Fatalf("CallbackHookBefore() payload.HookCallID = %q, want %q", captured.HookCallID, "call-hook-before")
	}
}

func TestCallbackHookCheck(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	lease := dto.LeaseKey{InstanceID: "instance-hook-check", Generation: 1}
	var captured dto.HookPayload
	addIndexedInstance(registry, &ToolInstance{
		Lease:         lease,
		Subscriptions: []string{"topic.hook.check"},
		Peer: &stubCallbackPeer{
			callbackFn: func(_ context.Context, method string, params any, result any) error {
				if method != dto.MethodHookCheck {
					return fmt.Errorf("method = %q, want %q", method, dto.MethodHookCheck)
				}
				if result != nil {
					return fmt.Errorf("result = %T, want nil", result)
				}
				payload, ok := params.(dto.HookPayload)
				if !ok {
					return fmt.Errorf("params type = %T, want dto.HookPayload", params)
				}
				captured = payload
				return nil
			},
		},
	})

	err := registry.CallbackHookCheck(context.Background(), "topic.hook.check", dto.HookPayload{HookCallID: "call-hook-check"})
	if err != nil {
		t.Fatalf("CallbackHookCheck() error = %v", err)
	}
	if captured.Topic != "topic.hook.check" {
		t.Fatalf("CallbackHookCheck() payload.Topic = %q, want %q", captured.Topic, "topic.hook.check")
	}
	if captured.HookCallID != "call-hook-check" {
		t.Fatalf("CallbackHookCheck() payload.HookCallID = %q, want %q", captured.HookCallID, "call-hook-check")
	}
}

func TestCallbackHookAfter(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	lease := dto.LeaseKey{InstanceID: "instance-hook-after", Generation: 1}
	var captured dto.HookPayload
	addIndexedInstance(registry, &ToolInstance{
		Lease:         lease,
		Subscriptions: []string{"topic.hook.after"},
		Peer: &stubCallbackPeer{
			callbackFn: func(_ context.Context, method string, params any, result any) error {
				if method != dto.MethodHookAfter {
					return fmt.Errorf("method = %q, want %q", method, dto.MethodHookAfter)
				}
				if result != nil {
					return fmt.Errorf("result = %T, want nil", result)
				}
				payload, ok := params.(dto.HookPayload)
				if !ok {
					return fmt.Errorf("params type = %T, want dto.HookPayload", params)
				}
				captured = payload
				return nil
			},
		},
	})

	err := registry.CallbackHookAfter(context.Background(), "topic.hook.after", dto.HookPayload{HookCallID: "call-hook-after"})
	if err != nil {
		t.Fatalf("CallbackHookAfter() error = %v", err)
	}
	if captured.Topic != "topic.hook.after" {
		t.Fatalf("CallbackHookAfter() payload.Topic = %q, want %q", captured.Topic, "topic.hook.after")
	}
	if captured.HookCallID != "call-hook-after" {
		t.Fatalf("CallbackHookAfter() payload.HookCallID = %q, want %q", captured.HookCallID, "call-hook-after")
	}
}
