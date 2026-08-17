//go:build windows

package multilsp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestInitializeClientWithWindows122RetryRetriesOnceAfterCleanup(t *testing.T) {
	var initializes, restarts, cleanups int
	client, err := initializeClientWithWindows122Retry(
		context.Background(), nil,
		func(Client) error {
			initializes++
			if initializes == 1 {
				return errors.New("LSP initialize request: write |1: The pipe is being closed. The data area passed to a system call is too small.")
			}
			return nil
		},
		func() (Client, error) { restarts++; return nil, nil },
		func(Client) error { cleanups++; return nil },
	)
	if err != nil || client != nil {
		t.Fatalf("retry result client=%v err=%v, want nil test client and success", client, err)
	}
	if initializes != 2 || restarts != 1 || cleanups != 1 {
		t.Fatalf("initialize=%d restart=%d cleanup=%d, want 2/1/1", initializes, restarts, cleanups)
	}
}

func TestInitializeClientWithWindows122RetryStopsAfterSecond122(t *testing.T) {
	var initializes, restarts, cleanups int
	_, err := initializeClientWithWindows122Retry(
		context.Background(), nil,
		func(Client) error {
			initializes++
			return errors.New("The pipe is being closed: The data area passed to a system call is too small.")
		},
		func() (Client, error) { restarts++; return nil, nil },
		func(Client) error { cleanups++; return nil },
	)
	if err == nil || !strings.Contains(err.Error(), "data area passed") {
		t.Fatalf("second 122 error=%v, want preserved startup error", err)
	}
	if initializes != 2 || restarts != 1 || cleanups != 2 {
		t.Fatalf("initialize=%d restart=%d cleanup=%d, want 2/1/2", initializes, restarts, cleanups)
	}
}

func TestIsWindows122StartupErrorRequiresPipeClosed(t *testing.T) {
	if isWindows122StartupError(context.Background(), nil, errors.New("The data area passed to a system call is too small.")) {
		t.Fatal("Win32 122 without pipe-closed transport evidence must not retry")
	}
	if !isWindows122StartupError(context.Background(), nil, errors.New("The pipe is being closed: The data area passed to a system call is too small.")) {
		t.Fatal("pipe-closed plus exact Win32 122 must retry")
	}
	if isWindows122StartupError(context.Background(), nil, errors.New("The pipe is being closed: ordinary startup failure")) {
		t.Fatal("ordinary pipe closure must not retry")
	}
	if !isWindows122StartupError(context.Background(), nil, errors.New("file already closed: The data area passed to a system call is too small.")) {
		t.Fatal("file-closed plus exact Win32 122 must retry")
	}
	if !isWindows122StartupError(context.Background(), nil, fmt.Errorf("initialize: %w: The data area passed to a system call is too small.", os.ErrClosed)) {
		t.Fatal("typed os.ErrClosed plus exact Win32 122 must retry")
	}
}

type windows122WrappedClient struct {
	Client
	underlying Client
}

func (w *windows122WrappedClient) UnderlyingLSPClient() Client { return w.underlying }

func windows122TestClient(stderr string) Client {
	buffer := &limitedBuffer{limit: 8 * 1024}
	_, _ = buffer.Write([]byte(stderr))
	return &client{transport: &transport{stderr: buffer}}
}

func TestInitializeClientWithWindows122RetryUnwrapsRealWrapper(t *testing.T) {
	oldUnderlying := windows122TestClient("clangd: The data area passed to a system call is too small.")
	old := &windows122WrappedClient{underlying: oldUnderlying}
	newUnderlying := windows122TestClient("")
	newClient := &windows122WrappedClient{underlying: newUnderlying}
	var events []string
	initializes, restarts, cleanups := 0, 0, 0
	got, err := initializeClientWithWindows122Retry(
		context.Background(), old,
		func(candidate Client) error {
			initializes++
			if candidate == old {
				events = append(events, "old_initialize")
				return errors.New("LSP initialize request: write |1: The pipe is being closed.")
			}
			events = append(events, "new_initialize")
			return nil
		},
		func() (Client, error) {
			restarts++
			if events[len(events)-1] != "old_cleanup" {
				t.Fatalf("restart occurred before old cleanup: %v", events)
			}
			return newClient, nil
		},
		func(candidate Client) error {
			cleanups++
			if candidate == old {
				events = append(events, "old_cleanup")
			} else {
				events = append(events, "new_cleanup")
			}
			return nil
		},
	)
	if err != nil || got != newClient {
		t.Fatalf("got=%p err=%v, want replacement=%p and nil error", got, err, newClient)
	}
	if initializes != 2 || restarts != 1 || cleanups != 1 {
		t.Fatalf("initialize=%d restart=%d cleanup=%d, want 2/1/1", initializes, restarts, cleanups)
	}
	if gotEvents := strings.Join(events, ","); gotEvents != "old_initialize,old_cleanup,new_initialize" {
		t.Fatalf("events=%s, want old_initialize,old_cleanup,new_initialize", gotEvents)
	}
}

func TestInitializeClientWithWindows122RetryWrappedWithout122DoesNotRetry(t *testing.T) {
	old := &windows122WrappedClient{underlying: windows122TestClient("clangd: ordinary startup failure")}
	restarts, cleanups := 0, 0
	_, err := initializeClientWithWindows122Retry(
		context.Background(), old,
		func(Client) error { return errors.New("LSP initialize request: write |1: The pipe is being closed.") },
		func() (Client, error) { restarts++; return old, nil },
		func(Client) error { cleanups++; return nil },
	)
	if err == nil || restarts != 0 || cleanups != 0 {
		t.Fatalf("err=%v restart=%d cleanup=%d, want failure without retry", err, restarts, cleanups)
	}
}

func TestIsWindows122StartupErrorObservesAsyncStderrDeterministically(t *testing.T) {
	oldPause := windows122RetryPause
	t.Cleanup(func() { windows122RetryPause = oldPause })
	underlying := windows122TestClient("")
	concrete, ok := concreteClient(underlying)
	if !ok || concrete.transport == nil || concrete.transport.stderr == nil {
		t.Fatal("test client did not expose transport stderr")
	}
	wrapper := &windows122WrappedClient{underlying: underlying}
	pauseCalled := make(chan struct{})
	writerDone := make(chan struct{})
	windows122RetryPause = func(context.Context) {
		close(pauseCalled)
		<-writerDone
	}
	go func() {
		<-pauseCalled
		_, _ = concrete.transport.stderr.Write([]byte("clangd: The data area passed to a system call is too small."))
		close(writerDone)
	}()
	if !isWindows122StartupError(context.Background(), wrapper, fmt.Errorf("initialize: %w", os.ErrClosed)) {
		t.Fatal("async exact Win122 stderr must be observed")
	}
}

func TestIsWindows122StartupErrorWaitsForDoneAfterStderr(t *testing.T) {
	underlying := windows122TestClient("")
	concrete, ok := concreteClient(underlying)
	if !ok || concrete.transport == nil || concrete.transport.stderr == nil {
		t.Fatal("test client did not expose transport stderr")
	}
	concrete.transport.done = make(chan struct{})
	_, _ = concrete.transport.stderr.Write([]byte("clangd: The data area passed to a system call is too small."))
	close(concrete.transport.done)
	wrapper := &windows122WrappedClient{underlying: underlying}
	if !isWindows122StartupError(context.Background(), wrapper, fmt.Errorf("initialize: %w", os.ErrClosed)) {
		t.Fatal("exact Win122 written before done close must classify")
	}
}

func TestIsWindows122StartupErrorDoneNeverClosesIsBounded(t *testing.T) {
	underlying := windows122TestClient("ordinary startup failure")
	concrete, ok := concreteClient(underlying)
	if !ok || concrete.transport == nil {
		t.Fatal("test client did not expose transport")
	}
	concrete.transport.done = make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	if isWindows122StartupError(ctx, &windows122WrappedClient{underlying: underlying}, fmt.Errorf("initialize: %w", os.ErrClosed)) {
		t.Fatal("ordinary stderr with unclosed done must not classify")
	}
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("unclosed done wait elapsed=%s, want <=200ms", elapsed)
	}
}

func TestIsWindows122StartupErrorCanceledContextStopsImmediately(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	if isWindows122StartupError(ctx, nil, fmt.Errorf("initialize: %w", os.ErrClosed)) {
		t.Fatal("canceled context must not classify Win122")
	}
	if elapsed := time.Since(started); elapsed > 50*time.Millisecond {
		t.Fatalf("canceled context elapsed=%s, want immediate return", elapsed)
	}
}

func TestIsWindows122StartupErrorNoExactStderrStopsWithoutRetry(t *testing.T) {
	oldPause := windows122RetryPause
	t.Cleanup(func() { windows122RetryPause = oldPause })
	pauses := 0
	windows122RetryPause = func(context.Context) { pauses++ }
	underlying := windows122TestClient("ordinary startup failure")
	wrapper := &windows122WrappedClient{underlying: underlying}
	if isWindows122StartupError(context.Background(), wrapper, fmt.Errorf("initialize: %w", os.ErrClosed)) {
		t.Fatal("ordinary stderr must not classify as Win122")
	}
	if pauses != 19 {
		t.Fatalf("pause count=%d, want 19 bounded waits", pauses)
	}
}

func TestInitializeClientWithWindows122RetryWrappedSecond122Stops(t *testing.T) {
	old := &windows122WrappedClient{underlying: windows122TestClient("clangd: The data area passed to a system call is too small.")}
	replacement := &windows122WrappedClient{underlying: windows122TestClient("clangd: The data area passed to a system call is too small.")}
	initializes, restarts, cleanups := 0, 0, 0
	_, err := initializeClientWithWindows122Retry(
		context.Background(), old,
		func(Client) error {
			initializes++
			return errors.New("LSP initialize request: write |1: The pipe is being closed.")
		},
		func() (Client, error) { restarts++; return replacement, nil },
		func(Client) error { cleanups++; return nil },
	)
	if err == nil || initializes != 2 || restarts != 1 || cleanups != 2 {
		t.Fatalf("err=%v initialize=%d restart=%d cleanup=%d, want error and 2/1/2", err, initializes, restarts, cleanups)
	}
}

func TestInitializeClientWithWindows122RetryDoesNotRetryOtherErrors(t *testing.T) {
	var restarts, cleanups int
	want := errors.New("ordinary initialize failure")
	_, err := initializeClientWithWindows122Retry(
		context.Background(), nil,
		func(Client) error { return want },
		func() (Client, error) { restarts++; return nil, nil },
		func(Client) error { cleanups++; return nil },
	)
	if !errors.Is(err, want) || restarts != 0 || cleanups != 0 {
		t.Fatalf("err=%v restart=%d cleanup=%d, want original error and no retry", err, restarts, cleanups)
	}
}

func TestInitializeClientWithWindows122RetryDoesNotRetryCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var restarts, cleanups int
	_, err := initializeClientWithWindows122Retry(
		ctx, nil,
		func(Client) error { return errors.New("The data area passed to a system call is too small.") },
		func() (Client, error) { restarts++; return nil, nil },
		func(Client) error { cleanups++; return nil },
	)
	if err == nil || restarts != 0 || cleanups != 0 {
		t.Fatalf("err=%v restart=%d cleanup=%d, want immediate cancel failure", err, restarts, cleanups)
	}
}
