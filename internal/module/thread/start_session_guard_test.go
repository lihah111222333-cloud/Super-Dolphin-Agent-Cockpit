package thread

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func TestResolveStartConfigAppliesDefaultsAndDangerPolicy(t *testing.T) {
	t.Parallel()

	req, err := resolveStartConfig(StartRequest{
		Provider: " Claude ",
		Sandbox:  json.RawMessage(`{"type":"danger-full-access"}`),
	})
	if err != nil {
		t.Fatalf("resolveStartConfig() error = %v", err)
	}
	if req.Provider != "claude" {
		t.Fatalf("provider = %q, want claude", req.Provider)
	}
	if req.CWD != wantStartCWD(t) {
		t.Fatalf("cwd = %q, want %q", req.CWD, wantStartCWD(t))
	}
	if req.ApprovalPolicy != "never" {
		t.Fatalf("approvalPolicy = %q, want never", req.ApprovalPolicy)
	}
}

func TestResolveStartConfigRejectsInvalidProvider(t *testing.T) {
	t.Parallel()

	_, err := resolveStartConfig(StartRequest{Provider: "other"})
	if err == nil || !strings.Contains(err.Error(), "invalid provider") {
		t.Fatalf("resolveStartConfig() error = %v, want invalid provider", err)
	}
}

func TestResolveStartConfigRejectsInvalidApprovalPolicy(t *testing.T) {
	t.Parallel()

	_, err := resolveStartConfig(StartRequest{ApprovalPolicy: "later"})
	if err == nil || !strings.Contains(err.Error(), "invalid approval policy") {
		t.Fatalf("resolveStartConfig() error = %v, want invalid approval policy", err)
	}
}

func TestResolveStartConfigDropsMalformedSandbox(t *testing.T) {
	t.Parallel()

	req, err := resolveStartConfig(StartRequest{Sandbox: json.RawMessage("{")})
	if err != nil {
		t.Fatalf("resolveStartConfig() error = %v", err)
	}
	if len(req.Sandbox) != 0 {
		t.Fatalf("sandbox = %q, want empty", string(req.Sandbox))
	}
	if req.ApprovalPolicy != "" {
		t.Fatalf("approvalPolicy = %q, want empty", req.ApprovalPolicy)
	}
}

func TestResolveStartConfigAcceptsLegacyApprovalPolicies(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		policy string
	}{
		{name: "on-request", policy: "on-request"},
		{name: "on-failure", policy: "on-failure"},
		{name: "untrusted", policy: "untrusted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := resolveStartConfig(StartRequest{ApprovalPolicy: tc.policy})
			if err != nil {
				t.Fatalf("resolveStartConfig() error = %v", err)
			}
			if req.ApprovalPolicy != tc.policy {
				t.Fatalf("approvalPolicy = %q, want %q", req.ApprovalPolicy, tc.policy)
			}
		})
	}
}

func TestServiceStartUsesResolvedStartConfig(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{}
	bindings := &stubBindingStore{}
	sessions := &stubSessionProvider{}
	starter := &startOnlySessionStarter{
		onStart: func(_ context.Context, req dto.StartSessionRequest) (contract.Session, error) {
			if req.Provider != "claude" {
				t.Fatalf("provider = %q, want claude", req.Provider)
			}
			if req.CWD != wantStartCWD(t) {
				t.Fatalf("cwd = %q, want %q", req.CWD, wantStartCWD(t))
			}
			if req.Instructions != "launch me" {
				t.Fatalf("instructions = %q, want launch me", req.Instructions)
			}
			if got := req.Config["approvalPolicy"]; got != "never" {
				t.Fatalf("approvalPolicy = %#v, want never", got)
			}
			sandbox, _ := req.Config["sandbox"].(map[string]any)
			if sandbox["type"] != "danger-full-access" {
				t.Fatalf("sandbox = %#v, want danger-full-access", req.Config["sandbox"])
			}
			session := &stubSession{threadID: "provider-thread-1"}
			sessions.session = session
			return session, nil
		},
	}
	orch := &stubThreadOrchestration{}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil).(*service)

	result, err := svc.Start(context.Background(), StartRequest{
		AgentID:          "agent-start",
		Provider:         " Claude ",
		BaseInstructions: "  launch me  ",
		Sandbox:          json.RawMessage(`{"type":"danger-full-access"}`),
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if result.ThreadID != "agent-start" || result.SessionID != "provider-thread-1" || result.AgentID != "agent-start" {
		t.Fatalf("result = %#v", result)
	}
	if orch.launchReq.Cwd != wantStartCWD(t) {
		t.Fatalf("launch cwd = %q, want %q", orch.launchReq.Cwd, wantStartCWD(t))
	}
	if orch.launchReq.Name != "launch me" {
		t.Fatalf("launch name = %q, want launch me", orch.launchReq.Name)
	}
	if threads.upsert.Cwd != wantStartCWD(t) || bindings.upsert.Cwd != wantStartCWD(t) {
		t.Fatalf("persisted cwd = %q/%q, want %q", threads.upsert.Cwd, bindings.upsert.Cwd, wantStartCWD(t))
	}
	if bindings.upsert.Provider != "claude" {
		t.Fatalf("binding provider = %q, want claude", bindings.upsert.Provider)
	}
	if bindings.upsert.ProviderThreadID != "provider-thread-1" || bindings.upsert.CodexThreadID != "agent-start" {
		t.Fatalf("binding upsert = %#v", bindings.upsert)
	}
}

func TestNewThreadHandlersDispatchStartRejectsInvalidConfig(t *testing.T) {
	t.Parallel()

	server := newThreadTestServer(NewService(silentLogger(), nil, nil, nil, nil, nil, nil, nil))
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{name: "provider", raw: `{"provider":"other","prompt":"hello"}`, want: "invalid provider"},
		{name: "approval", raw: `{"approval_policy":"later","prompt":"hello"}`, want: "invalid approval policy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := server.Dispatch(context.Background(), "thread/start", json.RawMessage(tc.raw))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Dispatch(thread/start) error = %v, want %q", err, tc.want)
			}
		})
	}
}

type startOnlySessionStarter struct {
	onStart func(context.Context, dto.StartSessionRequest) (contract.Session, error)
}

func (s *startOnlySessionStarter) StartSession(ctx context.Context, req dto.StartSessionRequest) (contract.Session, error) {
	if s.onStart == nil {
		return nil, errors.New("unexpected start session")
	}
	return s.onStart(ctx, req)
}

func (s *startOnlySessionStarter) ResumeSession(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
	return nil, errors.New("unexpected resume session")
}

func wantStartCWD(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil || strings.TrimSpace(wd) == "" {
		return "."
	}
	return wd
}
