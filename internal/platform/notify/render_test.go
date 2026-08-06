package notify

import (
	"strings"
	"testing"
)

func TestRendererInstancesOwnRenderingRules(t *testing.T) {
	t.Parallel()
	first := NewRenderer()
	second := NewRenderer()
	if first == second {
		t.Fatal("NewRenderer returned a shared renderer")
	}
	if got := first.NormalizeBody("<!channel> *alert*", DefaultMaxBodyBytes); got != ` \*alert\*` {
		t.Fatalf("first renderer normalized body = %q", got)
	}
	if got := second.NormalizeTitle("<!here> title"); got != " title" {
		t.Fatalf("second renderer normalized title = %q", got)
	}
}

func TestMarkdownEscapeCoversAllPlatforms(t *testing.T) {
	t.Parallel()
	in := "`bold`* _italic_ #h1 [x](y) >quote | pipe \\slash"
	out := NewRenderer().MarkdownEscape(in)
	for _, mustEscape := range []string{`\` + "`", `\*`, `\_`, `\#`, `\[`, `\]`, `\(`, `\)`, `\>`, `\|`, `\\`} {
		if !strings.Contains(out, mustEscape) {
			t.Fatalf("expected %q in %q", mustEscape, out)
		}
	}
}

func TestStripMentionsBlocksBroadcastTokens(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"hello <!channel> world":         "hello  world",
		"hi <!here> team":                "hi  team",
		"cc <@U01ABCD> pls":              "cc  pls",
		"ping <at user_id=\"all\"></at>": "ping ",
		"@所有人 please":                    " please",
		"@all fyi":                       " fyi",
		"@everyone note":                 " note",
		"@13800000000 safe":              " safe",
	}
	for in, want := range cases {
		got := NewRenderer().StripMentions(in)
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
	if got := NewRenderer().StripMentions(in); got != in {
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
	out := NewRenderer().NormalizeBody(body, 0)
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

func TestNormalizeBodyStripsMentionsBeforeEscape(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		input   string
		blocked []string
	}{
		{name: "slack_channel", input: "<!channel>", blocked: []string{"<!channel>", `<!channel\>`}},
		{name: "slack_here", input: "<!here>", blocked: []string{"<!here>", `<!here\>`}},
		{name: "slack_everyone", input: "<!everyone>", blocked: []string{"<!everyone>", `<!everyone\>`}},
		{name: "slack_user", input: "<@U1234>", blocked: []string{"<@U1234>", `<@U1234\>`}},
		{name: "feishu_all", input: `<at user_id="all"></at>`, blocked: []string{`<at user_id="all"></at>`, `<at user_id="all"\>\</at\>`}},
		{name: "feishu_cn", input: "@所有人", blocked: []string{"@所有人"}},
		{name: "dingtalk_all", input: "@all", blocked: []string{"@all"}},
		{name: "dingtalk_mobile", input: "@13800000000", blocked: []string{"@13800000000"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := NewRenderer().NormalizeBody("prefix "+tc.input+" suffix > quote", 0)
			for _, token := range tc.blocked {
				if strings.Contains(out, token) {
					t.Fatalf("NormalizeBody(%q) leaked %q in %q", tc.input, token, out)
				}
			}
			if !strings.Contains(out, `\> quote`) {
				t.Fatalf("NormalizeBody did not escape markdown after stripping mention: %q", out)
			}
		})
	}
}

func TestNormalizeTitleStripsMentionsBeforeEscape(t *testing.T) {
	t.Parallel()
	out := NewRenderer().NormalizeTitle("<!channel> title > quote")
	for _, token := range []string{"<!channel>", `<!channel\>`} {
		if strings.Contains(out, token) {
			t.Fatalf("NormalizeTitle leaked %q in %q", token, out)
		}
	}
	if !strings.Contains(out, `\> quote`) {
		t.Fatalf("NormalizeTitle did not escape markdown after stripping mention: %q", out)
	}
}

func TestRedactURLStripsQueryAndFragment(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"https://hooks.slack.com/services/T/B/XYZ":            "https://hooks.slack.com/services/redacted",
		"https://oapi.dingtalk.com/robot/send?access_token=s": "https://oapi.dingtalk.com/robot/send",
		"https://example.com/path#frag":                       "https://example.com/path",
	}
	for in, want := range cases {
		if got := RedactURL(in); got != want {
			t.Fatalf("RedactURL(%q) = %q, want %q", in, got, want)
		}
	}
}
