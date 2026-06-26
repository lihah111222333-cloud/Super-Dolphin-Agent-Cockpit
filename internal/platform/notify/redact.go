package notify

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// RedactError 返回可写入结构化日志的错误文本，重点清理 net/url.Error 中携带的 webhook URL。
func RedactError(err error) string {
	if err == nil {
		return ""
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		if parsed, perr := url.Parse(ue.URL); perr == nil {
			return fmt.Sprintf("%s %q: %v", ue.Op, redactURLString(parsed.String()), ue.Unwrap())
		}
		return fmt.Sprintf("%s %q: %v", ue.Op, "[redacted]", ue.Unwrap())
	}
	return err.Error()
}

// redactURLString 去掉 query 和 fragment，并折叠 Slack webhook path 中的 bearer secret。
func redactURLString(raw string) string {
	u := strings.TrimSpace(raw)
	if u == "" {
		return ""
	}
	parsed, err := url.Parse(u)
	if err != nil {
		if q := strings.IndexAny(u, "?#"); q >= 0 {
			return u[:q] + "#redacted"
		}
		return u
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	if isSlackWebhookURL(parsed) {
		parsed.Path = "/services/redacted"
	}
	redacted := parsed.String()
	if redacted == "" {
		return u
	}
	return redacted
}

// isSlackWebhookURL 判断 URL 是否为 Slack incoming webhook，Slack 的 path 本身是凭据。
func isSlackWebhookURL(u *url.URL) bool {
	if u == nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "hooks.slack.com" && strings.HasPrefix(u.Path, "/services/")
}
