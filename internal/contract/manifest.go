package contract

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

// ManifestBuildFunc 构建外部执行器需要的 MCP binary manifest。
type ManifestBuildFunc func(ctx dto.ManifestContext) dto.MCPManifest

// BuildManifest 根据 provider 上下文生成 MCP binary manifest。
// 它优先使用 HTTP proxy/peer 地址；没有可用 HTTP 通道时才落回 stdio binary，
// 并在落回前补齐受控环境变量和 LSP 工作区根。
func BuildManifest(ctx dto.ManifestContext) dto.MCPManifest {
	families := []dto.ToolFamily{dto.FamilyLSP, dto.FamilyOrch}

	env := normalizeManifestEnv(ctx.Env)
	autoApprove := append([]string(nil), ctx.AutoApprove...)

	bins := make([]dto.MCPBinary, 0, len(families))
	for _, fam := range families {
		serverName := string(fam)
		if ctx.TransportMode != dto.ManifestTransportStdioOnly {
			if proxyAddr := strings.TrimSpace(ctx.ProxyHTTPAddr); proxyAddr != "" {
				var headers map[string]string
				if token := strings.TrimSpace(ctx.ProxyHTTPToken); token != "" {
					headers = map[string]string{"Authorization": "Bearer " + token}
				}
				bins = append(bins, dto.NewManagedMCPBinary(dto.MCPBinary{
					Name:        serverName,
					Type:        "http",
					URL:         "http://" + proxyAddr + "/mcp/" + string(fam) + "/" + ctx.AgentID,
					Headers:     headers,
					AutoApprove: append([]string(nil), autoApprove...),
				}))
				continue
			}
			if addr := strings.TrimSpace(ctx.PeerHTTPAddrs[fam]); addr != "" {
				var headers map[string]string
				if token := strings.TrimSpace(ctx.PeerHTTPTokens[fam]); token != "" {
					headers = map[string]string{"Authorization": "Bearer " + token}
				}
				bins = append(bins, dto.NewManagedMCPBinary(dto.MCPBinary{
					Name:        serverName,
					Type:        "http",
					URL:         "http://" + addr + "/mcp",
					Headers:     headers,
					AutoApprove: append([]string(nil), autoApprove...),
				}))
				continue
			}
		}
		binaryName := "mcp-" + string(fam)
		binEnv := cloneManifestEnv(env)
		binEnv[manifestDependencyProfileEnvKey] = string(DependencyProfileProduction)
		addMCPProjectRootEnv(binEnv, ctx)
		if fam == dto.FamilyLSP {
			addLSPWorkspaceRootEnv(binEnv, ctx)
		}
		bins = append(bins, dto.NewManagedMCPBinary(dto.MCPBinary{
			Name:        serverName,
			Command:     []string{filepath.Join(ctx.BinaryDir, binaryName)},
			Env:         binEnv,
			AutoApprove: append([]string(nil), autoApprove...),
		}))
	}
	return dto.MCPManifest{Binaries: appendExtraManifestBinaries(bins, ctx.ExtraBinaries)}
}

// appendExtraManifestBinaries 追加调用方显式传入的额外 MCP binary。
// 同名 binary 会被忽略，避免外部配置覆盖核心 lsp/orch 入口。
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
		extra.TrustedServerID = strings.TrimSpace(extra.TrustedServerID)
		extra.Headers = cloneManifestEnv(extra.Headers)
		extra.Env = cloneManifestEnv(extra.Env)
		extra.Command = append([]string(nil), extra.Command...)
		extra.AutoApprove = append([]string(nil), extra.AutoApprove...)
		bins = append(bins, extra)
		seen[extra.Name] = struct{}{}
	}
	return bins
}

const (
	// manifestDependencyProfileEnvKey 是核心 MCP 子进程选择依赖装配配置的环境变量名。
	manifestDependencyProfileEnvKey = "SUPER_DOLPHIN_DEPENDENCY_PROFILE"
	// manifestProjectRootEnvKey 是 MCP 子进程识别当前项目根的环境变量名。
	manifestProjectRootEnvKey = "PROJECT_ROOT"
)

// addMCPProjectRootEnv 为 stdio MCP 子进程补齐 PROJECT_ROOT。
// 查找顺序是显式 env、当前进程 env、ManifestContext.ProjectRoot、再从 binaryDir 向上推导。
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

// inferManifestProjectRootFromBinaryDir 从 binaryDir 向上寻找包含 SQLite migrations 的项目根。
// 找不到时返回空串，让调用方保留无 PROJECT_ROOT 的显式失败边界。
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

// normalizeManifestProjectRoot 清理 manifest 使用的项目根路径。
// 相对路径会尽量转成绝对路径，Abs 失败时保留清理后的原路径供后续校验处理。
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

// hasManifestMigrationsDir 判断目录是否具备本项目 SQLite migrations 结构。
func hasManifestMigrationsDir(root string) bool {
	info, err := os.Stat(filepath.Join(root, "internal", "platform", "db", "sqlite", "migrations"))
	return err == nil && info.IsDir()
}

// addLSPWorkspaceRootEnv 为 LSP MCP binary 注入单根和多根工作区环境变量。
// roots 必须可 JSON 编码；编码失败代表内部不变量被破坏，因此直接 panic 暴露。
func addLSPWorkspaceRootEnv(env map[string]string, ctx dto.ManifestContext) {
	roots := normalizeManifestWorkspaceRoots(ctx.CWD, ctx.AdditionalWorkingDirectories)
	if len(roots) == 0 {
		return
	}
	raw, err := json.Marshal(roots)
	if err != nil {
		// archguard:ignore panic_count -- []string workspace roots 必须始终可 JSON 编码。
		panic(fmt.Sprintf("manifest workspace roots must encode as JSON: %v", err))
	}
	env["GO_AGENT_LSP_ROOT"] = roots[0]
	env["GO_AGENT_LSP_ROOTS"] = string(raw)
}

// normalizeManifestWorkspaceRoots 规范化主工作区和额外工作区根目录。
// 主 cwd 不可解析时返回 nil，避免给 LSP 子进程注入不可信根。
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

// normalizeManifestWorkspaceRoot 规范化单个 manifest 工作区根目录。
// 相对路径只有在有 base 时才按 base 解析，避免凭当前进程目录静默兜底。
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

// cloneManifestEnv 复制 manifest env，并在复制后移除禁止透传的数据库变量。
func cloneManifestEnv(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	maps.Copy(out, in)
	removeManifestDatabaseEnv(out)
	return out
}

// MCP 必要环境变量键名列表，不含这些键时 provider 进程无法正常注册。
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
	"SUPER_DOLPHIN_RUNTIME_MODE",
	"SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR",
}

// MCP 透传环境变量键名列表：这些键允许透传到 provider 进程。
var mcpPassthroughEnvKeys = []string{"SUPER_DOLPHIN_MODEL_REGISTRY"}

// SQLite / 数据库路径环境变量键名常量，禁止透传给 provider 进程。
const (
	SQLitePathEnvKey         = "SUPER_DOLPHIN_SQLITE_PATH"
	InternalSQLitePathEnvKey = "SUPER_DOLPHIN_INTERNAL_SQLITE_PATH"
)

// mcpForbiddenDatabaseEnvKeys 是禁止透传的数据库环境变量键集合，方便 O(1) 查找。
var mcpForbiddenDatabaseEnvKeys = map[string]struct{}{
	"DATABASE_URL":               {},
	"POSTGRES_CONNECTION_STRING": {},
	SQLitePathEnvKey:             {},
	InternalSQLitePathEnvKey:     {},
}

// ForbiddenDatabaseEnvKeyNames 返回禁止透传到 provider manifest 的数据库环境变量键名列表。
func ForbiddenDatabaseEnvKeyNames() []string {
	return []string{"DATABASE_URL", "POSTGRES_CONNECTION_STRING", SQLitePathEnvKey, InternalSQLitePathEnvKey}
}

// IsForbiddenDatabaseEnvKey 判断给定键名是否属于禁止透传的数据库环境变量。
func IsForbiddenDatabaseEnvKey(key string) bool {
	_, ok := mcpForbiddenDatabaseEnvKeys[strings.ToUpper(strings.TrimSpace(key))]
	return ok
}

// ScrubDatabaseEnv 从 key=value 格式的环境变量切片中移除数据库相关键。
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

// ScrubDatabaseEnvMap 从 map 中就地删除数据库相关环境变量键。
func ScrubDatabaseEnvMap(env map[string]string) {
	for key := range env {
		if IsForbiddenDatabaseEnvKey(key) {
			delete(env, key)
		}
	}
}

// 兼容输入键映射表，将旧控制面键收敛到规范环境变量。
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

// normalizeManifestEnv 规范化 MCP manifest 环境变量。
// 它会提升兼容别名、补齐必需控制面变量，并在返回前再次清理数据库连接信息。
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
