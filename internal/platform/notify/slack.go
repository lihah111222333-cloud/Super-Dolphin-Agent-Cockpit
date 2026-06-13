package notify

import (
	"encoding/json"
	"fmt"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// RenderSlack builds a Slack Block Kit body. Slack incoming webhooks
// treat the URL itself as the bearer credential (no HMAC); the
// resolver keeps the URL hidden and we never log it verbatim.
// RenderSlack 渲染slack。
func RenderSlack(cfg ChannelConfig, msg contract.NotifyMessage) (postURL string, body []byte, contentType string, err error) {
	if cfg.Platform != PlatformSlack {
		return "", nil, "", fmt.Errorf("slack: wrong platform %q", cfg.Platform)
	}
	title := NormalizeTitle(msg.Title)
	text := NormalizeBody(msg.Body, 0)
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
