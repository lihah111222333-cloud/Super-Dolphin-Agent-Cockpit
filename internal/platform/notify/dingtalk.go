package notify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// RenderDingtalk builds the signed webhook URL and Markdown card body
// for a Dingtalk robot. The robot accepts POSTed JSON at the signed URL
// (query params timestamp + sign).
//
// Signature algorithm (official spec):
//
//	stringToSign := timestamp_ms + "\n" + secret
//	sign := base64(urlEncode(HMAC-SHA256(secret, stringToSign)))
//
// The rendered body is a Dingtalk markdown card so colour / icon
// follows NotifyLevel.
// RenderDingtalk 渲染dingtalk。
func RenderDingtalk(cfg ChannelConfig, msg contract.NotifyMessage, timestampMS int64) (signedURL string, body []byte, contentType string, err error) {
	if cfg.Platform != PlatformDingtalk {
		return "", nil, "", fmt.Errorf("dingtalk: wrong platform %q", cfg.Platform)
	}
	sign, err := dingtalkSign(cfg.Secret, timestampMS)
	if err != nil {
		return "", nil, "", err
	}
	u, err := url.Parse(cfg.URL)
	if err != nil {
		return "", nil, "", fmt.Errorf("%w: %w", ErrInvalidURL, err)
	}
	q := u.Query()
	q.Set("timestamp", strconv.FormatInt(timestampMS, 10))
	q.Set("sign", sign)
	u.RawQuery = q.Encode()

	title := NormalizeTitle(msg.Title)
	text := NormalizeBody(msg.Body, 0)
	header := levelMarker(msg.Level)
	markdown := strings.Builder{}
	if header != "" {
		markdown.WriteString("> ")
		markdown.WriteString(header)
		markdown.WriteString("\n\n")
	}
	markdown.WriteString("### ")
	markdown.WriteString(title)
	markdown.WriteString("\n\n")
	markdown.WriteString(text)
	payload := map[string]any{
		"msgtype": "markdown",
		"markdown": map[string]string{
			"title": title,
			"text":  markdown.String(),
		},
	}
	body, err = json.Marshal(payload)
	if err != nil {
		return "", nil, "", fmt.Errorf("dingtalk: marshal: %w", err)
	}
	return u.String(), body, "application/json", nil
}

// dingtalkSign computes the urlencoded base64 HMAC-SHA256 per the
// Dingtalk spec. Exposed for tests.
func dingtalkSign(secret string, timestampMS int64) (string, error) {
	if strings.TrimSpace(secret) == "" {
		return "", fmt.Errorf("%w: dingtalk secret is empty", ErrMissingField)
	}
	stringToSign := strconv.FormatInt(timestampMS, 10) + "\n" + secret
	h := hmac.New(sha256.New, []byte(secret))
	if _, err := h.Write([]byte(stringToSign)); err != nil {
		return "", fmt.Errorf("dingtalk: hmac write: %w", err)
	}
	raw := base64.StdEncoding.EncodeToString(h.Sum(nil))
	return url.QueryEscape(raw), nil
}

// levelMarker maps the NotifyLevel into a plain-text header decoration
// used by all three platforms. Level icons are plain unicode so they
// survive markdown escaping unchanged.
func levelMarker(level contract.NotifyLevel) string {
	switch level {
	case contract.NotifyLevelWarn:
		return "\u26a0\ufe0f  WARN"
	case contract.NotifyLevelError:
		return "\U0001f534  ERROR"
	case contract.NotifyLevelInfo:
		return "\u2139\ufe0f  INFO"
	default:
		return ""
	}
}
