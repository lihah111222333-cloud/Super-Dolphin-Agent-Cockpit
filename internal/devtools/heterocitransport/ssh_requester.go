package heterocitransport

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

const maxSSHProbeResponseBytes = 1 << 20

// SSHRequesterConfig 显式绑定 SSH 可执行文件、动态 manifest 和每节点远端 probe argv。
type SSHRequesterConfig struct {
	SSHExecutable string
	Manifest      heteroci.Manifest
	NodeCommands  map[string][]string
	Timeout       time.Duration
	WaitDelay     time.Duration
}

// SSHRequester 通过 caller-provided SSH endpoint 调用远端严格 probe 程序。
type SSHRequester struct {
	executable     string
	connectSeconds int64
	timeout        time.Duration
	waitDelay      time.Duration
	nodes          map[string]sshProbeNode
}

var _ Requester = (*SSHRequester)(nil)

type sshProbeNode struct {
	config  heteroci.NodeConfig
	host    string
	command []string
}

// NewSSHRequester 构造无节点发现、默认 host 或命令 fallback 的 SSH requester。
func NewSSHRequester(config SSHRequesterConfig) (*SSHRequester, error) {
	if err := validateSSHRequesterConfig(config); err != nil {
		return nil, err
	}
	nodes := make(map[string]sshProbeNode, len(config.Manifest.Nodes))
	for _, node := range config.Manifest.Nodes {
		host, err := parseSSHProbeEndpoint(node.Endpoint)
		if err != nil {
			return nil, fmt.Errorf("%w: node endpoint", ErrInvalidDependencies)
		}
		nodes[node.Endpoint] = sshProbeNode{config: node, host: host, command: slices.Clone(config.NodeCommands[node.NodeID])}
	}
	return &SSHRequester{
		executable: config.SSHExecutable, connectSeconds: int64(config.Timeout / time.Second),
		timeout: config.Timeout, waitDelay: config.WaitDelay, nodes: nodes,
	}, nil
}

// Do 校验 core request 身份后只调用 manifest 中同一 endpoint 的远端 probe。
func (requester *SSHRequester) Do(ctx context.Context, endpoint string, request []byte) (ResponseBody, error) {
	if requester == nil || ctx == nil {
		return nil, ErrInvalidDependencies
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	node, ok := requester.nodes[endpoint]
	if !ok {
		return nil, ErrInvalidRequest
	}
	decoded, err := heteroci.DecodeProbeRequest(request)
	if err != nil || decoded.NodeID != node.config.NodeID || decoded.ProbeIdentity != node.config.ProbeIdentity {
		return nil, ErrInvalidRequest
	}
	output, unavailable, err := requester.run(ctx, node, request)
	if unavailable {
		return nil, ErrTransportUnavailable
	}
	if err != nil {
		return nil, err
	}
	return newSSHResponseBody(output), nil
}

// run 在独立 deadline 内执行单个 SSH probe，并把 exit 255 唯一映射为 unavailable。
func (requester *SSHRequester) run(ctx context.Context, node sshProbeNode, request []byte) ([]byte, bool, error) {
	runCtx, cancel := context.WithTimeout(ctx, requester.timeout)
	defer cancel()
	remoteCommand := sshRemoteProbeCommand(node.command, request)
	args := []string{"-o", "BatchMode=yes", "-o", fmt.Sprintf("ConnectTimeout=%d", requester.connectSeconds), "--", node.host, remoteCommand}
	command := exec.CommandContext(runCtx, requester.executable, args...)
	command.WaitDelay = requester.waitDelay
	var stdout, stderr bytes.Buffer
	command.Stdout = &limitedBuffer{buffer: &stdout, limit: maxSSHProbeResponseBytes}
	command.Stderr = &limitedBuffer{buffer: &stderr, limit: 64 << 10}
	err := command.Run()
	if runCtx.Err() != nil {
		return nil, false, errors.Join(ErrInvalidResponse, runCtx.Err())
	}
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) && exitError.ExitCode() == 255 {
			return nil, true, nil
		}
		return nil, false, errors.Join(ErrInvalidResponse, errors.New("SSH probe command failed"))
	}
	if stdout.Len() == 0 || stdout.Len() > maxSSHProbeResponseBytes {
		return nil, false, ErrInvalidResponse
	}
	return slices.Clone(stdout.Bytes()), false, nil
}

// validateSSHRequesterConfig 校验 requester 的完整依赖和动态 node-command 覆盖。
func validateSSHRequesterConfig(config SSHRequesterConfig) error {
	if err := validateSSHExecutable(config.SSHExecutable); err != nil {
		return err
	}
	if err := config.Manifest.Validate(); err != nil {
		return fmt.Errorf("%w: manifest", ErrInvalidDependencies)
	}
	if err := validateSSHRequesterTiming(config.Timeout, config.WaitDelay); err != nil {
		return err
	}
	if len(config.NodeCommands) != len(config.Manifest.Nodes) {
		return fmt.Errorf("%w: node command coverage", ErrInvalidDependencies)
	}
	for _, node := range config.Manifest.Nodes {
		if err := validateSSHRemoteCommand(config.NodeCommands[node.NodeID]); err != nil {
			return err
		}
	}
	return nil
}

// validateSSHRequesterTiming 校验进程 deadline 与 WaitDelay 的显式有界关系。
func validateSSHRequesterTiming(timeout, waitDelay time.Duration) error {
	validTimeout := timeout >= time.Second && timeout <= 120*time.Second && timeout%time.Second == 0
	if !validTimeout || waitDelay <= 0 || waitDelay > timeout {
		return fmt.Errorf("%w: timeout", ErrInvalidDependencies)
	}
	return nil
}

// validateSSHExecutable 拒绝相对、非 clean、非普通或不可执行的 SSH 路径。
func validateSSHExecutable(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("%w: SSH executable path", ErrInvalidDependencies)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode()&0o111 == 0 {
		return fmt.Errorf("%w: SSH executable", ErrInvalidDependencies)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return fmt.Errorf("%w: SSH executable path contains a symlink", ErrInvalidDependencies)
	}
	return nil
}

// parseSSHProbeEndpoint 只从显式 ssh:// endpoint 提取安全 host alias。
func parseSSHProbeEndpoint(endpoint string) (string, error) {
	host, ok := strings.CutPrefix(endpoint, "ssh://")
	if !ok || host == "" || len(host) > 128 {
		return "", ErrInvalidDependencies
	}
	for index := 0; index < len(host); index++ {
		character := host[index]
		if !isSSHHostAliasCharacter(character) {
			return "", ErrInvalidDependencies
		}
	}
	return host, nil
}

// isSSHHostAliasCharacter 将 host alias 限定为 ASCII 字母、数字和固定分隔符。
func isSSHHostAliasCharacter(character byte) bool {
	return character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
		character >= '0' && character <= '9' || strings.ContainsRune("._-", rune(character))
}

// validateSSHRemoteCommand 校验 caller-provided 远端 probe argv 不含空值或控制字符。
func validateSSHRemoteCommand(command []string) error {
	if len(command) == 0 {
		return fmt.Errorf("%w: remote probe command", ErrInvalidDependencies)
	}
	for _, argument := range command {
		if argument == "" || len(argument) > 4096 || strings.IndexFunc(argument, unicode.IsControl) >= 0 {
			return fmt.Errorf("%w: remote probe command", ErrInvalidDependencies)
		}
	}
	return nil
}

func sshRemoteProbeCommand(command []string, request []byte) string {
	parts := make([]string, 0, len(command)+2)
	for _, argument := range command {
		parts = append(parts, shellQuoteSSHArgument(argument))
	}
	parts = append(parts, "--probe-request-base64", shellQuoteSSHArgument(base64.StdEncoding.EncodeToString(request)))
	return strings.Join(parts, " ")
}

func shellQuoteSSHArgument(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

type limitedBuffer struct {
	buffer *bytes.Buffer
	limit  int
}

// Write 在写入前拒绝超过 probe 输出边界的数据。
func (buffer *limitedBuffer) Write(data []byte) (int, error) {
	if buffer.buffer.Len()+len(data) > buffer.limit {
		return 0, ErrInvalidResponse
	}
	return buffer.buffer.Write(data)
}

type sshResponseBody struct {
	mu     sync.Mutex
	reader *bytes.Reader
	closed bool
}

func newSSHResponseBody(data []byte) *sshResponseBody {
	return &sshResponseBody{reader: bytes.NewReader(slices.Clone(data))}
}

// Read 在锁内读取 immutable response，并优先观察调用 context。
func (body *sshResponseBody) Read(ctx context.Context, destination []byte) (int, error) {
	if ctx == nil {
		return 0, ErrInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	body.mu.Lock()
	defer body.mu.Unlock()
	if body.closed {
		return 0, ErrInvalidResponse
	}
	return body.reader.Read(destination)
}

// Close 幂等关闭内存 response body。
func (body *sshResponseBody) Close() error {
	body.mu.Lock()
	defer body.mu.Unlock()
	if body.closed {
		return nil
	}
	body.closed = true
	return nil
}
