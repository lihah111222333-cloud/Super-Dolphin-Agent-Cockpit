package contract

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

// ManifestBuildFunc builds MCP binary metadata for external executors.
type ManifestBuildFunc func(ctx dto.ManifestContext) dto.MCPManifest

// BuildManifest returns declarative MCP binary metadata for external executors.
// BuildManifest 构建manifest。
func BuildManifest(ctx dto.ManifestContext) dto.MCPManifest {
	families := []dto.ToolFamily{dto.FamilyLSP, dto.FamilyOrch}

	env := normalizeManifestEnv(ctx.Env)
	autoApprove := append([]string(nil), ctx.AutoApprove...)

	bins := make([]dto.MCPBinary, 0, len(families))
	for _, fam := range families {
		serverName := string(fam)
		if ctx.TransportMode != dto.ManifestTransportStdioOnly {
			if proxyAddr := strings.TrimSpace(ctx.ProxyHTTPAddr); proxyAddr != "" {
				bins = append(bins, dto.MCPBinary{
					Name:        serverName,
					Type:        "http",
					URL:         "http://" + proxyAddr + "/mcp/" + string(fam) + "/" + ctx.AgentID,
					AutoApprove: append([]string(nil), autoApprove...),
				})
				continue
			}
			if addr := strings.TrimSpace(ctx.PeerHTTPAddrs[fam]); addr != "" {
				var headers map[string]string
				if token := strings.TrimSpace(ctx.PeerHTTPTokens[fam]); token != "" {
					headers = map[string]string{"Authorization": "Bearer " + token}
				}
				bins = append(bins, dto.MCPBinary{
					Name:        serverName,
					Type:        "http",
					URL:         "http://" + addr + "/mcp",
					Headers:     headers,
					AutoApprove: append([]string(nil), autoApprove...),
				})
				continue
			}
		}
		binaryName := "mcp-" + string(fam)
		binEnv := cloneManifestEnv(env)
		addMCPProjectRootEnv(binEnv, ctx)
		if fam == dto.FamilyLSP {
			addLSPWorkspaceRootEnv(binEnv, ctx)
		}
		bins = append(bins, dto.MCPBinary{
			Name:        serverName,
			Command:     []string{filepath.Join(ctx.BinaryDir, binaryName)},
			Env:         binEnv,
			AutoApprove: append([]string(nil), autoApprove...),
		})
	}
	return dto.MCPManifest{Binaries: appendExtraManifestBinaries(bins, ctx.ExtraBinaries)}
}

// appendExtraManifestBinaries 追加extramanifest二进制。
func appendExtraManifestBinaries(bins []dto.MCPBinary, extras []dto.MCPBinary) []dto.MCPBinary {
	if len(extras) == 0 {
		return bins
	}
	seen := make(map[string]struct{}, len(bins))
	for _, bin := range bins {
		if name := strings.TrimSpace(bin.Name); name != "" {
			seen[name] = struct{}{}
		}
	}
	for _, extra := range extras {
		extra.Name = strings.TrimSpace(extra.Name)
		if extra.Name == "" {
			continue
		}
		if _, exists := seen[extra.Name]; exists {
			continue
		}
		extra.Type = strings.TrimSpace(extra.Type)
		extra.URL = strings.TrimSpace(extra.URL)
		extra.Headers = cloneManifestEnv(extra.Headers)
		extra.Env = cloneManifestEnv(extra.Env)
		extra.Command = append([]string(nil), extra.Command...)
		extra.AutoApprove = append([]string(nil), extra.AutoApprove...)
		bins = append(bins, extra)
		seen[extra.Name] = struct{}{}
	}
	return bins
}

const manifestProjectRootEnvKey = "PROJECT_ROOT"

func addMCPProjectRootEnv(env map[string]string, ctx dto.ManifestContext) {
	if value := strings.TrimSpace(env[manifestProjectRootEnvKey]); value != "" {
		env[manifestProjectRootEnvKey] = normalizeManifestProjectRoot(value)
		return
	}
	if value := strings.TrimSpace(os.Getenv(manifestProjectRootEnvKey)); value != "" {
		env[manifestProjectRootEnvKey] = normalizeManifestProjectRoot(value)
		return
	}
	if value := strings.TrimSpace(ctx.ProjectRoot); value != "" {
		env[manifestProjectRootEnvKey] = normalizeManifestProjectRoot(value)
		return
	}
	if root := inferManifestProjectRootFromBinaryDir(ctx.BinaryDir); root != "" {
		env[manifestProjectRootEnvKey] = root
	}
}

func inferManifestProjectRootFromBinaryDir(binaryDir string) string {
	dir := normalizeManifestProjectRoot(binaryDir)
	if dir == "" {
		return ""
	}
	for {
		if hasManifestMigrationsDir(dir) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func normalizeManifestProjectRoot(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
	}
	return filepath.Clean(path)
}

func hasManifestMigrationsDir(root string) bool {
	info, err := os.Stat(filepath.Join(root, "internal", "platform", "db", "sqlite", "migrations"))
	return err == nil && info.IsDir()
}

func addLSPWorkspaceRootEnv(env map[string]string, ctx dto.ManifestContext) {
	roots := normalizeManifestWorkspaceRoots(ctx.CWD, ctx.AdditionalWorkingDirectories)
	if len(roots) == 0 {
		return
	}
	raw, err := json.Marshal(roots)
	if err != nil {
		return
	}
	env["GO_AGENT_LSP_ROOT"] = roots[0]
	env["GO_AGENT_LSP_ROOTS"] = string(raw)
}

// normalizeManifestWorkspaceRoots 规范化manifest工作区根目录。
func normalizeManifestWorkspaceRoots(cwd string, dirs []string) []string {
	out := make([]string, 0, len(dirs)+1)
	seen := map[string]struct{}{}
	primary := normalizeManifestWorkspaceRoot("", cwd)
	if primary == "" {
		return nil
	}
	add := func(path string) {
		path = normalizeManifestWorkspaceRoot(primary, path)
		if path == "" {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	add(primary)
	for _, dir := range dirs {
		add(dir)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// normalizeManifestWorkspaceRoot 规范化manifest工作区根目录。
func normalizeManifestWorkspaceRoot(base, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if strings.TrimSpace(base) != "" && !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	if strings.TrimSpace(base) == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}

func cloneManifestEnv(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	maps.Copy(out, in)
	removeManifestDatabaseEnv(out)
	return out
}

var mcpRequiredEnvKeys = []string{
	"GO_AGENT_CTL_RPC_ADDR",
	"GO_AGENT_CTL_INSTANCE_ID",
	"GO_AGENT_CTL_BOOT_ID",
	"GO_AGENT_CTL_BINARY_NAME",
	"GO_AGENT_CTL_CLIENT_KIND",
	"GO_AGENT_CTL_AGENT_ID",
	"GO_AGENT_CTL_THREAD_ID",
	"GO_AGENT_CTL_SESSION_TOKEN",
	"GO_AGENT_CTL_BOOTSTRAP_JSON",
}

var mcpPassthroughEnvKeys = []string{"SUPER_DOLPHIN_MODEL_REGISTRY"}

const (
	SQLitePathEnvKey         = "SUPER_DOLPHIN_SQLITE_PATH"
	InternalSQLitePathEnvKey = "SUPER_DOLPHIN_INTERNAL_SQLITE_PATH"
)

var mcpForbiddenDatabaseEnvKeys = map[string]struct{}{
	"DATABASE_URL":               {},
	"POSTGRES_CONNECTION_STRING": {},
	SQLitePathEnvKey:             {},
	InternalSQLitePathEnvKey:     {},
}

// ForbiddenDatabaseEnvKeyNames returns database environment keys stripped from child MCP processes.
func ForbiddenDatabaseEnvKeyNames() []string {
	return []string{"DATABASE_URL", "POSTGRES_CONNECTION_STRING", SQLitePathEnvKey, InternalSQLitePathEnvKey}
}

// IsForbiddenDatabaseEnvKey reports whether a key would leak database routing into MCP children.
func IsForbiddenDatabaseEnvKey(key string) bool {
	_, ok := mcpForbiddenDatabaseEnvKeys[strings.ToUpper(strings.TrimSpace(key))]
	return ok
}

// ScrubDatabaseEnv removes forbidden database entries from process environment slices.
func ScrubDatabaseEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, item := range env {
		key, _, ok := strings.Cut(item, "=")
		if ok && IsForbiddenDatabaseEnvKey(key) {
			continue
		}
		out = append(out, item)
	}
	return out
}

// ScrubDatabaseEnvMap removes forbidden database entries from a mutable environment map.
func ScrubDatabaseEnvMap(env map[string]string) {
	for key := range env {
		if IsForbiddenDatabaseEnvKey(key) {
			delete(env, key)
		}
	}
}

var mcpLegacyEnvAliases = map[string][]string{
	"GO_AGENT_CTL_RPC_ADDR":       {"RPC_ADDR"},
	"GO_AGENT_CTL_INSTANCE_ID":    {"GO_AGENT_MCP_INSTANCE_ID"},
	"GO_AGENT_CTL_BOOT_ID":        {"GO_AGENT_MCP_BOOT_ID"},
	"GO_AGENT_CTL_BINARY_NAME":    {"GO_AGENT_MCP_BINARY_NAME"},
	"GO_AGENT_CTL_CLIENT_KIND":    {"GO_AGENT_MCP_CLIENT_KIND"},
	"GO_AGENT_CTL_AGENT_ID":       {"GO_AGENT_MCP_AGENT_ID"},
	"GO_AGENT_CTL_THREAD_ID":      {"GO_AGENT_MCP_THREAD_ID"},
	"GO_AGENT_CTL_SESSION_TOKEN":  {"GO_AGENT_MCP_SESSION_TOKEN"},
	"GO_AGENT_CTL_BOOTSTRAP_JSON": {"GO_AGENT_MCP_BOOT_CONTEXT"},
}

// normalizeManifestEnv 规范化manifestenv。
func normalizeManifestEnv(in map[string]string) map[string]string {
	out := cloneManifestEnv(in)
	for key, aliases := range mcpLegacyEnvAliases {
		promoteManifestEnv(out, key, aliases...)
	}
	for _, key := range mcpRequiredEnvKeys {
		if value := strings.TrimSpace(out[key]); value != "" {
			continue
		}
		if val := strings.TrimSpace(os.Getenv(key)); val != "" {
			out[key] = val
			continue
		}
		for _, alias := range mcpLegacyEnvAliases[key] {
			if val := strings.TrimSpace(os.Getenv(alias)); val != "" {
				out[key] = val
				break
			}
		}
	}
	for _, key := range mcpPassthroughEnvKeys {
		if value := strings.TrimSpace(out[key]); value != "" {
			continue
		}
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			out[key] = value
		}
	}
	removeManifestDatabaseEnv(out)
	return out
}

// removeManifestDatabaseEnv 移除不允许透传到 provider manifest 的数据库环境变量。
func removeManifestDatabaseEnv(env map[string]string) {
	ScrubDatabaseEnvMap(env)
}

// promoteManifestEnv 将旧环境变量别名提升到规范键，并删除别名键。
func promoteManifestEnv(env map[string]string, canonical string, aliases ...string) {
	if value := strings.TrimSpace(env[canonical]); value != "" {
		env[canonical] = value
		for _, alias := range aliases {
			delete(env, alias)
		}
		return
	}
	for _, alias := range aliases {
		if value := strings.TrimSpace(env[alias]); value != "" {
			env[canonical] = value
			break
		}
	}
	for _, alias := range aliases {
		delete(env, alias)
	}
}
