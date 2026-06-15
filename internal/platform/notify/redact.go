package notify

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// RedactError returns an error string safe for structured logs. In
// particular, net/url.Error includes the target URL in Error(); webhook
// URLs are credentials for Slack and carry signed query params for
// Dingtalk / Feishu, so we strip those secrets before logging.
// RedactError 脱敏错误。
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

// redactURLString 脱敏URLstring。
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

func isSlackWebhookURL(u *url.URL) bool {
	if u == nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "hooks.slack.com" && strings.HasPrefix(u.Path, "/services/")
}
