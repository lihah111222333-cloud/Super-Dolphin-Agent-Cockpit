package toolbridge

import (
	"context"
	"errors"
	"fmt"
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

func TestLazyMCPSchemaExecutorOwnerCancellationAllowsHealthyRetry(t *testing.T) {
	firstStarted := make(chan struct{})
	var initializeCalls atomic.Int32
	executor := &lazyMCPSchemaExecutor{
		initializeClient: func(ctx context.Context) (mcpSchemaExecutor, error) {
			if initializeCalls.Add(1) == 1 {
				close(firstStarted)
				<-ctx.Done()
				return nil, fmt.Errorf("first owner canceled: %w", ctx.Err())
			}
			return successfulMCPExecutor(), nil
		},
	}
	ownerCtx, cancelOwner := context.WithCancel(context.Background())
	ownerResult := make(chan error, 1)
	safego.Go(ownerCtx, nil, "toolbridge.lazy-schema-owner-cancel-test", func(context.Context) {
		_, err := executor.Execute(ownerCtx, schema.Invocation{}, nil)
		ownerResult <- err
	})
	assertLazyInitializationStarted(t, firstStarted)
	cancelOwner()
	if err := <-ownerResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled owner error = %v, want context canceled", err)
	}
	if _, err := executor.Execute(context.Background(), schema.Invocation{}, nil); err != nil {
		t.Fatalf("healthy retry error = %v", err)
	}
	if got := initializeCalls.Load(); got != 2 {
		t.Fatalf("initializer calls after healthy retry = %d, want 2", got)
	}
}

func TestLazyMCPSchemaExecutorCanceledWaitersDoNotPoisonHealthyOwner(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var initializeCalls atomic.Int32
	executor := &lazyMCPSchemaExecutor{
		initializeClient: func(ctx context.Context) (mcpSchemaExecutor, error) {
			initializeCalls.Add(1)
			close(started)
			select {
			case <-release:
				return successfulMCPExecutor(), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	}
	ownerResult := make(chan error, 1)
	ownerCtx, cancelOwner := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelOwner()
	safego.Go(ownerCtx, nil, "toolbridge.lazy-schema-multi-waiter-owner", func(context.Context) {
		_, err := executor.Execute(ownerCtx, schema.Invocation{}, nil)
		ownerResult <- err
	})
	assertLazyInitializationStarted(t, started)
	assertCanceledLazyWaiters(t, executor, 8)
	close(release)
	assertLazyInitializationOwnerReturned(t, ownerResult)
	if _, err := executor.Execute(context.Background(), schema.Invocation{}, nil); err != nil {
		t.Fatalf("cached healthy client error = %v", err)
	}
	if got := initializeCalls.Load(); got != 1 {
		t.Fatalf("initializer calls with canceled waiters = %d, want 1", got)
	}
}

func TestLazyMCPSchemaExecutorCachesStableInitializationError(t *testing.T) {
	stableErr := errors.New("stable helper package verification failed")
	var initializeCalls atomic.Int32
	executor := &lazyMCPSchemaExecutor{
		initializeClient: func(context.Context) (mcpSchemaExecutor, error) {
			initializeCalls.Add(1)
			return nil, stableErr
		},
	}
	for attempt := range 2 {
		if _, err := executor.Execute(context.Background(), schema.Invocation{}, nil); !errors.Is(err, stableErr) {
			t.Fatalf("stable initialization attempt %d error = %v", attempt+1, err)
		}
	}
	if got := initializeCalls.Load(); got != 1 {
		t.Fatalf("stable initializer calls = %d, want 1", got)
	}
}

func successfulMCPExecutor() mcpSchemaExecutor {
	return mcpSchemaExecutorFunc(func(context.Context, schema.Invocation, schema.FenceHook) (schema.Result, error) {
		return schema.Result{}, nil
	})
}

func assertCanceledLazyWaiters(t *testing.T, executor *lazyMCPSchemaExecutor, count int) {
	t.Helper()
	results := make(chan error, count)
	for range count {
		waiterCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		safego.Go(waiterCtx, nil, "toolbridge.lazy-schema-canceled-waiter", func(context.Context) {
			_, err := executor.Execute(waiterCtx, schema.Invocation{}, nil)
			results <- err
		})
	}
	for index := range count {
		if err := <-results; !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("canceled waiter %d error = %v", index+1, err)
		}
	}
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
