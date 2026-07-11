package notify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// RenderFeishu 生成飞书交互卡片体并按机器人规范签名。
// 飞书校验 body 顶层 timestamp/sign，正文在模板包装前已去 mention、转义 markdown 并限长。
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

// feishuSign 按飞书“key=timestamp\nsecret、message 为空”的 HMAC 规则计算签名。
func feishuSign(secret string, timestampSec int64) (string, error) {
	if strings.TrimSpace(secret) == "" {
		return "", fmt.Errorf("%w: feishu secret is empty", ErrMissingField)
	}
	stringToSign := strconv.FormatInt(timestampSec, 10) + "\n" + secret
	h := hmac.New(sha256.New, []byte(stringToSign))
	// 飞书规范要求空消息体，签名内容只体现在 HMAC key 中。
	return base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
}
