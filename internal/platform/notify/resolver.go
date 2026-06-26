// Package notify 提供 core 和 mcp-orch 共享的通知基础设施。
// 它负责 alias 解析、HTTPS-only webhook 发送、SSRF 防护、mention 清洗和三端消息渲染。
package notify

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Platform 标识 channel alias 对应的 webhook 平台；未知值会在配置解析时 fail-fast。
type Platform string

const (
	PlatformDingtalk Platform = "dingtalk"
	PlatformFeishu   Platform = "feishu"
	PlatformSlack    Platform = "slack"
)

// ChannelConfig 是单个 alias 解析后的跨模块 wire 配置。
type ChannelConfig struct {
	Platform Platform
	// URL 是完整 HTTPS webhook endpoint，日志输出前必须走脱敏规则。
	URL string
	// Secret 是钉钉/飞书 HMAC key；Slack 为空，因为 URL 本身就是凭据。
	Secret string
}

// 通知配置错误用于区分启动期配置问题和运行期 alias 未命中。
var (
	ErrDuplicateAlias      = errors.New("notify: duplicate channel alias")
	ErrUnsupportedPlatform = errors.New("notify: unsupported platform")
	ErrMissingField        = errors.New("notify: missing required field")
	ErrAliasNotFound       = errors.New("notify: channel alias not found")
)

// Resolver 将 channel alias 映射为 ChannelConfig；实现构造后不可变，读取无需加锁。
type Resolver interface {
	Resolve(alias string) (ChannelConfig, error)
}

// staticResolver 是从 NOTIFY_CHANNELS_JSON 解析出的默认内存 Resolver。
type staticResolver struct {
	channels map[string]ChannelConfig
}

// Resolve 按修剪后的 alias 返回 ChannelConfig；未知 alias 用 ErrAliasNotFound 明确表示配置缺失。
func (r *staticResolver) Resolve(alias string) (ChannelConfig, error) {
	a := strings.TrimSpace(alias)
	if a == "" {
		return ChannelConfig{}, fmt.Errorf("%w: alias is empty", ErrAliasNotFound)
	}
	c, ok := r.channels[a]
	if !ok {
		return ChannelConfig{}, fmt.Errorf("%w: %q", ErrAliasNotFound, a)
	}
	return c, nil
}

// ParseChannelsJSON 将 NOTIFY_CHANNELS_JSON 解析为不可变 Resolver。
// 它先扫描顶层 key 以拒绝重复 alias，避免 encoding/json 最后写入覆盖前值导致配置误判。
func ParseChannelsJSON(raw string) (Resolver, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return &staticResolver{channels: map[string]ChannelConfig{}}, nil
	}
	aliases, err := scanTopLevelAliases(text)
	if err != nil {
		return nil, err
	}
	var decoded map[string]rawChannel
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		return nil, fmt.Errorf("notify: parse channels: %w", err)
	}
	out := make(map[string]ChannelConfig, len(decoded))
	for _, alias := range aliases {
		rc, ok := decoded[alias]
		if !ok {
			// 防御性分支：理论上扫描到的 key 必然已被 decoded 覆盖，异常时跳过而不是 panic。
			continue
		}
		cfg, err := rc.toChannelConfig(alias)
		if err != nil {
			return nil, err
		}
		out[alias] = cfg
	}
	return &staticResolver{channels: out}, nil
}

// rawChannel 是单个 alias 的 JSON 形状，字段保持 string 以便错误类型直接由 json.Unmarshal 暴露。
type rawChannel struct {
	Platform string `json:"platform"`
	URL      string `json:"url"`
	Secret   string `json:"secret,omitempty"`
}

// toChannelConfig 校验平台、HTTPS URL 和必需 secret，错误会携带 alias 便于定位配置项。
func (r rawChannel) toChannelConfig(alias string) (ChannelConfig, error) {
	plat := Platform(strings.TrimSpace(strings.ToLower(r.Platform)))
	switch plat {
	case PlatformDingtalk, PlatformFeishu, PlatformSlack:
	default:
		return ChannelConfig{}, fmt.Errorf("%w: alias=%q platform=%q", ErrUnsupportedPlatform, alias, r.Platform)
	}
	url := strings.TrimSpace(r.URL)
	if url == "" {
		return ChannelConfig{}, fmt.Errorf("%w: alias=%q url is empty", ErrMissingField, alias)
	}
	if !strings.HasPrefix(url, "https://") {
		return ChannelConfig{}, fmt.Errorf("%w: alias=%q url must be https", ErrMissingField, alias)
	}
	// 钉钉和飞书必须有 HMAC secret；Slack 的凭据在 URL 中。
	secret := strings.TrimSpace(r.Secret)
	if (plat == PlatformDingtalk || plat == PlatformFeishu) && secret == "" {
		return ChannelConfig{}, fmt.Errorf("%w: alias=%q %s requires secret", ErrMissingField, alias, plat)
	}
	return ChannelConfig{Platform: plat, URL: url, Secret: secret}, nil
}

// scanTopLevelAliases 只扫描顶层 JSON key，专门用于在 typed unmarshal 前发现重复 alias。
func scanTopLevelAliases(text string) ([]string, error) {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("notify: parse channels: %w", err)
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil, fmt.Errorf("notify: channels JSON must be an object, got %v", tok)
	}
	seen := map[string]struct{}{}
	var order []string
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("notify: parse channels key: %w", err)
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, fmt.Errorf("notify: channels key must be string, got %T", keyTok)
		}
		if _, dup := seen[key]; dup {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateAlias, key)
		}
		seen[key] = struct{}{}
		order = append(order, key)
		// 顶层扫描只关心 key，value 形状交给后续 typed unmarshal 校验。
		if err := skipValue(dec); err != nil {
			return nil, fmt.Errorf("notify: parse channels value for %q: %w", key, err)
		}
	}
	if _, err := dec.Token(); err != nil {
		return nil, fmt.Errorf("notify: parse channels close: %w", err)
	}
	return order, nil
}

// skipValue 从 decoder 中跳过一个完整 JSON 值，包括嵌套对象和数组。
func skipValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	depth := 1
	for depth > 0 {
		next, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := next.(json.Delim); ok {
			switch d {
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			}
		}
	}
	_ = delim
	return nil
}
