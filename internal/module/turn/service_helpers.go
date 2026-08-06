package turn

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/idgen"
)

// ensureLocalTurnID 返回已有本地 turnID，缺失时生成新的 turn 前缀 ID。
func ensureLocalTurnID(localID string) string {
	if localID = strings.TrimSpace(localID); localID != "" {
		return localID
	}
	return idgen.NewID("turn")
}

// isTerminalTurnState 判断字符串状态是否属于 tracker 终态；空值和未知状态都保持非终态。
func isTerminalTurnState(state string) bool {
	switch TurnState(strings.TrimSpace(state)) {
	case StateCompleted, StateInterrupted, StateFailed, StateStalled:
		return true
	}
	return false
}

// resolveBinaryDir 解析 provider manifest 使用的 peer 二进制目录，优先使用显式 peer 目录。
func resolveBinaryDir() string {
	if dir := resolvePeerBinDir(); dir != "" {
		return dir
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Dir(exe)
}

// resolvePeerBinDir 从环境变量候选目录中挑选可用的 managed peer 目录。
func resolvePeerBinDir() string {
	dirs := peerBinDirCandidates()
	if len(dirs) == 0 {
		return ""
	}
	if dir := firstManagedPeerBinDir(dirs); dir != "" {
		return dir
	}
	return dirs[0]
}

// peerBinDirCandidates 解析 GO_AGENT_PEER_BIN_DIR，支持平台路径列表。
func peerBinDirCandidates() []string {
	raw := strings.TrimSpace(os.Getenv(peerBinDirEnv))
	if raw == "" {
		return nil
	}
	dirs := make([]string, 0, 1)
	for _, part := range filepath.SplitList(raw) {
		if dir := strings.TrimSpace(part); dir != "" {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

// firstManagedPeerBinDir 返回第一个包含 mcp-lsp 或 mcp-orch 二进制的目录。
func firstManagedPeerBinDir(dirs []string) string {
	for _, dir := range dirs {
		if hasManagedPeerBinary(dir) {
			return dir
		}
	}
	return ""
}

// hasManagedPeerBinary 判断目录中是否存在受管 MCP peer 二进制。
func hasManagedPeerBinary(dir string) bool {
	for _, name := range []string{"mcp-lsp", "mcp-orch"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

// waitForHandle 等待 provider handle 完成、上下文取消或 deadline 到期。
func waitForHandle(ctx context.Context, handle contract.TurnHandle, deadline time.Time) error {
	if handle == nil {
		return nil
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-handle.Done():
		return nil
	case <-timer.C:
		return context.DeadlineExceeded
	}
}

// cleanupStaleToolResults 按 FRC 配置清理旧工具结果，并在有清理时记录 debug 日志。
func (s *service) cleanupStaleToolResults(threadID string, input PrepareInput) {
	result := s.toolResults.Cleanup(threadID, input.Model, input.FRCConfig)
	if s == nil || s.logger == nil || result.Cleared == 0 {
		return
	}
	s.logger.Debug("turn tool-result lifecycle cleanup", "thread_id", threadID, "cleared", result.Cleared, "kept", result.Kept, "deleted_files", result.DeletedFiles)
}

// turnMCPSnapshot 把 manifest 中实际声明的 MCP binary 名称回填到上下文快照。
func turnMCPSnapshot(snapshot contract.MCPSnapshot, manifest dto.MCPManifest) contract.MCPSnapshot {
	cloned := cloneMCPSnapshot(snapshot)
	servers := make([]string, 0, len(manifest.Binaries))
	for _, binary := range manifest.Binaries {
		if name := strings.TrimSpace(binary.Name); name != "" {
			servers = append(servers, name)
		}
	}
	cloned.Servers = uniqueTurnMCPServerNames(cloned.Servers, servers)
	return cloned
}

// hydrateMCPServerConfigs 把项目级 MCP server 配置合并到本次 turn 的快照里。
// 它先规范化调用方快照，再读取持久化配置，确保后续 manifest 只看到已校验配置。
func (s *service) hydrateMCPServerConfigs(ctx context.Context, input PrepareInput) (PrepareInput, error) {
	snapshot, err := normalizeTurnMCPSnapshot(input.MCPSnapshot)
	if err != nil {
		return PrepareInput{}, err
	}
	input.MCPSnapshot = snapshot
	if s == nil || s.mcpServers == nil {
		return input, nil
	}
	configs, err := s.mcpServers.ListMCPServerConfigs(ctx, turnMCPServerConfigLookupRoot(input))
	if err != nil {
		return PrepareInput{}, fmt.Errorf("list configured mcp servers: %w", err)
	}
	input.MCPSnapshot, err = mergeTurnConfiguredMCPServers(input.MCPSnapshot, configs)
	if err != nil {
		return PrepareInput{}, err
	}
	return input, nil
}

// normalizeTurnMCPSnapshot 规范化 turn 输入携带的 MCP 快照，并同步 server 名称列表。
func normalizeTurnMCPSnapshot(snapshot contract.MCPSnapshot) (contract.MCPSnapshot, error) {
	configs, names, err := normalizeTurnMCPServerConfigs(snapshot.ServerConfigs)
	if err != nil {
		return contract.MCPSnapshot{}, err
	}
	snapshot = cloneMCPSnapshot(snapshot)
	snapshot.ServerConfigs = configs
	snapshot.Servers = uniqueTurnMCPServerNames(snapshot.Servers, names)
	return snapshot, nil
}

// mergeTurnConfiguredMCPServers 将持久化项目配置合入 turn 快照，已在线的可复用 HTTP server 会跳过。
func mergeTurnConfiguredMCPServers(
	snapshot contract.MCPSnapshot,
	input map[string]contract.MCPServerConfig,
) (contract.MCPSnapshot, error) {
	configs, names, err := normalizeTurnMCPServerConfigs(input)
	if err != nil {
		return contract.MCPSnapshot{}, err
	}
	if len(configs) == 0 {
		return snapshot, nil
	}
	configs, names = skipTurnConfiguredMCPServersWithActiveNames(snapshot.Servers, configs, names)
	if len(configs) == 0 {
		return snapshot, nil
	}
	snapshot = cloneMCPSnapshot(snapshot)
	snapshot.Servers = uniqueTurnMCPServerNames(snapshot.Servers, names)
	snapshot.ServerConfigs = mergeTurnMCPServerConfigMaps(snapshot.ServerConfigs, configs)
	return snapshot, nil
}

// normalizeTurnMCPServerConfigs 校验 turn 快照里的 MCP server 配置并生成稳定名称列表。
// 这里同时接受 HTTP 和显式 stdio 配置，后续 manifest 构建会按 transport 分流。
func normalizeTurnMCPServerConfigs(input map[string]contract.MCPServerConfig) (map[string]contract.MCPServerConfig, []string, error) {
	if len(input) == 0 {
		return nil, nil, nil
	}
	names := make([]string, 0, len(input))
	rawNames := make(map[string]string, len(input))
	for rawName := range input {
		name := strings.TrimSpace(rawName)
		if name == "" {
			return nil, nil, errors.New("configured mcp server name is required")
		}
		if isManagedTurnMCPServerName(name) {
			return nil, nil, fmt.Errorf("configured mcp server conflicts with managed server: %s", name)
		}
		if _, exists := rawNames[name]; exists {
			return nil, nil, fmt.Errorf("configured mcp server name is duplicated after trimming: %s", name)
		}
		names = append(names, name)
		rawNames[name] = rawName
	}
	sort.Strings(names)
	out := make(map[string]contract.MCPServerConfig, len(names))
	enabledNames := make([]string, 0, len(names))
	for _, name := range names {
		config, err := normalizeTurnMCPServerConfig(name, input[rawNames[name]])
		if err != nil {
			return nil, nil, err
		}
		if !turnMCPServerConfigEnabled(config) {
			continue
		}
		out[name] = config
		enabledNames = append(enabledNames, name)
	}
	return out, enabledNames, nil
}

// normalizeTurnMCPServerConfig 根据 transport 分派到 HTTP 或 stdio 配置校验。
func normalizeTurnMCPServerConfig(name string, config contract.MCPServerConfig) (contract.MCPServerConfig, error) {
	transport := strings.TrimSpace(config.Transport)
	if transport == "" {
		return contract.MCPServerConfig{}, fmt.Errorf("configured mcp server transport is required: %s", name)
	}
	switch strings.ToLower(transport) {
	case "http":
		return normalizeTurnHTTPMCPServerConfig(name, config)
	case "stdio":
		return normalizeTurnStdioMCPServerConfig(name, config)
	default:
		return contract.MCPServerConfig{}, fmt.Errorf("configured mcp server transport is unsupported: %s", transport)
	}
}

// normalizeTurnHTTPMCPServerConfig 校验 HTTP MCP server URL 和 headers。
func normalizeTurnHTTPMCPServerConfig(name string, config contract.MCPServerConfig) (contract.MCPServerConfig, error) {
	rawURL := strings.TrimSpace(config.URL)
	if rawURL == "" {
		return contract.MCPServerConfig{}, fmt.Errorf("configured mcp server url is required: %s", name)
	}
	headers, err := normalizeTurnMCPServerHeaders(name, config.Headers)
	if err != nil {
		return contract.MCPServerConfig{}, err
	}
	return contract.MCPServerConfig{
		TrustedServerID: strings.TrimSpace(config.TrustedServerID),
		Transport:       "http",
		URL:             rawURL,
		Headers:         headers,
		Enabled:         normalizeTurnMCPEnabled(config.Enabled),
	}, nil
}

// normalizeTurnStdioMCPServerConfig 校验 stdio MCP server command、args 和 env。
func normalizeTurnStdioMCPServerConfig(name string, config contract.MCPServerConfig) (contract.MCPServerConfig, error) {
	command := strings.TrimSpace(config.Command)
	if command == "" {
		return contract.MCPServerConfig{}, fmt.Errorf("configured mcp server command is required: %s", name)
	}
	args, err := normalizeTurnMCPServerArgs(name, config.Args)
	if err != nil {
		return contract.MCPServerConfig{}, err
	}
	env, err := normalizeTurnMCPServerEnv(name, config.Env)
	if err != nil {
		return contract.MCPServerConfig{}, err
	}
	return contract.MCPServerConfig{
		TrustedServerID: strings.TrimSpace(config.TrustedServerID),
		Transport:       "stdio",
		Command:         command,
		Args:            args,
		Env:             env,
		Enabled:         normalizeTurnMCPEnabled(config.Enabled),
	}, nil
}

// normalizeTurnMCPServerHeaders 清理并校验 HTTP headers，空 key/value 会 fail-fast。
func normalizeTurnMCPServerHeaders(serverName string, input map[string]string) (map[string]string, error) {
	if len(input) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(input))
	for rawName, rawValue := range input {
		name := strings.TrimSpace(rawName)
		if name == "" {
			return nil, fmt.Errorf("configured mcp server header name is required: %s", serverName)
		}
		value := strings.TrimSpace(rawValue)
		if value == "" {
			return nil, fmt.Errorf("configured mcp server header value is required: %s.%s", serverName, name)
		}
		out[name] = value
	}
	return out, nil
}

// normalizeTurnMCPServerArgs 清理 stdio args，空参数会 fail-fast。
func normalizeTurnMCPServerArgs(serverName string, input []string) ([]string, error) {
	if len(input) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(input))
	for _, rawValue := range input {
		value := strings.TrimSpace(rawValue)
		if value == "" {
			return nil, fmt.Errorf("configured mcp server arg is required: %s", serverName)
		}
		out = append(out, value)
	}
	return out, nil
}

// normalizeTurnMCPServerEnv 清理 stdio env，空 key/value 会 fail-fast。
func normalizeTurnMCPServerEnv(serverName string, input map[string]string) (map[string]string, error) {
	if len(input) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(input))
	for rawName, rawValue := range input {
		name := strings.TrimSpace(rawName)
		if name == "" {
			return nil, fmt.Errorf("configured mcp server env name is required: %s", serverName)
		}
		value := strings.TrimSpace(rawValue)
		if value == "" {
			return nil, fmt.Errorf("configured mcp server env value is required: %s.%s", serverName, name)
		}
		out[name] = value
	}
	return out, nil
}

// skipTurnConfiguredMCPServersWithActiveNames 跳过已经在线的 MCP server。
// 在线实例由运行态注册表负责，项目配置只补充尚未在线的 HTTP server。
func skipTurnConfiguredMCPServersWithActiveNames(existing []string, configs map[string]contract.MCPServerConfig, names []string) (map[string]contract.MCPServerConfig, []string) {
	if len(configs) == 0 || len(names) == 0 {
		return configs, names
	}
	active := turnMCPServerNameSet(existing)
	if len(active) == 0 {
		return configs, names
	}
	filteredConfigs := make(map[string]contract.MCPServerConfig, len(configs))
	filteredNames := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if turnMCPServerNameIsActive(active, name) && turnMCPServerConfigUsesReusableHTTP(configs[name]) {
			continue
		}
		filteredConfigs[name] = configs[name]
		filteredNames = append(filteredNames, name)
	}
	return turnConfiguredMCPFilterResult(filteredConfigs, filteredNames)
}

// turnMCPServerConfigUsesReusableHTTP 判断配置是否可由已在线 HTTP server 复用。
func turnMCPServerConfigUsesReusableHTTP(config contract.MCPServerConfig) bool {
	return strings.EqualFold(strings.TrimSpace(config.Transport), "http")
}

// turnMCPServerNameSet 生成已声明 server 名称集合。
func turnMCPServerNameSet(names []string) map[string]struct{} {
	if len(names) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" {
			out[name] = struct{}{}
		}
	}
	return out
}

// turnMCPServerNameIsActive 判断 server 名称是否已经在快照中 active，空名称视为 active 以便跳过。
func turnMCPServerNameIsActive(active map[string]struct{}, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return true
	}
	_, ok := active[name]
	return ok
}

// turnConfiguredMCPFilterResult 规范化过滤结果，空集合返回 nil。
func turnConfiguredMCPFilterResult(configs map[string]contract.MCPServerConfig, names []string) (map[string]contract.MCPServerConfig, []string) {
	if len(configs) == 0 {
		return nil, nil
	}
	return configs, names
}

// turnMCPServerConfigLookupRoot 选择读取项目级 MCP 配置的根目录，GitRoot 优先于 CWD。
func turnMCPServerConfigLookupRoot(input PrepareInput) string {
	if root := strings.TrimSpace(input.GitRoot); root != "" {
		return root
	}
	return strings.TrimSpace(input.CWD)
}

// isManagedTurnMCPServerName 判断配置是否试图覆盖内置受管 peer 名称。
func isManagedTurnMCPServerName(name string) bool {
	switch strings.TrimSpace(name) {
	case string(dto.FamilyLSP), string(dto.FamilyOrch):
		return true
	default:
		return false
	}
}

// uniqueTurnMCPServerNames 合并多个 MCP server 名称列表并保持首次出现顺序。
// 这能避免配置和运行态快照重复声明同一个 server。
func uniqueTurnMCPServerNames(groups ...[]string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0)
	for _, group := range groups {
		for _, value := range group {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			out = append(out, value)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// turnMCPServerConfigNames 返回配置 map 中的非空 server 名称。
func turnMCPServerConfigNames(configs map[string]contract.MCPServerConfig) []string {
	if len(configs) == 0 {
		return nil
	}
	names := make([]string, 0, len(configs))
	for name := range configs {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// mergeTurnMCPServerConfigMaps 深拷贝并合并 MCP server 配置，extra 覆盖 base 同名项。
func mergeTurnMCPServerConfigMaps(base, extra map[string]contract.MCPServerConfig) map[string]contract.MCPServerConfig {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	out := make(map[string]contract.MCPServerConfig, len(base)+len(extra))
	copyTurnMCPServerConfigs(out, base)
	copyTurnMCPServerConfigs(out, extra)
	if len(out) == 0 {
		return nil
	}
	return out
}

// copyTurnMCPServerConfigs 把输入配置复制到目标 map，并清理字符串字段。
func copyTurnMCPServerConfigs(out map[string]contract.MCPServerConfig, input map[string]contract.MCPServerConfig) {
	for name, config := range input {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		out[name] = contract.MCPServerConfig{
			TrustedServerID: strings.TrimSpace(config.TrustedServerID),
			Transport:       strings.TrimSpace(config.Transport),
			URL:             strings.TrimSpace(config.URL),
			Headers:         cloneTurnStringMap(config.Headers),
			Command:         strings.TrimSpace(config.Command),
			Args:            cloneTurnStringList(config.Args),
			Env:             cloneTurnStringMap(config.Env),
			Enabled:         cloneTurnBoolPtr(config.Enabled),
		}
	}
}

// cloneTurnStringList 复制字符串列表，空列表返回 nil。
func cloneTurnStringList(input []string) []string {
	if len(input) == 0 {
		return nil
	}
	return append([]string(nil), input...)
}

// normalizeTurnMCPEnabled 把 nil enabled 规范化为 true，保持历史默认启用语义。
func normalizeTurnMCPEnabled(enabled *bool) *bool {
	if enabled == nil {
		return turnBoolPtr(true)
	}
	return turnBoolPtr(*enabled)
}

// turnMCPServerConfigEnabled 判断配置是否启用，nil enabled 视为启用。
func turnMCPServerConfigEnabled(config contract.MCPServerConfig) bool {
	return config.Enabled == nil || *config.Enabled
}

// cloneTurnBoolPtr 复制 bool 指针。
func cloneTurnBoolPtr(input *bool) *bool {
	if input == nil {
		return nil
	}
	return turnBoolPtr(*input)
}

// turnBoolPtr 返回 bool 值的独立指针。
func turnBoolPtr(value bool) *bool {
	return &value
}

// cloneTurnStringMap 复制并清理 turn 快照里的 MCP 字符串 map。
// 空 key/value 会被丢弃，避免 stdio env 或 HTTP header 带入无效配置。
func cloneTurnStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// syntheticMemoryContext 调用 turn context provider 生成注入 provider 的合成上下文。
func (s *service) syntheticMemoryContext(
	ctx context.Context,
	session contract.Session,
	input PrepareInput,
	threadID, userText string,
	mcp dto.MCPManifest,
) (contract.TurnContextPayload, error) {
	if s == nil || s.turnContextProvider == nil {
		return contract.TurnContextPayload{}, nil
	}
	buildCtx := contract.BuildCtx{
		CWD:                          strings.TrimSpace(input.CWD),
		GitRoot:                      strings.TrimSpace(input.GitRoot),
		IsWorktree:                   input.IsWorktree,
		Language:                     strings.TrimSpace(input.Language),
		Provider:                     strings.TrimSpace(input.Provider),
		Model:                        strings.TrimSpace(input.Model),
		EnabledTools:                 append([]string(nil), input.EnabledTools...),
		AdditionalWorkingDirectories: append([]string(nil), input.AdditionalWorkingDirectories...),
		MCPSnapshot:                  turnMCPSnapshot(input.MCPSnapshot, mcp),
		SessionFlags:                 clonePrepareFlags(input.SessionFlags),
	}
	if provider, ok := s.turnContextProvider.(turnContextProviderWithError); ok {
		payload, err := provider.PrepareTurnContextWithError(ctx, session, buildCtx, threadID, userText)
		if err != nil {
			s.logMemoryContextError(threadID, err)
			return contract.TurnContextPayload{}, err
		}
		return payload, nil
	}
	return s.turnContextProvider.PrepareTurnContext(ctx, session, buildCtx, threadID, userText), nil
}

type turnContextProviderWithError interface {
	PrepareTurnContextWithError(
		context.Context,
		contract.Session,
		contract.BuildCtx,
		string,
		string,
	) (contract.TurnContextPayload, error)
}

func (s *service) logMemoryContextError(threadID string, err error) {
	if s == nil || s.logger == nil || err == nil {
		return
	}
	attrs := []any{"thread_id", strings.TrimSpace(threadID), "error", err}
	var logged interface {
		LogFields() []any
	}
	if errors.As(err, &logged) {
		attrs = append(attrs, logged.LogFields()...)
	}
	s.logger.Warn("turn: memory context failed", attrs...)
}
