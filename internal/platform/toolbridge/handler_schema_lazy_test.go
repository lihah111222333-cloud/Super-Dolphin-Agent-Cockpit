package toolbridge

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/toolbridge/schema"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

type mcpSchemaExecutorFunc func(context.Context, schema.Invocation, schema.FenceHook) (schema.Result, error)

func (fn mcpSchemaExecutorFunc) Execute(ctx context.Context, invocation schema.Invocation, fence schema.FenceHook) (schema.Result, error) {
	return fn(ctx, invocation, fence)
}

func TestLazyMCPSchemaExecutorConcurrentFirstInitializationIsContextBounded(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var initializeCalls atomic.Int32
	executor := &lazyMCPSchemaExecutor{
		initializeClient: func(ctx context.Context) (mcpSchemaExecutor, error) {
			initializeCalls.Add(1)
			close(started)
			select {
			case <-release:
				return mcpSchemaExecutorFunc(func(context.Context, schema.Invocation, schema.FenceHook) (schema.Result, error) {
					return schema.Result{}, nil
				}), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	}
	ownerResult := make(chan error, 1)
	ownerCtx, cancelOwner := context.WithTimeout(context.Background(), time.Second)
	defer cancelOwner()
	safego.Go(ownerCtx, nil, "toolbridge.lazy-schema-initialization-test", func(context.Context) {
		_, err := executor.Execute(ownerCtx, schema.Invocation{}, nil)
		ownerResult <- err
	})
	assertLazyInitializationStarted(t, started)
	assertConcurrentLazyWaiter(t, executor, &initializeCalls)
	close(release)
	assertLazyInitializationOwnerReturned(t, ownerResult)
}

func assertLazyInitializationStarted(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first lazy schema initialization did not start")
	}
}

func assertConcurrentLazyWaiter(t *testing.T, executor *lazyMCPSchemaExecutor, initializeCalls *atomic.Int32) {
	t.Helper()
	waiterCtx, cancelWaiter := context.WithTimeout(context.Background(), 35*time.Millisecond)
	defer cancelWaiter()
	waiterStarted := time.Now()
	_, waiterErr := executor.Execute(waiterCtx, schema.Invocation{}, nil)
	if !errors.Is(waiterErr, context.DeadlineExceeded) {
		t.Fatalf("concurrent lazy waiter error = %v, want deadline exceeded", waiterErr)
	}
	if elapsed := time.Since(waiterStarted); elapsed > 250*time.Millisecond {
		t.Fatalf("concurrent lazy waiter returned after %s", elapsed)
	}
	if got := initializeCalls.Load(); got != 1 {
		t.Fatalf("lazy initialization calls = %d, want 1", got)
	}
}

func assertLazyInitializationOwnerReturned(t *testing.T, ownerResult <-chan error) {
	t.Helper()
	select {
	case err := <-ownerResult:
		if err != nil {
			t.Fatalf("initialization owner error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("initialization owner did not return after release")
	}
}
