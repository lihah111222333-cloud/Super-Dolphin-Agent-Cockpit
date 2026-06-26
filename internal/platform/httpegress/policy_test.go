package httpegress

import (
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

func TestValidateResolvedIPRejectsUnsafeAddresses(t *testing.T) {
	tests := []struct {
		name string
		addr string
	}{
		{name: "loopback", addr: "127.0.0.1"},
		{name: "private", addr: "10.0.0.5"},
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
	if err := ValidateResolvedIP("example.com", netip.MustParseAddr("93.184.216.34")); err != nil {
		t.Fatalf("ValidateResolvedIP() error = %v, want public address allowed", err)
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
