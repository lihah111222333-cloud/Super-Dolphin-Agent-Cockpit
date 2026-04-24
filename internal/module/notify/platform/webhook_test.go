package platform

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPostRejectsNonHTTPS(t *testing.T) {
	t.Parallel()
	c := NewWebhookClient(WebhookClientConfig{})
	err := c.Post(context.Background(), "http://example.com/hook", "application/json", []byte("{}"))
	if !errors.Is(err, ErrDisallowedScheme) {
		t.Fatalf("want ErrDisallowedScheme, got %v", err)
	}
}

func TestPostRejectsFileScheme(t *testing.T) {
	t.Parallel()
	c := NewWebhookClient(WebhookClientConfig{})
	err := c.Post(context.Background(), "file:///etc/passwd", "application/json", []byte(""))
	if !errors.Is(err, ErrDisallowedScheme) {
		t.Fatalf("want ErrDisallowedScheme for file://, got %v", err)
	}
}

func TestPostRejectsLoopbackAddress(t *testing.T) {
	t.Parallel()
	// Stand up a local TLS server on 127.0.0.1 and verify the SSRF
	// guard refuses to reach it even when the hostname is `localhost`.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := NewWebhookClient(WebhookClientConfig{Timeout: 2 * time.Second})
	// Override the client's TLS verification for the self-signed cert
	// so the test would succeed *if* the SSRF guard weren't in place.
	c.http.Transport.(*http.Transport).TLSClientConfig = srv.Client().Transport.(*http.Transport).TLSClientConfig
	err := c.Post(context.Background(), srv.URL, "application/json", []byte("{}"))
	if !errors.Is(err, ErrDisallowedAddress) {
		t.Fatalf("want ErrDisallowedAddress on loopback, got %v", err)
	}
}

func TestPostAllowsPrivateCIDRWhenOptedIn(t *testing.T) {
	t.Parallel()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := NewWebhookClient(WebhookClientConfig{
		Timeout:          2 * time.Second,
		AllowPrivateCIDR: true,
	})
	c.http.Transport.(*http.Transport).TLSClientConfig = srv.Client().Transport.(*http.Transport).TLSClientConfig
	if err := c.Post(context.Background(), srv.URL, "application/json", []byte("{}")); err != nil {
		t.Fatalf("AllowPrivateCIDR=true should succeed: %v", err)
	}
}

func TestIsBlockedIPCoversSSRFRanges(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		ip   net.IP
		want bool
	}{
		{name: "nil", ip: nil, want: true},
		{name: "unspecified_ipv4", ip: net.ParseIP("0.0.0.0"), want: true},
		{name: "loopback_ipv4", ip: net.ParseIP("127.0.0.1"), want: true},
		{name: "link_local_unicast_ipv4", ip: net.ParseIP("169.254.169.254"), want: true},
		{name: "multicast_ipv4", ip: net.ParseIP("224.0.0.1"), want: true},
		{name: "link_local_multicast_ipv6", ip: net.ParseIP("ff02::1"), want: true},
		{name: "interface_local_multicast_ipv6", ip: net.ParseIP("ff01::1"), want: true},
		{name: "private_10", ip: net.ParseIP("10.0.0.1"), want: true},
		{name: "private_192_168", ip: net.ParseIP("192.168.1.1"), want: true},
		{name: "private_172_16", ip: net.ParseIP("172.16.0.1"), want: true},
		{name: "rfc6598_cgnet", ip: net.ParseIP("100.64.0.1"), want: true},
		{name: "ula_ipv6", ip: net.ParseIP("fc00::1"), want: true},
		{name: "link_local_unicast_ipv6", ip: net.ParseIP("fe80::1"), want: true},
		{name: "public_ipv4", ip: net.ParseIP("8.8.8.8"), want: false},
		{name: "public_ipv6", ip: net.ParseIP("2001:4860:4860::8888"), want: false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			if got := isBlockedIP(tt.ip); got != tt.want {
				t.Fatalf("isBlockedIP(%v) = %t, want %t", tt.ip, got, tt.want)
			}
		})
	}
}

// TestCheckRedirectEnforcesHTTPSAgain: even if the initial URL is fine,
// a rogue webhook returning 302 -> http:// must be refused.
func TestCheckRedirectEnforcesHTTPSAgain(t *testing.T) {
	t.Parallel()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com/malicious", http.StatusFound)
	}))
	defer srv.Close()
	c := NewWebhookClient(WebhookClientConfig{
		Timeout:          2 * time.Second,
		AllowPrivateCIDR: true,
	})
	c.http.Transport.(*http.Transport).TLSClientConfig = srv.Client().Transport.(*http.Transport).TLSClientConfig
	err := c.Post(context.Background(), srv.URL, "application/json", []byte("{}"))
	if err == nil {
		t.Fatal("redirect to http should have failed the request")
	}
	if !strings.Contains(err.Error(), "https") {
		t.Fatalf("error should mention scheme rejection, got %v", err)
	}
}

// helper: fmt for errors across Go versions that evolved the wrapping
// formatting of net.OpError; silence the linter on unused import when
// no test uses fmt.
var _ = fmt.Sprintf
