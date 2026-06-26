package notify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultTimeout 限制单次 webhook POST，避免外部服务卡住通知 flusher。
const DefaultTimeout = 10 * time.Second

// SSRF 和 URL 校验错误用于调用方和测试区分安全拒绝与普通传输失败。
var (
	ErrDisallowedScheme    = errors.New("notify: only https scheme is allowed")
	ErrDisallowedAddress   = errors.New("notify: target address is disallowed")
	ErrInvalidURL          = errors.New("notify: invalid webhook url")
	ErrIPv6ZoneIDForbidden = errors.New("notify: ipv6 zone id is forbidden")
)

// WebhookClientConfig 配置 webhook 客户端；生产零值安全，测试可打开 AllowPrivateCIDR 覆盖正向路径。
type WebhookClientConfig struct {
	Timeout          time.Duration
	AllowPrivateCIDR bool
	UserAgent        string
	MaxResponseBytes int64
}

// WebhookClient 是三端通知共享的 HTTPS-only 客户端。
// 它在初始 URL、redirect 和 DialContext 三处执行 SSRF 校验，并禁用环境代理。
type WebhookClient struct {
	http             *http.Client
	userAgent        string
	maxResponseBytes int64
}

// NewWebhookClient 创建带 SSRF 防护的 webhook 客户端；生产默认拒绝私网和非 HTTPS 目标。
func NewWebhookClient(cfg WebhookClientConfig) *WebhookClient {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ua := strings.TrimSpace(cfg.UserAgent)
	if ua == "" {
		ua = "super-agent-notify/1"
	}
	maxResp := cfg.MaxResponseBytes
	if maxResp <= 0 {
		maxResp = 64 * 1024
	}
	transport := &http.Transport{
		// 显式禁用环境代理，避免 HTTP_PROXY/HTTPS_PROXY 劫持 webhook 出站流量。
		Proxy: nil,
		DialContext: (&ssrfGuardedDialer{
			Dialer:           &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second},
			allowPrivateCIDR: cfg.AllowPrivateCIDR,
		}).DialContext,
		MaxIdleConns:        10,
		IdleConnTimeout:     60 * time.Second,
		TLSHandshakeTimeout: timeout,
	}
	return &WebhookClient{
		http: &http.Client{
			Transport: transport,
			Timeout:   timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return errors.New("notify: too many redirects")
				}
				if err := validateHTTPSURL(req.URL); err != nil {
					return err
				}
				return validateRedirectTarget(req.Context(), req.URL, cfg.AllowPrivateCIDR)
			},
		},
		userAgent:        ua,
		maxResponseBytes: maxResp,
	}
}

// HTTPClient 暴露底层客户端给测试调整 TLS/transport 参数，生产路径不应依赖它修改行为。
func (c *WebhookClient) HTTPClient() *http.Client { return c.http }

// Post 发送已编码的 webhook 请求，并在请求前校验 HTTPS 目标。
// 响应体只读取受限字节数用于连接复用，避免大响应占用内存。
func (c *WebhookClient) Post(ctx context.Context, target, contentType string, body []byte) error {
	u, err := url.Parse(target)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}
	if err := validateHTTPSURL(u); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("notify: build request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", c.userAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("notify: post [redacted url]: %w", err)
	}
	defer resp.Body.Close()
	// 只丢弃受限前缀以维持 keep-alive，同时防止大响应撑爆内存。
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, c.maxResponseBytes))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("notify: unexpected status %d", resp.StatusCode)
	}
	return nil
}

// validateHTTPSURL 执行初始请求和 redirect 复用的 scheme/host/IPv6 zone 校验。
func validateHTTPSURL(u *url.URL) error {
	if u == nil {
		return fmt.Errorf("%w: nil url", ErrInvalidURL)
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return fmt.Errorf("%w: scheme=%q", ErrDisallowedScheme, u.Scheme)
	}
	if strings.TrimSpace(u.Host) == "" {
		return fmt.Errorf("%w: empty host", ErrInvalidURL)
	}
	if strings.Contains(u.Hostname(), "%") {
		return fmt.Errorf("%w: %s", ErrIPv6ZoneIDForbidden, u.Hostname())
	}
	return nil
}

// validateRedirectTarget 对重定向目标重新解析 DNS，并拒绝指向受限地址的跳转。
func validateRedirectTarget(ctx context.Context, u *url.URL, allowPrivateCIDR bool) error {
	if err := validateHTTPSURL(u); err != nil {
		return err
	}
	host := strings.TrimSpace(u.Hostname())
	if host == "" {
		return fmt.Errorf("%w: empty host", ErrInvalidURL)
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return err
	}
	if len(ips) == 0 {
		return fmt.Errorf("%w: %s resolved no addresses", ErrDisallowedAddress, host)
	}
	if allowPrivateCIDR {
		return nil
	}
	for _, ip := range ips {
		if isBlockedIP(ip) {
			return fmt.Errorf("%w: %s", ErrDisallowedAddress, ip.String())
		}
	}
	return nil
}

// ssrfGuardedDialer 包装 net.Dialer，先解析并校验 IP，再连接到已校验地址以抵御 DNS rebinding。
type ssrfGuardedDialer struct {
	*net.Dialer
	allowPrivateCIDR bool
}

// DialContext 解析 host、拒绝受限 IP，并逐个尝试连接已校验的解析结果。
func (d *ssrfGuardedDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if d.Dialer == nil {
		d.Dialer = &net.Dialer{Timeout: DefaultTimeout}
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("%w: split host/port: %v", ErrInvalidURL, err)
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("%w: %s resolved no addresses", ErrDisallowedAddress, host)
	}
	if !d.allowPrivateCIDR {
		for _, ip := range ips {
			if isBlockedIP(ip) {
				return nil, fmt.Errorf("%w: %s", ErrDisallowedAddress, ip.String())
			}
		}
	}
	var lastErr error
	for _, ip := range ips {
		dialAddr := net.JoinHostPort(ip.String(), port)
		conn, err := d.Dialer.DialContext(ctx, network, dialAddr)
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// 额外阻断网段在包初始化时解析，避免每次连接时分配。
var (
	cgnet = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}
	ula   = &net.IPNet{IP: net.ParseIP("fc00::"), Mask: net.CIDRMask(7, 128)}
)

// isBlockedIP 判断地址是否属于默认拒绝范围，是抵御 DNS rebinding 的连接时防线。
func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	return isBlockedByStdlib(ip) || isBlockedByRange(ip)
}

// isBlockedByStdlib 使用标准库覆盖未指定、回环、链路本地、组播和私网地址。
func isBlockedByStdlib(ip net.IP) bool {
	return ip.IsUnspecified() ||
		ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsPrivate()
}

// isBlockedByRange 补充标准库未显式覆盖或需要读者可见的保留网段。
func isBlockedByRange(ip net.IP) bool {
	// RFC 6598 carrier-grade NAT 不由 IsPrivate 覆盖，需要显式阻断共享租户网段。
	if cgnet.Contains(ip) {
		return true
	}
	// IPv6 ULA 已由 IsPrivate 覆盖，这里保留显式判断让防护范围可读。
	return ula.Contains(ip)
}
