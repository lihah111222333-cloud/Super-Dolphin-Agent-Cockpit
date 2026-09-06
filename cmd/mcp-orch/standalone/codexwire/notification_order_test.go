package codexwire

import (
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"
)

// TestCodexNotificationsKeepWireOrder 验证真实 client 的 delta 和 completion 不被异步分发重排。
func TestCodexNotificationsKeepWireOrder(t *testing.T) {
	reader, writer := io.Pipe()
	t.Cleanup(func() {
		if err := writer.Close(); err != nil {
			t.Errorf("close writer: %v", err)
		}
	})
	const count = 512
	observed := make(chan int, count)
	client := newClient(reader, &testWriteCloser{}, func(request *jrpc2.Request) {
		var event struct {
			Index int `json:"index"`
		}
		if err := request.UnmarshalParams(&event); err != nil {
			observed <- -1
			return
		}
		observed <- event.Index
	})
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close client: %v", err)
		}
	})
	writeNotifications(t, writer, count)
	assertNotificationOrder(t, observed, count)
}

func writeNotifications(t *testing.T, writer *io.PipeWriter, count int) {
	t.Helper()
	for index := range count {
		method := "item/agentMessage/delta"
		if index == count-1 {
			method = "turn/completed"
		}
		if _, err := fmt.Fprintf(writer, "{\"method\":%q,\"params\":{\"index\":%d}}\n", method, index); err != nil {
			t.Fatalf("write notification: %v", err)
		}
	}
}

func assertNotificationOrder(t *testing.T, observed <-chan int, count int) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for index := range count {
		select {
		case got := <-observed:
			if got != index {
				t.Fatalf("notification %d delivered at position %d", got, index)
			}
		case <-deadline.C:
			t.Fatalf("notification %d not delivered", index)
		}
	}
}
