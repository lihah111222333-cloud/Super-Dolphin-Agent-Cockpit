package toolbridge

import (
	"context"
	"encoding/json"
	"testing"
)

func TestStdioMCPClientRequestSkipsNotificationsUntilMatchingResponse(t *testing.T) {
	transport := &fakeStdioTransport{reads: []json.RawMessage{
		json.RawMessage(`{"jsonrpc":"2.0","method":"notifications/progress","params":{"progressToken":"p1"}}`),
		json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`),
	}}
	client := &stdioMCPClient{transport: transport}

	raw, err := client.request(context.Background(), "tools/list", map[string]any{})
	if err != nil {
		t.Fatalf("request() error = %v", err)
	}
	if string(raw) != `{"ok":true}` {
		t.Fatalf("request() result = %s, want {\"ok\":true}", raw)
	}
	if len(transport.writes) != 1 {
		t.Fatalf("writes = %d, want 1", len(transport.writes))
	}
}

type fakeStdioTransport struct {
	reads  []json.RawMessage
	writes []any
}

func (t *fakeStdioTransport) ReadMessage() (json.RawMessage, error) {
	next := t.reads[0]
	t.reads = t.reads[1:]
	return next, nil
}

func (t *fakeStdioTransport) WriteMessage(payload any) error {
	t.writes = append(t.writes, payload)
	return nil
}
