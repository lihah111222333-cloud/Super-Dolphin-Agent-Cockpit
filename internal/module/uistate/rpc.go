package uistate

import (
	"context"
	"strings"

	"github.com/creachadair/jrpc2/handler"

	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

type scopeParams struct {
	Cwd               string `json:"cwd,omitempty"`
	ThreadID          string `json:"threadId,omitempty"`
	IncludeDiff       bool   `json:"includeDiff,omitempty"`
	KnownDiffRevision int    `json:"knownDiffRevision,omitempty"`
}

type preferenceGetParams struct {
	Key string `json:"key,omitempty"`
	Cwd string `json:"cwd,omitempty"`
}

type preferenceSetParams struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
	Cwd   string `json:"cwd,omitempty"`
}

type projectPathParams struct {
	Path string `json:"path,omitempty"`
	Cwd  string `json:"cwd,omitempty"`
}

func NewUIStateHandlers(svc Service) platformrpc.HandlerMapResult {
	return platformrpc.HandlerMapResult{Handlers: handler.Map{
		"ui/state/get": platformrpc.StrictHandler(func(ctx context.Context, p scopeParams) (any, error) {
			ctx = withPreferenceScope(ctx, p.Cwd)
			ctx = withDiffStateRequest(ctx, strings.TrimSpace(p.ThreadID), p.IncludeDiff, p.KnownDiffRevision)
			return svc.GetState(ctx)
		}),
		"ui/sidebar/get": platformrpc.StrictHandler(func(ctx context.Context, p scopeParams) (any, error) {
			return svc.GetSidebar(withPreferenceScope(ctx, p.Cwd))
		}),
		"ui/preferences/get": platformrpc.StrictHandler(func(ctx context.Context, p preferenceGetParams) (any, error) {
			prefs, err := svc.GetPreferences(withPreferenceScope(ctx, p.Cwd))
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(p.Key) == "" {
				return prefs, nil
			}
			return preferenceValue(*prefs, p.Key), nil
		}),
		"ui/preferences/getAll": platformrpc.StrictHandler(func(ctx context.Context, p scopeParams) (any, error) {
			return svc.GetPreferences(withPreferenceScope(ctx, p.Cwd))
		}),
		"ui/preferences/set": platformrpc.StrictHandler(func(ctx context.Context, p preferenceSetParams) (any, error) {
			if err := svc.SetPreference(withPreferenceScope(ctx, p.Cwd), p.Key, p.Value); err != nil {
				return nil, err
			}
			return map[string]any{"ok": true}, nil
		}),
		"ui/projects/get": platformrpc.StrictHandler(func(ctx context.Context, p scopeParams) (any, error) {
			return svc.GetProjects(withPreferenceScope(ctx, p.Cwd))
		}),
		"ui/projects/setActive": platformrpc.StrictHandler(func(ctx context.Context, p projectPathParams) (any, error) {
			return svc.SetActiveProject(withPreferenceScope(ctx, p.Cwd), p.Path)
		}),
		"ui/projects/add": platformrpc.StrictHandler(func(ctx context.Context, p projectPathParams) (any, error) {
			return svc.AddProject(withPreferenceScope(ctx, p.Cwd), p.Path)
		}),
		"ui/projects/remove": platformrpc.StrictHandler(func(ctx context.Context, p projectPathParams) (any, error) {
			return svc.RemoveProject(withPreferenceScope(ctx, p.Cwd), p.Path)
		}),
	}}
}
