package mcpwire

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNegotiateProtocolVersionDoesNotEchoUnsupportedVersion(t *testing.T) {
	got, err := NegotiateProtocolVersion("2099-01-01")
	if err != nil {
		t.Fatalf("NegotiateProtocolVersion() error = %v", err)
	}
	if got != LatestProtocolVersion {
		t.Fatalf("NegotiateProtocolVersion() = %q, want %q", got, LatestProtocolVersion)
	}
}

func TestNegotiateProtocolVersionRejectsUnsafeValue(t *testing.T) {
	for _, value := range []string{"", " " + LatestProtocolVersion, LatestProtocolVersion + "\r\nX-Evil: yes"} {
		t.Run(strings.ReplaceAll(value, "\n", `\n`), func(t *testing.T) {
			if _, err := NegotiateProtocolVersion(value); err == nil {
				t.Fatalf("NegotiateProtocolVersion(%q) error = nil", value)
			}
		})
	}
}

func TestDecodeInitializeProtocolVersionStrict(t *testing.T) {
	tests := []struct {
		name    string
		raw     json.RawMessage
		want    string
		wantErr bool
	}{
		{name: "latest", raw: json.RawMessage(`{"protocolVersion":"2025-11-25"}`), want: LatestProtocolVersion},
		{name: "legacy", raw: json.RawMessage(`{"protocolVersion":"2024-11-05"}`), want: LegacyProtocolVersion},
		{name: "missing", raw: json.RawMessage(`{"capabilities":{}}`), wantErr: true},
		{name: "wrong type", raw: json.RawMessage(`{"protocolVersion":1}`), wantErr: true},
		{name: "unsupported", raw: json.RawMessage(`{"protocolVersion":"2099-01-01"}`), wantErr: true},
		{name: "control", raw: json.RawMessage(`{"protocolVersion":"2025-11-25\r\nX-Evil: yes"}`), wantErr: true},
		{name: "array", raw: json.RawMessage(`[]`), wantErr: true},
		{name: "null", raw: json.RawMessage(`null`), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeInitializeProtocolVersion(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("DecodeInitializeProtocolVersion(%s) error = nil", tt.raw)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("DecodeInitializeProtocolVersion(%s) = %q, %v; want %q", tt.raw, got, err, tt.want)
			}
		})
	}
}
