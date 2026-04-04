package shared

import (
	"encoding/json"
	"testing"
	"time"
)

func TestParseRFC3339Loose(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want time.Time
	}{
		{
			name: "nano",
			raw:  "2026-04-04T10:11:12.123456789Z",
			want: time.Date(2026, 4, 4, 10, 11, 12, 123456789, time.UTC),
		},
		{
			name: "plain",
			raw:  "2026-04-04T10:11:12Z",
			want: time.Date(2026, 4, 4, 10, 11, 12, 0, time.UTC),
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ParseRFC3339Loose(tt.raw); !got.Equal(tt.want) {
				t.Fatalf("ParseRFC3339Loose(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
	if got := ParseRFC3339Loose("invalid"); !got.IsZero() {
		t.Fatalf("invalid parse = %v", got)
	}
}

func TestDecodeHistoryMetadata(t *testing.T) {
	t.Parallel()

	if got := DecodeHistoryMetadata(nil); got != nil {
		t.Fatalf("nil metadata = %#v", got)
	}
	if got := DecodeHistoryMetadata(json.RawMessage("null")); got != nil {
		t.Fatalf("null metadata = %#v", got)
	}
	if got := DecodeHistoryMetadata(json.RawMessage("{")); got != nil {
		t.Fatalf("invalid metadata = %#v", got)
	}
	if got := DecodeHistoryMetadata(json.RawMessage(`{}`)); got != nil {
		t.Fatalf("empty metadata = %#v", got)
	}
	got := DecodeHistoryMetadata(json.RawMessage(`{"kind":"history","count":2}`))
	if got["kind"] != "history" || got["count"] != float64(2) {
		t.Fatalf("decoded metadata = %#v", got)
	}
}
