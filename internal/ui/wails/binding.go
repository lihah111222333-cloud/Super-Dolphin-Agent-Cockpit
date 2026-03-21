package wails

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// LaunchAgent preserves the legacy desktop entrypoint while routing creation
// through the typed V3 thread/start RPC using the V2 baseInstructions field.
// The legacy name is deferred until a first-class thread naming flow is restored.
func (a *App) LaunchAgent(name, prompt, cwd string) (any, error) {
	_ = name
	return a.callAPIObject("thread/start", map[string]string{
		"cwd":              strings.TrimSpace(cwd),
		"baseInstructions": prompt,
	})
}

// StopAgent keeps the V2 method name while delegating execution to thread/stop.
func (a *App) StopAgent(threadID string) error {
	_, err := a.callAPIObject("thread/stop", map[string]string{
		"threadId": strings.TrimSpace(threadID),
	})
	return err
}

func (a *App) ListAgents() (any, error) {
	return a.callAPIObject("agent.list", struct{}{})
}

func (a *App) GetBuildInfo() map[string]string {
	return currentBuildInfo()
}

func (a *App) GetGroup() string {
	return defaultGroup
}

// TODO(P9): Defer multi-window desktop support until the V3 window lifecycle
// and cross-window routing contracts are settled.
func (a *App) OpenNewWindow(group string, n int, uiBootstrap, cwd string) error {
	return deferredBindingError("OpenNewWindow", "multi-window desktop flow is deferred")
}

// TODO(P9): Restore desktop LSP convenience bindings after the typed LSP tool
// family lands.
func (a *App) GetLSPDiagnostics(filePath string) (string, error) {
	return "", deferredBindingError("GetLSPDiagnostics", "desktop LSP helpers move to the P9 LSP tool family")
}

// TODO(P9): Restore desktop LSP convenience bindings after the typed LSP tool
// family lands.
func (a *App) GetLSPStatus() (any, error) {
	return nil, deferredBindingError("GetLSPStatus", "desktop LSP helpers move to the P9 LSP tool family")
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

func (a *App) callAPIObject(method string, params any) (any, error) {
	paramsJSON, err := encodeParamsJSON(params)
	if err != nil {
		return nil, err
	}
	return a.CallAPI(method, paramsJSON)
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

func encodeParamsJSON(params any) (string, error) {
	if params == nil {
		return "{}", nil
	}
	data, err := json.Marshal(params)
	if err != nil {
		return "", err
	}
	return string(data), nil
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

func deferredBindingError(method, reason string) error {
	return fmt.Errorf("wails binding: %s is not implemented: %s", method, reason)
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
