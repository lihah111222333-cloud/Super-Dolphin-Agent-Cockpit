package uistate

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/creachadair/jrpc2/handler"

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
	CWD string `json:"cwd"`
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
	files sharedfilestore.Store,
) rpc.HandlerMapResult {
	return rpc.HandlerMapResult{Handlers: handler.Map{
		"config/read": rpc.StrictHandler(func(context.Context, struct{}) (any, error) {
			return runtimeConfigResult{CWD: configCWD(cfg)}, nil
		}),
		"config/lspPromptHint/read": rpc.StrictHandler(func(ctx context.Context, p scopeParams) (any, error) {
			return readLSPPromptHint(ctx, prefs, files, p.Cwd)
		}),
		"config/lspPromptHint/write": rpc.StrictHandler(func(ctx context.Context, p lspPromptHintWriteParams) (any, error) {
			return writeLSPPromptHint(ctx, prefs, files, p.Cwd, p.Hint)
		}),
	}}
}

func configCWD(cfg *platformconfig.Config) string {
	if cfg == nil {
		return ""
	}
	value := strings.TrimSpace(cfg.ProjectRoot)
	if value == "" {
		return ""
	}
	// Intentional: desktop frontend needs the normalized window cwd for scope-aware UI state.
	return filepath.Clean(value)
}

func readLSPPromptHint(
	ctx context.Context,
	prefs uipreference.Store,
	files sharedfilestore.Store,
	cwd string,
) (*lspPromptHintResult, error) {
	defaultHint, err := readDefaultLSPPromptHint(ctx, files)
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
	files sharedfilestore.Store,
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
	defaultHint, err := readDefaultLSPPromptHint(ctx, files)
	if err != nil {
		return nil, err
	}
	return buildLSPPromptHintResult(defaultHint, overrideHint), nil
}

func readDefaultLSPPromptHint(ctx context.Context, files sharedfilestore.Store) (string, error) {
	if files == nil {
		return "", nil
	}
	file, err := files.Get(ctx, lspPromptHintDefaultPath)
	switch {
	case err == nil:
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
