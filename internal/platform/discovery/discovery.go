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

const fallbackDiscoveryDir = "/tmp"
const peerHealthProbeTimeout = 250 * time.Millisecond

func discoveryRootDir() string {
	dir := strings.TrimSpace(os.TempDir())
	if dir == "" {
		return filepath.Clean(fallbackDiscoveryDir)
	}
	return filepath.Clean(dir)
}

// discoveryPath returns the conventional path for the peer discovery file.
// Format: <temp>/super-agent-mcp-{binary}-{parentPID}.port
func discoveryPath(binaryName string, parentPID int) string {
	return filepath.Join(discoveryRootDir(),
		fmt.Sprintf("super-agent-mcp-%s-%d.port", binaryName, parentPID))
}

// WriteDiscoveryFile atomically writes the HTTP listen address to the
// discovery file so that BuildManifest() can find the peer's HTTP endpoint.
// Uses write-to-temp + rename for crash-safe, race-free updates.
// WriteDiscoveryFile 写入discovery文件。
func WriteDiscoveryFile(binaryName string, parentPID int, addr string) error {
	path := discoveryPath(binaryName, parentPID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.TrimSpace(addr)+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ReadDiscoveryAddr reads the peer HTTP listen address from the discovery file.
// Returns the address string (e.g. "127.0.0.1:9091") or an error if not found.
// ReadDiscoveryAddr 读取discoveryaddr。
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
// CleanupDiscoveryFile 处理cleanupdiscovery文件。
func CleanupDiscoveryFile(binaryName string, parentPID int) error {
	return os.Remove(discoveryPath(binaryName, parentPID))
}

// DiscoverPeerHTTPAddr reads and verifies a peer HTTP endpoint. Stale
// discovery is fail-closed: if the address cannot answer a short MCP ping, the
// discovery file is removed and no address is returned.
// DiscoverPeerHTTPAddr 处理discoverpeerHTTPaddr。
func DiscoverPeerHTTPAddr(binaryName string) (string, error) {
	return DiscoverPeerHTTPAddrWithToken(binaryName, "")
}

// DiscoverPeerHTTPAddrWithToken 处理带令牌的discoverpeerHTTPaddr。
func DiscoverPeerHTTPAddrWithToken(binaryName, token string) (string, error) {
	return DiscoverPeerHTTPAddrForParentWithToken(binaryName, os.Getpid(), token)
}

// DiscoverPeerHTTPAddrForParent is the parent-PID-aware form used by tests and
// parent processes that need to inspect a specific discovery file.
// DiscoverPeerHTTPAddrForParent 为parent处理discoverpeerHTTPaddr。
func DiscoverPeerHTTPAddrForParent(binaryName string, parentPID int) (string, error) {
	return DiscoverPeerHTTPAddrForParentWithToken(binaryName, parentPID, "")
}

// DiscoverPeerHTTPAddrForParentWithToken 处理带令牌的discoverpeerHTTPaddrparent。
func DiscoverPeerHTTPAddrForParentWithToken(binaryName string, parentPID int, token string) (string, error) {
	addr, err := ReadDiscoveryAddr(binaryName, parentPID)
	if err != nil {
		return "", err
	}
	if err := ProbePeerHTTPAddrWithToken(addr, token); err != nil {
		_ = CleanupDiscoveryFile(binaryName, parentPID)
		return "", err
	}
	return addr, nil
}

// ProbePeerHTTPAddr verifies that addr speaks the MCP HTTP ping endpoint.
// ProbePeerHTTPAddr 处理probepeerHTTPaddr。
func ProbePeerHTTPAddr(addr string) error {
	return ProbePeerHTTPAddrWithToken(addr, "")
}

// ProbePeerHTTPAddrWithToken 处理带令牌的probepeerHTTPaddr。
func ProbePeerHTTPAddrWithToken(addr, token string) error {
	addr = strings.TrimSpace(addr)
	if !IsValidHTTPAddr(addr) {
		return fmt.Errorf("invalid peer HTTP address %q", addr)
	}
	body := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	client := &http.Client{Timeout: peerHealthProbeTimeout}
	req, err := newPeerProbeRequest(addr, token, body)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
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

func newPeerProbeRequest(addr, token string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/mcp", body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token = strings.TrimSpace(token); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req, nil
}

// WritePeerDiscovery writes the discovery file using the current process's
// parent PID. This is intended for use inside a peer process.
// WritePeerDiscovery 写入peerdiscovery。
func WritePeerDiscovery(binaryName string, addr string) error {
	ppid := os.Getppid()
	if ppid <= 1 {
		return fmt.Errorf("invalid parent PID %d for discovery file", ppid)
	}
	return WriteDiscoveryFile(binaryName, ppid, addr)
}

// CleanupPeerDiscovery removes the discovery file using the current process's
// parent PID. Intended for use in peer process shutdown.
// CleanupPeerDiscovery 处理cleanuppeerdiscovery。
func CleanupPeerDiscovery(binaryName string) error {
	ppid := os.Getppid()
	if ppid <= 1 {
		return nil
	}
	return CleanupDiscoveryFile(binaryName, ppid)
}

// IsValidHTTPAddr does a basic check that addr looks like host:port.
// IsValidHTTPAddr 判断validHTTPaddr是否可用。
func IsValidHTTPAddr(addr string) bool {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	port, err := strconv.Atoi(portStr)
	return err == nil && port > 0 && port <= 65535
}
