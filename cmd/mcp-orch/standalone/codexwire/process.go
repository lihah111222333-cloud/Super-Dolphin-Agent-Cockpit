package codexwire

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"reflect"
	"sync"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/processctl"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

// ErrContextRequired 表示启动 Codex wire 时未提供可检查的 context。
var ErrContextRequired = errors.New("Codex process context is required")

// Process 拥有一个官方 CLI app-server 进程和其全部管道/等待生命周期。
type Process struct {
	Client           *jrpc2.Client
	cmd              *exec.Cmd
	guard            *processctl.Guard
	done             chan struct{}
	workers          sync.WaitGroup
	closeOnce        sync.Once
	closeErr         error
	exitErr          error
	exitMu           sync.Mutex
	exitBind         contract.CLIProcessExitBinding
	exitOnce         sync.Once
	closeReq         bool
	archivedThreadID string
}

// Start 启动已明确配置的 executable，不经 shell 或 V3 宿主。
func Start(ctx context.Context, executable, cwd string, env []string, onNotify func(*jrpc2.Request)) (*Process, error) {
	return StartWithExit(ctx, executable, cwd, env, onNotify, contract.CLIProcessExitBinding{})
}

// StartWithExit 启动 Codex CLI，并在 Wait 完成时最多发出一次退出通知。
// binding 必须携带完整生命周期身份；不完整 binding 采用 fail-closed（不发事件）。
func StartWithExit(ctx context.Context, executable, cwd string, env []string, onNotify func(*jrpc2.Request), binding contract.CLIProcessExitBinding) (*Process, error) {
	if err := requireContext(ctx); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cmd := exec.Command(executable, "app-server", "--listen", "stdio://")
	cmd.Dir, cmd.Env, cmd.Stderr = cwd, env, io.Discard
	processctl.Configure(cmd)
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		return nil, errors.Join(err, input.Close())
	}
	if err := cmd.Start(); err != nil {
		return nil, errors.Join(err, input.Close(), output.Close())
	}
	p := &Process{cmd: cmd, guard: processctl.Attach(cmd, nil), done: make(chan struct{}), exitBind: binding}
	p.workers.Go(func() {
		exitErr := cmd.Wait()
		p.exitMu.Lock()
		p.exitErr = exitErr
		p.exitMu.Unlock()
		close(p.done)
		p.emitExitAsync()
	})
	p.Client = newClient(output, input, onNotify)
	if err := initialize(ctx, p.Client); err != nil {
		return nil, errors.Join(err, p.Close())
	}
	return p, nil
}

// newClient 将有序通知绑定到线协议 reader；jrpc2 继续负责请求响应关联。
func newClient(output io.ReadCloser, input io.WriteCloser, onNotify func(*jrpc2.Request)) *jrpc2.Client {
	channel := NewChannel(output, input)
	channel.onNotify = onNotify
	return jrpc2.NewClient(channel, nil)
}

func requireContext(ctx context.Context) error {
	if ctx == nil {
		return ErrContextRequired
	}
	value := reflect.ValueOf(ctx)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if value.IsNil() {
			return ErrContextRequired
		}
	}
	return nil
}

// BindExitFence 在 thread/start 返回真实 session 后补齐 Codex 退出事件身份。
// 进程已退出或 binding 不完整时拒绝修改，避免晚到回调伪造新生命周期。
func (p *Process) BindExitFence(agentID, sessionID string, launchSeq uint64) error {
	if p == nil {
		return contract.ErrMissingCLIProcessExitIdentity
	}
	p.exitMu.Lock()
	defer p.exitMu.Unlock()
	select {
	case <-p.done:
		return errors.New("Codex process has already exited")
	default:
	}
	if agentID == "" || sessionID == "" || launchSeq == 0 {
		return contract.ErrMissingCLIProcessExitIdentity
	}
	p.exitBind.AgentID, p.exitBind.SessionID, p.exitBind.LaunchSeq = agentID, sessionID, launchSeq
	return nil
}

func (p *Process) emitExit() {
	event, sink, ok := p.takeExitEvent()
	if !ok {
		return
	}
	defer func() { _ = recover() }()
	sink(event)
}

// emitExitAsync prevents the cmd.Wait worker from synchronously re-entering
// lifecycle shutdown. A callback may call Process.Close, which waits for the
// Wait worker; running it on that worker would deadlock.
func (p *Process) emitExitAsync() {
	event, sink, ok := p.takeExitEvent()
	if !ok {
		return
	}
	safego.Go(context.Background(), nil, "standalone.codexwire.exit-callback", func(context.Context) {
		defer func() { _ = recover() }()
		sink(event)
	})
}

func (p *Process) takeExitEvent() (contract.CLIProcessExit, func(contract.CLIProcessExit), bool) {
	var event contract.CLIProcessExit
	var sink func(contract.CLIProcessExit)
	valid := false
	p.exitOnce.Do(func() {
		p.exitMu.Lock()
		binding := p.exitBind
		unexpected := !p.closeReq
		err := p.exitErr
		p.exitMu.Unlock()
		if !binding.Valid() {
			return
		}
		event = contract.CLIProcessExit{AgentID: binding.AgentID, SessionID: binding.SessionID,
			LaunchSeq: binding.LaunchSeq, Err: err, Unexpected: unexpected}
		sink = binding.OnExit
		valid = true
	})
	if !valid || sink == nil {
		return contract.CLIProcessExit{}, nil, false
	}
	return event, sink, true
}

// initialize 串行完成官方握手，后续调用不会抢在 initialized 之前发送。
func initialize(ctx context.Context, client *jrpc2.Client) error {
	if err := requireContext(ctx); err != nil {
		return err
	}
	var result struct {
		UserAgent string `json:"userAgent"`
	}
	err := client.CallResult(ctx, "initialize", map[string]any{
		"clientInfo":   map[string]string{"name": "mcp_orch", "version": "standalone-v1"},
		"capabilities": map[string]bool{"experimentalApi": true},
	}, &result)
	if err != nil {
		return fmt.Errorf("initialize Codex CLI: %w", err)
	}
	return client.Notify(ctx, "initialized", map[string]any{})
}

// Running 只以实际进程等待句柄判定，不以旧 session ID 充当存活证据。
func (p *Process) Running() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

// ExitError 在进程已退出后读取真实退出原因，避免吞掉 provider 崩溃。
func (p *Process) ExitError() error {
	select {
	case <-p.done:
		p.exitMu.Lock()
		defer p.exitMu.Unlock()
		return p.exitErr
	default:
		return nil
	}
}

// Close 先清理原生thread工具资源，再关闭协议流及受管进程；所有清理错误返回调用者。
func (p *Process) Close() error {
	p.closeOnce.Do(func() {
		p.exitMu.Lock()
		p.closeReq = true
		threadID := p.exitBind.SessionID
		archived := p.archivedThreadID == threadID
		p.exitMu.Unlock()
		if threadID != "" && !archived && p.Running() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			p.closeErr = p.CleanBackgroundTerminals(ctx, threadID)
			cancel()
		}
		p.closeErr = errors.Join(p.closeErr, p.Client.Close())
		if p.Running() {
			p.closeErr = errors.Join(p.closeErr, processctl.RequestStop(p.cmd, p.guard))
		}
		select {
		case <-p.done:
		case <-time.After(3 * time.Second):
			p.closeErr = errors.Join(p.closeErr, processctl.ForceStop(p.cmd, p.guard))
			<-p.done
		}
		p.workers.Wait()
		p.guard.Close()
	})
	return p.closeErr
}

// PID 返回当前 CLI 子进程 PID；进程未启动或已不可用时 fail-fast。
func (p *Process) PID() (int, error) {
	if p == nil || p.cmd == nil || p.cmd.Process == nil || p.cmd.Process.Pid <= 1 {
		return 0, errors.New("codex process PID unavailable")
	}
	return p.cmd.Process.Pid, nil
}
