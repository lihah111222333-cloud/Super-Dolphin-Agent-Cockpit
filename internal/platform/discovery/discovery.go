package discovery

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const discoveryDir = "/tmp"
const peerHealthProbeTimeout = 250 * time.Millisecond

// discoveryPath returns the conventional path for the peer discovery file.
// Format: /tmp/super-agent-mcp-{binary}-{parentPID}.port
func discoveryPath(binaryName string, parentPID int) string {
	return filepath.Join(discoveryDir,
		fmt.Sprintf("super-agent-mcp-%s-%d.port", binaryName, parentPID))
}

// WriteDiscoveryFile atomically writes the HTTP listen address to the
// discovery file so that BuildManifest() can find the peer's HTTP endpoint.
// Uses write-to-temp + rename for crash-safe, race-free updates.
func WriteDiscoveryFile(binaryName string, parentPID int, addr string) error {
	path := discoveryPath(binaryName, parentPID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.TrimSpace(addr)+"\n"), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
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

// DiscoverPeerHTTPAddr reads and verifies a peer HTTP endpoint. Stale
// discovery is fail-closed: if the address cannot answer a short MCP ping, the
// discovery file is removed and no address is returned.
func DiscoverPeerHTTPAddr(binaryName string) (string, error) {
	return DiscoverPeerHTTPAddrForParent(binaryName, os.Getpid())
}

// DiscoverPeerHTTPAddrForParent is the parent-PID-aware form used by tests and
// parent processes that need to inspect a specific discovery file.
func DiscoverPeerHTTPAddrForParent(binaryName string, parentPID int) (string, error) {
	addr, err := ReadDiscoveryAddr(binaryName, parentPID)
	if err != nil {
		return "", err
	}
	if err := ProbePeerHTTPAddr(addr); err != nil {
		_ = CleanupDiscoveryFile(binaryName, parentPID)
		return "", err
	}
	return addr, nil
}

// ProbePeerHTTPAddr verifies that addr speaks the MCP HTTP ping endpoint.
func ProbePeerHTTPAddr(addr string) error {
	addr = strings.TrimSpace(addr)
	if !IsValidHTTPAddr(addr) {
		return fmt.Errorf("invalid peer HTTP address %q", addr)
	}
	body := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	client := &http.Client{Timeout: peerHealthProbeTimeout}
	resp, err := client.Post("http://"+addr+"/mcp", "application/json", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("peer health probe status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	var envelope struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Error   json.RawMessage `json:"error,omitempty"`
		Result  json.RawMessage `json:"result,omitempty"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("peer health probe response decode: %w", err)
	}
	if strings.TrimSpace(envelope.JSONRPC) != "2.0" || len(envelope.Error) != 0 {
		return fmt.Errorf("peer health probe unhealthy response")
	}
	return nil
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
