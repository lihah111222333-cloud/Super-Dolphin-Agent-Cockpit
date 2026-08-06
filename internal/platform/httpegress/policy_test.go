package httpegress

import (
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

func TestEgressPolicyOwnersPreserveHeaderAndCGNATBoundaries(t *testing.T) {
	for _, header := range []string{"Host", " transfer-encoding ", "PROXY-AUTHORIZATION"} {
		if !isUnsafeHeaderName(header) {
			t.Fatalf("isUnsafeHeaderName(%q) = false, want true", header)
		}
	}
	if isUnsafeHeaderName("X-Request-ID") {
		t.Fatal("isUnsafeHeaderName(X-Request-ID) = true, want false")
	}
	prefix := carrierGradeNATPrefix()
	if !prefix.Contains(netip.MustParseAddr("100.64.0.0")) || !prefix.Contains(netip.MustParseAddr("100.127.255.255")) || prefix.Contains(netip.MustParseAddr("100.128.0.0")) {
		t.Fatalf("carrierGradeNATPrefix() = %s, want exact 100.64.0.0/10 boundary", prefix)
	}
}

func TestValidateResolvedIPRejectsUnsafeAddresses(t *testing.T) {
	tests := []struct {
		name string
		addr string
	}{
		{name: "loopback", addr: "127.0.0.1"},
		{name: "private", addr: "10.0.0.5"},
		{name: "cgnat first", addr: "100.64.0.0"},
		{name: "cgnat last", addr: "100.127.255.255"},
		{name: "cgnat ipv4 mapped", addr: "::ffff:100.64.0.1"},
		{name: "link local", addr: "169.254.169.254"},
		{name: "unspecified", addr: "0.0.0.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateResolvedIP("example.com", netip.MustParseAddr(tt.addr))
			if err == nil || !strings.Contains(err.Error(), "private network") {
				t.Fatalf("ValidateResolvedIP(%s) error = %v, want private network rejection", tt.addr, err)
			}
		})
	}
}

func TestValidateResolvedIPAllowsPublicAddress(t *testing.T) {
	for _, raw := range []string{"93.184.216.34", "100.63.255.255", "100.128.0.0"} {
		if err := ValidateResolvedIP("example.com", netip.MustParseAddr(raw)); err != nil {
			t.Fatalf("ValidateResolvedIP(%s) error = %v, want public address allowed", raw, err)
		}
	}
}

func TestNewPublicHTTPTransportDoesNotInheritEnvironmentProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:18080")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:18080")

	transport, ok := NewPublicHTTPTransport().(*http.Transport)
	if !ok {
		t.Fatalf("NewPublicHTTPTransport() type = %T, want *http.Transport", transport)
	}
	if transport.Proxy != nil {
		req := &http.Request{URL: mustParseHTTPURL(t, "https://example.com")}
		proxyURL, err := transport.Proxy(req)
		if err != nil {
			t.Fatalf("transport.Proxy() error = %v", err)
		}
		t.Fatalf("NewPublicHTTPTransport() proxy = %v, want disabled environment proxy", proxyURL)
	}
}

func TestValidateRedirectRejectsLoopbackTarget(t *testing.T) {
	req := &http.Request{URL: mustParseHTTPURL(t, "http://127.0.0.1/mcp")}
	err := ValidateRedirect(req, nil)
	if err == nil || !strings.Contains(err.Error(), "private network") {
		t.Fatalf("ValidateRedirect() error = %v, want private network rejection", err)
	}
}

func mustParseHTTPURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse URL %q: %v", raw, err)
	}
	return parsed
}
