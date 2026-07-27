package httpegress

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// unsafeHeaderNames 列出调用方不得覆盖的传输层头，避免绕过出站请求边界。
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

var carrierGradeNATPrefix = netip.MustParsePrefix("100.64.0.0/10")

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

// NewPublicHTTPClient 创建只允许公网目标的 HTTP client。
// 重定向会逐跳校验 URL，拨号前会解析目标 IP，解析到本机或内网地址时直接失败。
func NewPublicHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:       timeout,
		Transport:     NewPublicHTTPTransport(),
		CheckRedirect: ValidateRedirect,
	}
}

// NewPublicHTTPTransport 返回带解析后 IP 校验的 HTTP transport，供需要自定义 client 的出站调用复用。
func NewPublicHTTPTransport() http.RoundTripper {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// 环境代理会把 DialContext 的地址替换为代理端点，使目标域名绕过本地解析后 IP 校验。
	// 公网出站边界不接受隐式代理；若未来需要代理，必须建立可验证目标地址的显式协议。
	transport.Proxy = nil
	dialer := &publicDialer{
		resolver: net.DefaultResolver,
		dialer: &net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		},
	}
	transport.DialContext = dialer.DialContext
	return transport
}

// ValidateRedirect 校验 HTTP client 即将跟随的重定向目标，拒绝跳到本机、内网或非 HTTP(S) URL。
func ValidateRedirect(req *http.Request, _ []*http.Request) error {
	if req == nil || req.URL == nil {
		return fmt.Errorf("redirect URL is required")
	}
	_, err := ValidatePublicURL(req.URL.String())
	return err
}

// ValidateResolvedIP 校验 DNS 解析结果，避免公网域名在请求时解析到本机、内网或链路本地地址。
func ValidateResolvedIP(host string, addr netip.Addr) error {
	if isUnsafeHTTPAddr(addr) {
		return fmt.Errorf("private network URL host %q resolved to %s is not allowed", strings.TrimSpace(host), addr.Unmap())
	}
	return nil
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

type publicDialer struct {
	resolver *net.Resolver
	dialer   *net.Dialer
}

// DialContext 解析目标主机并只拨向通过公网校验的 IP，避免 DNS rebinding 绕过 URL host 校验。
func (d *publicDialer) DialContext(ctx context.Context, network string, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("parse HTTP dial address %q: %w", address, err)
	}
	addrs, err := d.resolveHost(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, addr := range addrs {
		if err := ValidateResolvedIP(host, addr); err != nil {
			return nil, err
		}
	}
	return d.dialResolvedAddrs(ctx, network, host, port, addrs)
}

// resolveHost 解析拨号目标并先应用主机名级别的拒绝规则，防止单标签或显式内网地址进入 DNS/拨号阶段。
func (d *publicDialer) resolveHost(ctx context.Context, host string) ([]netip.Addr, error) {
	if isUnsafeHTTPHost(host) {
		return nil, fmt.Errorf("private network URL host %q is not allowed", host)
	}
	if addr, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		return []netip.Addr{addr}, nil
	}
	resolver := d.resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	addrs, err := resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolve public HTTP host %q: %w", host, err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("resolve public HTTP host %q: no addresses", host)
	}
	return addrs, nil
}

func (d *publicDialer) dialResolvedAddrs(
	ctx context.Context,
	network string,
	host string,
	port string,
	addrs []netip.Addr,
) (net.Conn, error) {
	dialer := d.dialer
	if dialer == nil {
		dialer = &net.Dialer{}
	}
	var lastErr error
	for _, addr := range addrs {
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(addr.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("dial public HTTP host %q: %w", host, lastErr)
}

// isUnsafeHTTPHost 判断主机名是否会落到本机、内网、链路本地或非 FQDN 目标。
// 只在这里收口 SSRF 主机判定，调用方拿到 true 必须拒绝请求而不是降级放行。
func isUnsafeHTTPHost(host string) bool {
	normalized := strings.Trim(strings.ToLower(host), ".")
	if normalized == "localhost" || strings.HasSuffix(normalized, ".localhost") {
		return true
	}
	addr, err := netip.ParseAddr(normalized)
	if err == nil {
		return isUnsafeHTTPAddr(addr)
	}
	return !strings.Contains(normalized, ".")
}

// isUnsafeHTTPAddr 判断解析后的 IP 是否属于本机、内网、CGNAT、链路本地、多播或未指定地址。
func isUnsafeHTTPAddr(addr netip.Addr) bool {
	addr = addr.Unmap()
	return !addr.IsGlobalUnicast() ||
		addr.IsPrivate() ||
		carrierGradeNATPrefix.Contains(addr) ||
		addr.IsLoopback() ||
		addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() ||
		addr.IsMulticast() ||
		addr.IsUnspecified()
}
