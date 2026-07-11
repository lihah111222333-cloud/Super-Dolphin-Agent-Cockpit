package notify

import (
	"encoding/json"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestRenderSlackUsesBlocksKit(t *testing.T) {
	t.Parallel()
	cfg := ChannelConfig{
		Platform: PlatformSlack,
		URL:      "https://hooks.slack.com/services/T/B/XYZ",
	}
	postURL, body, ct, err := RenderSlack(cfg, contract.NotifyMessage{
		Title: "error: provider down",
		Body:  "try again",
		Level: contract.NotifyLevelError,
	})
	if err != nil {
		t.Fatalf("RenderSlack error = %v", err)
	}
	if postURL != cfg.URL || ct != "application/json" {
		t.Fatalf("postURL/ct mismatch: %s / %s", postURL, ct)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body not json: %v", err)
	}
	blocks, ok := decoded["blocks"].([]any)
	if !ok || len(blocks) < 2 {
		t.Fatalf("blocks missing or too small: %v", decoded)
	}
	header, _ := blocks[0].(map[string]any)
	if header["type"] != "header" {
		t.Fatalf("first block not header: %v", header)
	}
}
