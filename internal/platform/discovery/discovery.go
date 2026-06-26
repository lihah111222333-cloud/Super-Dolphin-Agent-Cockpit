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

// peerHealthProbeTimeout 限制 discovery 健康探测耗时，防止启动路径被失效 peer 地址拖住。
const peerHealthProbeTimeout = 250 * time.Millisecond

func discoveryRootDir() string {
	dir := strings.TrimSpace(os.TempDir())
	if dir == "" {
		return filepath.Clean(fallbackDiscoveryDir)
	}
	return filepath.Clean(dir)
}

// discoveryPath 返回 peer discovery 文件路径。
// 文件名带 binary 和 parentPID，避免同一机器上多个父进程的 peer 地址互相覆盖。
func discoveryPath(binaryName string, parentPID int) string {
	return filepath.Join(discoveryRootDir(),
		fmt.Sprintf("super-agent-mcp-%s-%d.port", binaryName, parentPID))
}

// WriteDiscoveryFile 原子写入 peer HTTP 监听地址。
// 采用临时文件加 rename，避免 BuildManifest 读到半写入内容；文件权限收紧到当前用户可读写。
func WriteDiscoveryFile(binaryName string, parentPID int, addr string) error {
	path := discoveryPath(binaryName, parentPID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.TrimSpace(addr)+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ReadDiscoveryAddr 从 discovery 文件读取 peer HTTP 地址。
// 空文件视为损坏并返回错误，调用方不能把空地址当作未发现静默处理。
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

// CleanupDiscoveryFile 删除指定 binary 和父进程对应的 discovery 文件。
func CleanupDiscoveryFile(binaryName string, parentPID int) error {
	return os.Remove(discoveryPath(binaryName, parentPID))
}

// DiscoverPeerHTTPAddr 读取并探测当前父进程下的 peer HTTP 地址。
// 失效 discovery 会被 fail-closed 清理，避免继续使用 stale 端口。
func DiscoverPeerHTTPAddr(binaryName string) (string, error) {
	return DiscoverPeerHTTPAddrWithToken(binaryName, "")
}

// DiscoverPeerHTTPAddrWithToken 使用 bearer token 探测当前父进程下的 peer HTTP 地址。
func DiscoverPeerHTTPAddrWithToken(binaryName, token string) (string, error) {
	return DiscoverPeerHTTPAddrForParentWithToken(binaryName, os.Getpid(), token)
}

// DiscoverPeerHTTPAddrForParent 读取并探测指定父进程的 peer HTTP 地址。
// 测试和外层进程可用它检查非当前进程的 discovery 文件。
func DiscoverPeerHTTPAddrForParent(binaryName string, parentPID int) (string, error) {
	return DiscoverPeerHTTPAddrForParentWithToken(binaryName, parentPID, "")
}

// DiscoverPeerHTTPAddrForParentWithToken 使用 bearer token 探测指定父进程的 peer HTTP 地址。
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

// ProbePeerHTTPAddr 验证地址是否提供 MCP HTTP ping 端点。
func ProbePeerHTTPAddr(addr string) error {
	return ProbePeerHTTPAddrWithToken(addr, "")
}

// ProbePeerHTTPAddrWithToken 携带可选 bearer token 验证 MCP HTTP ping 端点。
// 非 2xx、非 JSON-RPC 2.0 或返回 error 字段都会视为不可用。
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

// WritePeerDiscovery 使用当前进程父 PID 写入 peer discovery 文件。
// 该函数在 peer 进程内部调用，父 PID 异常时立即报错，避免写入无法归属的地址文件。
func WritePeerDiscovery(binaryName string, addr string) error {
	ppid := os.Getppid()
	if ppid <= 1 {
		return fmt.Errorf("invalid parent PID %d for discovery file", ppid)
	}
	return WriteDiscoveryFile(binaryName, ppid, addr)
}

// CleanupPeerDiscovery 在 peer 进程退出时删除当前父 PID 对应的 discovery 文件。
// 父 PID 已失效时直接跳过，避免 shutdown 路径因清理文件失败放大错误。
func CleanupPeerDiscovery(binaryName string) error {
	ppid := os.Getppid()
	if ppid <= 1 {
		return nil
	}
	return CleanupDiscoveryFile(binaryName, ppid)
}

// IsValidHTTPAddr 检查地址是否是带有效端口的 host:port。
// 这里只做格式边界校验，实际 MCP 可用性由 ProbePeerHTTPAddr 负责。
func IsValidHTTPAddr(addr string) bool {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	port, err := strconv.Atoi(portStr)
	return err == nil && port > 0 && port <= 65535
}
