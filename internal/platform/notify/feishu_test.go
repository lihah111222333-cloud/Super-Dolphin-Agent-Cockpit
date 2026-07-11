package notify

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestFeishuSignDeterministicAndBase64(t *testing.T) {
	t.Parallel()
	sig, err := feishuSign("s3cr3t", 1_700_000_000)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if _, err := base64.StdEncoding.DecodeString(sig); err != nil {
		t.Fatalf("sig must be base64: %v", err)
	}
	if sig2, _ := feishuSign("s3cr3t", 1_700_000_000); sig != sig2 {
		t.Fatal("sign must be deterministic")
	}
	if sig3, _ := feishuSign("s3cr3t", 1_700_000_001); sig == sig3 {
		t.Fatal("timestamp change must change sign")
	}
}

func TestRenderFeishuCardHasSignAndElements(t *testing.T) {
	t.Parallel()
	cfg := ChannelConfig{
		Platform: PlatformFeishu,
		URL:      "https://open.feishu.cn/open-apis/bot/v2/hook/abc",
		Secret:   "sec",
	}
	postURL, body, ct, err := RenderFeishu(cfg, contract.NotifyMessage{
		Title: "Turn interrupted",
		Body:  "user cancelled",
		Level: contract.NotifyLevelWarn,
	}, 1_700_000_000)
	if err != nil {
		t.Fatalf("RenderFeishu error = %v", err)
	}
	if ct != "application/json" || postURL != cfg.URL {
		t.Fatalf("url/ct wrong: %s / %s", postURL, ct)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body not json: %v", err)
	}
	if decoded["sign"] == "" || decoded["timestamp"] == "" {
		t.Fatalf("feishu body missing sign/timestamp: %v", decoded)
	}
	if decoded["msg_type"] != "interactive" {
		t.Fatalf("msg_type = %v", decoded["msg_type"])
	}
}
