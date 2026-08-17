package discovery

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestWriteDiscoveryFileUsesOwnerOnlyPermissions(t *testing.T) {
	binaryName := "mcp-test-perms"
	parentPID := os.Getpid()
	if err := WriteDiscoveryFile(binaryName, parentPID, "127.0.0.1:12345"); err != nil {
		t.Fatalf("WriteDiscoveryFile() error = %v", err)
	}
	t.Cleanup(func() { _ = CleanupDiscoveryFile(binaryName, parentPID) })

	info, err := os.Stat(discoveryPath(binaryName, parentPID))
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	// discovery 文件存在性跨平台共享；owner-only 权限由 tagged helper 按平台语义断言。
	assertDiscoveryFileOwnerOnly(t, info)
}

func TestDiscoverPeersDeletesStalePeerAddrAfterHealthProbeFailure(t *testing.T) {
	binaryName := "mcp-lsp"
	addr := closedTCPAddr(t)
	if err := WriteDiscoveryFile(binaryName, os.Getpid(), addr); err != nil {
		t.Fatalf("WriteDiscoveryFile() error = %v", err)
	}
	t.Cleanup(func() { _ = CleanupDiscoveryFile(binaryName, os.Getpid()) })

	got, err := DiscoverPeerHTTPAddr(binaryName)
	if err == nil {
		t.Fatalf("DiscoverPeerHTTPAddr() = %q, nil error; want stale peer failure", got)
	}
	if got != "" {
		t.Fatalf("DiscoverPeerHTTPAddr() addr = %q, want empty on stale peer", got)
	}
	if _, err := ReadDiscoveryAddr(binaryName, os.Getpid()); !os.IsNotExist(err) {
		t.Fatalf("ReadDiscoveryAddr() after stale cleanup error = %v, want os.IsNotExist", err)
	}
}

func TestDiscoverPeerHTTPAddrWithTokenSendsBearerToken(t *testing.T) {
	binaryName := "mcp-test-auth"
	wantToken := "secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+wantToken {
			http.Error(w, "missing bearer token", http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/mcp" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  map[string]any{},
		})
	}))
	t.Cleanup(server.Close)

	addr := strings.TrimPrefix(server.URL, "http://")
	if err := WriteDiscoveryFile(binaryName, os.Getpid(), addr); err != nil {
		t.Fatalf("WriteDiscoveryFile() error = %v", err)
	}
	t.Cleanup(func() { _ = CleanupDiscoveryFile(binaryName, os.Getpid()) })

	got, err := DiscoverPeerHTTPAddrWithToken(binaryName, wantToken)
	if err != nil {
		t.Fatalf("DiscoverPeerHTTPAddrWithToken() error = %v", err)
	}
	if got != addr {
		t.Fatalf("DiscoverPeerHTTPAddrWithToken() = %q, want %q", got, addr)
	}
}

func closedTCPAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return addr
}
