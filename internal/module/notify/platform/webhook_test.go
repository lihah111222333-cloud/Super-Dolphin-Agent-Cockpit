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
	blocked := []string{
		"127.0.0.1",          // loopback
		"::1",                 // loopback v6
		"10.0.0.1",            // RFC 1918
		"192.168.1.2",         // RFC 1918
		"172.16.5.7",          // RFC 1918
		"169.254.169.254",     // link-local (IMDS)
		"100.64.1.2",          // RFC 6598 CGN
		"fc00::1",             // ULA
		"fe80::1234",          // link-local v6
		"224.0.0.5",           // multicast
		"0.0.0.0",             // unspecified
	}
	for _, s := range blocked {
		ip := net.ParseIP(s)
		if !isBlockedIP(ip) {
			t.Errorf("isBlockedIP(%q) = false, want true", s)
		}
	}
	publics := []string{
		"8.8.8.8", "1.1.1.1", "140.82.121.4", "2606:4700:4700::1111",
	}
	for _, s := range publics {
		ip := net.ParseIP(s)
		if isBlockedIP(ip) {
			t.Errorf("isBlockedIP(%q) = true, want false", s)
		}
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
