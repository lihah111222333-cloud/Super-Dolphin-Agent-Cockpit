package notify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// RenderFeishu builds the interactive card body and signs per the
// Feishu bot spec. Feishu validates the body, not the URL, so the
// payload includes {timestamp, sign, ...} at the top level.
//
// Signature algorithm (official spec):
//
//	stringToSign := timestamp_sec + "\n" + secret
//	sign := base64(HMAC-SHA256(stringToSign, "") ... )
//	      (Go idiom: HMAC key is stringToSign, message is empty)
//
// RenderFeishu 渲染feishu。
func RenderFeishu(cfg ChannelConfig, msg contract.NotifyMessage, timestampSec int64) (postURL string, body []byte, contentType string, err error) {
	if cfg.Platform != PlatformFeishu {
		return "", nil, "", fmt.Errorf("feishu: wrong platform %q", cfg.Platform)
	}
	sign, err := feishuSign(cfg.Secret, timestampSec)
	if err != nil {
		return "", nil, "", err
	}
	title := NormalizeTitle(msg.Title)
	text := NormalizeBody(msg.Body, 0)
	header := levelMarker(msg.Level)
	if header != "" {
		text = header + "\n\n" + text
	}
	payload := map[string]any{
		"timestamp": strconv.FormatInt(timestampSec, 10),
		"sign":      sign,
		"msg_type":  "interactive",
		"card": map[string]any{
			"header": map[string]any{
				"title": map[string]any{
					"tag":     "plain_text",
					"content": title,
				},
			},
			"elements": []any{
				map[string]any{
					"tag":     "markdown",
					"content": text,
				},
			},
		},
	}
	body, err = json.Marshal(payload)
	if err != nil {
		return "", nil, "", fmt.Errorf("feishu: marshal: %w", err)
	}
	return cfg.URL, body, "application/json", nil
}

// feishuSign computes base64(HMAC-SHA256(key=timestamp\nsecret, msg=nil)).
// Feishu uses the "key is stringToSign, message is empty" idiom which
// differs from Dingtalk's "key is secret, message is stringToSign".
func feishuSign(secret string, timestampSec int64) (string, error) {
	if strings.TrimSpace(secret) == "" {
		return "", fmt.Errorf("%w: feishu secret is empty", ErrMissingField)
	}
	stringToSign := strconv.FormatInt(timestampSec, 10) + "\n" + secret
	h := hmac.New(sha256.New, []byte(stringToSign))
	// No Write — empty message is the spec.
	return base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
}
