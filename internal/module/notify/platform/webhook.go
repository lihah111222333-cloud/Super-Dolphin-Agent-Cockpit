package platform

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
	"syscall"
	"time"
)

// DefaultTimeout bounds any single webhook POST. External webhooks have
// no SLA; a stuck host must not tie up the notifier's flush worker.
const DefaultTimeout = 10 * time.Second

// ErrDisallowedScheme / ErrDisallowedAddress / ErrInvalidURL surface
// SSRF-relevant rejections so callers / tests can branch on them.
var (
	ErrDisallowedScheme  = errors.New("notify: only https scheme is allowed")
	ErrDisallowedAddress = errors.New("notify: target address is disallowed")
	ErrInvalidURL        = errors.New("notify: invalid webhook url")
)

// WebhookClientConfig is an optional knob bag for NewWebhookClient.
// Zero values land on sensible defaults for production; tests override
// AllowPrivateCIDR (to exercise the positive SSRF path) and Timeout.
type WebhookClientConfig struct {
	Timeout           time.Duration
	AllowPrivateCIDR  bool
	UserAgent         string
	MaxResponseBytes  int64
}

// WebhookClient is the SSRF-guarded HTTPS client shared by every
// platform sender. It enforces four layers of defence documented in
// the P2 plan:
//
//  1. Scheme: only https is accepted; http / file / ftp / empty reject
//     at build time so a misconfigured alias cannot bypass.
//  2. DialContext: rejects any resolved IP that matches the SSRF
//     block list (loopback, private, link-local, ULA, multicast).
//     Dial IP is checked on every TCP connect so DNS rebinding cannot
//     slip a second resolution past the first.
//  3. Redirects: CheckRedirect re-runs scheme + DialContext against
//     the new URL on every hop; forged 302 -> http://169.254/...
//     rebinds are refused.
//  4. Proxy: explicitly nil so HTTP_PROXY / HTTPS_PROXY / no_proxy
//     cannot route webhook traffic through a local interception proxy.
type WebhookClient struct {
	http             *http.Client
	userAgent        string
	maxResponseBytes int64
}

// NewWebhookClient constructs a WebhookClient with the given config.
// AllowPrivateCIDR=false is the production default.
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
		// Proxy deliberately nil so environmental HTTP_PROXY /
		// HTTPS_PROXY cannot intercept webhook egress.
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
				return nil
			},
		},
		userAgent:        ua,
		maxResponseBytes: maxResp,
	}
}

// HTTPClient exposes the underlying *http.Client so tests can tune
// transport-level knobs (for example TLSClientConfig when hitting an
// httptest.NewTLSServer). Production callers should not need this.
func (c *WebhookClient) HTTPClient() *http.Client { return c.http }

// Post issues POST <target> with the given body and content type. The
// caller is responsible for JSON / form encoding; this method is
// transport-only.
//
// Returned error wraps ErrDisallowedScheme / ErrDisallowedAddress /
// ErrInvalidURL when the guards fire, or a generic transport error
// otherwise. The response body is read and discarded up to
// maxResponseBytes so TCP teardown is clean and no memory is wasted
// on chatty webhook servers.
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
		return fmt.Errorf("notify: post: %w", err)
	}
	defer resp.Body.Close()
	// Drain a bounded prefix so keep-alive works and a large body
	// cannot balloon memory.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, c.maxResponseBytes))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("notify: unexpected status %d", resp.StatusCode)
	}
	return nil
}

// validateHTTPSURL performs the scheme + host checks used in both the
// initial request and the CheckRedirect hop.
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
	return nil
}

// ssrfGuardedDialer wraps net.Dialer. DialContext resolves via the
// embedded dialer and then inspects the connected RemoteAddr; if the
// IP is in the block list, the connection is closed and
// ErrDisallowedAddress is returned.
type ssrfGuardedDialer struct {
	*net.Dialer
	allowPrivateCIDR bool
}

func (d *ssrfGuardedDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if d.Dialer == nil {
		d.Dialer = &net.Dialer{Timeout: DefaultTimeout}
	}
	conn, err := d.Dialer.DialContext(ctx, network, addr)
	if err != nil {
		// Short-circuit "connection refused to 127.0.0.1" style errors
		// that come from the dialer itself; they don't imply we
		// bypassed the guard.
		if isConnRefused(err) {
			return nil, err
		}
		return nil, err
	}
	if d.allowPrivateCIDR {
		return conn, nil
	}
	remote, ok := conn.RemoteAddr().(*net.TCPAddr)
	if !ok {
		_ = conn.Close()
		return nil, fmt.Errorf("%w: non-TCP remote addr", ErrDisallowedAddress)
	}
	if ip := remote.IP; isBlockedIP(ip) {
		_ = conn.Close()
		return nil, fmt.Errorf("%w: %s", ErrDisallowedAddress, ip.String())
	}
	return conn, nil
}

// cgnet / ula are parsed once at package init so the isBlockedIP hot
// path does not allocate on every connect.
var (
	cgnet = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}
	ula   = &net.IPNet{IP: net.ParseIP("fc00::"), Mask: net.CIDRMask(7, 128)}
)

// isBlockedIP returns true for addresses the P2 plan refuses by
// default: loopback, link-local, private RFC 1918 + RFC 6598, ULA,
// multicast, unspecified. The check is the first line of defence
// against DNS rebinding: even if a hostname resolves to a public IP
// at resolve time and a private IP at connect time, this guard
// catches the connect-time IP.
func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	return isBlockedByStdlib(ip) || isBlockedByRange(ip)
}

func isBlockedByStdlib(ip net.IP) bool {
	return ip.IsUnspecified() ||
		ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsPrivate()
}

func isBlockedByRange(ip net.IP) bool {
	// RFC 6598 (carrier-grade NAT 100.64.0.0/10) is not covered by
	// IsPrivate; add it explicitly so shared tenancy CGN ranges do
	// not leak webhook egress.
	if cgnet.Contains(ip) {
		return true
	}
	// IPv6 ULA (fc00::/7). Go's IsPrivate already covers this, but the
	// explicit check keeps readers from chasing the stdlib definition.
	return ula.Contains(ip)
}

// isConnRefused lets callers distinguish "SSRF guard rejected the
// conn" (new error) from "OS said ECONNREFUSED" (preserve original
// error so higher-level retry logic stays accurate).
func isConnRefused(err error) bool {
	if err == nil {
		return false
	}
	var se syscall.Errno
	if errors.As(err, &se) {
		return se == syscall.ECONNREFUSED
	}
	return false
}
