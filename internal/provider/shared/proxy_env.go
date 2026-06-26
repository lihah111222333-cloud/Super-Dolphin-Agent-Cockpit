package shared

import "strings"

// EnsureLoopbackNoProxy 保留外部代理配置，同时强制本地 app-server/MCP 流量绕过代理。
// 只有存在代理或已有 NO_PROXY 时才改写环境，避免无代理场景产生多余变量。
func EnsureLoopbackNoProxy(env []string) []string {
	const loopbacks = "127.0.0.1,localhost,::1"
	var existing []string
	hasProxy := false
	filtered := make([]string, 0, len(env)+2)
	for _, kv := range env {
		key, val, ok := splitEnv(kv)
		if !ok {
			filtered = append(filtered, kv)
			continue
		}
		switch strings.ToUpper(key) {
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY":
			hasProxy = hasProxy || strings.TrimSpace(val) != ""
			filtered = append(filtered, kv)
		case "NO_PROXY":
			existing = append(existing, val)
		default:
			filtered = append(filtered, kv)
		}
	}
	if !hasProxy && len(existing) == 0 {
		return env
	}
	merged := mergeCSV(append(existing, loopbacks)...)
	return append(filtered, "NO_PROXY="+merged, "no_proxy="+merged)
}

func splitEnv(kv string) (string, string, bool) {
	idx := strings.IndexByte(kv, '=')
	if idx <= 0 {
		return "", "", false
	}
	return strings.TrimSpace(kv[:idx]), kv[idx+1:], true
}

// mergeCSV 合并逗号分隔列表并按大小写去重。
// 原始顺序会保留，便于 NO_PROXY 中的用户自定义规则保持优先级。
func mergeCSV(parts ...string) string {
	seen := make(map[string]struct{}, 8)
	out := make([]string, 0, 8)
	for _, group := range parts {
		for _, raw := range strings.Split(group, ",") {
			item := strings.TrimSpace(raw)
			if item == "" {
				continue
			}
			key := strings.ToLower(item)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, item)
		}
	}
	return strings.Join(out, ",")
}
