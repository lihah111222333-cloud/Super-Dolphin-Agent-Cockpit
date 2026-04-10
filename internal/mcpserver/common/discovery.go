package common

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const discoveryDir = "/tmp"

// discoveryPath returns the conventional path for the peer discovery file.
// Format: /tmp/super-agent-mcp-{binary}-{parentPID}.port
func discoveryPath(binaryName string, parentPID int) string {
	return filepath.Join(discoveryDir,
		fmt.Sprintf("super-agent-mcp-%s-%d.port", binaryName, parentPID))
}

// WriteDiscoveryFile writes the HTTP listen address to the discovery file
// so that BuildManifest() can find the peer's HTTP endpoint.
func WriteDiscoveryFile(binaryName string, parentPID int, addr string) error {
	path := discoveryPath(binaryName, parentPID)
	return os.WriteFile(path, []byte(strings.TrimSpace(addr)+"\n"), 0o644)
}

// ReadDiscoveryAddr reads the peer HTTP listen address from the discovery file.
// Returns the address string (e.g. "127.0.0.1:9091") or an error if not found.
func ReadDiscoveryAddr(binaryName string, parentPID int) (string, error) {
	path := discoveryPath(binaryName, parentPID)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	addr := strings.TrimSpace(string(data))
	if addr == "" {
		return "", fmt.Errorf("empty discovery file: %s", path)
	}
	return addr, nil
}

// CleanupDiscoveryFile removes the discovery file for a given binary.
func CleanupDiscoveryFile(binaryName string, parentPID int) error {
	return os.Remove(discoveryPath(binaryName, parentPID))
}

// DiscoverPeerHTTPAddr is a convenience function that tries to read the
// discovery file using the current process's PID as the parent PID.
// This is intended for use inside the parent process (super-agent-debug).
func DiscoverPeerHTTPAddr(binaryName string) (string, error) {
	return ReadDiscoveryAddr(binaryName, os.Getpid())
}

// WritePeerDiscovery writes the discovery file using the current process's
// parent PID. This is intended for use inside a peer process.
func WritePeerDiscovery(binaryName string, addr string) error {
	ppid := os.Getppid()
	if ppid <= 1 {
		return fmt.Errorf("invalid parent PID %d for discovery file", ppid)
	}
	return WriteDiscoveryFile(binaryName, ppid, addr)
}

// CleanupPeerDiscovery removes the discovery file using the current process's
// parent PID. Intended for use in peer process shutdown.
func CleanupPeerDiscovery(binaryName string) error {
	ppid := os.Getppid()
	if ppid <= 1 {
		return nil
	}
	return CleanupDiscoveryFile(binaryName, ppid)
}

// IsValidHTTPAddr does a basic check that addr looks like host:port.
func IsValidHTTPAddr(addr string) bool {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	port, err := strconv.Atoi(portStr)
	return err == nil && port > 0 && port <= 65535
}
