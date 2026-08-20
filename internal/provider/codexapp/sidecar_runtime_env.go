package codexapp

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcp "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
)

const (
	sidecarRuntimeModeEnv       = "SUPER_DOLPHIN_RUNTIME_MODE"
	sidecarRuntimeResourcesEnv  = "SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR"
	sidecarDependencyProfileEnv = "SUPER_DOLPHIN_DEPENDENCY_PROFILE"
	sidecarOwnerIDEnv           = "SUPER_DOLPHIN_SIDECAR_OWNER_ID"
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

// peerEnvForTest 为测试暴露与生产一致的 peer 环境组装路径。
func (l *execPeerLauncher) peerEnvForTest(name string, parent []string) ([]string, error) {
	if l == nil || l.workspaceRoots == nil {
		return peerProcessEnv(name, parent, nil)
	}
	return peerProcessEnv(name, parent, l.workspaceRoots())
}

// peerEnvForLaunch 仅给 mcp-orch 注入 managed bootstrap，其他 sidecar 只使用既有环境合同。
func (l *execPeerLauncher) peerEnvForLaunch(
	name string,
	parent []string,
	managed *mcp.ManagedAuthorityBootstrap,
) ([]string, error) {
	var roots []string
	if l != nil && l.workspaceRoots != nil {
		roots = l.workspaceRoots()
	}
	env, err := peerProcessEnv(name, parent, roots)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(name) != "mcp-orch" {
		return injectPeerOwnerIdentity(env, l.ownerID), nil
	}
	if managed == nil {
		return nil, errors.New("mcp-orch peer requires managed authority bootstrap")
	}
	env, err = injectManagedPeerBootstrap(env, *managed)
	if err != nil {
		return nil, err
	}
	return injectPeerOwnerIdentity(env, l.ownerID), nil
}

func injectPeerOwnerIdentity(env []string, ownerID string) []string {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		return env
	}
	return append(removeEnvKeys(env, sidecarOwnerIDEnv), sidecarOwnerIDEnv+"="+ownerID)
}

// peerProcessEnv 组装 sidecar peer 进程环境变量。
// 它先清洗父进程中的数据库连接变量，再按 peer 名称保留可信 ORCH_SQLITE_PATH，
// 然后为 peer 固定 production 依赖 profile，最后用配置的 workspace roots 覆盖默认工作区边界，避免 peer 继承错误目录。
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
	env = append(removeEnvKeys(env, sidecarDependencyProfileEnv), sidecarDependencyProfileEnv+"="+string(contract.DependencyProfileProduction))
	env, err = injectPeerBootstrapIdentity(env, name)
	if err != nil {
		return nil, err
	}
	return applyMcpLSPPeerWorkspaceEnv(env, name, configuredRoots)
}

// applyMcpLSPPeerWorkspaceEnv 为 mcp-lsp 注入可信 workspace root 环境。
// 显式配置优先；继承父环境时必须重新校验，缺少 root 会 fail-fast 阻止 LSP 扫错仓库。
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

// trustedOrchSQLitePath 只允许 mcp-orch 继承公开 SQLite 路径并转写为内部环境变量。
// 如果公开和内部路径同时存在且不一致，立即报错，避免 peer 连接到错误数据库。
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

// injectPeerBootstrapIdentity 清除父进程身份并写入当前 peer 的基础启动身份。
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
		"GO_AGENT_CTL_MANAGED_TOKEN",
		"GO_AGENT_CTL_MANAGED_PROTOCOL_VERSION",
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

// injectManagedPeerBootstrap 用 registry 签发值覆盖 mcp-orch 身份和 managed authority 环境。
func injectManagedPeerBootstrap(env []string, managed mcp.ManagedAuthorityBootstrap) ([]string, error) {
	if managed.InstanceID == "" || managed.BootID == "" || managed.Token == "" ||
		managed.ProtocolVersion != mcp.ManagedAuthorityProtocolVersion {
		return nil, errors.New("mcp-orch managed authority bootstrap is incomplete")
	}
	env = removeEnvKeys(env,
		"GO_AGENT_CTL_INSTANCE_ID", "GO_AGENT_MCP_INSTANCE_ID",
		"GO_AGENT_CTL_BOOT_ID", "GO_AGENT_MCP_BOOT_ID",
		"GO_AGENT_CTL_MANAGED_TOKEN",
		"GO_AGENT_CTL_MANAGED_PROTOCOL_VERSION",
		peerBootstrapJSONEnv, "GO_AGENT_MCP_BOOT_CONTEXT",
	)
	boot, err := json.Marshal(map[string]string{
		"instance_id": managed.InstanceID,
		"boot_id":     managed.BootID,
		"binary_name": "mcp-orch",
		"client_kind": mcp.ClientKindOrch,
	})
	if err != nil {
		return nil, err
	}
	return append(env,
		"GO_AGENT_CTL_INSTANCE_ID="+managed.InstanceID,
		"GO_AGENT_CTL_BOOT_ID="+managed.BootID,
		"GO_AGENT_CTL_MANAGED_TOKEN="+managed.Token,
		"GO_AGENT_CTL_MANAGED_PROTOCOL_VERSION="+managed.ProtocolVersion,
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

// validateMcpLSPPeerWorkspaceRoots 校验 mcp-lsp peer 传入的工作区根目录列表。
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

// normalizePeerWorkspaceRoots 规范化peer工作区根目录。
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

// resolvePeerBinDirs 返回 peer 二进制的探测目录顺序。
// GO_AGENT_PEER_BIN_DIR 覆盖默认可执行文件目录，便于打包和测试环境显式注入。
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

// findPeerBinary 查找peer二进制。
func findPeerBinary(dirs []string, name string) (string, bool) {
	// Windows peer 文件实际带 .exe 后缀；先探测补后缀形式，再回退到原始名称以兼容调用方已传 .exe。
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
