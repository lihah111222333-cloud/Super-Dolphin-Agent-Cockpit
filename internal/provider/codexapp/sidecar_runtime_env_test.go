package codexapp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPeerProcessEnvRequiresParentSidecarRuntimeContract(t *testing.T) {
	_, err := peerProcessEnv("mcp-orch", []string{"PATH=/bin", "GO_AGENT_CTL_SESSION_TOKEN=test-peer-token"}, nil)
	if err == nil || !strings.Contains(err.Error(), "parent sidecar runtime contract") {
		t.Fatalf("peerProcessEnv() error = %v, want parent sidecar runtime contract failure", err)
	}
}

func TestPeerProcessEnvRejectsProjectRootFallbackWhenRuntimeModeMissing(t *testing.T) {
	_, err := peerProcessEnv("mcp-orch", []string{
		"PATH=/bin",
		"GO_AGENT_CTL_SESSION_TOKEN=test-peer-token",
		"PROJECT_ROOT=/work/repo",
		"SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR=/work/repo",
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "missing SUPER_DOLPHIN_RUNTIME_MODE") {
		t.Fatalf("peerProcessEnv() error = %v, want missing runtime mode failure", err)
	}
}

func TestPeerProcessEnvRejectsProjectRootFallbackWhenResourcesMissing(t *testing.T) {
	_, err := peerProcessEnv("mcp-orch", []string{
		"PATH=/bin",
		"GO_AGENT_CTL_SESSION_TOKEN=test-peer-token",
		"PROJECT_ROOT=/work/repo",
		"SUPER_DOLPHIN_RUNTIME_MODE=dev",
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "missing SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR") {
		t.Fatalf("peerProcessEnv() error = %v, want missing runtime resources failure", err)
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

func TestPeerProcessEnvConfiguredMcpLSPWorkspaceRootsOverrideStaleParentEnv(t *testing.T) {
	staleRoot := t.TempDir()
	currentRoot := t.TempDir()
	rawCurrentRoots := marshalTestRoots(t, currentRoot)
	rawStaleRoots := marshalTestRoots(t, staleRoot)

	env, err := peerProcessEnv("mcp-lsp", append(testPeerParentEnv(),
		"GO_AGENT_LSP_ROOT="+staleRoot,
		"GO_AGENT_LSP_ROOTS="+rawStaleRoots,
	), []string{currentRoot})
	if err != nil {
		t.Fatalf("peerProcessEnv() error = %v", err)
	}
	requireEnvValue(t, env, "GO_AGENT_LSP_ROOT", currentRoot)
	requireEnvValue(t, env, "GO_AGENT_LSP_ROOTS", rawCurrentRoots)
	requireEnvItemAbsent(t, env, "GO_AGENT_LSP_ROOT="+staleRoot)
	requireEnvItemAbsent(t, env, "GO_AGENT_LSP_ROOTS="+rawStaleRoots)
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

func marshalTestRoots(t *testing.T, roots ...string) string {
	t.Helper()
	raw, err := json.Marshal(roots)
	if err != nil {
		t.Fatalf("Marshal roots: %v", err)
	}
	return string(raw)
}

func requireEnvItemAbsent(t *testing.T, env []string, item string) {
	t.Helper()
	for _, candidate := range env {
		if candidate == item {
			t.Fatalf("env unexpectedly contains %q: %#v", item, env)
		}
	}
}
