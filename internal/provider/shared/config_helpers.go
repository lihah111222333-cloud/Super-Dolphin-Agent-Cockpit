package shared

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/httpegress"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/configutil"
)

const peerBinDirEnv = "GO_AGENT_PEER_BIN_DIR"

const (
	projectRootEnv          = "PROJECT_ROOT"
	requireBundledCodexEnv  = "SUPER_DOLPHIN_REQUIRE_BUNDLED_CODEX"
	runtimeManifestFilename = "runtime-manifest.json"
)

type binaryDirResolver struct {
	executablePath func() (string, error)
	lookPath       func(string) (string, error)
}

// ResolveBinaryDir 解析 provider peer 二进制目录。
// 优先使用打包运行时和显式配置，再回退到当前可执行文件、cwd 与 PATH。
func ResolveBinaryDir(cwd string, cfg map[string]any) string {
	return defaultBinaryDirResolver().ResolveBinaryDir(cwd, cfg)
}

func defaultBinaryDirResolver() binaryDirResolver {
	return binaryDirResolver{
		executablePath: os.Executable,
		lookPath:       exec.LookPath,
	}
}

// ResolveBinaryDir 按运行时优先级解析 mcp-lsp/mcp-orch 所在目录。
// 返回值可能是不存在受管二进制的候选目录，调用方需在启动时继续校验。
func (r binaryDirResolver) ResolveBinaryDir(cwd string, cfg map[string]any) string {
	if dir := r.packagedBinaryDir(); dir != "" {
		return dir
	}
	if dir := ConfigString(cfg, "binary_dir", "binaryDir"); dir != "" {
		return dir
	}
	candidates := make([]string, 0, 4)
	candidates = append(candidates, peerBinDirCandidates()...)
	if exe, err := r.executablePath(); err == nil {
		candidates = append(candidates, filepath.Dir(exe))
	}
	if dir := strings.TrimSpace(cwd); dir != "" {
		candidates = append(candidates, dir)
	}
	if dir := r.lookPathBinaryDir(); dir != "" {
		candidates = append(candidates, dir)
	}
	if dir := firstManagedBinaryDir(candidates...); dir != "" {
		return dir
	}
	for _, dir := range candidates {
		if dir = strings.TrimSpace(dir); dir != "" {
			return dir
		}
	}
	return ""
}

func (r binaryDirResolver) packagedBinaryDir() string {
	if dir := packagedBinaryDirFromProjectRoot(); dir != "" {
		return dir
	}
	if strings.TrimSpace(os.Getenv(requireBundledCodexEnv)) != "1" {
		return ""
	}
	candidates := peerBinDirCandidates()
	if len(candidates) == 0 {
		return ""
	}
	return candidates[0]
}

func packagedBinaryDirFromProjectRoot() string {
	root := strings.TrimSpace(os.Getenv(projectRootEnv))
	if root == "" {
		return ""
	}
	info, err := os.Stat(filepath.Join(root, runtimeManifestFilename))
	if err != nil || info.IsDir() {
		return ""
	}
	return filepath.Join(root, "bin")
}

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

func (r binaryDirResolver) lookPathBinaryDir() string {
	for _, name := range managedBinaryNames() {
		if bin, err := r.lookPath(name); err == nil {
			return filepath.Dir(bin)
		}
	}
	return ""
}

func managedBinaryNames() [2]string {
	return [2]string{"mcp-lsp", "mcp-orch"}
}

func firstManagedBinaryDir(dirs ...string) string {
	for _, dir := range dirs {
		if hasManagedBinary(dir) {
			return dir
		}
	}
	return ""
}

func hasManagedBinary(dir string) bool {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return false
	}
	for _, name := range managedBinaryNames() {
		info, err := os.Stat(filepath.Join(dir, name))
		if err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

// ConfigString 读取配置里的第一个有效字符串。
// provider 层保留该薄封装，避免调用方直接依赖 util/configutil。
func ConfigString(cfg map[string]any, keys ...string) string {
	return configutil.ConfigString(cfg, keys...)
}

// SanitizeConfigString 清理配置字符串中的空值和前端占位值。
func SanitizeConfigString(value string) string {
	return configutil.SanitizeConfigString(value)
}

// StringMap 将配置对象转换为 string map。
// 非字符串值由 configutil 统一处理，provider 层只暴露稳定入口。
func StringMap(raw any) map[string]string {
	return configutil.StringMap(raw)
}

// ConfigStringSlice 读取配置中的字符串数组。
// 支持多个候选 key，返回值已按 configutil 规则裁剪和清理。
func ConfigStringSlice(cfg map[string]any, keys ...string) []string {
	return configutil.ConfigStringSlice(cfg, keys...)
}

// NormalizeConfigStringSlice 规范化任意配置值为字符串数组。
func NormalizeConfigStringSlice(values any) []string {
	return configutil.NormalizeConfigStringSlice(values)
}

// TrimConfigStringValues 裁剪 []any 中的字符串配置值。
func TrimConfigStringValues(values []any) []string {
	return configutil.TrimConfigStringValues(values)
}

// SplitConfigStringSlice 拆分逗号/路径列表形式的字符串配置。
func SplitConfigStringSlice(value string) []string {
	return configutil.SplitConfigStringSlice(value)
}

// TrimStrings 裁剪字符串数组并丢弃空项。
func TrimStrings(values []string) []string {
	return configutil.TrimStrings(values)
}

// ConfigMCPBinaries 从运行时配置中解析 MCP server 二进制声明。
// 配置格式错误会直接返回 error，避免 provider 拉起半有效的 MCP 命令。
func ConfigMCPBinaries(cfg map[string]any, keys ...string) ([]dto.MCPBinary, error) {
	raw, ok := firstConfigValue(cfg, keys...)
	if !ok {
		return nil, nil
	}
	servers, err := mcpConfigServerObjects(raw)
	if err != nil {
		return nil, err
	}
	return mcpBinariesFromServerObjects(servers)
}

func mcpConfigServerObjects(raw any) (map[string]any, error) {
	top, err := configObject(raw, "mcpConfig")
	if err != nil {
		return nil, err
	}
	rawServers, ok := top["mcpServers"]
	if !ok {
		return nil, fmt.Errorf("mcpConfig.mcpServers is required")
	}
	servers, err := configObject(rawServers, "mcpConfig.mcpServers")
	if err != nil {
		return nil, err
	}
	return servers, nil
}

func mcpBinariesFromServerObjects(servers map[string]any) ([]dto.MCPBinary, error) {
	names, rawNames, err := sortedMCPConfigServerNames(servers)
	if err != nil {
		return nil, err
	}
	binaries := make([]dto.MCPBinary, 0, len(names))
	for _, name := range names {
		binary, err := mcpBinaryFromServerObject(name, servers[rawNames[name]])
		if err != nil {
			return nil, err
		}
		binaries = append(binaries, binary)
	}
	return binaries, nil
}

func sortedMCPConfigServerNames(servers map[string]any) ([]string, map[string]string, error) {
	names := make([]string, 0, len(servers))
	rawNames := make(map[string]string, len(servers))
	for rawName := range servers {
		name := strings.TrimSpace(rawName)
		if name == "" {
			return nil, nil, fmt.Errorf("mcpConfig.mcpServers name is required")
		}
		if _, exists := rawNames[name]; exists {
			return nil, nil, fmt.Errorf("mcpConfig.mcpServers name is duplicated after trimming: %s", name)
		}
		if isManagedManifestServerName(name) {
			return nil, nil, fmt.Errorf("mcpConfig.mcpServers.%s conflicts with managed MCP server", name)
		}
		names = append(names, name)
		rawNames[name] = rawName
	}
	sort.Strings(names)
	return names, rawNames, nil
}

// mcpBinaryFromServerObject 将单个 mcpServers 条目转换为 provider manifest 二进制描述。
// transport/type 必须是 http 或 stdio，未知类型会 fail-fast。
func mcpBinaryFromServerObject(name string, raw any) (dto.MCPBinary, error) {
	label := "mcpConfig.mcpServers." + name
	server, err := configObject(raw, label)
	if err != nil {
		return dto.MCPBinary{}, err
	}
	serverID, err := contract.DefaultRuntimeMCPPolicy().ValidateRuntimeServerReference(name, server)
	if err != nil {
		return dto.MCPBinary{}, err
	}
	transport, err := requiredConfigString(server, label, "transport", "type")
	if err != nil {
		return dto.MCPBinary{}, err
	}
	switch strings.ToLower(strings.TrimSpace(transport)) {
	case "http":
		binary, err := httpMCPBinaryFromServerObject(name, server, label)
		binary.TrustedServerID = serverID
		return binary, err
	case "stdio":
		binary, err := stdioMCPBinaryFromServerObject(name, server, label)
		binary.TrustedServerID = serverID
		return binary, err
	default:
		return dto.MCPBinary{}, fmt.Errorf("%s.transport unsupported: %s", label, transport)
	}
}

func httpMCPBinaryFromServerObject(name string, server map[string]any, label string) (dto.MCPBinary, error) {
	url, err := requiredConfigString(server, label, "url")
	if err != nil {
		return dto.MCPBinary{}, err
	}
	url, err = httpegress.ValidatePublicURL(url)
	if err != nil {
		return dto.MCPBinary{}, fmt.Errorf("%s.url: %w", label, err)
	}
	headers, err := configStringHeaderMap(server["headers"], label+".headers")
	if err != nil {
		return dto.MCPBinary{}, err
	}
	if err := httpegress.ValidateHeaders(headers); err != nil {
		return dto.MCPBinary{}, fmt.Errorf("%s.headers: %w", label, err)
	}
	return dto.MCPBinary{
		Name:    name,
		Type:    "http",
		URL:     url,
		Headers: headers,
	}, nil
}

func stdioMCPBinaryFromServerObject(name string, server map[string]any, label string) (dto.MCPBinary, error) {
	command, err := requiredConfigString(server, label, "command")
	if err != nil {
		return dto.MCPBinary{}, err
	}
	args, err := configStringSlice(server["args"], label+".args")
	if err != nil {
		return dto.MCPBinary{}, err
	}
	env, err := configStringMap(server["env"], label+".env")
	if err != nil {
		return dto.MCPBinary{}, err
	}
	if err := contract.DefaultRuntimeMCPPolicy().ValidateRuntimeStdioCommand(command, args, ""); err != nil {
		return dto.MCPBinary{}, fmt.Errorf("%s.command: %w", label, err)
	}
	return dto.MCPBinary{
		Name:    name,
		Command: append([]string{command}, args...),
		Env:     env,
	}, nil
}

func firstConfigValue(cfg map[string]any, keys ...string) (any, bool) {
	if len(cfg) == 0 {
		return nil, false
	}
	for _, key := range keys {
		if value, ok := cfg[key]; ok && value != nil {
			return value, true
		}
	}
	return nil, false
}

// configObject 将配置值解码为 JSON object。
// 支持 map、RawMessage、[]byte 和 JSON 字符串；其他类型会带字段 label 报错。
func configObject(raw any, label string) (map[string]any, error) {
	switch value := raw.(type) {
	case map[string]any:
		return value, nil
	case map[string]string:
		out := make(map[string]any, len(value))
		for key, text := range value {
			out[key] = text
		}
		return out, nil
	case json.RawMessage:
		return decodeConfigObject(value, label)
	case []byte:
		return decodeConfigObject(value, label)
	case string:
		text := strings.TrimSpace(value)
		if text == "" {
			return nil, fmt.Errorf("%s must be an object", label)
		}
		return decodeConfigObject([]byte(text), label)
	default:
		return nil, fmt.Errorf("%s must be an object", label)
	}
}

func decodeConfigObject(raw []byte, label string) (map[string]any, error) {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("%s must be valid JSON object: %w", label, err)
	}
	if obj == nil {
		return nil, fmt.Errorf("%s must be an object", label)
	}
	return obj, nil
}

func requiredConfigString(obj map[string]any, label string, keys ...string) (string, error) {
	for _, key := range keys {
		value, ok := obj[key]
		if !ok {
			continue
		}
		text, ok := value.(string)
		if !ok {
			return "", fmt.Errorf("%s.%s must be a string", label, key)
		}
		text = strings.TrimSpace(text)
		if text == "" {
			return "", fmt.Errorf("%s.%s is required", label, key)
		}
		return text, nil
	}
	return "", fmt.Errorf("%s.%s is required", label, keys[0])
}

// configStringHeaderMap 解析 HTTP MCP headers 配置。
// header 名和值都不能为空，防止把半有效鉴权信息传给 provider。
func configStringHeaderMap(raw any, label string) (map[string]string, error) {
	if raw == nil {
		return nil, nil
	}
	obj, err := configObject(raw, label)
	if err != nil {
		return nil, err
	}
	headers := make(map[string]string, len(obj))
	for rawName, rawValue := range obj {
		name := strings.TrimSpace(rawName)
		if name == "" {
			return nil, fmt.Errorf("%s header name is required", label)
		}
		value, ok := rawValue.(string)
		if !ok {
			return nil, fmt.Errorf("%s.%s must be a string", label, name)
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, fmt.Errorf("%s.%s is required", label, name)
		}
		headers[name] = value
	}
	if len(headers) == 0 {
		return nil, nil
	}
	return headers, nil
}

// configStringSlice 解析配置里的字符串数组，遇到非字符串或空值直接报错。
// stdio MCP args 依赖这个校验，避免 provider 拉起半有效命令。
func configStringSlice(raw any, label string) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	var values []any
	switch current := raw.(type) {
	case []any:
		values = current
	case []string:
		values = make([]any, 0, len(current))
		for _, value := range current {
			values = append(values, value)
		}
	default:
		return nil, fmt.Errorf("%s must be an array", label)
	}
	out := make([]string, 0, len(values))
	for i, rawValue := range values {
		value, ok := rawValue.(string)
		if !ok {
			return nil, fmt.Errorf("%s[%d] must be a string", label, i)
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, fmt.Errorf("%s[%d] is required", label, i)
		}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// configStringMap 解析配置里的字符串 map，空 key/value 会被视为配置错误。
// 它用于 stdio env 等必须精确传给子进程的字段。
func configStringMap(raw any, label string) (map[string]string, error) {
	if raw == nil {
		return nil, nil
	}
	obj, err := configObject(raw, label)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(obj))
	for rawName, rawValue := range obj {
		name := strings.TrimSpace(rawName)
		if name == "" {
			return nil, fmt.Errorf("%s name is required", label)
		}
		value, ok := rawValue.(string)
		if !ok {
			return nil, fmt.Errorf("%s.%s must be a string", label, name)
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, fmt.Errorf("%s.%s is required", label, name)
		}
		out[name] = value
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func isManagedManifestServerName(name string) bool {
	return contract.IsManagedRuntimeMCPServerName(name)
}
