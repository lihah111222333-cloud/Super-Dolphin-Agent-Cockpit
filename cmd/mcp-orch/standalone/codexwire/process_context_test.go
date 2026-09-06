package codexwire

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestStartRejectsNilContextsBeforeStartingExecutable(t *testing.T) {
	var nilContext context.Context
	typedNil := context.Context((*nilCodexContext)(nil))
	for _, test := range []struct {
		name string
		ctx  context.Context
	}{
		{name: "nil", ctx: nilContext},
		{name: "typed-nil", ctx: typedNil},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Start(test.ctx, "/path/that/must/not/be/started", "", nil, nil)
			if !errors.Is(err, ErrContextRequired) {
				t.Fatalf("Start() error = %v, want ErrContextRequired", err)
			}
		})
	}
}

func TestInitializeRejectsNilContextBeforeUsingClient(t *testing.T) {
	var nilContext context.Context
	if err := initialize(nilContext, nil); !errors.Is(err, ErrContextRequired) {
		t.Fatalf("initialize() error = %v, want ErrContextRequired", err)
	}
}

type nilCodexContext struct{}

func (*nilCodexContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (*nilCodexContext) Done() <-chan struct{}       { return nil }
func (*nilCodexContext) Err() error                  { return nil }
func (*nilCodexContext) Value(any) any               { return nil }
