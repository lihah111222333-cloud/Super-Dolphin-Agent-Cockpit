package common

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

var errBodyReadAttempted = errors.New("body read attempted")

type headerOnlyReader struct {
	header    string
	sent      bool
	bodyReads int
}

type countingReader struct {
	reader io.Reader
	read   int
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.read += n
	return n, err
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

func TestStdioTransportRejectsUnterminatedOversizeHeaderWithinReadBudget(t *testing.T) {
	input := &countingReader{reader: strings.NewReader(strings.Repeat("x", 64<<10))}
	transport := NewStdioTransport(input, &bytes.Buffer{})

	_, err := transport.ReadMessage()
	if err == nil || !strings.Contains(err.Error(), "header exceeds") {
		t.Fatalf("ReadMessage() error = %v, want classified oversized header", err)
	}
	if input.read > 16<<10 {
		t.Fatalf("reader consumed %d bytes, want no more than header budget", input.read)
	}
}

func TestStdioTransportAcceptsHeaderLineAtExactLimit(t *testing.T) {
	line := "X:" + strings.Repeat("x", MaxStdioHeaderLineBytes-len("X:\n")) + "\n"
	input := bytes.NewBufferString(line + "Content-Length: 2\r\n\r\n{}")

	got, err := NewStdioTransport(input, &bytes.Buffer{}).ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage() error = %v", err)
	}
	if string(got) != "{}" {
		t.Fatalf("ReadMessage() = %q, want {}", got)
	}
}

func TestStdioTransportRejectsOversizeFramedHeaderTotal(t *testing.T) {
	line := "X: " + strings.Repeat("x", MaxStdioHeaderLineBytes-len("X: \n")) + "\n"
	input := strings.NewReader(strings.Repeat(line, MaxStdioHeaderBytes/MaxStdioHeaderLineBytes+1))

	_, err := NewStdioTransport(input, &bytes.Buffer{}).ReadMessage()
	if !errors.Is(err, errStdioHeaderTooLarge) {
		t.Fatalf("ReadMessage() error = %v, want oversized header total", err)
	}
}

func TestStdioTransportRejectsTooManyFramedHeaderLines(t *testing.T) {
	header := strings.Repeat("X: y\r\n", MaxStdioHeaderLines+1) + "\r\n"

	_, err := NewStdioTransport(strings.NewReader(header), &bytes.Buffer{}).ReadMessage()
	if !errors.Is(err, errStdioHeaderTooLarge) {
		t.Fatalf("ReadMessage() error = %v, want oversized header line count", err)
	}
}

func TestStdioTransportRejectsDuplicateContentLength(t *testing.T) {
	input := strings.NewReader("Content-Length: 2\r\nContent-Length: 2\r\n\r\n{}")

	_, err := NewStdioTransport(input, &bytes.Buffer{}).ReadMessage()
	if err == nil || !strings.Contains(err.Error(), "duplicate Content-Length") {
		t.Fatalf("ReadMessage() error = %v, want duplicate Content-Length error", err)
	}
}
