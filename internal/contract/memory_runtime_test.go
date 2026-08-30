package contract

import (
	"errors"
	"strings"
	"testing"
)

// TestMemoryRuntimeCapabilityValidUsesClosedWhitelist 验证 runtime capability 只接受契约声明的固定值。
func TestMemoryRuntimeCapabilityValidUsesClosedWhitelist(t *testing.T) {
	tests := []struct {
		name       string
		capability MemoryRuntimeCapability
		want       bool
	}{
		{name: "prompt", capability: MemoryRuntimePromptCapability, want: true},
		{name: "turn context", capability: MemoryRuntimeTurnContextCapability, want: true},
		{name: "empty", capability: "", want: false},
		{name: "unknown", capability: "memory.private", want: false},
		{name: "trimmed known value", capability: " memory.prompt ", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.capability.Valid(); got != tt.want {
				t.Fatalf("MemoryRuntimeCapability(%q).Valid() = %v, want %v", tt.capability, got, tt.want)
			}
		})
	}
}

// TestMemoryRuntimeUnsupportedErrorRedactsUnknownCapability 验证未知 capability 不会进入错误文本。
func TestMemoryRuntimeUnsupportedErrorRedactsUnknownCapability(t *testing.T) {
	secret := "/private/tenant-token\naccount@example.test"
	err := NewMemoryRuntimeUnsupported(MemoryRuntimeCapability(secret))

	if !errors.Is(err, ErrMemoryRuntimeUnsupported) {
		t.Fatalf("errors.Is(%v, ErrMemoryRuntimeUnsupported) = false", err)
	}
	var typed MemoryRuntimeUnsupportedError
	if !errors.As(err, &typed) {
		t.Fatalf("errors.As(%v, *MemoryRuntimeUnsupportedError) = false", err)
	}
	if typed.Capability != MemoryRuntimeCapability(secret) {
		t.Fatalf("typed Capability = %q, want original value retained for compatibility", typed.Capability)
	}

	got := err.Error()
	want := "memory runtime unsupported: capability_unknown"
	if got != want {
		t.Fatalf("error text = %q, want %q", got, want)
	}
	if strings.Contains(got, secret) || strings.ContainsAny(got, "\r\n") {
		t.Fatalf("error text leaked capability or control characters: %q", got)
	}
}

// TestMemoryRuntimeUnsupportedErrorAllowsKnownCapabilityCode 验证白名单能力使用稳定、可诊断的错误格式。
func TestMemoryRuntimeUnsupportedErrorAllowsKnownCapabilityCode(t *testing.T) {
	for _, capability := range []MemoryRuntimeCapability{
		MemoryRuntimePromptCapability,
		MemoryRuntimeTurnContextCapability,
	} {
		err := NewMemoryRuntimeUnsupported(capability)
		want := "memory runtime unsupported: " + string(capability)
		if got := err.Error(); got != want {
			t.Fatalf("capability %q error text = %q, want %q", capability, got, want)
		}
	}
}

// TestMemoryRuntimeUnsupportedErrorEmptyCapabilityPreservesSentinel 验证空 capability 仍保持旧 sentinel 文本兼容。
func TestMemoryRuntimeUnsupportedErrorEmptyCapabilityPreservesSentinel(t *testing.T) {
	err := NewMemoryRuntimeUnsupported("")
	if got, want := err.Error(), ErrMemoryRuntimeUnsupported.Error(); got != want {
		t.Fatalf("empty capability error text = %q, want %q", got, want)
	}
}
