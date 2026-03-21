package rpc

import (
	"context"
	"errors"
	"testing"

	"github.com/creachadair/jrpc2"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
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
	if !caps.Has(dto.CapMessageSend) {
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
	if err == nil || err.Error() != "thread session is not available" {
		t.Fatalf("resolver() error = %v, want thread session is not available", err)
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
	caps dto.CapabilitySet
}

func (s stubRPCSession) ThreadID() string { return "" }

func (s stubRPCSession) Capabilities() dto.CapabilitySet { return s.caps }

func (s stubRPCSession) StartTurn(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
	return nil, nil
}

func (s stubRPCSession) Interrupt(context.Context, dto.InterruptRequest) error { return nil }

func (s stubRPCSession) ForceComplete(context.Context, dto.ForceCompleteRequest) error { return nil }

func (s stubRPCSession) ListThreads(context.Context) ([]dto.ThreadRef, error) { return nil, nil }

func (s stubRPCSession) ForkThread(context.Context, dto.ForkRequest) (dto.ForkResult, error) {
	return dto.ForkResult{}, nil
}

func (s stubRPCSession) ReadHistory(context.Context, string, int) ([]dto.Message, error) {
	return nil, nil
}

func (s stubRPCSession) Configure(context.Context, dto.ThreadConfigPatch) error { return nil }

func (s stubRPCSession) Close(context.Context) error { return nil }

func (s stubRPCSession) ForceStop() error { return nil }
