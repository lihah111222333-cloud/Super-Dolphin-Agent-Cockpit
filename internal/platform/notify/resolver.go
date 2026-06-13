// Package notify is the cross-tree shared library backing the notify
// module. It lives under internal/platform/notify so both core
// (internal/module/notify) and cmd/mcp-orch can import it without
// violating mcp-service-convention.md S3.1.
//
// The package provides:
//   - ChannelConfig + Resolver: alias -> {platform, url, secret} lookup
//     with duplicate-key-aware JSON parsing.
//   - Webhook client: HTTPS-only, SSRF-guarded DialContext, redirect
//     revalidation, ProxyFromEnvironment disabled.
//   - Render helpers: markdownEscape, mention suppression, length
//     truncation.
//   - Per-platform signers / body builders for Dingtalk, Feishu, Slack.
package notify

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Platform identifies the webhook flavour backing a channel alias. The
// set is closed in v1 so a typo in NOTIFY_CHANNELS_JSON fails fast at
// startup instead of silently falling back to generic POST.
type Platform string

const (
	PlatformDingtalk Platform = "dingtalk"
	PlatformFeishu   Platform = "feishu"
	PlatformSlack    Platform = "slack"
)

// ChannelConfig is the resolved configuration for one alias.
type ChannelConfig struct {
	Platform Platform
	// URL is the fully-qualified HTTPS webhook endpoint. Treated as a
	// secret in logs (see render / webhook for redaction rules).
	URL string
	// Secret is the HMAC key used by Dingtalk / Feishu. Slack leaves
	// this empty because the URL itself is the secret.
	Secret string
}

// ErrDuplicateAlias / ErrUnsupportedPlatform / ErrMissingField /
// ErrAliasNotFound let callers distinguish startup-time config errors
// from runtime lookup misses.
var (
	ErrDuplicateAlias      = errors.New("notify: duplicate channel alias")
	ErrUnsupportedPlatform = errors.New("notify: unsupported platform")
	ErrMissingField        = errors.New("notify: missing required field")
	ErrAliasNotFound       = errors.New("notify: channel alias not found")
)

// Resolver maps a channel alias to its ChannelConfig. Implementations
// are immutable after construction so lookups are lock-free.
type Resolver interface {
	Resolve(alias string) (ChannelConfig, error)
}

// staticResolver is the default Resolver backed by an in-memory map
// parsed from NOTIFY_CHANNELS_JSON.
type staticResolver struct {
	channels map[string]ChannelConfig
}

// Resolve returns the ChannelConfig for the trimmed alias. An unknown
// alias returns ErrAliasNotFound so callers can branch on
// misconfiguration vs transient failures.
// Resolve 解析平台notify。
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

// ParseChannelsJSON parses NOTIFY_CHANNELS_JSON into a Resolver. It
// uses a duplicate-key-aware decoder so two keys with the same alias
// (a common copy-paste misconfiguration) fail with ErrDuplicateAlias
// instead of being silently collapsed by encoding/json's last-write-
// wins semantics.
//
// The input shape is a JSON object of the form:
//
//	{
//	  "slack.default":  {"platform":"slack",    "url":"https://..."},
//	  "dingtalk.ops":   {"platform":"dingtalk", "url":"...", "secret":"..."},
//	  "feishu.ops":     {"platform":"feishu",   "url":"...", "secret":"..."}
//	}
//
// Empty / whitespace input parses to an empty resolver (no channels).
// ParseChannelsJSON 解析channelsJSON。
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
			// Defensive: scanTopLevelAliases already saw this key, but
			// a malformed value would trip Unmarshal above. Skip rather
			// than panic.
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

// rawChannel is the JSON shape for a single alias value. All fields
// are strings so an accidental numeric / object value fails via
// json.Unmarshal rather than silently coercing.
type rawChannel struct {
	Platform string `json:"platform"`
	URL      string `json:"url"`
	Secret   string `json:"secret,omitempty"`
}

// toChannelConfig 把平台notify处理为channel配置。
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
	// Dingtalk + Feishu require a secret for HMAC; Slack does not.
	secret := strings.TrimSpace(r.Secret)
	if (plat == PlatformDingtalk || plat == PlatformFeishu) && secret == "" {
		return ChannelConfig{}, fmt.Errorf("%w: alias=%q %s requires secret", ErrMissingField, alias, plat)
	}
	return ChannelConfig{Platform: plat, URL: url, Secret: secret}, nil
}

// scanTopLevelAliases walks the top-level JSON object keys without
// unmarshaling so duplicate aliases can be detected before
// encoding/json silently collapses them. Keys seen more than once
// surface ErrDuplicateAlias.
// scanTopLevelAliases 扫描toplevelaliases。
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
		// Skip the value — we don't care about shape here, Unmarshal
		// will re-read the whole object for the typed decode.
		if err := skipValue(dec); err != nil {
			return nil, fmt.Errorf("notify: parse channels value for %q: %w", key, err)
		}
	}
	if _, err := dec.Token(); err != nil {
		return nil, fmt.Errorf("notify: parse channels close: %w", err)
	}
	return order, nil
}

// skipValue consumes one JSON value from the decoder, including nested
// arrays / objects. Used to fast-forward past an alias value once the
// key has been recorded.
// skipValue 处理skip值。
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
