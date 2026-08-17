package multilsp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

// TestTransportServerNotificationHook 保证通用 seam 的优先级、错误语义和并发回发。
func TestTransportServerNotificationHook(t *testing.T) {
	t.Run("standard notification takes priority", func(t *testing.T) {
		standard := &serverNotificationTestHandler{}
		called := false
		transport := newServerNotificationTestTransport(standard, func(context.Context, string, json.RawMessage, ServerNotificationSender) error {
			called = true
			return nil
		})
		payload := serverNotificationTestPayload(t, protocol.MethodLogMessage, protocol.LogMessageParams{
			Type:    protocol.LogMessageInfo,
			Message: "standard",
		})
		if err := transport.handleNotification(payload); err != nil {
			t.Fatalf("handleNotification() error = %v", err)
		}
		if standard.logMessages != 1 || called {
			t.Fatalf("standard handler count = %d, custom called = %v; want 1, false", standard.logMessages, called)
		}
	})

	t.Run("custom notification forwards and sender is serialized", func(t *testing.T) {
		writer := &serverNotificationTestWriteCloser{}
		var gotMethod string
		var gotParams json.RawMessage
		transport := newServerNotificationTestTransportWithWriter(writer, nil, func(_ context.Context, method string, params json.RawMessage, send ServerNotificationSender) error {
			gotMethod = method
			gotParams = append(json.RawMessage(nil), params...)
			var group sync.WaitGroup
			for index := 0; index < 4; index++ {
				group.Add(1)
				go func(index int) {
					defer group.Done()
					if err := send(context.Background(), "custom/response", map[string]int{"index": index}); err != nil {
						t.Errorf("sender() error = %v", err)
					}
				}(index)
			}
			group.Wait()
			return nil
		})
		payload := serverNotificationTestPayload(t, "custom/request", map[string]string{"value": "ok"})
		if err := transport.handleNotification(payload); err != nil {
			t.Fatalf("handleNotification() error = %v", err)
		}
		if gotMethod != "custom/request" || string(gotParams) != `{"value":"ok"}` {
			t.Fatalf("custom hook = method %q params %s", gotMethod, gotParams)
		}
		messages := decodeServerNotificationTestFrames(t, writer.Bytes())
		if len(messages) != 4 {
			t.Fatalf("sender wrote %d framed notifications, want 4", len(messages))
		}
		for _, message := range messages {
			if message.Method != "custom/response" {
				t.Fatalf("sender method = %q, want custom/response", message.Method)
			}
		}
	})

	t.Run("unsupported is ignored but other errors propagate", func(t *testing.T) {
		unsupported := newServerNotificationTestTransport(nil, func(context.Context, string, json.RawMessage, ServerNotificationSender) error {
			return ErrMethodNotSupported
		})
		if err := unsupported.handleNotification(serverNotificationTestPayload(t, "custom/ignored", nil)); err != nil {
			t.Fatalf("unsupported notification error = %v, want nil", err)
		}

		want := errors.New("custom notification failed")
		failed := newServerNotificationTestTransport(nil, func(context.Context, string, json.RawMessage, ServerNotificationSender) error {
			return want
		})
		if err := failed.handleNotification(serverNotificationTestPayload(t, "custom/failed", nil)); !errors.Is(err, want) {
			t.Fatalf("custom notification error = %v, want %v", err, want)
		}
	})
}

type serverNotificationTestHandler struct {
	logMessages int
}

func (h *serverNotificationTestHandler) PublishDiagnostics(protocol.PublishDiagnosticsParams) error {
	return nil
}

func (h *serverNotificationTestHandler) LogMessage(protocol.LogMessageParams) error {
	h.logMessages++
	return nil
}

type serverNotificationTestWriteCloser struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (w *serverNotificationTestWriteCloser) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.Write(data)
}

func (*serverNotificationTestWriteCloser) Close() error { return nil }

func (w *serverNotificationTestWriteCloser) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]byte(nil), w.buffer.Bytes()...)
}

func newServerNotificationTestTransport(
	standard protocol.NotificationHandler,
	custom ServerNotificationHandler,
) *transport {
	return newServerNotificationTestTransportWithWriter(&serverNotificationTestWriteCloser{}, standard, custom)
}

func newServerNotificationTestTransportWithWriter(
	writer *serverNotificationTestWriteCloser,
	standard protocol.NotificationHandler,
	custom ServerNotificationHandler,
) *transport {
	return &transport{
		stdin:                     writer,
		actorCtx:                  context.Background(),
		notificationHandler:       standard,
		serverNotificationHandler: custom,
	}
}

func serverNotificationTestPayload(t *testing.T, method string, params any) json.RawMessage {
	t.Helper()
	notification, err := protocol.BuildNotification(method, params)
	if err != nil {
		t.Fatalf("BuildNotification() error = %v", err)
	}
	payload, err := protocol.EncodeMessage(notification)
	if err != nil {
		t.Fatalf("EncodeMessage() error = %v", err)
	}
	return payload
}

func decodeServerNotificationTestFrames(t *testing.T, data []byte) []protocol.Envelope {
	t.Helper()
	var messages []protocol.Envelope
	for len(data) > 0 {
		separator := bytes.Index(data, []byte("\r\n\r\n"))
		if separator < 0 {
			t.Fatalf("framed output has no header separator: %q", data)
		}
		length := 0
		for _, line := range strings.Split(string(data[:separator]), "\r\n") {
			if strings.HasPrefix(strings.ToLower(line), "content-length:") {
				parsed, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(strings.ToLower(line), "content-length:")))
				if err != nil {
					t.Fatalf("parse Content-Length %q: %v", line, err)
				}
				length = parsed
			}
		}
		start := separator + len("\r\n\r\n")
		if length < 0 || start+length > len(data) {
			t.Fatalf("invalid framed payload length %d for %d bytes", length, len(data)-start)
		}
		message, err := protocol.DecodeEnvelope(data[start : start+length])
		if err != nil {
			t.Fatalf("DecodeEnvelope() error = %v", err)
		}
		messages = append(messages, message)
		data = data[start+length:]
	}
	return messages
}
