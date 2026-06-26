package notify

import (
	"regexp"
	"strings"
)

// DefaultMaxBodyBytes 是渲染后正文的硬上限，超过部分会追加截断标记，避免异常事件刷爆聊天通道。
const DefaultMaxBodyBytes = 4 * 1024

// markdownEscapeReplacer 转义三端都会解释为 markdown 的字符，所有 renderer 共用同一套规则。
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

// MarkdownEscape 对用户文本应用三端共享的 markdown 转义集，防止模板字段注入格式。
func MarkdownEscape(s string) string {
	return markdownEscapeReplacer.Replace(s)
}

// 各平台 mention 正则统一在渲染前清除，避免用户输入触发广播或定向提醒。
var (
	// Slack mention token 示例：<!channel>、<!here>、<!everyone>、<@U12345>。
	slackMentionRE = regexp.MustCompile(`(?i)<!(channel|here|everyone)>|<@[UW][A-Z0-9]+>`)
	// 飞书 mention token 示例：<at user_id="..."></at>、<at user_id="all"></at>、@{all}、@所有人。
	feishuMentionRE = regexp.MustCompile(`(?i)<at[^>]*>\s*</at>|@\{all\}|@所有人`)
	// 钉钉：清除 @all、@everyone 和 6 位以上手机号 mention；中文姓名只按显式规则处理。
	dingtalkMentionRE = regexp.MustCompile(`(?i)@(all|everyone)\b|@\d{6,}`)
)

// StripMentions 移除广播式 mention token；需要定向 mention 时必须由模板常量显式生成。
func StripMentions(s string) string {
	s = slackMentionRE.ReplaceAllString(s, "")
	s = feishuMentionRE.ReplaceAllString(s, "")
	s = dingtalkMentionRE.ReplaceAllString(s, "")
	return s
}

// Truncate 按字节上限裁剪渲染文本，maxBytes 非正数时使用默认正文上限。
func Truncate(s string, maxBytes int) string {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBodyBytes
	}
	if len(s) <= maxBytes {
		return s
	}
	marker := "...truncated..."
	if maxBytes <= len(marker) {
		// 极小上限无法容纳截断标记时，只返回原始前缀。
		return s[:maxBytes]
	}
	return s[:maxBytes-len(marker)] + marker
}

// NormalizeBody 是平台 renderer 的正文清洗管线：先去 mention，再转义 markdown，最后限长。
// mention 必须先处理，否则 Slack token 里的 '>' 被转义后就无法匹配。
func NormalizeBody(s string, maxBytes int) string {
	return Truncate(MarkdownEscape(StripMentions(s)), maxBytes)
}

// NormalizeTitle 使用与正文相同的清洗流程，但标题使用更小上限以适配卡片头部。
func NormalizeTitle(s string) string {
	return Truncate(MarkdownEscape(StripMentions(s)), 256)
}

// RedactURL 移除 webhook URL 中的 query/fragment 和 Slack bearer-style path secret。
func RedactURL(u string) string {
	return redactURLString(u)
}
