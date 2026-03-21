package wails

import (
	"context"
	"strings"

	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/module/uistate"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

const codeLocateLimit = 24

type scopeParams struct {
	Project  string   `json:"project,omitempty"`
	Projects []string `json:"projects,omitempty"`
}

type codeSaveParams struct {
	FilePath  string `json:"filePath"`
	Content   string `json:"content"`
	CreateNew bool   `json:"createNew,omitempty"`
	scopeParams
}

type codeLocateParams struct {
	FilePath string `json:"filePath"`
	scopeParams
}

type codeOpenParams struct {
	FilePath string `json:"filePath"`
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
	scopeParams
}

type copyTextParams struct {
	Text string `json:"text"`
}

type selectProjectDirParams struct {
	DefaultPath string `json:"defaultPath,omitempty"`
}

type selectFilesParams struct {
	DefaultPath string `json:"defaultPath,omitempty"`
}

type openNewWindowParams struct {
	Group       string `json:"group,omitempty"`
	N           int    `json:"n,omitempty"`
	UIBootstrap string `json:"uiBootstrap,omitempty"`
	Cwd         string `json:"cwd,omitempty"`
}

// NewRPCHandlers registers desktop-only UI helpers behind the generic RPC bridge.
func NewRPCHandlers(app *App, cfg *config.Config, uiState uistate.Service) rpc.HandlerMapResult {
	return rpc.HandlerMapResult{Handlers: handler.Map{
		"ui/code/save": rpc.StrictHandler(func(ctx context.Context, p codeSaveParams) (any, error) {
			return handleCodeSave(ctx, cfg, uiState, p)
		}),
		"ui/code/locate": rpc.StrictHandler(func(ctx context.Context, p codeLocateParams) (any, error) {
			return handleCodeLocate(ctx, cfg, uiState, p)
		}),
		"ui/code/open": rpc.StrictHandler(func(ctx context.Context, p codeOpenParams) (any, error) {
			return handleCodeOpen(ctx, cfg, uiState, p)
		}),
		"ui/copyText": rpc.StrictHandler(func(ctx context.Context, p copyTextParams) (any, error) {
			return handleCopyText(app, strings.TrimSpace(p.Text))
		}),
		"ui/selectProjectDir": rpc.StrictHandler(func(ctx context.Context, p selectProjectDirParams) (any, error) {
			path, err := app.selectProjectDir(p.DefaultPath)
			if err != nil {
				return nil, err
			}
			return map[string]string{"path": path}, nil
		}),
		"ui/selectProjectDirs": rpc.StrictHandler(func(ctx context.Context, p selectProjectDirParams) (any, error) {
			paths, err := app.SelectProjectDirs(p.DefaultPath)
			if err != nil {
				return nil, err
			}
			return map[string][]string{"paths": paths}, nil
		}),
		"ui/selectFiles": rpc.StrictHandler(func(ctx context.Context, p selectFilesParams) (any, error) {
			paths, err := app.selectFiles(p.DefaultPath)
			if err != nil {
				return nil, err
			}
			return map[string][]string{"paths": paths}, nil
		}),
		"ui/openNewWindow": rpc.StrictHandler(func(ctx context.Context, p openNewWindowParams) (any, error) {
			windowID, err := app.openNewWindow(p.Group, p.N, p.UIBootstrap, p.Cwd)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"ok":       true,
				"windowId": windowID,
				"cwd":      strings.TrimSpace(p.Cwd),
			}, nil
		}),
	}}
}

func handleCodeSave(
	ctx context.Context,
	cfg *config.Config,
	uiState uistate.Service,
	p codeSaveParams,
) (codeSaveResult, error) {
	roots, err := requestScopeRoots(ctx, cfg, uiState, p.Project, p.Projects)
	if err != nil {
		return codeSaveResult{}, err
	}
	return saveScopedFile(p.FilePath, p.Content, roots, p.CreateNew)
}

func handleCodeLocate(
	ctx context.Context,
	cfg *config.Config,
	uiState uistate.Service,
	p codeLocateParams,
) (codeLocateResult, error) {
	roots, err := requestScopeRoots(ctx, cfg, uiState, p.Project, p.Projects)
	if err != nil {
		return codeLocateResult{}, err
	}
	return locateScopedFile(ctx, p.FilePath, roots, codeLocateLimit)
}

func handleCodeOpen(
	ctx context.Context,
	cfg *config.Config,
	uiState uistate.Service,
	p codeOpenParams,
) (codeOpenResult, error) {
	roots, err := requestScopeRoots(ctx, cfg, uiState, p.Project, p.Projects)
	if err != nil {
		return codeOpenResult{}, err
	}
	return openScopedFile(ctx, p.FilePath, p.Line, p.Column, roots)
}

func handleCopyText(app *App, text string) (map[string]any, error) {
	if app == nil || app.wailsApp == nil {
		return map[string]any{
			"ok":    false,
			"error": "clipboard not available in headless mode",
		}, nil
	}
	ok, err := app.CopyText(text)
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": ok}, nil
}
