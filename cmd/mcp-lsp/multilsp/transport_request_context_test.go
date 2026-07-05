package multilsp

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestTransportRequestWriteHonorsContext(t *testing.T) {
	if os.Getenv("MCP_LSP_BLOCK_STDIN_HELPER") == "1" {
		time.Sleep(30 * time.Second)
		return
	}

	tr, err := newTransport(transportOptions{
		Binary: os.Args[0],
		Args:   []string{"-test.run=^TestTransportRequestWriteHonorsContext$"},
		Env:    []string{"MCP_LSP_BLOCK_STDIN_HELPER=1"},
	})
	if err != nil {
		t.Fatalf("newTransport() error = %v", err)
	}
	t.Cleanup(func() { _ = tr.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		_, err := tr.request(ctx, "workspace/executeCommand", map[string]string{
			"payload": strings.Repeat("x", 32<<20),
		})
		errCh <- err
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("request() error = %v, want context deadline exceeded", err)
		}
	case <-time.After(2 * time.Second):
		_ = tr.killProcess()
		select {
		case err := <-errCh:
			t.Fatalf("request() returned only after forced process kill with error %v; want ctx deadline to cancel the blocked write", err)
		case <-time.After(2 * time.Second):
			t.Fatalf("request() stayed blocked after context deadline and forced transport close")
		}
	}

	select {
	case <-tr.done:
	case <-time.After(5 * time.Second):
		t.Fatalf("request() returned but LSP process stayed alive; want context cancellation to terminate the process")
	}
}
