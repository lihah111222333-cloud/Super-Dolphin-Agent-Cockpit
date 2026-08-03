package multilsp

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestGoAdapterUsesResolvedIdleTimeoutForRemoteArg(t *testing.T) {
	registry := NewLanguageAdapterRegistryFromConfig(contract.LSPConfig{IdleTimeout: 2500 * time.Millisecond})
	adapter, ok := registry.AdapterForLanguage("go")
	if !ok {
		t.Fatal("missing go adapter")
	}
	command, err := adapter.ServerCommand(context.Background(), ResolvedLanguageScope{})
	if err != nil {
		t.Fatalf("ServerCommand() error = %v", err)
	}
	want := []string{goplsRemoteAutoArg, "-remote.listen.timeout=2.5s"}
	if !slices.Equal(command.Args, want) {
		t.Fatalf("gopls args = %#v, want %#v", command.Args, want)
	}
}

func TestGoAdapterRejectsMissingResolvedIdleTimeout(t *testing.T) {
	registry := NewLanguageAdapterRegistryFromConfig(contract.LSPConfig{})
	adapter, ok := registry.AdapterForLanguage("go")
	if !ok {
		t.Fatal("missing go adapter")
	}
	if _, err := adapter.ServerCommand(context.Background(), ResolvedLanguageScope{}); err == nil {
		t.Fatal("ServerCommand() error = nil, want missing effective idle timeout error")
	}
}

func TestGoAdapterRemovesSharedDaemonArgsOnWindows(t *testing.T) {
	args, err := goplsServerArgs(15*time.Minute, "windows")
	if err != nil {
		t.Fatalf("goplsServerArgs() error = %v", err)
	}
	if len(args) != 0 {
		t.Fatalf("Windows gopls args = %#v, want no shared-daemon args", args)
	}
}
