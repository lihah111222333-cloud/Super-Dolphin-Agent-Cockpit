package wails

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

func encodeWindowBootstrapSnapshot(snapshot map[string]any) (string, error) {
	if len(snapshot) == 0 {
		return "", nil
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeWindowBootstrapSnapshot(raw string) (map[string]any, error) {
	encoded := strings.TrimSpace(raw)
	if encoded == "" {
		return nil, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	var snapshot map[string]any
	if err := json.Unmarshal(decoded, &snapshot); err != nil {
		return nil, err
	}
	if len(snapshot) == 0 {
		return nil, nil
	}
	return snapshot, nil
}

func normalizeWindowGroup(group, fallback string) string {
	if value := strings.TrimSpace(group); value != "" {
		return value
	}
	return strings.TrimSpace(fallback)
}

// registerWindowState 注册window状态。
func (a *App) registerWindowState(name, group string, snapshot map[string]any) {
	if a == nil {
		return
	}

	name = strings.TrimSpace(name)
	group = strings.TrimSpace(group)

	a.windowStateMu.Lock()
	defer a.windowStateMu.Unlock()

	if name != "" && group != "" {
		if a.windowGroups == nil {
			a.windowGroups = make(map[string]string)
		}
		a.windowGroups[name] = group
	}

	if name == "" {
		return
	}
	if len(snapshot) == 0 {
		if a.windowBootstrapByName != nil {
			delete(a.windowBootstrapByName, name)
		}
		return
	}
	if a.windowBootstrapByName == nil {
		a.windowBootstrapByName = make(map[string]map[string]any)
	}
	a.windowBootstrapByName[name] = cloneWindowBootstrapSnapshot(snapshot)
}

func cloneWindowBootstrapSnapshot(snapshot map[string]any) map[string]any {
	if len(snapshot) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(snapshot))
	for key, value := range snapshot {
		cloned[key] = value
	}
	return cloned
}

// consumeWindowBootstrapSnapshot 处理consumewindow启动快照。
func (a *App) consumeWindowBootstrapSnapshot() map[string]any {
	if a == nil {
		return nil
	}

	name := a.currentWindowName()

	a.windowStateMu.Lock()
	defer a.windowStateMu.Unlock()

	if snapshot := a.consumeWindowBootstrapByNameLocked(name); snapshot != nil {
		return snapshot
	}
	if len(a.windowBootstrap) != 0 {
		snapshot := cloneWindowBootstrapSnapshot(a.windowBootstrap)
		a.windowBootstrap = nil
		return snapshot
	}
	if len(a.windowBootstrapByName) == 1 {
		for key, snapshot := range a.windowBootstrapByName {
			delete(a.windowBootstrapByName, key)
			return cloneWindowBootstrapSnapshot(snapshot)
		}
	}
	return nil
}

func (a *App) consumeWindowBootstrapByNameLocked(name string) map[string]any {
	name = strings.TrimSpace(name)
	if name == "" || len(a.windowBootstrapByName) == 0 {
		return nil
	}
	snapshot, ok := a.windowBootstrapByName[name]
	if !ok {
		return nil
	}
	delete(a.windowBootstrapByName, name)
	return cloneWindowBootstrapSnapshot(snapshot)
}

func (a *App) currentWindowGroup() string {
	if a == nil {
		return defaultGroup
	}

	name := a.currentWindowName()

	a.windowStateMu.Lock()
	defer a.windowStateMu.Unlock()

	if name != "" {
		if group := strings.TrimSpace(a.windowGroups[name]); group != "" {
			return group
		}
	}
	if group := strings.TrimSpace(a.group); group != "" {
		return group
	}
	return defaultGroup
}

func (a *App) currentWindowName() string {
	if a == nil {
		return ""
	}
	if a.currentWindowNameFn != nil {
		return strings.TrimSpace(a.currentWindowNameFn())
	}
	if a.wailsApp == nil {
		return ""
	}
	if current := a.wailsApp.Window.Current(); current != nil {
		return strings.TrimSpace(current.Name())
	}
	return ""
}
