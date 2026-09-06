package eventstore

import (
	"errors"
	"strings"
	"testing"
)

func TestCanonicalCursorPreservesWireFormat(t *testing.T) {
	tests := []struct {
		name        string
		sourceID    string
		sourceEpoch string
		lane        Lane
		sequence    int64
		want        string
	}{
		{name: "plain source", sourceID: "source", sourceEpoch: "epoch", lane: LaneControl, sequence: 1, want: "source/epoch/control/1"},
		{name: "task source", sourceID: "task/agent-alpha", sourceEpoch: "incarnation-alpha", lane: LaneOutput, sequence: 42, want: "task/agent-alpha/incarnation-alpha/output/42"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CanonicalCursor(tt.sourceID, tt.sourceEpoch, tt.lane, tt.sequence)
			if err != nil {
				t.Fatalf("CanonicalCursor() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("CanonicalCursor() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCanonicalCursorRejectsInvalidTuple(t *testing.T) {
	tests := []struct {
		name        string
		sourceID    string
		sourceEpoch string
		lane        Lane
		sequence    int64
	}{
		{name: "empty source", sourceEpoch: "epoch", lane: LaneControl, sequence: 1},
		{name: "trimmed source", sourceID: " source", sourceEpoch: "epoch", lane: LaneControl, sequence: 1},
		{name: "control source", sourceID: "source\nvalue", sourceEpoch: "epoch", lane: LaneControl, sequence: 1},
		{name: "oversized source", sourceID: strings.Repeat("s", 257), sourceEpoch: "epoch", lane: LaneControl, sequence: 1},
		{name: "empty epoch", sourceID: "source", lane: LaneControl, sequence: 1},
		{name: "nul epoch", sourceID: "source", sourceEpoch: "epoch\x00bad", lane: LaneControl, sequence: 1},
		{name: "control epoch", sourceID: "source", sourceEpoch: "epoch\tvalue", lane: LaneControl, sequence: 1},
		{name: "oversized epoch", sourceID: "source", sourceEpoch: strings.Repeat("e", 129), lane: LaneControl, sequence: 1},
		{name: "unknown lane", sourceID: "source", sourceEpoch: "epoch", lane: Lane("unknown"), sequence: 1},
		{name: "zero sequence", sourceID: "source", sourceEpoch: "epoch", lane: LaneControl},
		{name: "negative sequence", sourceID: "source", sourceEpoch: "epoch", lane: LaneControl, sequence: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CanonicalCursor(tt.sourceID, tt.sourceEpoch, tt.lane, tt.sequence)
			if !errors.Is(err, ErrInvalidEvent) {
				t.Fatalf("CanonicalCursor() error = %v, want ErrInvalidEvent", err)
			}
			if got != "" {
				t.Fatalf("CanonicalCursor() returned unsafe value %q", got)
			}
		})
	}
}
