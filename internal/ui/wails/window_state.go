package wails

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

// encodeWindowBootstrapSnapshot 将新窗口启动快照编码为 URL-safe 字符串。
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

// decodeWindowBootstrapSnapshot 解码 URL 中的启动快照并校验其 JSON 结构。
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

// normalizeWindowGroup 选择显式窗口组，空值时使用 fallback。
func normalizeWindowGroup(group, fallback string) string {
	if value := strings.TrimSpace(group); value != "" {
		return value
	}
	return strings.TrimSpace(fallback)
}

// registerWindowState 登记窗口组和一次性启动快照。
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

// cloneWindowBootstrapSnapshot 复制启动快照，避免调用方修改内部状态。
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

// consumeWindowBootstrapSnapshot 消费当前窗口的一次性启动快照。
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

// consumeWindowBootstrapByNameLocked 在持锁状态下按窗口名取出并删除启动快照。
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

// currentWindowGroup 返回当前窗口组，缺失时使用默认组。
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

// currentWindowName 返回当前 Wails 窗口名，测试可通过 currentWindowNameFn 替换。
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
