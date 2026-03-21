package contract

import (
	"context"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

// Driver is the provider factory contract.
type Driver interface {
	Name() string
	StartSession(ctx context.Context, req dto.StartSessionRequest) (Session, error)
	ResumeSession(ctx context.Context, req dto.ResumeSessionRequest) (Session, error)
}

// DriverFactory constructs Driver instances for DI registration.
type DriverFactory struct {
	Name   string
	Create func() Driver
}

// Session is the unified provider session abstraction.
type Session interface {
	ThreadID() string
	Capabilities() dto.CapabilitySet

	StartTurn(ctx context.Context, req dto.TurnRequest) (TurnHandle, error)
	Interrupt(ctx context.Context, req dto.InterruptRequest) error
	ForceComplete(ctx context.Context, req dto.ForceCompleteRequest) error

	ListThreads(ctx context.Context) ([]dto.ThreadRef, error)
	ForkThread(ctx context.Context, req dto.ForkRequest) (dto.ForkResult, error)
	ReadHistory(ctx context.Context, threadID string, limit int) ([]dto.Message, error)

	Configure(ctx context.Context, patch dto.ThreadConfigPatch) error
	Close(ctx context.Context) error
	ForceStop() error
}

// TurnHandle is the handle for an in-flight turn.
type TurnHandle interface {
	LocalID() string
	ProviderID() string
	Done() <-chan struct{}
	Err() error
}
