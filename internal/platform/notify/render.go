package notify

import (
	"regexp"
	"strings"
)

// DefaultMaxBodyBytes is the P2-plan-mandated hard cap on the rendered
// body. Anything beyond is truncated with a "...truncated..." suffix
// so a misbehaving source event cannot DoS a chat channel.
const DefaultMaxBodyBytes = 4 * 1024

// markdownEscapeReplacer escapes characters that all three platforms
// treat as markdown markup. Dingtalk / Feishu / Slack all agree on
// these, so a single replacer works for every renderer.
var markdownEscapeReplacer = strings.NewReplacer(
	`\`, `\\`,
	"*", `\*`,
	"_", `\_`,
	"`", "\\`",
	"~", `\~`,
	">", `\>`,
	"#", `\#`,
	"-", `\-`,
	"+", `\+`,
	"=", `\=`,
	"|", `\|`,
	"{", `\{`,
	"}", `\}`,
	"[", `\[`,
	"]", `\]`,
	"(", `\(`,
	")", `\)`,
)

// MarkdownEscape applies the shared escape set. Callers should run
// this on every user-supplied field before wrapping it in a platform
// template so a rogue backtick or pipe can't inject formatting.
// MarkdownEscape 处理markdown转义。
func MarkdownEscape(s string) string {
	return markdownEscapeReplacer.Replace(s)
}

// Mention patterns that each platform treats as a ping / broadcast.
// We strip them unconditionally because the P2 plan refuses to
// auto-@all any audience; templates that want a mention must declare
// it explicitly in the template, not from user input.
var (
	// Slack: <!channel>, <!here>, <!everyone>, <@U12345>.
	slackMentionRE = regexp.MustCompile(`(?i)<!(channel|here|everyone)>|<@[UW][A-Z0-9]+>`)
	// Feishu: <at user_id="..."></at>, <at user_id="all"></at>,
	// @{all}, @所有人.
	feishuMentionRE = regexp.MustCompile(`(?i)<at[^>]*>\s*</at>|@\{all\}|@所有人`)
	// Dingtalk: @mobile number (digits run of 6+ after @), @all /
	// @everyone literal, and "@张三" — we strip @-prefixed ASCII names
	// only conservatively (the explicit markdown @<number> tokens).
	dingtalkMentionRE = regexp.MustCompile(`(?i)@(all|everyone)\b|@\d{6,}`)
)

// StripMentions removes every broadcast-style mention token. Channel
// templates wanting a targeted mention are expected to render it from
// a safe constant, not from user text.
// StripMentions 处理stripmentions。
func StripMentions(s string) string {
	s = slackMentionRE.ReplaceAllString(s, "")
	s = feishuMentionRE.ReplaceAllString(s, "")
	s = dingtalkMentionRE.ReplaceAllString(s, "")
	return s
}

// Truncate clamps the rendered body to maxBytes. When maxBytes <= 0
// the DefaultMaxBodyBytes guard applies.
// Truncate 截断平台notify。
func Truncate(s string, maxBytes int) string {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBodyBytes
	}
	if len(s) <= maxBytes {
		return s
	}
	marker := "...truncated..."
	if maxBytes <= len(marker) {
		// Pathologically small cap; return the prefix only.
		return s[:maxBytes]
	}
	return s[:maxBytes-len(marker)] + marker
}

// NormalizeBody is the convenience pipeline every platform renderer
// calls before wrapping the text in its template: strip mentions,
// escape markdown, then clamp length. Mention stripping must run before
// markdown escaping because escaping '>' would turn Slack tokens such as
// <!channel> into <!channel\>, which no longer match the mention regex.
// NormalizeBody 规范化正文。
func NormalizeBody(s string, maxBytes int) string {
	return Truncate(MarkdownEscape(StripMentions(s)), maxBytes)
}

// NormalizeTitle is the same pipeline with a tighter default cap — a
// title in a Dingtalk card has much less real estate than the body.
// NormalizeTitle 规范化title。
func NormalizeTitle(s string) string {
	return Truncate(MarkdownEscape(StripMentions(s)), 256)
}

// RedactURL strips secrets from a webhook URL so logs never leak bearer
// tokens. Query / fragment are always removed, and Slack's bearer-style
// path secret is collapsed to /services/[redacted].
// RedactURL 脱敏URL。
func RedactURL(u string) string {
	return redactURLString(u)
}
