package platform

import (
	"strings"
	"testing"
)

func TestMarkdownEscapeCoversAllPlatforms(t *testing.T) {
	t.Parallel()
	in := "`bold`* _italic_ #h1 [x](y) >quote | pipe \\slash"
	out := MarkdownEscape(in)
	for _, mustEscape := range []string{`\` + "`", `\*`, `\_`, `\#`, `\[`, `\]`, `\(`, `\)`, `\>`, `\|`, `\\`} {
		if !strings.Contains(out, mustEscape) {
			t.Fatalf("expected %q in %q", mustEscape, out)
		}
	}
}

func TestStripMentionsBlocksBroadcastTokens(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"hello <!channel> world":        "hello  world",
		"hi <!here> team":               "hi  team",
		"cc <@U01ABCD> pls":             "cc  pls",
		"ping <at user_id=\"all\"></at>": "ping ",
		"@所有人 please":                    " please",
		"@all fyi":                      " fyi",
		"@everyone note":                " note",
		"@13800000000 safe":             " safe",
	}
	for in, want := range cases {
		got := StripMentions(in)
		// Collapse whitespace for a loose compare since the regex may
		// leave a double-space where the token sat.
		if strings.Join(strings.Fields(got), " ") != strings.Join(strings.Fields(want), " ") {
			t.Fatalf("StripMentions(%q) = %q, want (loose) %q", in, got, want)
		}
	}
}

func TestStripMentionsKeepsBenignAtText(t *testing.T) {
	t.Parallel()
	// Short @ mentions (e.g. an email local-part) should pass through
	// because we only strip numeric mobile patterns and explicit
	// broadcast tokens.
	in := "email me at dev@example.com"
	if got := StripMentions(in); got != in {
		t.Fatalf("StripMentions ate benign @-text: %q -> %q", in, got)
	}
}

func TestTruncateClampsLongBody(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("a", DefaultMaxBodyBytes+100)
	got := Truncate(long, 0)
	if len(got) > DefaultMaxBodyBytes {
		t.Fatalf("truncated len %d > cap %d", len(got), DefaultMaxBodyBytes)
	}
	if !strings.HasSuffix(got, "...truncated...") {
		t.Fatalf("truncate marker missing: tail=%q", got[len(got)-20:])
	}
}

func TestNormalizeBodyPipelineFullChain(t *testing.T) {
	t.Parallel()
	// "Evil" body with markdown, mention, and surplus length.
	body := "<!channel> **hi** " + strings.Repeat("x", 10_000) + "_end_"
	out := NormalizeBody(body, 0)
	if len(out) > DefaultMaxBodyBytes {
		t.Fatalf("pipeline output %d > cap %d", len(out), DefaultMaxBodyBytes)
	}
	if strings.Contains(out, "<!channel>") {
		t.Fatal("pipeline left Slack broadcast token")
	}
	if !strings.Contains(out, `\*`) {
		t.Fatal("pipeline did not escape markdown asterisk")
	}
}

func TestRedactURLStripsQueryAndFragment(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"https://hooks.slack.com/services/T/B/XYZ":           "https://hooks.slack.com/services/T/B/XYZ",
		"https://oapi.dingtalk.com/robot/send?access_token=s": "https://oapi.dingtalk.com/robot/send#redacted",
		"https://example.com/path#frag":                       "https://example.com/path#redacted",
	}
	for in, want := range cases {
		if got := RedactURL(in); got != want {
			t.Fatalf("RedactURL(%q) = %q, want %q", in, got, want)
		}
	}
}
