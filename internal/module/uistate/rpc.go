package uistate

import (
	"context"
	"strings"

	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

type scopeParams struct {
	Cwd         string `json:"cwd,omitempty"`
	ThreadID    string `json:"threadId,omitempty"`
	IncludeDiff bool   `json:"includeDiff,omitempty"`
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

func NewUIStateHandlers(svc Service) rpc.HandlerMapResult {
	return rpc.HandlerMapResult{Handlers: handler.Map{
		"ui/state/get": rpc.StrictHandler(func(ctx context.Context, p scopeParams) (any, error) {
			return svc.GetState(withPreferenceScope(ctx, p.Cwd))
		}),
		"ui/sidebar/get": rpc.StrictHandler(func(ctx context.Context, p scopeParams) (any, error) {
			return svc.GetSidebar(withPreferenceScope(ctx, p.Cwd))
		}),
		"ui/preferences/get": rpc.StrictHandler(func(ctx context.Context, p preferenceGetParams) (any, error) {
			prefs, err := svc.GetPreferences(withPreferenceScope(ctx, p.Cwd))
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(p.Key) == "" {
				return prefs, nil
			}
			return preferenceValue(*prefs, p.Key), nil
		}),
		"ui/preferences/getAll": rpc.StrictHandler(func(ctx context.Context, p scopeParams) (any, error) {
			return svc.GetPreferences(withPreferenceScope(ctx, p.Cwd))
		}),
		"ui/preferences/set": rpc.StrictHandler(func(ctx context.Context, p preferenceSetParams) (any, error) {
			if err := svc.SetPreference(withPreferenceScope(ctx, p.Cwd), p.Key, p.Value); err != nil {
				return nil, err
			}
			return map[string]any{"ok": true}, nil
		}),
	}}
}
