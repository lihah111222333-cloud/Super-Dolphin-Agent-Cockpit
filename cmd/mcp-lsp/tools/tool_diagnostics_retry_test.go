package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func TestDiagnosticsReportsPartialBootstrapFailure(t *testing.T) {
	registry := &diagnosticsTestRegistry{
		bootstrapErrByURI: map[string]error{
			"file:///repo/bad.ts": errors.New("bootstrap boom"),
		},
	}
	handler := handlerBase{registry: registry}

	_, err := handler.reactiveBootstrap(context.Background(), []string{"file:///repo/bad.ts", "file:///repo/good.ts"})
	if err == nil || !strings.Contains(err.Error(), "bootstrap boom") || !strings.Contains(err.Error(), "bad.ts") {
		t.Fatalf("fetchDiagnosticsWithRetry() error = %v, want partial bootstrap failure", err)
	}
}

func TestDiagnosticsRecoversStartupWaitByBootstrappingTarget(t *testing.T) {
	root := t.TempDir()
	target := writeDiagnosticsFixture(t, root, "startup.go")
	registry := &diagnosticsTestRegistry{
		waitErrs: []error{lspmanager.ErrDiagnosticsNotReady},
	}
	handler := NewDiagnosticsHandler(Config{WorkspaceRoot: root, Registry: registry})
	req := marshalDiagnosticsInput(t, fileToolInput{Action: "diagnostics", FilePath: "startup.go"})

	if _, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), req); err != nil {
		t.Fatalf("diagnostics returned startup wait error: %v", err)
	}
	wantURI := canonicalFileURI(t, target)
	assertDiagnosticURIs(t, registry.bootstrapURIs, []string{wantURI, wantURI})
	if registry.waitCalls != 2 {
		t.Fatalf("WaitDiagnosticsStable calls = %d, want retry after startup bootstrap", registry.waitCalls)
	}
	if len(registry.callOrder) == 0 || registry.callOrder[len(registry.callOrder)-1] != "diagnostics" {
		t.Fatalf("diagnostics call order = %#v, want diagnostics after startup recovery", registry.callOrder)
	}
}

func TestDiagnosticsRetriesStartupWaitUntilFifthRetry(t *testing.T) {
	root := t.TempDir()
	target := writeDiagnosticsFixture(t, root, "slow.go")
	registry := &diagnosticsTestRegistry{
		waitErrs: []error{
			lspmanager.ErrDiagnosticsNotReady,
			lspmanager.ErrDiagnosticsNotReady,
			lspmanager.ErrDiagnosticsNotReady,
			lspmanager.ErrDiagnosticsNotReady,
			lspmanager.ErrDiagnosticsNotReady,
		},
	}
	handler := NewDiagnosticsHandler(Config{WorkspaceRoot: root, Registry: registry})
	req := marshalDiagnosticsInput(t, fileToolInput{Action: "diagnostics", FilePath: "slow.go"})

	if _, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), req); err != nil {
		t.Fatalf("diagnostics returned startup retry error: %v", err)
	}
	wantURI := canonicalFileURI(t, target)
	assertDiagnosticURIs(t, registry.bootstrapURIs, []string{wantURI, wantURI})
	if registry.waitCalls != 6 {
		t.Fatalf("WaitDiagnosticsStable calls = %d, want initial wait plus 5 retries", registry.waitCalls)
	}
}

func TestDiagnosticsReportsStartupTimeoutAfterFiveRetries(t *testing.T) {
	root := t.TempDir()
	target := writeDiagnosticsFixture(t, root, "never.go")
	registry := &diagnosticsTestRegistry{
		waitErrs: []error{
			lspmanager.ErrDiagnosticsNotReady,
			lspmanager.ErrDiagnosticsNotReady,
			lspmanager.ErrDiagnosticsNotReady,
			lspmanager.ErrDiagnosticsNotReady,
			lspmanager.ErrDiagnosticsNotReady,
			lspmanager.ErrDiagnosticsNotReady,
		},
	}
	handler := NewDiagnosticsHandler(Config{WorkspaceRoot: root, Registry: registry})
	req := marshalDiagnosticsInput(t, fileToolInput{Action: "diagnostics", FilePath: "never.go"})

	_, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), req)
	if err == nil || !errors.Is(err, lspmanager.ErrDiagnosticsNotReady) {
		t.Fatalf("diagnostics error = %v, want ErrDiagnosticsNotReady after retries", err)
	}
	wantURI := canonicalFileURI(t, target)
	assertDiagnosticURIs(t, registry.bootstrapURIs, []string{wantURI, wantURI})
	if registry.waitCalls != 6 {
		t.Fatalf("WaitDiagnosticsStable calls = %d, want initial wait plus 5 retries", registry.waitCalls)
	}
}

func TestDiagnosticsRetryBackoffSequence(t *testing.T) {
	tests := []struct {
		retry int
		want  time.Duration
	}{
		{retry: 0, want: 0},
		{retry: 1, want: 300 * time.Millisecond},
		{retry: 2, want: 600 * time.Millisecond},
		{retry: 3, want: 1200 * time.Millisecond},
		{retry: 4, want: 2400 * time.Millisecond},
		{retry: 5, want: 4800 * time.Millisecond},
	}

	for _, tt := range tests {
		if got := diagnosticsRetryBackoff(tt.retry); got != tt.want {
			t.Fatalf("diagnosticsRetryBackoff(%d) = %s, want %s", tt.retry, got, tt.want)
		}
	}
}

func TestDiagnosticsBatchReturnsPartialAfterStartupRetryMissesOneTarget(t *testing.T) {
	root := t.TempDir()
	first := writeDiagnosticsFixture(t, root, "ready.go")
	second := writeDiagnosticsFixture(t, root, "slow.go")
	firstURI := canonicalFileURI(t, first)
	secondURI := canonicalFileURI(t, second)
	registry := &diagnosticsTestRegistry{
		waitFn: func(_ int, uris []string) error {
			if len(uris) == 1 && uris[0] == firstURI {
				return nil
			}
			return fmt.Errorf("%w: diagnostics did not publish for requested targets before 1.5s: %s", lspmanager.ErrDiagnosticsNotReady, secondURI)
		},
	}
	handler := NewDiagnosticsHandler(Config{WorkspaceRoot: root, Registry: registry})
	req := marshalDiagnosticsInput(t, fileToolInput{Action: "diagnostics", FilePaths: []string{"ready.go", "slow.go"}})

	result, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), req)
	if err != nil {
		t.Fatalf("diagnostics returned batch readiness error: %v", err)
	}
	envelope, ok := result.(diagnosticsResponse)
	if !ok {
		t.Fatalf("diagnostics result = %T, want diagnosticsResponse for no diagnostic rows", result)
	}
	if !strings.Contains(envelope.Meta.Message, "partial") || !strings.Contains(envelope.Meta.Message, secondURI) {
		t.Fatalf("diagnostics message = %q, want partial warning for %s", envelope.Meta.Message, secondURI)
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal diagnostics response: %v", err)
	}
	if strings.Contains(string(raw), "source") {
		t.Fatalf("diagnostics response exposes source: %s", string(raw))
	}
	assertDiagnosticURIs(t, registry.bootstrapURIs, []string{firstURI, secondURI, firstURI, secondURI})
	if registry.waitCalls < 3 {
		t.Fatalf("WaitDiagnosticsStable calls = %d, want batch retry plus per-target wait", registry.waitCalls)
	}
}

func TestDiagnosticsPropagatesNonStartupWaitError(t *testing.T) {
	root := t.TempDir()
	target := writeDiagnosticsFixture(t, root, "broken.go")
	registry := &diagnosticsTestRegistry{
		waitErrs: []error{errors.New("diagnostic cache corrupt")},
	}
	handler := NewDiagnosticsHandler(Config{WorkspaceRoot: root, Registry: registry})
	req := marshalDiagnosticsInput(t, fileToolInput{Action: "diagnostics", FilePath: "broken.go"})

	_, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), req)
	if err == nil || !strings.Contains(err.Error(), "diagnostic cache corrupt") {
		t.Fatalf("diagnostics error = %v, want non-startup wait failure", err)
	}
	assertDiagnosticURIs(t, registry.bootstrapURIs, []string{canonicalFileURI(t, target)})
}
