package notify

import (
	"encoding/json"
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// RenderSlack 生成 Slack Block Kit 消息体。
// Slack webhook URL 本身就是凭据，没有额外 HMAC；渲染层只返回 URL，日志脱敏由调用链负责。
func (r *Renderer) RenderSlack(cfg ChannelConfig, msg contract.NotifyMessage) (postURL string, body []byte, contentType string, err error) {
	if cfg.Platform != PlatformSlack {
		return "", nil, "", fmt.Errorf("slack: wrong platform %q", cfg.Platform)
	}
	title := r.NormalizeTitle(msg.Title)
	text := r.NormalizeBody(msg.Body, 0)
	header := levelMarker(msg.Level)
	blocks := []any{
		map[string]any{
			"type": "header",
			"text": map[string]any{
				"type": "plain_text",
				"text": title,
			},
		},
	}
	if header != "" {
		blocks = append(blocks, map[string]any{
			"type": "context",
			"elements": []any{
				map[string]any{"type": "mrkdwn", "text": header},
			},
		})
	}
	blocks = append(blocks, map[string]any{
		"type": "section",
		"text": map[string]any{
			"type": "mrkdwn",
			"text": text,
		},
	})
	payload := map[string]any{"blocks": blocks}
	body, err = json.Marshal(payload)
	if err != nil {
		return "", nil, "", fmt.Errorf("slack: marshal: %w", err)
	}
	return cfg.URL, body, "application/json", nil
}
