package uistate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

const (
	// config RPC 偏好键和打包应用 home 解析参数。
	lspPromptHintOverrideKey = "config/lspPromptHint.override"
	packagedAppHomeEnvKey    = "SUPER_DOLPHIN_HOME"
	// lspPromptHintDefaultPath 指向默认注入提示的 shared-file 来源。
	lspPromptHintDefaultPath = "prompts/lsp-mandatory-prefix.md"
)

// errConfigPreferenceStoreRequired 表示写配置时缺少偏好存储依赖。
var errConfigPreferenceStoreRequired = errors.New("uistate: ui preference store is not configured")

type sharedFileReader interface {
	Get(ctx context.Context, path string) (*sharedFile, error)
}

type sharedFile struct {
	Path    string
	Content string
}

// runtimeConfigResult 是 config/read 返回给前端的运行时配置视图。
type runtimeConfigResult struct {
	Model                 string                   `json:"model"`
	ModelProvider         any                      `json:"modelProvider"`
	CWD                   string                   `json:"cwd"`
	ApprovalPolicy        string                   `json:"approvalPolicy"`
	Sandbox               any                      `json:"sandbox"`
	Config                any                      `json:"config"`
	BaseInstructions      any                      `json:"baseInstructions"`
	DeveloperInstructions any                      `json:"developerInstructions"`
	Personality           any                      `json:"personality"`
	ToolRouting           runtimeConfigToolRouting `json:"toolRouting"`
}

// runtimeConfigToolRouting 描述前端可读的工具路由配置。
type runtimeConfigToolRouting struct {
	Mode                string  `json:"mode"`
	RouterModel         string  `json:"routerModel"`
	RouterProvider      string  `json:"routerProvider"`
	RouterBaseURL       string  `json:"routerBaseURL"`
	RouterHasAPIKey     bool    `json:"routerHasAPIKey"`
	ConfidenceThreshold float64 `json:"confidenceThreshold"`
	TimeoutSec          int     `json:"timeoutSec"`
}

// lspPromptHintWriteParams 是 LSP prompt hint 写接口入参。
type lspPromptHintWriteParams struct {
	Hint string `json:"hint,omitempty"`
	Cwd  string `json:"cwd,omitempty"`
}

// lspPromptHintResult 返回默认 hint、覆盖 hint 和最终生效 hint。
type lspPromptHintResult struct {
	Hint         string `json:"hint"`
	DefaultHint  string `json:"defaultHint"`
	OverrideHint string `json:"overrideHint"`
	UsingDefault bool   `json:"usingDefault"`
}

// NewConfigHandlers 注册配置读取、LSP prompt hint 和 builtin tools 配置 RPC。
func NewConfigHandlers(
	cfg *contract.Config,
	prefs preferenceStore,
	sharedFiles sharedFileReader,
	threads contract.ThreadConfigReader,
	skillStore contract.SkillLister,
	nativeTools []contract.NativeToolDescriptor,
) platformrpc.HandlerMapResult {
	toolIndex := buildNativeToolIndex(nativeTools)
	return platformrpc.HandlerMapResult{Handlers: handler.Map{
		"config/read": platformrpc.StrictHandler(func(ctx context.Context, _ struct{}) (any, error) {
			return readRuntimeConfig(ctx, cfg, prefs, threads), nil
		}),
		"config/lspPromptHint/read": platformrpc.StrictHandler(func(ctx context.Context, p scopeParams) (any, error) {
			return readLSPPromptHint(ctx, prefs, sharedFiles, p.Cwd)
		}),
		"config/lspPromptHint/write": platformrpc.StrictHandler(func(ctx context.Context, p lspPromptHintWriteParams) (any, error) {
			return writeLSPPromptHint(ctx, prefs, sharedFiles, p.Cwd, p.Hint)
		}),
		"config/builtinTools/read": platformrpc.StrictHandler(func(ctx context.Context, p scopeParams) (any, error) {
			return readBuiltinTools(ctx, prefs, skillStore, nativeTools, toolIndex, p.Cwd)
		}),
		"config/builtinTools/write": platformrpc.StrictHandler(func(ctx context.Context, p builtinToolsWriteParams) (any, error) {
			return writeBuiltinTool(ctx, prefs, skillStore, nativeTools, toolIndex, p)
		}),
	}}
}

// readRuntimeConfig 读取默认配置并叠加当前 active thread 的运行时覆盖。
func readRuntimeConfig(
	ctx context.Context,
	cfg *contract.Config,
	prefs preferenceValueReader,
	threads contract.ThreadConfigReader,
) runtimeConfigResult {
	result := defaultRuntimeConfig(cfg)
	threadID := readActiveThreadID(ctx, prefs, result.CWD)
	if threadID == "" || threads == nil {
		return result
	}
	if reader, ok := threads.(contract.ThreadRuntimeConfigReader); ok {
		runtimeCfg, err := reader.ReadRuntimeConfig(ctx, threadID)
		if err == nil {
			applyRuntimeConfigOverrides(&result, runtimeCfg)
		}
	}
	threadCfg, err := threads.GetConfig(ctx, threadID)
	if err != nil {
		return result
	}
	if model := strings.TrimSpace(threadCfg.Effective.Model); model != "" {
		result.Model = model
	}
	if approval := firstRuntimeConfigValue(threadCfg.Effective.Approvals, threadCfg.Override.Approvals); approval != "" {
		result.ApprovalPolicy = approval
	}
	return result
}

// applyRuntimeConfigOverrides 将线程 runtime config 的字符串和对象字段覆盖到结果上。
func applyRuntimeConfigOverrides(result *runtimeConfigResult, cfg map[string]any) {
	if result == nil || len(cfg) == 0 {
		return
	}
	applyRuntimeStringOverrides(result, cfg)
	applyRuntimeObjectOverrides(result, cfg)
}

// applyRuntimeStringOverrides 应用字符串类 runtime override，空值不会覆盖默认配置。
func applyRuntimeStringOverrides(result *runtimeConfigResult, cfg map[string]any) {
	if value := runtimeConfigString(cfg, "modelProvider"); value != "" {
		result.ModelProvider = value
	}
	if value := runtimeConfigString(cfg, "approvalPolicy", "approval_policy", "approvals"); value != "" {
		result.ApprovalPolicy = value
	}
	if value := runtimeConfigString(cfg, "baseInstructions", "instructions"); value != "" {
		result.BaseInstructions = value
	}
	if value := runtimeConfigString(cfg, "developerInstructions", "developer_instructions"); value != "" {
		result.DeveloperInstructions = value
	}
	if value := runtimeConfigString(cfg, "personality"); value != "" {
		result.Personality = value
	}
}

// applyRuntimeObjectOverrides 应用对象类 runtime override，例如 sandbox 和 toolRouting。
func applyRuntimeObjectOverrides(result *runtimeConfigResult, cfg map[string]any) {
	if value, ok := cfg["sandbox"]; ok && value != nil {
		result.Sandbox = value
	}
	if routing, ok := runtimeToolRouting(result.ToolRouting, cfg["toolRouting"]); ok {
		result.ToolRouting = routing
	}
}

// defaultRuntimeConfig 从静态配置生成前端默认运行时视图。
func defaultRuntimeConfig(cfg *contract.Config) runtimeConfigResult {
	return runtimeConfigResult{
		Model:                 "gpt-5.5",
		ModelProvider:         nil,
		CWD:                   configCWD(cfg),
		ApprovalPolicy:        "on-failure",
		Sandbox:               "workspace-write",
		Config:                nil,
		BaseInstructions:      nil,
		DeveloperInstructions: "You have access to a `video_with_audio` MCP tool that calls SiliconFlow Wan2.2 API, generates voiceover audio, and returns a merged MP4 `output_path`. When the user asks you to generate a video, call this tool directly with `prompt` and `voice_text`. Do not call `video_generate`, `tts_generate`, or `av_merge` separately.",
		Personality:           nil,
		ToolRouting: runtimeConfigToolRouting{
			Mode:                "legacy",
			RouterModel:         "",
			RouterProvider:      "openai_compatible",
			RouterBaseURL:       "",
			RouterHasAPIKey:     false,
			ConfidenceThreshold: 0.65,
			TimeoutSec:          8,
		},
	}
}

// readActiveThreadID 从 UI preference 中读取当前 cwd 的 active thread。
func readActiveThreadID(ctx context.Context, prefs preferenceValueReader, cwd string) string {
	if prefs == nil {
		return ""
	}
	raw, err := prefs.GetValue(ctx, strings.TrimSpace(cwd), normalizePreferenceKey(preferenceActiveThreadID))
	switch {
	case err == nil:
	case contract.IsNotFound(err):
		return ""
	default:
		return ""
	}
	value, ok := decodePreferenceValue(raw).(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

// firstRuntimeConfigValue 返回第一个非空运行时配置字符串。
func firstRuntimeConfigValue(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

// runtimeConfigString 从动态 map 中按候选 key 读取非空字符串。
func runtimeConfigString(cfg map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := cfg[key].(string)
		if !ok {
			continue
		}
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

// runtimeToolRouting 解析运行时工具路由配置，只有至少一个字段生效时才返回 ok=true。
func runtimeToolRouting(base runtimeConfigToolRouting, raw any) (runtimeConfigToolRouting, bool) {
	values, ok := raw.(map[string]any)
	if !ok {
		return runtimeConfigToolRouting{}, false
	}
	out := base
	applied := false
	if value := runtimeConfigString(values, "mode"); value != "" {
		out.Mode = value
		applied = true
	}
	if value := runtimeConfigString(values, "routerModel"); value != "" {
		out.RouterModel = value
		applied = true
	}
	if value := runtimeConfigString(values, "routerProvider"); value != "" {
		out.RouterProvider = value
		applied = true
	}
	if value := runtimeConfigString(values, "routerBaseURL"); value != "" {
		out.RouterBaseURL = value
		applied = true
	}
	if value, ok := values["routerHasAPIKey"].(bool); ok {
		out.RouterHasAPIKey = value
		applied = true
	}
	if value := runtimeConfigFloat(values, "confidenceThreshold"); value > 0 {
		out.ConfidenceThreshold = value
		applied = true
	}
	if value := runtimeConfigInt(values, "timeoutSec"); value > 0 {
		out.TimeoutSec = value
		applied = true
	}
	if !applied {
		return runtimeConfigToolRouting{}, false
	}
	return out, true
}

// runtimeConfigFloat 从动态配置读取数值，兼容 JSON 默认的 float64。
func runtimeConfigFloat(cfg map[string]any, key string) float64 {
	switch value := cfg[key].(type) {
	case float64:
		return value
	case float32:
		return float64(value)
	case int:
		return float64(value)
	case int64:
		return float64(value)
	default:
		return 0
	}
}

// runtimeConfigInt 从动态配置读取整数，兼容 JSON float64。
func runtimeConfigInt(cfg map[string]any, key string) int {
	switch value := cfg[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}

// configCWD 返回前端可用工作目录，打包资源目录会映射到用户 home 下的应用目录。
func configCWD(cfg *contract.Config) string {
	if cfg == nil {
		return ""
	}
	value := strings.TrimSpace(cfg.ProjectRoot)
	if value == "" {
		return ""
	}
	clean := filepath.Clean(value)
	if isPackagedResourceRoot(clean) {
		return packagedAppHomeCWD()
	}
	// 前端需要 cwd 来展示项目上下文并读取作用域化 UI 状态，不能回落到资源目录。
	return clean
}

// packagedAppHomeCWD 解析打包应用的默认工作目录，环境变量非法时返回空值。
func packagedAppHomeCWD() string {
	value := strings.TrimSpace(os.Getenv(packagedAppHomeEnvKey))
	if value == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return ""
		}
		return filepath.Join(home, "Super Dolphin")
	}
	clean := filepath.Clean(value)
	if clean == "." || !filepath.IsAbs(clean) || isPackagedResourceRoot(clean) {
		return ""
	}
	return clean
}

// isPackagedResourceRoot 判断路径是否是只读打包资源根目录。
func isPackagedResourceRoot(root string) bool {
	info, err := os.Stat(filepath.Join(root, "runtime-manifest.json"))
	return err == nil && !info.IsDir()
}

// readLSPPromptHint 合并 shared-file 默认 hint 和用户覆盖值。
func readLSPPromptHint(
	ctx context.Context,
	prefs preferenceValueReader,
	sharedFiles sharedFileReader,
	cwd string,
) (*lspPromptHintResult, error) {
	defaultHint, err := readDefaultLSPPromptHint(ctx, sharedFiles)
	if err != nil {
		return nil, err
	}
	overrideHint, err := readLSPPromptOverride(ctx, prefs, cwd)
	if err != nil {
		return nil, err
	}
	return buildLSPPromptHintResult(defaultHint, overrideHint), nil
}

// writeLSPPromptHint 写入当前 cwd 的 LSP prompt hint 覆盖，并返回新的生效视图。
func writeLSPPromptHint(
	ctx context.Context,
	prefs preferenceStore,
	sharedFiles sharedFileReader,
	cwd, hint string,
) (*lspPromptHintResult, error) {
	if prefs == nil {
		return nil, errConfigPreferenceStoreRequired
	}
	overrideHint := hint
	if strings.TrimSpace(overrideHint) == "" {
		overrideHint = ""
	}
	if err := storePreference(ctx, prefs, strings.TrimSpace(cwd), lspPromptHintOverrideKey, overrideHint); err != nil {
		return nil, err
	}
	defaultHint, err := readDefaultLSPPromptHint(ctx, sharedFiles)
	if err != nil {
		return nil, err
	}
	return buildLSPPromptHintResult(defaultHint, overrideHint), nil
}

// readDefaultLSPPromptHint 从 shared file store 读取内置 LSP 必读提示，缺文件时返回空。
func readDefaultLSPPromptHint(ctx context.Context, sharedFiles sharedFileReader) (string, error) {
	if sharedFiles == nil {
		return "", nil
	}
	file, err := sharedFiles.Get(ctx, lspPromptHintDefaultPath)
	switch {
	case err == nil:
		if file == nil {
			return "", nil
		}
		return file.Content, nil
	case contract.IsNotFound(err):
		return "", nil
	default:
		return "", err
	}
}

// readLSPPromptOverride 读取当前 cwd 的 LSP prompt hint 覆盖值，非字符串会转成文本。
func readLSPPromptOverride(ctx context.Context, prefs preferenceValueReader, cwd string) (string, error) {
	if prefs == nil {
		return "", nil
	}
	raw, err := prefs.GetValue(ctx, strings.TrimSpace(cwd), normalizePreferenceKey(lspPromptHintOverrideKey))
	switch {
	case err == nil:
	case contract.IsNotFound(err):
		return "", nil
	default:
		return "", err
	}
	switch value := decodePreferenceValue(raw).(type) {
	case nil:
		return "", nil
	case string:
		return value, nil
	default:
		return strings.TrimSpace(fmt.Sprint(value)), nil
	}
}

// buildLSPPromptHintResult 根据覆盖值是否为空决定最终使用默认 hint 还是用户 hint。
func buildLSPPromptHintResult(defaultHint, overrideHint string) *lspPromptHintResult {
	usingDefault := strings.TrimSpace(overrideHint) == ""
	hint := overrideHint
	if usingDefault {
		hint = defaultHint
	}
	return &lspPromptHintResult{
		Hint:         hint,
		DefaultHint:  defaultHint,
		OverrideHint: overrideHint,
		UsingDefault: usingDefault,
	}
}
