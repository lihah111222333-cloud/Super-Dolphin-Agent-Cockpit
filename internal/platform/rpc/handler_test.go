package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

func TestNewCapabilityResolverReturnsCapabilities(t *testing.T) {
	t.Parallel()

	resolver := NewCapabilityResolver(stubSessionResolver{
		session: stubRPCSession{caps: dto.CapabilitySet{dto.CapMessageSend: true}},
	})
	caps, err := resolver(withThreadID(context.Background(), "thread-1"))
	if err != nil {
		t.Fatalf("resolver() error = %v", err)
	}
	if !contract.HasCapability(caps, dto.CapMessageSend) {
		t.Fatal("resolver() missing CapMessageSend")
	}
}

func TestNewCapabilityResolverReturnsLookupError(t *testing.T) {
	t.Parallel()

	want := errors.New("session lookup failed")
	resolver := NewCapabilityResolver(stubSessionResolver{err: want})
	_, err := resolver(withThreadID(context.Background(), "thread-1"))
	if !errors.Is(err, want) {
		t.Fatalf("resolver() error = %v, want %v", err, want)
	}
}

func TestNewCapabilityResolverRejectsNilSession(t *testing.T) {
	t.Parallel()

	resolver := NewCapabilityResolver(stubSessionResolver{})
	_, err := resolver(withThreadID(context.Background(), "thread-1"))
	if err == nil || !strings.Contains(err.Error(), "thread session is not available") {
		t.Fatalf("resolver() error = %v, want containing 'thread session is not available'", err)
	}
}

func TestCapabilityResolverErrorUsesInvalidStateCode(t *testing.T) {
	t.Parallel()

	err := capabilityResolverError(
		withThreadID(context.Background(), "thread-42"),
		errors.New("resolve session: thread \"thread-42\" not found"),
	)
	var rpcErr *jrpc2.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("capabilityResolverError() = %T, want *jrpc2.Error", err)
	}
	if rpcErr.Code != jrpc2.Code(CodeInvalidState) {
		t.Fatalf("rpcErr.Code = %v, want %v", rpcErr.Code, jrpc2.Code(CodeInvalidState))
	}
	if rpcErr.Message != `resolve session: thread "thread-42" not found` {
		t.Fatalf("rpcErr.Message = %q", rpcErr.Message)
	}
}

type stubSessionResolver struct {
	session contract.Session
	err     error
}

func (s stubSessionResolver) ResolveSession(context.Context, string) (contract.Session, error) {
	return s.session, s.err
}

type stubRPCSession struct {
	stubRPCSessionLifecycle
	stubRPCSessionOps
	caps dto.CapabilitySet
}

type stubRPCSessionLifecycle struct{}

func (stubRPCSessionLifecycle) ThreadID() string { return "" }

func (stubRPCSessionLifecycle) RolloutPath() string { return "" }

func (stubRPCSessionLifecycle) Close(context.Context) error { return nil }

func (stubRPCSessionLifecycle) ForceStop() error { return nil }

type stubRPCSessionOps struct{}

func (s stubRPCSession) Capabilities() dto.CapabilitySet { return s.caps }

func (stubRPCSessionOps) StartTurn(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
	return nil, nil
}

func (stubRPCSessionOps) Steer(context.Context, dto.SteerRequest) error { return nil }

func (stubRPCSessionOps) Interrupt(context.Context, dto.InterruptRequest) error { return nil }

func (stubRPCSessionOps) ForceComplete(context.Context, dto.ForceCompleteRequest) error { return nil }

func (stubRPCSessionOps) ListThreads(context.Context) ([]dto.ThreadRef, error) { return nil, nil }

func (stubRPCSessionOps) ForkThread(context.Context, dto.ForkRequest) (dto.ForkResult, error) {
	return dto.ForkResult{}, nil
}

func (stubRPCSessionOps) ReadHistory(context.Context, string, int) ([]dto.Message, error) {
	return nil, nil
}

func (stubRPCSessionOps) Configure(context.Context, dto.ThreadConfigPatch) error { return nil }

type threadAliasParams struct {
	ThreadID      string `json:"threadId,omitempty"`
	ThreadIDAlt   string `json:"threadID,omitempty"`
	ThreadIDSnake string `json:"thread_id,omitempty"`
}

func TestStrictHandlerRejectsArrayParams(t *testing.T) {
	t.Parallel()

	server := newTestServer()
	server.Register(handler.Map{"echo": StrictHandler(func(context.Context, struct {
		Value string `json:"value"`
	}) (string, error) {
		return "ok", nil
	})})

	_, err := server.Dispatch(context.Background(), "echo", json.RawMessage(`[{"value":"ok"}]`))
	var rpcErr *jrpc2.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("Dispatch() error = %T, want *jrpc2.Error", err)
	}
	if rpcErr.Code != jrpc2.Code(CodeInvalidParams) {
		t.Fatalf("rpcErr.Code = %v, want %v", rpcErr.Code, jrpc2.Code(CodeInvalidParams))
	}
}

func TestThreadHandlerExtractsThreadIDFromAliases(t *testing.T) {
	t.Parallel()

	tests := []string{
		`{"threadId":"thread-1"}`,
		`{"threadID":"thread-1"}`,
		`{"thread_id":"thread-1"}`,
	}

	for _, rawParams := range tests {
		t.Run(rawParams, func(t *testing.T) {
			t.Parallel()

			server := newTestServer()
			server.Register(handler.Map{"thread/echo": ThreadHandler(func(ctx context.Context, _ threadAliasParams) (map[string]string, error) {
				return map[string]string{"threadId": ThreadIDFrom(ctx)}, nil
			})})

			raw, err := server.Dispatch(context.Background(), "thread/echo", json.RawMessage(rawParams))
			if err != nil {
				t.Fatalf("Dispatch() error = %v", err)
			}

			var got map[string]string
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			if got["threadId"] != "thread-1" {
				t.Fatalf("threadId = %q, want %q", got["threadId"], "thread-1")
			}
		})
	}
}

func TestThreadHandlerRejectsMissingThreadID(t *testing.T) {
	t.Parallel()

	server := newTestServer()
	server.Register(handler.Map{"thread/echo": ThreadHandler(func(context.Context, threadAliasParams) (map[string]string, error) {
		return map[string]string{"ok": "true"}, nil
	})})

	_, err := server.Dispatch(context.Background(), "thread/echo", json.RawMessage(`{}`))
	var rpcErr *jrpc2.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("Dispatch() error = %T, want *jrpc2.Error", err)
	}
	if rpcErr.Code != jrpc2.Code(CodeInvalidParams) {
		t.Fatalf("rpcErr.Code = %v, want %v", rpcErr.Code, jrpc2.Code(CodeInvalidParams))
	}
}

func TestCapabilityThreadHandlerUsesResolverAndThreadScope(t *testing.T) {
	t.Parallel()

	server := newTestServer()
	resolver := func(ctx context.Context) (dto.CapabilitySet, error) {
		if got := ThreadIDFrom(ctx); got != "thread-1" {
			t.Fatalf("ThreadIDFrom(ctx) = %q, want %q", got, "thread-1")
		}
		return dto.CapabilitySet{dto.CapMessageSend: true}, nil
	}
	server.Register(handler.Map{"turn/start": CapabilityThreadHandler(dto.CapMessageSend, resolver, func(ctx context.Context, _ threadAliasParams) (map[string]string, error) {
		return map[string]string{"threadId": ThreadIDFrom(ctx)}, nil
	})})

	raw, err := server.Dispatch(context.Background(), "turn/start", json.RawMessage(`{"threadId":"thread-1"}`))
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}

	var got map[string]string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got["threadId"] != "thread-1" {
		t.Fatalf("threadId = %q, want %q", got["threadId"], "thread-1")
	}
}

func TestCapabilityThreadHandlerRejectsUnsupportedCapability(t *testing.T) {
	t.Parallel()

	server := newTestServer()
	server.Register(handler.Map{"turn/start": CapabilityThreadHandler(dto.CapMessageSend, func(context.Context) (dto.CapabilitySet, error) {
		return dto.CapabilitySet{}, nil
	}, func(context.Context, threadAliasParams) (map[string]string, error) {
		return map[string]string{"ok": "true"}, nil
	})})

	_, err := server.Dispatch(context.Background(), "turn/start", json.RawMessage(`{"threadId":"thread-1"}`))
	var rpcErr *jrpc2.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("Dispatch() error = %T, want *jrpc2.Error", err)
	}
	if rpcErr.Code != jrpc2.Code(CodeCapabilityGate) {
		t.Fatalf("rpcErr.Code = %v, want %v", rpcErr.Code, jrpc2.Code(CodeCapabilityGate))
	}
}

func TestThreadHandlerMapsRuntimeCapabilityErrorToGateCode(t *testing.T) {
	t.Parallel()

	server := newTestServer()
	server.Register(handler.Map{"thread/failcap": ThreadHandler(func(ctx context.Context, _ threadAliasParams) (string, error) {
		return "", contract.NewCapabilityError("thread.list", "claude")
	})})

	_, err := server.Dispatch(context.Background(), "thread/failcap", json.RawMessage(`{"threadId":"thread-1"}`))
	var rpcErr *jrpc2.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("Dispatch() error = %T, want *jrpc2.Error", err)
	}
	if rpcErr.Code != jrpc2.Code(CodeCapabilityGate) {
		t.Fatalf("rpcErr.Code = %v, want %v (CodeCapabilityGate)", rpcErr.Code, jrpc2.Code(CodeCapabilityGate))
	}
}
