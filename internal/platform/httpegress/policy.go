package httpegress

import (
	"fmt"
	"net/netip"
	"net/url"
	"strings"
)

var unsafeHeaderNames = map[string]struct{}{
	"accept":              {},
	"connection":          {},
	"content-length":      {},
	"content-type":        {},
	"host":                {},
	"proxy-authenticate":  {},
	"proxy-authorization": {},
	"te":                  {},
	"trailer":             {},
	"transfer-encoding":   {},
	"upgrade":             {},
}

// ValidatePublicURL 校验出站 HTTP URL 只能指向公网主机，避免 SSRF 访问本机、内网或云元数据地址。
func ValidatePublicURL(rawURL string) (string, error) {
	trimmed := strings.TrimSpace(rawURL)
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("parse URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("unsupported URL scheme %q", parsed.Scheme)
	}
	if parsed.User != nil {
		return "", fmt.Errorf("URL userinfo is not allowed")
	}
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return "", fmt.Errorf("URL host is required")
	}
	if isUnsafeHTTPHost(host) {
		return "", fmt.Errorf("private network URL host %q is not allowed", host)
	}
	return trimmed, nil
}

// ValidateHeaders 拒绝调用方覆盖传输层关键头，避免通过 Host/Transfer-Encoding 等头绕过出站边界。
func ValidateHeaders(headers map[string]string) error {
	for name := range headers {
		normalized := strings.ToLower(strings.TrimSpace(name))
		if _, unsafe := unsafeHeaderNames[normalized]; unsafe {
			return fmt.Errorf("unsafe header %q is not allowed", strings.TrimSpace(name))
		}
	}
	return nil
}

func isUnsafeHTTPHost(host string) bool {
	normalized := strings.Trim(strings.ToLower(host), ".")
	if normalized == "localhost" || strings.HasSuffix(normalized, ".localhost") {
		return true
	}
	addr, err := netip.ParseAddr(normalized)
	if err == nil {
		addr = addr.Unmap()
		return !addr.IsGlobalUnicast() ||
			addr.IsPrivate() ||
			addr.IsLoopback() ||
			addr.IsLinkLocalUnicast() ||
			addr.IsLinkLocalMulticast() ||
			addr.IsMulticast() ||
			addr.IsUnspecified()
	}
	return !strings.Contains(normalized, ".")
}
