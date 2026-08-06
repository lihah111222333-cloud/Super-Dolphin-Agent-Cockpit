package notify

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestDingtalkSignDeterministic(t *testing.T) {
	t.Parallel()
	a, err := dingtalkSign("s3cr3t", 1_700_000_000_000)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	b, err := dingtalkSign("s3cr3t", 1_700_000_000_000)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if a != b {
		t.Fatalf("dingtalk sign must be deterministic: %q vs %q", a, b)
	}
	// Tampering with either input must change the sig.
	if c, _ := dingtalkSign("s3cr3t", 1_700_000_000_001); c == a {
		t.Fatal("timestamp change must change sign")
	}
	if c, _ := dingtalkSign("s3cr3tt", 1_700_000_000_000); c == a {
		t.Fatal("secret change must change sign")
	}
}

func TestRenderDingtalkProducesSignedURLAndMarkdown(t *testing.T) {
	t.Parallel()
	cfg := ChannelConfig{
		Platform: PlatformDingtalk,
		URL:      "https://oapi.dingtalk.com/robot/send?access_token=abc",
		Secret:   "sec",
	}
	msg := contract.NotifyMessage{
		Title: "Turn 完成: daily",
		Body:  "check logs *ok*",
		Level: contract.NotifyLevelInfo,
	}
	signedURL, body, ct, err := NewRenderer().RenderDingtalk(cfg, msg, 1_700_000_000_000)
	if err != nil {
		t.Fatalf("RenderDingtalk error = %v", err)
	}
	if ct != "application/json" {
		t.Fatalf("content type = %q", ct)
	}
	assertDingtalkSignedURL(t, signedURL)
	assertDingtalkMarkdownBody(t, body)
}

func assertDingtalkSignedURL(t *testing.T, signedURL string) {
	t.Helper()
	u, err := url.Parse(signedURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	q := u.Query()
	if q.Get("timestamp") == "" {
		t.Fatalf("signed url missing timestamp: %s", signedURL)
	}
	if q.Get("sign") == "" {
		t.Fatalf("signed url missing sign: %s", signedURL)
	}
	if q.Get("access_token") != "abc" {
		t.Fatalf("original access_token lost: %s", signedURL)
	}
}

func assertDingtalkMarkdownBody(t *testing.T, body []byte) {
	t.Helper()
	// Verify body is a valid Dingtalk markdown payload.
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body not json: %v", err)
	}
	if decoded["msgtype"] != "markdown" {
		t.Fatalf("msgtype = %v", decoded["msgtype"])
	}
	md, _ := decoded["markdown"].(map[string]any)
	if md == nil || md["title"] == "" {
		t.Fatalf("markdown body malformed: %v", decoded)
	}
	if !strings.Contains(md["text"].(string), `\*ok\*`) {
		t.Fatalf("body not markdown-escaped: %v", md["text"])
	}
}
