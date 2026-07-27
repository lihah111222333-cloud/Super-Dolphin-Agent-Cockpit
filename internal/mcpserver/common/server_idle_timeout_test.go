package common

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"
)

func TestServerIdleTimeoutClosesBlockedStdioRead(t *testing.T) {
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close() })
	server := NewServer(
		"idle-test",
		"dev",
		NewStdioTransport(reader, &bytes.Buffer{}),
		nil,
		WithIdleTimeout(20*time.Millisecond),
	)
	done := make(chan error, 1)
	var waiters sync.WaitGroup
	waiters.Go(func() {
		done <- server.Run(context.Background())
	})
	defer waiters.Wait()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not exit after idle timeout")
	}
}
