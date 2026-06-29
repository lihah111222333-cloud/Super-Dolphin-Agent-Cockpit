package app

import (
	"context"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
)

func TestToolCallBindingFromStoreCarriesParentAgentID(t *testing.T) {
	got := toolCallBindingFromStore(&bindingstore.Binding{
		AgentID:       " agent-child ",
		ParentAgentID: " agent-root ",
	})
	if got.AgentID != "agent-child" {
		t.Fatalf("AgentID = %q, want agent-child", got.AgentID)
	}
	if got.ParentAgentID != "agent-root" {
		t.Fatalf("ParentAgentID = %q, want agent-root", got.ParentAgentID)
	}
}

func TestToolbridgeReadySessionStarterBlocksCodexBeforeInnerStarter(t *testing.T) {
	inner := &recordingToolbridgeSessionStarter{}
	starter := toolbridgeReadySessionStarter{
		inner:     inner,
		readiness: &codexToolbridgeReadinessProbe{},
	}

	_, err := starter.StartSession(context.Background(), dto.StartSessionRequest{Provider: "codex"})
	if err == nil {
		t.Fatal("StartSession() error = nil, want codex toolbridge readiness failure")
	}
	if !strings.Contains(err.Error(), "codex binding is not ready") {
		t.Fatalf("StartSession() error = %v, want codex binding readiness failure", err)
	}
	if inner.startCalls != 0 {
		t.Fatalf("inner StartSession calls = %d, want 0 before readiness", inner.startCalls)
	}
}

func TestToolbridgeReadySessionStarterDelegatesAfterCodexReady(t *testing.T) {
	inner := &recordingToolbridgeSessionStarter{}
	readiness := &codexToolbridgeReadinessProbe{}
	readiness.markReady()
	starter := toolbridgeReadySessionStarter{
		inner:     inner,
		readiness: readiness,
	}

	if _, err := starter.StartSession(context.Background(), dto.StartSessionRequest{Provider: "codex"}); err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	if inner.startCalls != 1 {
		t.Fatalf("inner StartSession calls = %d, want 1 after readiness", inner.startCalls)
	}
}

type recordingToolbridgeSessionStarter struct {
	startCalls  int
	resumeCalls int
}

func (s *recordingToolbridgeSessionStarter) StartSession(context.Context, dto.StartSessionRequest) (contract.Session, error) {
	s.startCalls++
	return nil, nil
}

func (s *recordingToolbridgeSessionStarter) ResumeSession(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
	s.resumeCalls++
	return nil, nil
}
