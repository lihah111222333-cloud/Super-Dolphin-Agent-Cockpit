package archtest

import (
	"os"
	"strings"
	"testing"
)

// TestObservabilityLogEventAnchorsWired enforces P22 P4 S6a /
// plan §321: the four stable log event anchors
// (bootstrap.hook_replay.begin/end, bootstrap.report_queue.drain,
// gopls.compat_fallback.hit) must remain wired into the production
// .go files that own their emit sites. Each anchor has an assigned
// producer file; an anchor missing from its producer means the
// observability contract regressed.
//
// The guard is source-shape based rather than runtime-based because
// the emission paths are driven by reconnect/drain/compat-fallback
// events that are difficult to synthesise cheaply from a unit test.
// Pinning the literal to its producer file gives the same freeze
// with zero runtime cost and a clear diff when the contract moves.
func TestObservabilityLogEventAnchorsWired(t *testing.T) {
	anchors := []struct {
		producerPath string
		anchors      []string
	}{
		{
			producerPath: "../../internal/mcpserver/common/bootstrap/hooks.go",
			anchors: []string{
				"\"bootstrap.hook_replay.begin\"",
				"\"bootstrap.hook_replay.end\"",
			},
		},
		{
			producerPath: "../../internal/mcpserver/common/bootstrap/report_queue.go",
			anchors: []string{
				"\"bootstrap.report_queue.drain\"",
			},
		},
		{
			producerPath: "../../cmd/mcp-lsp/multilsp/transport_compat.go",
			anchors: []string{
				"\"gopls.compat_fallback.hit\"",
			},
		},
	}

	for _, spec := range anchors {
		data, err := os.ReadFile(spec.producerPath)
		if err != nil {
			t.Fatalf("read %s: %v", spec.producerPath, err)
		}
		text := string(data)
		for _, anchor := range spec.anchors {
			if !strings.Contains(text, anchor) {
				t.Errorf("%s: expected observability anchor %s to stay wired (P22 P4 S6a / plan §321)", spec.producerPath, anchor)
			}
			// Also require the "event" key to be used at least once
			// in the producer so the anchor isn't present purely in a
			// comment or string literal disconnected from slog-style
			// key/value logging. A single check per file is enough
			// because the emit sites all share the same shape.
			if !strings.Contains(text, "\"event\",") {
				t.Errorf("%s: expected slog-style \"event\", <anchor> emission shape to stay wired", spec.producerPath)
			}
		}
	}
}
