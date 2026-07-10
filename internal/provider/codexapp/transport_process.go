package codexapp

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/util/safego"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type localProcess struct {
	cmd         *exec.Cmd
	guard       *processGuard
	stderr      *limitedBuffer
	stderrR     io.ReadCloser
	done        chan struct{}
	stderrDone  chan struct{}
	listenReady chan struct{}
	waitErr     error
	waitMu      sync.Mutex
	listenURL   string
	listenErr   error
	listenSet   bool
	listenMu    sync.Mutex
	listenOnce  sync.Once
	exited      atomic.Bool
}

type limitedBuffer struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	limit int
}

func newLimitedBuffer(limit int) *limitedBuffer { return &limitedBuffer{limit: limit} }

func nonNilContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// Write 追加 stderr 数据并只保留末尾 limit 字节。
// 该 buffer 会被多个 goroutine 读取或写入，必须在锁内完成裁剪以保证诊断尾部一致。
func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n, err := b.buf.Write(p)
	if b.buf.Len() <= b.limit {
		return n, err
	}
	raw := append([]byte(nil), b.buf.Bytes()...)
	b.buf.Reset()
	b.buf.Write(raw[len(raw)-b.limit:])
	return n, err
}

// String 返回当前 stderr 尾部快照。
// 调用方只用于诊断输出，因此这里复制 bytes.Buffer 的字符串视图而不暴露内部缓冲区。
func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func newLocalProcess(cmd *exec.Cmd, stderrR io.ReadCloser) *localProcess {
	return &localProcess{
		cmd:         cmd,
		stderr:      newLimitedBuffer(transportStderrLimitBytes),
		stderrR:     stderrR,
		done:        make(chan struct{}),
		stderrDone:  make(chan struct{}),
		listenReady: make(chan struct{}),
	}
}

func (p *localProcess) running() bool {
	return p != nil && p.cmd != nil && p.cmd.Process != nil && !p.exited.Load()
}

func (p *localProcess) pid() int {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

func (p *localProcess) setWaitErr(err error) {
	p.waitMu.Lock()
	defer p.waitMu.Unlock()
	p.waitErr = err
}

func (p *localProcess) waitErrValue() error {
	p.waitMu.Lock()
	defer p.waitMu.Unlock()
	return p.waitErr
}

func (p *localProcess) waitAsync() {
	safego.Go(context.Background(), nil, "codexapp.localProcess.wait", func(context.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				pkglogger.Error("codexapp: recovered waitAsync panic",
					"pid", p.pid(), "panic", rec)
			}
			p.exited.Store(true)
			close(p.done)
		}()
		p.setWaitErr(p.cmd.Wait())
	})
}

func (p *localProcess) waitForExit(timeout time.Duration) bool {
	select {
	case <-p.done:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (p *localProcess) waitForStderr(timeout time.Duration) {
	select {
	case <-p.stderrDone:
	case <-time.After(timeout):
	}
}

func (p *localProcess) stderrSummary() string {
	if p == nil || p.stderr == nil {
		return ""
	}
	return strings.TrimSpace(p.stderr.String())
}

func (p *localProcess) setListenResult(url string, err error) {
	p.listenOnce.Do(func() {
		p.listenMu.Lock()
		p.listenURL = url
		p.listenErr = err
		p.listenSet = true
		p.listenMu.Unlock()
		close(p.listenReady)
	})
}

func (p *localProcess) listenResult() (string, error, bool) {
	p.listenMu.Lock()
	defer p.listenMu.Unlock()
	return p.listenURL, p.listenErr, p.listenSet
}

// waitForListenURL 等待 app-server 在 stderr 中报告监听地址。
// 进程提前退出时返回进程错误；上下文取消时立即停止等待，避免启动路径卡死。
func (p *localProcess) waitForListenURL(ctx context.Context) (string, error) {
	ctx = nonNilContext(ctx)
	if url, err, ok := p.listenResult(); ok {
		return url, err
	}
	select {
	case <-p.listenReady:
		url, err, _ := p.listenResult()
		if url == "" && err == nil {
			return "", errors.New("codexapp: app-server did not report listen url")
		}
		return url, err
	case <-p.done:
		if url, err, ok := p.listenResult(); ok {
			return url, err
		}
		return "", p.exitError()
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (p *localProcess) exitError() error {
	if p == nil {
		return errors.New("codexapp: local process missing")
	}
	err := p.waitErrValue()
	if err == nil {
		err = errors.New("codexapp: local process exited unexpectedly")
	}
	if stderr := p.stderrSummary(); stderr != "" {
		return fmt.Errorf("%w: %s", err, stderr)
	}
	return err
}

func (p *localProcess) signal(sig processSig) error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	err := signalCodexProcess(p.cmd, p.guard, sig)
	if isProcessGoneErr(err) {
		return nil
	}
	return err
}

func parseListenURLLine(line string) string {
	line = strings.TrimSpace(line)
	const prefix = "listening on:"
	if !strings.HasPrefix(strings.ToLower(line), prefix) {
		return ""
	}
	return normalizeServerURL(strings.TrimSpace(line[len(prefix):]))
}

func enrichSpawnError(err error, proc *localProcess) error {
	if err == nil {
		return nil
	}
	stderr := proc.stderrSummary()
	errMsg := err.Error()
	// archguard:ignore priority_ssa_error_string -- this only avoids duplicating stderr already present in the display error.
	if stderr == "" || strings.Contains(errMsg, stderr) {
		return err
	}
	return fmt.Errorf("%w: %s", err, stderr)
}

// spawnLocal 启动本地 Codex app-server 并等待其监听地址可用。
// 启动失败会强制杀进程、等待 stderr 收尾并返回带诊断尾部的错误。
func (t *transport) spawnLocal(ctx context.Context) error {
	if t.processRunning() {
		return nil
	}
	ctx = nonNilContext(ctx)
	if err := ensureCodexCLIAvailable(ctx); err != nil {
		return err
	}
	argv := localSpawnAppServerArgs()
	pkglogger.Info("codexapp: spawning local app-server", "argv", argv)
	// 按平台包一层 fd 上限提升器：macOS 图形启动会继承 launchd 的 256 软限制，
	// 批量 agent 场景容易耗尽；Unix 包装器会在 exec 前提升，Windows 保持原样。
	cmd := wrapWithFDLimit(argv)
	cmd.Env = contract.ScrubDatabaseEnv(os.Environ())
	cmd.Stdout = io.Discard
	setCodexProcessAttrs(cmd)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	proc := newLocalProcess(cmd, stderr)
	proc.guard = attachProcessGuard(cmd)
	proc.waitAsync()
	t.startCollectProcessStderr(proc, stderr)
	serverURL, err := proc.waitForListenURL(ctx)
	if err != nil {
		_ = proc.signal(sigForceKill)
		proc.waitForExit(transportKillWaitTimeout)
		proc.waitForStderr(time.Second)
		proc.guard.close()
		return enrichSpawnError(err, proc)
	}
	t.stateMu.Lock()
	t.local = true
	t.serverURL = serverURL
	t.process = proc
	t.processErr = nil
	t.stateMu.Unlock()
	t.startWatchLocalProcess(proc)
	return nil
}

func (t *transport) startCollectProcessStderr(proc *localProcess, stderr io.ReadCloser) {
	safego.Go(context.Background(), nil, "codexapp.transport.collectProcessStderr", func(context.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				pkglogger.Error("codexapp: recovered collectProcessStderr panic", "panic", rec)
				select {
				case <-proc.stderrDone:
				default:
					close(proc.stderrDone)
				}
			}
		}()
		t.collectProcessStderr(proc, stderr)
	})
}

func (t *transport) startWatchLocalProcess(proc *localProcess) {
	safego.Go(context.Background(), nil, "codexapp.transport.watchLocalProcess", func(context.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				pkglogger.Error("codexapp: recovered watchLocalProcess panic", "panic", rec)
			}
		}()
		t.watchLocalProcess(proc)
	})
}

// collectProcessStderr 收集 app-server stderr，同时解析监听地址作为启动就绪信号。
// listenReady 只会设置一次；未发现地址或 scanner 出错时必须写入失败结果唤醒等待方。
func (t *transport) collectProcessStderr(proc *localProcess, stderr io.ReadCloser) {
	defer close(proc.stderrDone)
	if stderr == nil {
		proc.setListenResult("", errors.New("codexapp: local process stderr unavailable"))
		return
	}
	defer func() { _ = stderr.Close() }()
	scanner := bufio.NewScanner(stderr)
	scanner.Buffer(make([]byte, 0, 1024), 64*1024)
	foundListenURL := false
	for scanner.Scan() {
		line := scanner.Text()
		_, _ = proc.stderr.Write(append([]byte(line), '\n'))
		if foundListenURL {
			continue
		}
		if listenURL := parseListenURLLine(line); listenURL != "" {
			foundListenURL = true
			proc.setListenResult(listenURL, nil)
		}
	}
	if err := scanner.Err(); err != nil {
		if !foundListenURL {
			proc.setListenResult("", fmt.Errorf("codexapp: read app-server stderr: %w", err))
		}
		return
	}
	if !foundListenURL {
		proc.setListenResult("", errors.New("codexapp: app-server did not report listen url"))
	}
}

func (t *transport) watchLocalProcess(proc *localProcess) {
	<-proc.done
	proc.waitForStderr(time.Second)
	defer proc.guard.close()
	if t.closed.Load() || t.closing.Load() {
		t.clearProcess(proc, nil)
		return
	}
	exitErr := proc.exitError()
	t.clearProcess(proc, exitErr)
	_ = proc.signal(sigForceKill)
	t.closeSocket()
	t.failPending(exitErr)
}

func (t *transport) currentProcess() *localProcess {
	t.stateMu.RLock()
	defer t.stateMu.RUnlock()
	return t.process
}

func (t *transport) clearProcess(proc *localProcess, err error) {
	t.stateMu.Lock()
	defer t.stateMu.Unlock()
	if t.process == proc {
		t.process = nil
	}
	if err != nil {
		t.processErr = err
	}
}

func (t *transport) processFailure() error {
	t.stateMu.RLock()
	defer t.stateMu.RUnlock()
	return t.processErr
}

func (t *transport) localProcessReady() error {
	if !t.local {
		return nil
	}
	if t.processRunning() {
		return nil
	}
	if err := t.processFailure(); err != nil {
		return err
	}
	return errors.New("codexapp: local process not running")
}

// stopProcess 停止本地 app-server，并在优雅关闭超时后升级为强杀。
// 关闭前会快照子进程列表，退出后统一清理后代，避免 CLI 派生进程残留。
func (t *transport) stopProcess(graceful bool) error {
	t.stateMu.Lock()
	proc := t.process
	t.process = nil
	t.stateMu.Unlock()
	if proc == nil {
		return nil
	}
	descendants := snapshotProcessDescendants(proc.pid())
	if graceful {
		if err := proc.signal(sigTerminate); err != nil {
			killProcessDescendants(proc.pid(), descendants)
			return err
		}
		if proc.waitForExit(transportShutdownGracePeriod) {
			killProcessDescendants(proc.pid(), descendants)
			proc.waitForStderr(time.Second)
			return nil
		}
	}
	if err := proc.signal(sigForceKill); err != nil {
		killProcessDescendants(proc.pid(), descendants)
		return err
	}
	if proc.stderrR != nil {
		_ = proc.stderrR.Close()
	}
	if proc.waitForExit(transportKillWaitTimeout) {
		killProcessDescendants(proc.pid(), descendants)
		proc.waitForStderr(time.Second)
		return nil
	}
	killProcessDescendants(proc.pid(), descendants)
	return fmt.Errorf("codexapp: timed out waiting for local process exit: %w", proc.exitError())
}

func (t *transport) processRunning() bool {
	proc := t.currentProcess()
	return proc != nil && proc.running()
}

// localPID 返回本地 app-server 进程 PID；进程不存在或尚未启动时返回 0。
func (t *transport) localPID() int {
	proc := t.currentProcess()
	if proc == nil || proc.cmd == nil || proc.cmd.Process == nil {
		return 0
	}
	return proc.cmd.Process.Pid
}
