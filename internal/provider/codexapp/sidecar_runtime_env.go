package codexapp

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

const (
	sidecarRuntimeModeEnv      = "SUPER_DOLPHIN_RUNTIME_MODE"
	sidecarRuntimeResourcesEnv = "SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR"
)

func lookupEnvValue(env []string, key string) (string, bool) {
	prefix := key + "="
	for i := len(env) - 1; i >= 0; i-- {
		if value, ok := strings.CutPrefix(env[i], prefix); ok {
			return value, true
		}
	}
	return "", false
}

func lookupTrimmedEnvValue(env []string, key string) (string, bool) {
	value, ok := lookupEnvValue(env, key)
	value = strings.TrimSpace(value)
	return value, ok && value != ""
}

// Pure helpers (migrated from the deleted peer_spawn.go).
func (l *execPeerLauncher) peerEnvForTest(name string, parent []string) ([]string, error) {
	if l == nil || l.workspaceRoots == nil {
		return peerProcessEnv(name, parent, nil)
	}
	return peerProcessEnv(name, parent, l.workspaceRoots())
}

func peerProcessEnv(name string, parent []string, configuredRoots []string) ([]string, error) {
	orchSQLitePath, hasOrchSQLitePath, err := trustedOrchSQLitePath(parent, name)
	if err != nil {
		return nil, err
	}
	env := contract.ScrubDatabaseEnv(parent)
	if hasOrchSQLitePath {
		env = append(env, contract.InternalSQLitePathEnvKey+"="+orchSQLitePath)
	}
	env = append(env, peerModeEnv+"=1")
	env, err = ensurePeerSessionToken(env)
	if err != nil {
		return nil, err
	}
	if _, ok := lookupTrimmedEnvValue(env, sidecarRuntimeModeEnv); !ok {
		return nil, errors.New("peer process requires parent sidecar runtime contract: missing SUPER_DOLPHIN_RUNTIME_MODE")
	}
	if _, ok := lookupTrimmedEnvValue(env, sidecarRuntimeResourcesEnv); !ok {
		return nil, errors.New("peer process requires parent sidecar runtime contract: missing SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR")
	}
	env, err = injectPeerBootstrapIdentity(env, name)
	if err != nil {
		return nil, err
	}
	return applyMcpLSPPeerWorkspaceEnv(env, name, configuredRoots)
}

func applyMcpLSPPeerWorkspaceEnv(env []string, name string, configuredRoots []string) ([]string, error) {
	if strings.TrimSpace(name) != "mcp-lsp" {
		return env, nil
	}
	if len(configuredRoots) > 0 {
		roots, err := normalizePeerWorkspaceRoots(configuredRoots)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(roots)
		if err != nil {
			return nil, err
		}
		return append(removeEnvKeys(env, "GO_AGENT_LSP_ROOT", "GO_AGENT_LSP_ROOTS"), "GO_AGENT_LSP_ROOT="+roots[0], "GO_AGENT_LSP_ROOTS="+string(raw)), nil
	}
	if raw, ok := lookupEnvValue(env, "GO_AGENT_LSP_ROOTS"); ok {
		return env, validateMcpLSPPeerWorkspaceRoots(raw)
	}
	if root, ok := lookupEnvValue(env, "GO_AGENT_LSP_ROOT"); ok {
		return env, validateMcpLSPPeerWorkspaceRoot(root)
	}
	return nil, errors.New("mcp-lsp peer requires configured workspace root")
}

func trustedOrchSQLitePath(parent []string, name string) (string, bool, error) {
	publicSQLitePath, hasPublicSQLitePath := lookupTrimmedEnvValue(parent, contract.SQLitePathEnvKey)
	internalSQLitePath, hasInternalSQLitePath := lookupTrimmedEnvValue(parent, contract.InternalSQLitePathEnvKey)
	if hasPublicSQLitePath && hasInternalSQLitePath && publicSQLitePath != internalSQLitePath {
		return "", false, errors.New("peer process has conflicting SQLite path env: " + contract.SQLitePathEnvKey + " and " + contract.InternalSQLitePathEnvKey)
	}
	if strings.TrimSpace(name) != "mcp-orch" || !hasPublicSQLitePath {
		return "", false, nil
	}
	return publicSQLitePath, true, nil
}

func injectPeerBootstrapIdentity(env []string, name string) ([]string, error) {
	name = strings.TrimSpace(name)
	clientKind := map[string]string{"mcp-orch": "orch", "mcp-lsp": "lsp"}[name]
	if clientKind == "" {
		return nil, errors.New("peer process client kind is not configured for " + name)
	}
	env = removeEnvKeys(env,
		"GO_AGENT_CTL_INSTANCE_ID", "GO_AGENT_MCP_INSTANCE_ID",
		"GO_AGENT_CTL_BOOT_ID", "GO_AGENT_MCP_BOOT_ID",
		peerBinaryNameEnv, "GO_AGENT_MCP_BINARY_NAME",
		peerClientKindEnv, "GO_AGENT_MCP_CLIENT_KIND",
		"GO_AGENT_CTL_AGENT_ID", "GO_AGENT_MCP_AGENT_ID",
		"GO_AGENT_CTL_THREAD_ID", "GO_AGENT_MCP_THREAD_ID",
		peerBootstrapJSONEnv, "GO_AGENT_MCP_BOOT_CONTEXT",
	)
	boot, err := json.Marshal(map[string]string{
		"binary_name": name,
		"client_kind": clientKind,
	})
	if err != nil {
		return nil, err
	}
	return append(env,
		peerBinaryNameEnv+"="+name,
		peerClientKindEnv+"="+clientKind,
		peerBootstrapJSONEnv+"="+string(boot),
	), nil
}

func removeEnvKeys(env []string, keys ...string) []string {
	drop := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		drop[strings.ToUpper(key)] = struct{}{}
	}
	out := env[:0]
	for _, item := range env {
		key, _, ok := strings.Cut(item, "=")
		if !ok {
			out = append(out, item)
			continue
		}
		if _, ok := drop[strings.ToUpper(key)]; ok {
			continue
		}
		out = append(out, item)
	}
	return out
}

func ensurePeerSessionToken(env []string) ([]string, error) {
	if _, ok := lookupTrimmedEnvValue(env, "GO_AGENT_CTL_SESSION_TOKEN"); ok {
		return env, nil
	}
	if token, ok := lookupTrimmedEnvValue(env, "GO_AGENT_MCP_SESSION_TOKEN"); ok {
		return append(env, "GO_AGENT_CTL_SESSION_TOKEN="+token), nil
	}
	return nil, errors.New("peer process requires GO_AGENT_CTL_SESSION_TOKEN or GO_AGENT_MCP_SESSION_TOKEN")
}

func validateMcpLSPPeerWorkspaceRoot(root string) error {
	root = strings.TrimSpace(root)
	if root == "" {
		return errors.New("mcp-lsp peer requires non-empty GO_AGENT_LSP_ROOT workspace root")
	}
	if !filepath.IsAbs(root) {
		return errors.New("mcp-lsp peer GO_AGENT_LSP_ROOT workspace root must be absolute")
	}
	return nil
}

func validateMcpLSPPeerWorkspaceRoots(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return errors.New("mcp-lsp peer requires non-empty GO_AGENT_LSP_ROOTS")
	}
	var roots []string
	if err := json.Unmarshal([]byte(raw), &roots); err != nil {
		return errors.New("mcp-lsp peer GO_AGENT_LSP_ROOTS must be a JSON array: " + err.Error())
	}
	if len(roots) == 0 || strings.TrimSpace(roots[0]) == "" {
		return errors.New("mcp-lsp peer requires non-empty GO_AGENT_LSP_ROOTS")
	}
	if !filepath.IsAbs(strings.TrimSpace(roots[0])) {
		return errors.New("mcp-lsp peer GO_AGENT_LSP_ROOTS primary root must be absolute")
	}
	return nil
}

func normalizePeerWorkspaceRoots(roots []string) ([]string, error) {
	out := make([]string, 0, len(roots))
	seen := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		if !filepath.IsAbs(root) {
			return nil, errors.New("mcp-lsp peer configured workspace root must be absolute")
		}
		root = filepath.Clean(root)
		if _, ok := seen[root]; ok {
			continue
		}
		seen[root] = struct{}{}
		out = append(out, root)
	}
	if len(out) == 0 {
		return nil, errors.New("mcp-lsp peer requires configured workspace root")
	}
	return out, nil
}

// resolvePeerBinDirs returns the ordered list of directories to probe for peer
// binaries. GO_AGENT_PEER_BIN_DIR (path-list) wins over os.Executable()'s dir.
func resolvePeerBinDirs() ([]string, error) {
	var dirs []string
	if override := strings.TrimSpace(os.Getenv(peerBinDirEnv)); override != "" {
		for _, part := range filepath.SplitList(override) {
			if p := strings.TrimSpace(part); p != "" {
				dirs = append(dirs, p)
			}
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	dirs = append(dirs, filepath.Dir(exe))
	return dirs, nil
}

func findPeerBinary(dirs []string, name string) (string, bool) {
	// On Windows the binaries are mcp-orch.exe / mcp-lsp.exe but
	// defaultPeerNames returns the unsuffixed names (Unix convention).
	// Probe the .exe variant first on Windows so we resolve before
	// falling back to the literal name (which lets callers that already
	// include ".exe" still work).
	candidates := []string{name}
	if runtime.GOOS == "windows" && !strings.EqualFold(filepath.Ext(name), ".exe") {
		candidates = []string{name + ".exe", name}
	}
	for _, dir := range dirs {
		for _, leaf := range candidates {
			candidate := filepath.Join(dir, leaf)
			if _, err := os.Stat(candidate); err == nil {
				return candidate, true
			}
		}
	}
	return "", false
}
