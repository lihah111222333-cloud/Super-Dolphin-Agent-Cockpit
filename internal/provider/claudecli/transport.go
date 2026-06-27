package claudecli

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// processSig 是 transport 内部使用的跨平台进程信号抽象。
// 共享代码只表达中断/终止/强杀意图，具体映射交给 Unix 或 Windows 实现。
type processSig int

const (
	sigInterrupt processSig = iota
	sigTerminate
	sigForceKill
)

const (
	shutdownGracePeriod  = 3 * time.Second
	shutdownPollInterval = 50 * time.Millisecond
	stderrLimitBytes     = 8 * 1024
	maxCLILineBytes      = 20 << 20
)

type transport struct {
	cmd     *exec.Cmd
	guard   *processGuard
	stdin   io.WriteCloser
	stdout  *bufio.Scanner
	stdoutR io.ReadCloser
	stderr  *limitedBuffer
	done    chan struct{}
	doneErr error
	doneMu  sync.Mutex
	writeMu sync.Mutex
}

// newTransport 启动 Claude CLI 子进程并建立 stdin/stdout 流式通道。
// 环境变量会先剥离数据库连接信息，再补 loopback NO_PROXY，防止 MCP 本地握手被代理劫持。
func newTransport(binary string, args []string, cwd string, env []string) (*transport, error) {
	if binary == "" {
		binary = defaultClaudeCLIBin
	}
	binary = resolveClaudeBinary(binary)
	cmd := exec.Command(binary, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	baseEnv := contract.ScrubDatabaseEnv(os.Environ())
	if len(env) > 0 {
		baseEnv = append(baseEnv, contract.ScrubDatabaseEnv(env)...)
	}
	cmd.Env = ensureLoopbackNoProxy(baseEnv)
	setClaudeProcessAttrs(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr := newLimitedBuffer(stderrLimitBytes)
	cmd.Stderr = stderr
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), maxCLILineBytes)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	tr := &transport{
		cmd:     cmd,
		guard:   attachProcessGuard(cmd),
		stdin:   stdin,
		stdout:  scanner,
		stdoutR: stdout,
		stderr:  stderr,
		done:    make(chan struct{}),
	}
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				pkglogger.Get().Error("claudecli: transport wait panic", "recovered", rec)
				select {
				case <-tr.done:
				default:
					close(tr.done)
				}
			}
		}()
		tr.wait()
	}()
	return tr, nil
}

// ensureLoopbackNoProxy 确保本地 MCP endpoint 不会被 HTTP 代理接管。
// Claude CLI 的 Node 进程会继承 HTTP_PROXY；缺少 loopback NO_PROXY 时，本地握手可能被代理挂住并表现为空错误。
func ensureLoopbackNoProxy(env []string) []string {
	const loopbacks = "127.0.0.1,localhost,::1"
	var existing []string
	filtered := make([]string, 0, len(env)+2)
	for _, kv := range env {
		key, _, ok := strings.Cut(kv, "=")
		if ok && strings.EqualFold(key, "NO_PROXY") {
			existing = append(existing, strings.TrimPrefix(kv, key+"="))
			continue
		}
		filtered = append(filtered, kv)
	}
	merged := mergeCSV(append(existing, loopbacks)...)
	return append(filtered, "NO_PROXY="+merged, "no_proxy="+merged)
}

// mergeCSV 合并逗号分隔值并按大小写去重。
// NO_PROXY 同时来自父进程和本地补丁，保持原始大小写可减少用户排查环境差异的成本。
func mergeCSV(parts ...string) string {
	seen := make(map[string]struct{}, 8)
	out := make([]string, 0, 8)
	for _, group := range parts {
		for _, item := range strings.Split(group, ",") {
			trimmed := strings.TrimSpace(item)
			if trimmed == "" {
				continue
			}
			key := strings.ToLower(trimmed)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, trimmed)
		}
	}
	return strings.Join(out, ",")
}

// Send 向底层传输写入请求。
func (t *transport) Send(msg []byte) error {
	if t == nil {
		return errors.New("transport is nil")
	}
	payload := append([]byte(nil), msg...)
	if len(payload) == 0 || payload[len(payload)-1] != '\n' {
		payload = append(payload, '\n')
	}
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	if t.stdin == nil {
		return errors.New("transport stdin is not ready")
	}
	_, err := t.stdin.Write(payload)
	return err
}

// Receive 从底层传输读取事件。
func (t *transport) Receive() ([]byte, error) {
	if t == nil || t.stdout == nil {
		return nil, io.EOF
	}
	if t.stdout.Scan() {
		return append([]byte(nil), t.stdout.Bytes()...), nil
	}
	if err := t.stdout.Err(); err != nil {
		return nil, err
	}
	if err := t.waitErr(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}

// Close 按 stdin 关闭、SIGTERM、强杀兜底的顺序停止 Claude CLI。
// 返回值只反映信号发送错误，进程自然退出造成的 signal 状态会被 normalize。
func (t *transport) Close() error {
	if t == nil {
		return nil
	}
	t.closeInput()
	err := t.signalProcess(sigTerminate)
	t.waitForExit(shutdownGracePeriod)
	if t.Running() {
		_ = t.signalProcess(sigForceKill)
	}
	<-t.done
	return normalizeSignalError(err)
}

// Kill 终止底层进程或连接。
func (t *transport) Kill() error {
	if t == nil {
		return nil
	}
	t.closeInput()
	err := t.signalProcess(sigForceKill)
	<-t.done
	return normalizeSignalError(err)
}

// Running 判断 Claude CLI 进程是否仍可视为存活。
// done channel 优先于 PID 探测，避免已 Wait 完的进程被操作系统短暂复用 PID 时误判。
func (t *transport) Running() bool {
	if t == nil {
		return false
	}
	select {
	case <-t.done:
		return false
	default:
	}
	pid, err := t.ensureProcessAlive()
	return err == nil && pid > 0
}

// readyForSend 检查当前 transport 是否还可以安全写入 stdin。
// 重试路径会在持锁状态下调用它，必须同时确认 done、stdin 和子进程存活状态。
func (t *transport) readyForSend() bool {
	if t == nil {
		return false
	}
	select {
	case <-t.done:
		return false
	default:
	}
	t.writeMu.Lock()
	stdinReady := t.stdin != nil
	t.writeMu.Unlock()
	if !stdinReady {
		return false
	}
	if t.cmd == nil || t.cmd.Process == nil {
		return true
	}
	return t.Running()
}

func (t *transport) wait() {
	err := t.cmd.Wait()
	t.doneMu.Lock()
	t.doneErr = err
	t.doneMu.Unlock()
	t.guard.close()
	close(t.done)
}

func (t *transport) waitErr() error {
	select {
	case <-t.done:
	default:
		return nil
	}
	t.doneMu.Lock()
	defer t.doneMu.Unlock()
	return t.doneErr
}

func (t *transport) closeInput() {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	if t.stdin != nil {
		_ = t.stdin.Close()
		t.stdin = nil
	}
	if t.stdoutR != nil {
		_ = t.stdoutR.Close()
		t.stdoutR = nil
	}
}

func (t *transport) waitForExit(timeout time.Duration) {
	deadline := time.NewTimer(timeout)
	ticker := time.NewTicker(shutdownPollInterval)
	defer deadline.Stop()
	defer ticker.Stop()
	for t.Running() {
		select {
		case <-t.done:
			return
		case <-deadline.C:
			return
		case <-ticker.C:
		}
	}
}

func (t *transport) signalProcess(sig processSig) error {
	pid, err := t.ensureProcessAlive()
	if err != nil {
		return err
	}
	if pid == 0 {
		return nil
	}
	return signalClaudeProcess(t.cmd, t.guard, sig)
}

func normalizeSignalError(err error) error {
	if err == nil || isProcessGoneErr(err) {
		return nil
	}
	return err
}

type limitedBuffer struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	limit int
}

func newLimitedBuffer(limit int) *limitedBuffer {
	return &limitedBuffer{limit: limit}
}

// String 返回当前保留的 stderr 尾部内容。
// limitedBuffer 可能被 wait goroutine 和错误路径同时读取，必须在锁内复制字符串。
func (b *limitedBuffer) String() string {
	if b == nil {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Write 追加 stderr 输出并只保留尾部固定字节数。
// 返回值仍按底层 buffer 写入长度汇报，截断只影响后续错误摘要。
func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n, err := b.buf.Write(p)
	if b.buf.Len() > b.limit {
		raw := b.buf.Bytes()
		b.buf.Reset()
		b.buf.Write(raw[len(raw)-b.limit:])
	}
	return n, err
}
