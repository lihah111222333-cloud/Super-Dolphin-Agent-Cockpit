package codexapp

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/archtest"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
)

const testInternalSQLitePathEnvKey = "SUPER_DOLPHIN_INTERNAL_SQLITE_PATH"

func TestManagedAuthorityBootstrapRealEnvMapperConsumesEveryField(t *testing.T) {
	baseline := mcpdto.ManagedAuthorityBootstrap{
		InstanceID:      "managed:mcp-orch",
		BootID:          "boot-1",
		Token:           "token-1",
		ProtocolVersion: mcpdto.ManagedAuthorityProtocolVersion,
	}
	archtest.AssertWireDTOMapperConsumesProducerFieldsFrom(t, baseline, managedBootstrapOutput, nil, []archtest.WireDTOMapperProjection{
		{Field: "instance_id", ConsumerKey: "GO_AGENT_CTL_INSTANCE_ID", ExpectedOutput: managedBootstrapOutputAny},
		{Field: "boot_id", ConsumerKey: "GO_AGENT_CTL_BOOT_ID", ExpectedOutput: managedBootstrapOutputAny},
		{Field: "token", ConsumerKey: "GO_AGENT_CTL_MANAGED_TOKEN", ExpectedOutput: managedBootstrapOutputAny},
		{Field: "protocol_version", ConsumerKey: "GO_AGENT_CTL_MANAGED_PROTOCOL_VERSION", ExpectedOutput: managedBootstrapOutputAny},
	})
	archtest.AssertWireDTOMapperASTReferencesProducerFields[mcpdto.ManagedAuthorityBootstrap](
		t,
		"sidecar_runtime_env.go",
		"injectManagedPeerBootstrap",
		"managed",
	)
}

func managedBootstrapOutput(input mcpdto.ManagedAuthorityBootstrap) map[string]any {
	return managedBootstrapOutputAny(input)
}

func managedBootstrapOutputAny(input any) map[string]any {
	env, err := injectManagedPeerBootstrap(nil, input.(mcpdto.ManagedAuthorityBootstrap))
	output := map[string]any{"error": errorString(err)}
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			output[key] = value
		}
	}
	return output
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func TestPeerEnvStrictManagedActivationIsOrchOnly(t *testing.T) {
	root := t.TempDir()
	launcher := &execPeerLauncher{workspaceRoots: func() []string { return []string{root} }, ownerID: "task-a-sidecar"}
	parent := append(testPeerParentEnv(),
		"GO_AGENT_CTL_MANAGED_TOKEN=hostile-parent-token",
		"GO_AGENT_CTL_MANAGED_PROTOCOL_VERSION=hostile-parent-version",
	)
	issued := &mcpdto.ManagedAuthorityBootstrap{
		InstanceID:      "managed:mcp-orch",
		BootID:          "boot-1",
		Token:           "managed-token",
		ProtocolVersion: mcpdto.ManagedAuthorityProtocolVersion,
	}
	orchEnv, err := launcher.peerEnvForLaunch("mcp-orch", parent, issued)
	if err != nil {
		t.Fatalf("peerEnvForLaunch(mcp-orch) error = %v", err)
	}
	requireEnvValue(t, orchEnv, "GO_AGENT_CTL_MANAGED_TOKEN", issued.Token)
	requireEnvValue(t, orchEnv, "GO_AGENT_CTL_MANAGED_PROTOCOL_VERSION", issued.ProtocolVersion)
	raw, ok := lookupEnvValue(orchEnv, peerBootstrapJSONEnv)
	if !ok {
		t.Fatal("mcp-orch bootstrap JSON missing")
	}
	var boot map[string]string
	if err := json.Unmarshal([]byte(raw), &boot); err != nil {
		t.Fatalf("decode mcp-orch bootstrap JSON: %v", err)
	}
	if boot["instance_id"] != issued.InstanceID || boot["boot_id"] != issued.BootID {
		t.Fatalf("mcp-orch bootstrap identity = %#v", boot)
	}

	lspEnv, err := launcher.peerEnvForLaunch("mcp-lsp", parent, nil)
	if err != nil {
		t.Fatalf("peerEnvForLaunch(mcp-lsp) error = %v", err)
	}
	if _, ok := lookupEnvValue(lspEnv, "GO_AGENT_CTL_MANAGED_TOKEN"); ok {
		t.Fatal("mcp-lsp unexpectedly received managed token")
	}
	if _, ok := lookupEnvValue(lspEnv, "GO_AGENT_CTL_MANAGED_PROTOCOL_VERSION"); ok {
		t.Fatal("mcp-lsp unexpectedly received managed protocol version")
	}
	requireEnvValue(t, lspEnv, sidecarOwnerIDEnv, "task-a-sidecar")
}

func TestPeerEnvUsesDistinctOwnerIdentityWithoutChangingWorkspaceOrCache(t *testing.T) {
	parent := testPeerParentEnv()
	root := t.TempDir()
	a := &execPeerLauncher{workspaceRoots: func() []string { return []string{root} }, ownerID: "task-a"}
	b := &execPeerLauncher{workspaceRoots: func() []string { return []string{root} }, ownerID: "task-b"}
	aEnv, err := a.peerEnvForLaunch("mcp-lsp", parent, nil)
	if err != nil {
		t.Fatalf("owner a env: %v", err)
	}
	bEnv, err := b.peerEnvForLaunch("mcp-lsp", parent, nil)
	if err != nil {
		t.Fatalf("owner b env: %v", err)
	}
	requireEnvValue(t, aEnv, sidecarOwnerIDEnv, "task-a")
	requireEnvValue(t, bEnv, sidecarOwnerIDEnv, "task-b")
	requireEnvValue(t, aEnv, "GO_AGENT_LSP_ROOT", root)
	requireEnvValue(t, bEnv, "GO_AGENT_LSP_ROOT", root)
	if gotA, _ := lookupEnvValue(aEnv, sidecarOwnerIDEnv); gotA == lookupValueForTest(t, bEnv, sidecarOwnerIDEnv) {
		t.Fatalf("owner identities are shared: %q", gotA)
	}
}

func lookupValueForTest(t *testing.T, env []string, key string) string {
	t.Helper()
	value, ok := lookupEnvValue(env, key)
	if !ok {
		t.Fatalf("%s missing from env", key)
	}
	return value
}

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
	env, err := peerProcessEnv("mcp-orch", append(testPeerParentEnv(),
		"DATABASE_URL=postgres://parent@localhost/super_dolphin",
		"POSTGRES_CONNECTION_STRING=postgres://compat@localhost/super_dolphin",
		"NON_DB_PARENT=keep",
	), nil)
	if err != nil {
		t.Fatalf("peerProcessEnv() error = %v", err)
	}
	requireEnvValue(t, env, "SUPER_DOLPHIN_RUNTIME_MODE", "dev")
	requireEnvValue(t, env, "SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR", "/work/repo")
	requireEnvValue(t, env, "SUPER_DOLPHIN_DEPENDENCY_PROFILE", "production")
	for _, key := range []string{"DATABASE_URL", "POSTGRES_CONNECTION_STRING"} {
		requireEnvKeyAbsent(t, env, key)
	}
	requireEnvValue(t, env, "NON_DB_PARENT", "keep")
}

func TestPeerProcessEnvOverridesParentDependencyProfile(t *testing.T) {
	env, err := peerProcessEnv("mcp-orch", append(testPeerParentEnv(),
		"SUPER_DOLPHIN_DEPENDENCY_PROFILE=desktop_host",
	), nil)
	if err != nil {
		t.Fatalf("peerProcessEnv() error = %v", err)
	}
	requireEnvValue(t, env, "SUPER_DOLPHIN_DEPENDENCY_PROFILE", "production")
	requireEnvItemAbsent(t, env, "SUPER_DOLPHIN_DEPENDENCY_PROFILE=desktop_host")
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

func TestPeerProcessEnvPassesExplicitSQLitePathToTrustedOrchOnly(t *testing.T) {
	sqlitePath := filepath.Join(t.TempDir(), "super-dolphin.db")
	parent := append(testPeerParentEnv(), "SUPER_DOLPHIN_SQLITE_PATH="+sqlitePath)

	orchEnv, err := peerProcessEnv("mcp-orch", parent, nil)
	if err != nil {
		t.Fatalf("peerProcessEnv(mcp-orch) error = %v", err)
	}
	requireEnvKeyAbsent(t, orchEnv, "SUPER_DOLPHIN_SQLITE_PATH")
	requireEnvValue(t, orchEnv, testInternalSQLitePathEnvKey, sqlitePath)

	lspEnv, err := peerProcessEnv("mcp-lsp", parent, []string{t.TempDir()})
	if err != nil {
		t.Fatalf("peerProcessEnv(mcp-lsp) error = %v", err)
	}
	requireEnvKeyAbsent(t, lspEnv, "SUPER_DOLPHIN_SQLITE_PATH")
	requireEnvKeyAbsent(t, lspEnv, testInternalSQLitePathEnvKey)
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

func requireEnvKeyAbsent(t *testing.T, env []string, key string) {
	t.Helper()
	prefix := key + "="
	for _, candidate := range env {
		if strings.HasPrefix(candidate, prefix) {
			t.Fatalf("env unexpectedly contains key %s: %#v", key, env)
		}
	}
}
