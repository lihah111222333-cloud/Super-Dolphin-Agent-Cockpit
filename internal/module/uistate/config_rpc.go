package uistate

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/module/thread"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
	"github.com/anthropic-ai/super-agent-v3/internal/store/uipreference"
)

const (
	lspPromptHintOverrideKey = "config/lspPromptHint.override"
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
	cfg *platformconfig.Config,
	prefs uipreference.Store,
	sharedFiles sharedfilestore.Store,
	threads thread.Service,
) rpc.HandlerMapResult {
	return rpc.HandlerMapResult{Handlers: handler.Map{
		"config/read": rpc.StrictHandler(func(ctx context.Context, _ struct{}) (any, error) {
			return readRuntimeConfig(ctx, cfg, prefs, threads), nil
		}),
		"config/lspPromptHint/read": rpc.StrictHandler(func(ctx context.Context, p scopeParams) (any, error) {
			return readLSPPromptHint(ctx, prefs, sharedFiles, p.Cwd)
		}),
		"config/lspPromptHint/write": rpc.StrictHandler(func(ctx context.Context, p lspPromptHintWriteParams) (any, error) {
			return writeLSPPromptHint(ctx, prefs, sharedFiles, p.Cwd, p.Hint)
		}),
	}}
}

func readRuntimeConfig(
	ctx context.Context,
	cfg *platformconfig.Config,
	prefs uipreference.Store,
	threads thread.Service,
) runtimeConfigResult {
	result := defaultRuntimeConfig(cfg)
	threadID := readActiveThreadID(ctx, prefs, result.CWD)
	if threadID == "" || threads == nil {
		return result
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

func defaultRuntimeConfig(cfg *platformconfig.Config) runtimeConfigResult {
	return runtimeConfigResult{
		Model:                 "o4-mini",
		ModelProvider:         nil,
		CWD:                   configCWD(cfg),
		ApprovalPolicy:        "on-failure",
		Sandbox:               nil,
		Config:                nil,
		BaseInstructions:      nil,
		DeveloperInstructions: nil,
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
	case platformdb.IsNotFound(err):
		return ""
	default:
		return ""
	}
	value, _ := decodePreferenceValue(raw).(string)
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

func configCWD(cfg *platformconfig.Config) string {
	if cfg == nil {
		return ""
	}
	value := strings.TrimSpace(cfg.ProjectRoot)
	if value == "" {
		return ""
	}
	// Intentional: frontend needs cwd for project context and scope-aware UI state.
	return filepath.Clean(value)
}

func readLSPPromptHint(
	ctx context.Context,
	prefs uipreference.Store,
	sharedFiles sharedfilestore.Store,
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
	sharedFiles sharedfilestore.Store,
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

func readDefaultLSPPromptHint(ctx context.Context, sharedFiles sharedfilestore.Store) (string, error) {
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
	case platformdb.IsNotFound(err):
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
	case platformdb.IsNotFound(err):
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
