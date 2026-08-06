package common

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/toolbridge/mcpwire"
)

func TestTransportsDoNotEchoUnsupportedProtocolVersion(t *testing.T) {
	const request = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2099-01-01","capabilities":{},"clientInfo":{"name":"future-client"}}}`
	tests := []struct {
		name string
		http bool
	}{
		{name: "stdio"},
		{name: "http", http: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := runProtocolRequest(t, request, tt.http)
			var response struct {
				Error  *jsonRPCError `json:"error,omitempty"`
				Result struct {
					ProtocolVersion string `json:"protocolVersion"`
				} `json:"result"`
			}
			decodeJSONRPCOutput(t, raw, &response)
			if response.Error != nil {
				t.Fatalf("initialize error = %#v; raw=%s", response.Error, raw)
			}
			if response.Result.ProtocolVersion != mcpwire.LatestProtocolVersion {
				t.Fatalf("protocolVersion = %q, want %q; raw=%s", response.Result.ProtocolVersion, mcpwire.LatestProtocolVersion, raw)
			}
		})
	}
}

func TestTransportsRejectUnsafeProtocolVersion(t *testing.T) {
	const request = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25\r\nX-Evil: yes"}}`
	for _, httpTransport := range []bool{false, true} {
		raw := runProtocolRequest(t, request, httpTransport)
		assertJSONRPCError(t, raw, codeInvalidParams, "control characters")
	}
}

func TestTransportsDistinguishInvalidRequestFromParseError(t *testing.T) {
	tests := []struct {
		name     string
		payload  string
		wantCode int
	}{
		{name: "array", payload: `[]`, wantCode: codeInvalidReq},
		{name: "string", payload: `"request"`, wantCode: codeInvalidReq},
		{name: "number", payload: `1`, wantCode: codeInvalidReq},
		{name: "null", payload: `null`, wantCode: codeInvalidReq},
		{name: "malformed", payload: `{"jsonrpc":`, wantCode: codeParseError},
	}
	for _, tt := range tests {
		for _, httpTransport := range []bool{false, true} {
			name := tt.name + "/stdio"
			if httpTransport {
				name = tt.name + "/http"
			}
			t.Run(name, func(t *testing.T) {
				raw := runProtocolRequest(t, tt.payload, httpTransport)
				var response struct {
					Error *jsonRPCError `json:"error,omitempty"`
				}
				if err := json.Unmarshal(bytes.TrimSpace(raw), &response); err != nil {
					t.Fatalf("decode response %q: %v", raw, err)
				}
				if response.Error == nil || response.Error.Code != tt.wantCode {
					t.Fatalf("error = %#v, want code %d; raw=%s", response.Error, tt.wantCode, raw)
				}
			})
		}
	}
}

func TestFramedStdioMalformedJSONReturnsParseError(t *testing.T) {
	var input bytes.Buffer
	input.WriteString(framedPayload(`{"jsonrpc":`))
	var output bytes.Buffer

	server := newTestServer("test", "dev", NewStdioTransport(&input, &output), testToolProvider{})
	if err := server.Run(context.Background()); err == nil {
		t.Fatal("Run() error = nil, want malformed framed JSON to close the connection")
	}
	response, err := NewStdioTransport(bytes.NewReader(output.Bytes()), &bytes.Buffer{}).ReadMessage()
	if err != nil {
		t.Fatalf("read framed parse-error response: %v", err)
	}
	assertJSONRPCError(t, response, codeParseError, "parse error")
}

func runProtocolRequest(t *testing.T, payload string, httpTransport bool) []byte {
	t.Helper()
	if httpTransport {
		server := newTestHTTPServer("test", "dev", testToolProvider{})
		rec := httptest.NewRecorder()
		server.handleMCP(rec, httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(payload)))
		return append([]byte(nil), rec.Body.Bytes()...)
	}
	var output bytes.Buffer
	server := newTestServer("test", "dev", NewStdioTransport(strings.NewReader(payload), &output), testToolProvider{})
	if err := server.Run(context.Background()); err != nil && json.Valid([]byte(payload)) {
		t.Fatalf("Run() error = %v", err)
	}
	return append([]byte(nil), output.Bytes()...)
}
