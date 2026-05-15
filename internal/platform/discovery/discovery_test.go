package discovery

import (
	"net"
	"os"
	"testing"
)

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
