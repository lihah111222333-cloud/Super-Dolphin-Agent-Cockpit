package runtimesafe

import (
	"bytes"
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"
)

func TestSafeGoRunsFn(t *testing.T) {
	t.Parallel()
	var wg sync.WaitGroup
	wg.Add(1)
	SafeGo(context.Background(), nil, "test.runsFn", func(ctx context.Context) {
		defer wg.Done()
		if ctx == nil {
			t.Error("ctx must not be nil inside fn")
		}
	})
	wg.Wait()
}

func TestSafeGoRecoversPanic(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	var mu sync.Mutex
	handler := slog.NewTextHandler(&lockedWriter{buf: &buf, mu: &mu}, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(handler)

	done := make(chan struct{})
	SafeGo(context.Background(), logger, "test.panicking", func(ctx context.Context) {
		defer close(done)
		// archguard:ignore panic_count -- this test verifies SafeGo panic recovery logging.
		panic("boom")
	})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("panicking goroutine did not finish in time")
	}

	// Give logger a moment to flush.
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	out := buf.String()
	mu.Unlock()
	if out == "" {
		t.Fatal("expected panic log output, got empty")
	}
	for _, want := range []string{"recovered panic", "label", "test.panicking", "panic", "boom"} {
		if !contains(out, want) {
			t.Fatalf("log missing %q; got: %s", want, out)
		}
	}
}

func TestSafeGoNilFnIsNoop(t *testing.T) {
	t.Parallel()
	SafeGo(context.Background(), nil, "test.nilFn", nil)
}

func TestSafeGoNilCtxDefaultsBackground(t *testing.T) {
	t.Parallel()
	done := make(chan struct{})
	SafeGo(nil, nil, "test.nilCtx", func(ctx context.Context) { //nolint:staticcheck // testing nil-ctx defense
		defer close(done)
		if ctx == nil {
			t.Error("nil ctx should have been replaced by context.Background()")
		}
	})
	<-done
}

type lockedWriter struct {
	buf *bytes.Buffer
	mu  *sync.Mutex
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func contains(haystack, needle string) bool {
	return bytes.Contains([]byte(haystack), []byte(needle))
}
