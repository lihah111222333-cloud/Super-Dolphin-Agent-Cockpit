package common

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

var errBodyReadAttempted = errors.New("body read attempted")

type headerOnlyReader struct {
	header    string
	sent      bool
	bodyReads int
}

func (r *headerOnlyReader) Read(p []byte) (int, error) {
	if !r.sent {
		r.sent = true
		return copy(p, r.header), nil
	}
	r.bodyReads++
	return 0, errBodyReadAttempted
}

func TestStdioTransportRejectsOversizeFramedMessageBeforeBodyRead(t *testing.T) {
	reader := &headerOnlyReader{header: "Content-Length: 1048577\r\n\r\n"}
	transport := NewStdioTransport(reader, &bytes.Buffer{})

	_, err := transport.ReadMessage()
	if err == nil {
		t.Fatal("ReadMessage() error = nil, want oversize error")
	}
	if !strings.Contains(err.Error(), "exceeds stdio message limit") {
		t.Fatalf("ReadMessage() error = %v, want stdio message limit", err)
	}
	if reader.bodyReads != 0 {
		t.Fatalf("body reads = %d, want 0 before rejecting oversize Content-Length", reader.bodyReads)
	}
}

func TestStdioTransportRejectsOversizeRawMessage(t *testing.T) {
	var input bytes.Buffer
	input.WriteString(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"blob":"`)
	input.WriteString(strings.Repeat("x", 1048577))
	input.WriteString(`"}}`)
	transport := NewStdioTransport(&input, &bytes.Buffer{})

	_, err := transport.ReadMessage()
	if err == nil {
		t.Fatal("ReadMessage() error = nil, want oversize raw error")
	}
	if !strings.Contains(err.Error(), "exceeds stdio message limit") {
		t.Fatalf("ReadMessage() error = %v, want stdio message limit", err)
	}
}

func TestStdioTransportAcceptsSmallFramedMessage(t *testing.T) {
	payload := json.RawMessage(`{"ok":true}`)
	input := bytes.NewBufferString("Content-Length: 11\r\n\r\n" + string(payload))
	transport := NewStdioTransport(input, &bytes.Buffer{})

	got, err := transport.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage() error = %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("ReadMessage() = %s, want %s", got, payload)
	}
}
