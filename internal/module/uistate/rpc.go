package uistate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/creachadair/jrpc2/handler"

	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/runtimeenv"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
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

type videoAPIKeyParams struct {
	APIKey string `json:"apiKey"`
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

// NewUIStateHandlers 创建UI状态处理器。
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
			value := preferenceValue(*prefs, p.Key)
			logProviderPreferenceTrace("ui/preferences/get: trace", p.Cwd, p.Key, value)
			return value, nil
		}),
		"ui/preferences/getAll": platformrpc.StrictHandler(func(ctx context.Context, p scopeParams) (any, error) {
			return svc.GetPreferences(withPreferenceScope(ctx, p.Cwd))
		}),
		"ui/preferences/set": platformrpc.StrictHandler(func(ctx context.Context, p preferenceSetParams) (any, error) {
			if err := svc.SetPreference(withPreferenceScope(ctx, p.Cwd), p.Key, p.Value); err != nil {
				return nil, err
			}
			logProviderPreferenceTrace("ui/preferences/set: trace", p.Cwd, p.Key, p.Value)
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
		"ui/video/setApiKey": platformrpc.StrictHandler(func(_ context.Context, p videoAPIKeyParams) (any, error) {
			key := strings.TrimSpace(p.APIKey)
			if key == "" {
				return nil, errors.New("apiKey is required")
			}
			if err := os.Setenv("SILICONFLOW_API_KEY", key); err != nil {
				return nil, err
			}
			if err := runtimeenv.WriteVideoEnv(key); err != nil {
				return nil, err
			}
			return map[string]any{"ok": true}, nil
		}),
		"ui/video/getApiKey": platformrpc.StrictHandler(func(_ context.Context, _ struct{}) (any, error) {
			key := strings.TrimSpace(os.Getenv("SILICONFLOW_API_KEY"))
			masked := ""
			if len(key) > 8 {
				masked = key[:4] + strings.Repeat("*", len(key)-8) + key[len(key)-4:]
			} else if key != "" {
				masked = strings.Repeat("*", len(key))
			}
			return map[string]any{"configured": key != "", "masked": masked}, nil
		}),
	}}
}

func logProviderPreferenceTrace(msg, cwd, key string, value any) {
	key = strings.TrimSpace(key)
	if !shouldTraceProviderPreference(key) {
		return
	}
	valueText, valueKind := providerPreferenceTraceValue(value)
	pkglogger.Info(msg,
		"cwd", strings.TrimSpace(cwd),
		"key", key,
		"has_value", value != nil,
		"value_kind", valueKind,
		"value", valueText)
}

func shouldTraceProviderPreference(key string) bool {
	if key == "settings.provider.active" {
		return true
	}
	if !strings.HasPrefix(key, "settings.provider.") {
		return false
	}
	return strings.HasSuffix(key, ".model") ||
		strings.HasSuffix(key, ".effort") ||
		strings.HasSuffix(key, ".codexModelProvider")
}

// providerPreferenceTraceValue 处理providerpreferencetrace值。
func providerPreferenceTraceValue(value any) (string, string) {
	switch v := value.(type) {
	case nil:
		return "", "nil"
	case string:
		return truncatePreferenceTraceValue(v), "string"
	case bool:
		return strconv.FormatBool(v), "bool"
	case int:
		return strconv.Itoa(v), "number"
	case int64:
		return strconv.FormatInt(v, 10), "number"
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), "number"
	default:
		return "", fmt.Sprintf("%T", value)
	}
}

func truncatePreferenceTraceValue(value string) string {
	const maxPreferenceTraceValueLen = 160
	value = strings.TrimSpace(value)
	if len(value) <= maxPreferenceTraceValueLen {
		return value
	}
	return value[:maxPreferenceTraceValueLen] + "...(truncated)"
}
