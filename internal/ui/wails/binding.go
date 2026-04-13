package wails

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
)

const (
	defaultGroup = "default"
)

type App struct {
	dispatch    func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error)
	emitter     func(event string, data any)
	wailsApp    *application.App
	windowTitle string
	debug       bool

	group string

	windowStateMu         sync.Mutex
	windowBootstrap       map[string]any
	windowBootstrapByName map[string]map[string]any
	windowGroups          map[string]string

	openNewWindowInvoker func(group string, n int, uiBootstrap, cwd string) (string, error)
	currentWindowNameFn  func() string
}

func (a *App) CallAPI(method string, params json.RawMessage) (any, error) {
	if a == nil || a.dispatch == nil {
		return nil, errors.New("wails binding: dispatch is not configured")
	}
	method = strings.TrimSpace(method)
	if method == "" {
		return nil, errors.New("wails binding: method is required")
	}
	if len(params) == 0 {
		params = json.RawMessage("{}")
	}
	params = stripFrontendMeta(params)
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
	return a.currentWindowGroup()
}

func (a *App) OpenNewWindow(group string, n int, uiBootstrap, cwd string) error {
	_, err := a.openNewWindow(group, n, uiBootstrap, cwd)
	return err
}

func (a *App) openNewWindow(group string, n int, uiBootstrap, cwd string) (string, error) {
	if a != nil && a.openNewWindowInvoker != nil {
		return a.openNewWindowInvoker(group, n, uiBootstrap, cwd)
	}
	app, err := a.requireWailsApp()
	if err != nil {
		return "", err
	}
	snapshot, err := decodeWindowBootstrapSnapshot(uiBootstrap)
	if err != nil {
		return "", fmt.Errorf("wails binding: decode ui bootstrap: %w", err)
	}
	group = normalizeWindowGroup(group, a.currentWindowGroup())
	title := strings.TrimSpace(a.windowTitle)
	if title == "" {
		title = applicationTitle()
	}
	// TODO(P7.5): Frontend still needs to read ao_ui_bootstrap/ao_window_cwd
	// from window.location.search before these query params affect runtime state.
	name := buildWindowName(group, n)
	window := createWindow(app, title, a.debug, name, uiBootstrap, cwd)
	if window == nil {
		return "", errors.New("wails binding: failed to create window")
	}
	a.registerWindowState(name, group, snapshot)
	return fmt.Sprintf("%d", window.ID()), nil
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
	data, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	return a.CallAPI(method, json.RawMessage(data))
}

// stripFrontendMeta removes _ao-prefixed metadata fields that the frontend
// injects into every CallAPI payload (e.g. _aoClientKind, _aoClientRoute).
// These are useful for logging but must not reach StrictHandler RPC endpoints
// which reject unknown fields.
func stripFrontendMeta(raw json.RawMessage) json.RawMessage {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return raw // not an object — pass through as-is
	}
	changed := false
	for key := range obj {
		if strings.HasPrefix(key, "_ao") {
			delete(obj, key)
			changed = true
		}
	}
	if !changed {
		return raw
	}
	cleaned, err := json.Marshal(obj)
	if err != nil {
		return raw
	}
	return cleaned
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
