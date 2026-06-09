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
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
	"github.com/anthropic-ai/super-agent-v3/internal/store/uipreference"
)

const (
	lspPromptHintOverrideKey = "config/lspPromptHint.override"
	packagedAppHomeEnvKey    = "SUPER_DOLPHIN_HOME"
	// lspPromptHintDefaultPath is the shared-file source for the default injected prompt hint.
	lspPromptHintDefaultPath = "prompts/lsp-mandatory-prefix.md"
)

var errConfigPreferenceStoreRequired = errors.New("uistate: ui preference store is not configured")

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

type runtimeConfigToolRouting struct {
	Mode                string  `json:"mode"`
	RouterModel         string  `json:"routerModel"`
	RouterProvider      string  `json:"routerProvider"`
	RouterBaseURL       string  `json:"routerBaseURL"`
	RouterHasAPIKey     bool    `json:"routerHasAPIKey"`
	ConfidenceThreshold float64 `json:"confidenceThreshold"`
	TimeoutSec          int     `json:"timeoutSec"`
}

type lspPromptHintWriteParams struct {
	Hint string `json:"hint,omitempty"`
	Cwd  string `json:"cwd,omitempty"`
}

type lspPromptHintResult struct {
	Hint         string `json:"hint"`
	DefaultHint  string `json:"defaultHint"`
	OverrideHint string `json:"overrideHint"`
	UsingDefault bool   `json:"usingDefault"`
}

func NewConfigHandlers(
	cfg *contract.Config,
	prefs uipreference.Store,
	sharedFiles sharedfilestore.Reader,
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

func readRuntimeConfig(
	ctx context.Context,
	cfg *contract.Config,
	prefs uipreference.Store,
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

func applyRuntimeConfigOverrides(result *runtimeConfigResult, cfg map[string]any) {
	if result == nil || len(cfg) == 0 {
		return
	}
	applyRuntimeStringOverrides(result, cfg)
	applyRuntimeObjectOverrides(result, cfg)
}

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

func applyRuntimeObjectOverrides(result *runtimeConfigResult, cfg map[string]any) {
	if value, ok := cfg["sandbox"]; ok && value != nil {
		result.Sandbox = value
	}
	if routing, ok := runtimeToolRouting(result.ToolRouting, cfg["toolRouting"]); ok {
		result.ToolRouting = routing
	}
}

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

func readActiveThreadID(ctx context.Context, prefs uipreference.Store, cwd string) string {
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

func firstRuntimeConfigValue(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

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
	// Intentional: frontend needs cwd for project context and scope-aware UI state.
	return clean
}

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

func isPackagedResourceRoot(root string) bool {
	info, err := os.Stat(filepath.Join(root, "runtime-manifest.json"))
	return err == nil && !info.IsDir()
}

func readLSPPromptHint(
	ctx context.Context,
	prefs uipreference.Store,
	sharedFiles sharedfilestore.Reader,
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

func writeLSPPromptHint(
	ctx context.Context,
	prefs uipreference.Store,
	sharedFiles sharedfilestore.Reader,
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

func readDefaultLSPPromptHint(ctx context.Context, sharedFiles sharedfilestore.Reader) (string, error) {
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

func readLSPPromptOverride(ctx context.Context, prefs uipreference.Store, cwd string) (string, error) {
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
