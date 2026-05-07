package notify

import (
	"errors"
	"strings"
	"testing"
)

func TestParseChannelsJSONEmpty(t *testing.T) {
	t.Parallel()
	r, err := ParseChannelsJSON("")
	if err != nil || r == nil {
		t.Fatalf("empty input: err=%v r=%v", err, r)
	}
	if _, err := r.Resolve("anything"); !errors.Is(err, ErrAliasNotFound) {
		t.Fatalf("empty resolver must return ErrAliasNotFound, got %v", err)
	}
}

func TestParseChannelsJSONRejectsDuplicateAlias(t *testing.T) {
	t.Parallel()
	raw := `{
		"slack.default": {"platform":"slack","url":"https://hooks.slack.com/services/A"},
		"slack.default": {"platform":"slack","url":"https://hooks.slack.com/services/B"}
	}`
	_, err := ParseChannelsJSON(raw)
	if !errors.Is(err, ErrDuplicateAlias) {
		t.Fatalf("want ErrDuplicateAlias, got %v", err)
	}
}

func TestParseChannelsJSONRejectsUnsupportedPlatform(t *testing.T) {
	t.Parallel()
	raw := `{"x": {"platform":"discord","url":"https://example.com"}}`
	_, err := ParseChannelsJSON(raw)
	if !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("want ErrUnsupportedPlatform, got %v", err)
	}
}

func TestParseChannelsJSONRequiresHTTPS(t *testing.T) {
	t.Parallel()
	raw := `{"x": {"platform":"slack","url":"http://hooks.slack.com/services/A"}}`
	_, err := ParseChannelsJSON(raw)
	if !errors.Is(err, ErrMissingField) {
		t.Fatalf("want ErrMissingField for non-https, got %v", err)
	}
}

func TestParseChannelsJSONDingtalkRequiresSecret(t *testing.T) {
	t.Parallel()
	raw := `{"d": {"platform":"dingtalk","url":"https://oapi.dingtalk.com/robot/send?access_token=xyz"}}`
	_, err := ParseChannelsJSON(raw)
	if !errors.Is(err, ErrMissingField) {
		t.Fatalf("dingtalk without secret: want ErrMissingField, got %v", err)
	}
}

func TestParseChannelsJSONSlackNoSecretOK(t *testing.T) {
	t.Parallel()
	raw := `{"s": {"platform":"slack","url":"https://hooks.slack.com/services/A"}}`
	r, err := ParseChannelsJSON(raw)
	if err != nil {
		t.Fatalf("slack without secret should parse, err=%v", err)
	}
	c, err := r.Resolve("s")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.Platform != PlatformSlack || c.Secret != "" {
		t.Fatalf("slack channel unexpected: %+v", c)
	}
}

func TestResolveTrimsAliasAndRejectsEmpty(t *testing.T) {
	t.Parallel()
	r, _ := ParseChannelsJSON(`{"slack.default": {"platform":"slack","url":"https://hooks.slack.com/services/A"}}`)
	if _, err := r.Resolve("  slack.default  "); err != nil {
		t.Fatalf("trimmed alias should resolve: %v", err)
	}
	if _, err := r.Resolve("   "); !errors.Is(err, ErrAliasNotFound) {
		t.Fatalf("empty alias: want ErrAliasNotFound, got %v", err)
	}
}

func TestParseChannelsJSONParsesAllThreePlatforms(t *testing.T) {
	t.Parallel()
	raw := `{
		"s.default": {"platform":"slack","url":"https://hooks.slack.com/services/A"},
		"d.ops":     {"platform":"dingtalk","url":"https://oapi.dingtalk.com/robot/send?access_token=xyz","secret":"dsec"},
		"f.ops":     {"platform":"feishu","url":"https://open.feishu.cn/open-apis/bot/v2/hook/abc","secret":"fsec"}
	}`
	r, err := ParseChannelsJSON(raw)
	if err != nil {
		t.Fatalf("ParseChannelsJSON error = %v", err)
	}
	for alias, wantPlat := range map[string]Platform{
		"s.default": PlatformSlack,
		"d.ops":     PlatformDingtalk,
		"f.ops":     PlatformFeishu,
	} {
		c, err := r.Resolve(alias)
		if err != nil {
			t.Fatalf("resolve %q: %v", alias, err)
		}
		if c.Platform != wantPlat {
			t.Fatalf("alias %q platform = %q, want %q", alias, c.Platform, wantPlat)
		}
		if !strings.HasPrefix(c.URL, "https://") {
			t.Fatalf("alias %q url not https: %q", alias, c.URL)
		}
	}
}
