//go:build windows && e2e

package lspplatform

import (
	"strings"
	"testing"
	"time"
)

func TestWindowsGoplsSharedDaemonAvoidsAutoEndpointE2E(t *testing.T) {
	if !GoplsUsesSharedDaemon() {
		t.Fatal("Windows gopls platform must declare a shared daemon")
	}
	args, err := GoplsServerArgs(time.Minute)
	if err != nil {
		t.Fatalf("GoplsServerArgs() error = %v", err)
	}
	for _, arg := range args {
		if arg == GoplsRemoteAutoArg || strings.HasPrefix(arg, GoplsRemoteAutoArg+";") {
			t.Fatalf("GoplsServerArgs() returned unsupported Windows auto endpoint %q", arg)
		}
	}
}
