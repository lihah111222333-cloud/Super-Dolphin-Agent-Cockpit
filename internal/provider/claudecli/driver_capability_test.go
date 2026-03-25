package claudecli

import (
	"context"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

type stubRuntimeReporter struct {
	last  contract.RuntimeReport
	calls int
	err   error
}

func (s *stubRuntimeReporter) ReportRuntime(_ context.Context, report contract.RuntimeReport) error {
	s.calls++
	s.last = report
	return s.err
}

func TestNewDriverDefaultsLoggerAndBinaryPath(t *testing.T) {
	t.Parallel()

	got, ok := newDriver(nil, nil, nil).(*driver)
	if !ok {
		t.Fatalf("newDriver() type = %T, want *driver", newDriver(nil, nil, nil))
	}
	if got.logger == nil {
		t.Fatal("newDriver() logger = nil")
	}
	if got.binaryPath == "" {
		t.Fatal("newDriver() binaryPath = empty")
	}
	if got.Name() != "claude" {
		t.Fatalf("Name() = %q, want claude", got.Name())
	}
}

func TestNewDriverFactoryCreateReturnsClaudeDriver(t *testing.T) {
	t.Parallel()

	factory := NewDriverFactory(nil, nil, nil)
	if factory.Name != "claude" {
		t.Fatalf("factory.Name = %q, want claude", factory.Name)
	}
	first, ok := factory.Create().(*driver)
	if !ok {
		t.Fatalf("factory.Create() type = %T, want *driver", factory.Create())
	}
	second, ok := factory.Create().(*driver)
	if !ok {
		t.Fatalf("factory.Create() second type = %T, want *driver", factory.Create())
	}
	if first == second {
		t.Fatal("factory.Create() returned the same driver instance twice")
	}
}

func TestDriverReportRuntimeUsesProviderWithoutPort(t *testing.T) {
	t.Parallel()

	reporter := &stubRuntimeReporter{}
	got := newDriver(nil, nil, reporter).(*driver)
	got.reportRuntime(" agent-1 ")
	if reporter.calls != 1 {
		t.Fatalf("ReportRuntime() calls = %d, want 1", reporter.calls)
	}
	if reporter.last.AgentID != "agent-1" {
		t.Fatalf("AgentID = %q, want agent-1", reporter.last.AgentID)
	}
	if reporter.last.Provider != "claude" {
		t.Fatalf("Provider = %q, want claude", reporter.last.Provider)
	}
	if reporter.last.Port != 0 {
		t.Fatalf("Port = %d, want 0 for stdio transport", reporter.last.Port)
	}
}

func TestSessionCapabilitiesMatchClaudeDeclaration(t *testing.T) {
	t.Parallel()

	s := &session{caps: copyCapabilities(claudeCapabilities)}
	got := s.Capabilities()
	if len(got) != len(claudeCapabilities) {
		t.Fatalf("len(Capabilities()) = %d, want %d", len(got), len(claudeCapabilities))
	}
	for cap, want := range claudeCapabilities {
		if got[cap] != want {
			t.Fatalf("Capabilities()[%q] = %v, want %v", cap, got[cap], want)
		}
	}
}

func TestSessionCapabilitiesReturnsClone(t *testing.T) {
	t.Parallel()

	s := &session{caps: copyCapabilities(claudeCapabilities)}
	got := s.Capabilities()
	got[dto.CapMessageSend] = false
	if !s.caps.Has(dto.CapMessageSend) {
		t.Fatal("Capabilities() returned aliased map")
	}
}

func TestClaudeDeclarationOmitsContextCompact(t *testing.T) {
	t.Parallel()

	if claudeCapabilities.Has(dto.CapContextCompact) {
		t.Fatalf("claudeCapabilities unexpectedly declares %q", dto.CapContextCompact)
	}
}
