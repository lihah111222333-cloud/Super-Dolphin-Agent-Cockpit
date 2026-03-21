package claudecli

import (
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func TestNewDriverDefaultsLoggerAndBinaryPath(t *testing.T) {
	t.Parallel()

	got, ok := newDriver(nil, nil).(*driver)
	if !ok {
		t.Fatalf("newDriver() type = %T, want *driver", newDriver(nil, nil))
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

	factory := NewDriverFactory(nil, nil)
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
