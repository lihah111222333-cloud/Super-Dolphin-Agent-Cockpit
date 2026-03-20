package wails

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
)

const defaultGroup = ""

type App struct {
	dispatch func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error)
	emitter  func(event string, data any)
	wailsApp *application.App
}

func (a *App) CallAPI(method string, paramsJSON string) (any, error) {
	if a == nil || a.dispatch == nil {
		return nil, errors.New("wails binding: dispatch is not configured")
	}
	method = strings.TrimSpace(method)
	if method == "" {
		return nil, errors.New("wails binding: method is required")
	}
	params, err := parseParamsJSON(paramsJSON)
	if err != nil {
		return nil, err
	}
	result, err := a.dispatch(a.callContext(), method, params)
	if err != nil {
		return nil, err
	}
	return decodeAPIResult(result)
}

func (a *App) GetBuildInfo() map[string]string {
	return currentBuildInfo()
}

func (a *App) GetGroup() string {
	return defaultGroup
}

func (a *App) bindRuntime(wailsApp *application.App) {
	a.wailsApp = wailsApp
	a.emitter = func(event string, data any) {
		if wailsApp == nil || wailsApp.Event == nil {
			return
		}
		wailsApp.Event.Emit(event, data)
	}
}

func (a *App) emit(event string, data any) {
	if a == nil || a.emitter == nil {
		return
	}
	a.emitter(event, data)
}

func (a *App) callContext() context.Context {
	if a != nil && a.wailsApp != nil {
		if ctx := a.wailsApp.Context(); ctx != nil {
			return ctx
		}
	}
	return context.Background()
}

func parseParamsJSON(raw string) (json.RawMessage, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return json.RawMessage("{}"), nil
	}
	payload := json.RawMessage(text)
	if !json.Valid(payload) {
		return nil, errors.New("wails binding: paramsJSON must be valid JSON")
	}
	return payload, nil
}

func decodeAPIResult(result json.RawMessage) (any, error) {
	if len(result) == 0 || string(result) == "null" {
		return nil, nil
	}
	var value any
	if err := json.Unmarshal(result, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func currentBuildInfo() map[string]string {
	info := map[string]string{
		"version": "dev",
		"commit":  "unknown",
		"runtime": runtime.GOOS + "/" + runtime.GOARCH,
	}
	buildInfo, ok := debug.ReadBuildInfo()
	if !ok {
		return info
	}
	if version := strings.TrimSpace(buildInfo.Main.Version); version != "" && version != "(devel)" {
		info["version"] = version
	}
	for _, setting := range buildInfo.Settings {
		applyBuildSetting(info, setting.Key, setting.Value)
	}
	return info
}

func applyBuildSetting(info map[string]string, key, value string) {
	switch key {
	case "vcs.revision":
		if commit := shortCommit(value); commit != "" {
			info["commit"] = commit
		}
	case "vcs.time":
		if value = strings.TrimSpace(value); value != "" {
			info["buildTime"] = value
		}
	case "vcs.modified":
		if strings.EqualFold(strings.TrimSpace(value), "true") {
			info["dirty"] = "true"
		}
	}
}

func shortCommit(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) <= 12 {
		return raw
	}
	return raw[:12]
}
