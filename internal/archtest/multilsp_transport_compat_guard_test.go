package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMultiLSPTransportCompatFreeze enforces P22 P4 §309-311: the multilsp
// transport's server-request compatibility contract must live in a
// single authoritative file (cmd/mcp-lsp/multilsp/transport_compat.go),
// not inlined inside transport.go or elsewhere under cmd/mcp-lsp/multilsp.
//
// The guard pins every frozen method literal in the compat contract
// to the producer file. Any future entry must land in
// transport_compat.go so code review and downstream consumers see the
// contract change as a single-file diff.
func TestMultiLSPTransportCompatFreeze(t *testing.T) {
	const (
		dir      = "../../cmd/mcp-lsp/multilsp"
		producer = "transport_compat.go"
	)

	frozen := []string{
		"\"client/registerCapability\"",
		"\"client/unregisterCapability\"",
		"\"window/workDoneProgress/create\"",
		"\"workspace/configuration\"",
		"\"workspace/semanticTokens/refresh\"",
		"\"workspace/codeLens/refresh\"",
		"\"workspace/inlayHint/refresh\"",
		"\"workspace/diagnostic/refresh\"",
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if name == producer {
			continue
		}
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(data)
		for _, tok := range frozen {
			if strings.Contains(text, tok) {
				t.Errorf("%s: frozen LSP compat literal %s appears outside %s (P22 P4 §309-311: register the method + response shape in %s instead of inlining)", path, tok, producer, producer)
			}
		}
	}

	// Also verify transport_compat.go still contains each frozen
	// literal, so deletions don't silently weaken the freeze.
	data, err := os.ReadFile(filepath.Join(dir, producer))
	if err != nil {
		t.Fatalf("read %s: %v", producer, err)
	}
	text := string(data)
	for _, tok := range frozen {
		if !strings.Contains(text, tok) {
			t.Errorf("%s: expected frozen LSP compat literal %s to be present", producer, tok)
		}
	}
}
