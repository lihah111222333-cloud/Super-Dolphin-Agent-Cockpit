//go:build e2e

package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestStandaloneBinaryFullClaudeLifecycle is the explicit, binary-only Claude
// lifecycle selector.  It deliberately reuses the exercised MCP helpers (and
// never treats the control probe as model success), while additionally checking
// that the private state domain emitted a receipt containing source/identity
// and content evidence before the process was reaped.
func TestStandaloneBinaryFullClaudeLifecycle(t *testing.T) {
	cli, home, model := requireManagedProviderInputs(t, "CLAUDE")
	binary := buildServeE2EBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	t.Cleanup(cancel)
	fixture := writeManagedServeE2EFixture(t, "claude", cli, home, model)
	daemon := startServeE2E(t, ctx, binary, fixture)
	client := &http.Client{Timeout: 90 * time.Second}
	session, _ := initializeServeE2E(t, client, daemon.ready.Endpoint, fixture.token)
	launchManagedClaudeThroughMCP(t, client, daemon.ready.Endpoint, fixture, session, model)
	first := waitManagedClaudeReportThroughMCP(t, ctx, client, daemon.ready.Endpoint, fixture.token, session, "daemon-managed-claude", 0, "DAEMON_CLAUDE_TURN_OK")
	waitManagedClaudeRuntimeIdleThroughMCP(t, ctx, client, daemon.ready.Endpoint, fixture.token, session, "daemon-managed-claude")
	observed := assertStandaloneManagedProcessTree(t, daemon, cli)
	second := sendManagedClaudeFollowUpThroughMCP(t, client, daemon.ready.Endpoint, fixture.token, session)
	if second.ReportSeq <= first || !strings.Contains(second.Report, "DAEMON_CLAUDE_SECOND_OK") {
		t.Fatalf("follow-up receipt invalid: seq=%d first=%d report=%q", second.ReportSeq, first, second.Report)
	}
	if err := stopServeProcessE2E(t, daemon, nil); err != nil {
		t.Fatalf("stop binary: %v", err)
	}
	assertServeEndpointClosed(t, client, daemon.ready.Endpoint)
	// Receipt evidence is private-domain output; fail closed when absent rather
	// than converting an HTTP/control success into a model success.
	if !hasReceiptEvidence(fixture.state) {
		t.Fatalf("no source/identity/content receipt found under private state %s", fixture.state)
	}
	assertNoStandaloneManagedDescendants(t, observed)
}

func hasReceiptEvidence(root string) bool {
	found := false
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr == nil && receiptEvidenceText(string(raw)) {
			found = true
		}
		return nil
	})
	return found
}

func receiptEvidenceText(raw string) bool {
	text := strings.ToLower(raw)
	return strings.Contains(text, "source") && strings.Contains(text, "identity") && strings.Contains(text, "content")
}
