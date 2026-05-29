package codexapp

import (
	"strings"
	"testing"
)

func TestPeerProcessEnvRequiresParentSidecarRuntimeContract(t *testing.T) {
	_, err := peerProcessEnv("mcp-orch", []string{"PATH=/bin", "GO_AGENT_CTL_SESSION_TOKEN=test-peer-token"}, nil)
	if err == nil || !strings.Contains(err.Error(), "parent sidecar runtime contract") {
		t.Fatalf("peerProcessEnv() error = %v, want parent sidecar runtime contract failure", err)
	}
}

func TestPeerProcessEnvConsumesDevSidecarRuntimeContract(t *testing.T) {
	env, err := peerProcessEnv("mcp-orch", testPeerParentEnv(), nil)
	if err != nil {
		t.Fatalf("peerProcessEnv() error = %v", err)
	}
	requireEnvValue(t, env, "SUPER_DOLPHIN_RUNTIME_MODE", "dev")
	requireEnvValue(t, env, "SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR", "/work/repo")
}

func TestPeerProcessEnvPreservesPackagedSidecarRuntimeContract(t *testing.T) {
	env, err := peerProcessEnv("mcp-orch", []string{
		"PATH=/bin",
		"GO_AGENT_CTL_SESSION_TOKEN=test-peer-token",
		"SUPER_DOLPHIN_RUNTIME_MODE=packaged",
		"SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR=/Applications/Super Dolphin.app/Contents/Resources",
	}, nil)
	if err != nil {
		t.Fatalf("peerProcessEnv() error = %v", err)
	}
	requireEffectiveEnvValue(t, env, "SUPER_DOLPHIN_RUNTIME_MODE", "packaged")
	requireEffectiveEnvValue(t, env, "SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR", "/Applications/Super Dolphin.app/Contents/Resources")
}

func requireEffectiveEnvValue(t *testing.T, env []string, key, want string) {
	t.Helper()
	got, ok := lookupEnvValue(env, key)
	if !ok {
		t.Fatalf("%s missing from env", key)
	}
	if got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}
