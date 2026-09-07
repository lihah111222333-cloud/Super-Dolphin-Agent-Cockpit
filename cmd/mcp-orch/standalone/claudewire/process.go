package claudewire

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"reflect"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/processctl"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

// Process 拥有 Claude stream-json 进程，回调由有界行读取循环顺序交付。
type Process struct {
	cmd       *exec.Cmd
	guard     *processctl.Guard
	input     io.WriteCloser
	output    io.ReadCloser
	writeMu   sync.Mutex
	mu        sync.Mutex
	failure   error
	done      chan struct{}
	waitDone  chan struct{}
	workers   sync.WaitGroup
	closeOnce sync.Once
	closeErr  error
	closed    bool
	// eventCallbackActive marks the synchronous reader callback. Close must not
	// wait for the reader while this is set: the callback may be the caller of
	// Close itself.
	eventCallbackActive bool
	resourceOnce        sync.Once
	resourceErr         error
	exitMu              sync.Mutex
	exitBind            contract.CLIProcessExitBinding
	exitOnce            sync.Once
}

var (
	errContextRequired       = errors.New("Claude process context is required")
	errLaunchContextRequired = errors.New("Claude process launch context is required")
)

// Start 启动显式 CLI 与固定 session，保留 stdin 以接收多轮用户消息。
func Start(executable, cwd, model, sessionID, instructions string, env []string, onEvent func(Event)) (*Process, error) {
	return StartWithExit(executable, cwd, model, sessionID, instructions, env, onEvent, contract.CLIProcessExitBinding{SessionID: sessionID})
}

// StartWithExit 启动 Claude CLI，并在读取/Wait 完成时最多发出一次退出通知。
func StartWithExit(executable, cwd, model, sessionID, instructions string, env []string, onEvent func(Event), binding contract.CLIProcessExitBinding) (*Process, error) {
	return StartWithExitContext(context.Background(), executable, cwd, model, sessionID, instructions, env, onEvent, binding)
}

// StartWithExitContext 启动 Claude CLI，并在启动阶段响应 ctx 取消。
// 保留 StartWithExit 作为不带上下文的兼容入口，受管启动应使用此函数。
func StartWithExitContext(ctx context.Context, executable, cwd, model, sessionID, instructions string, env []string, onEvent func(Event), binding contract.CLIProcessExitBinding) (*Process, error) {
	if err := validateLaunch(ctx, executable, cwd, model, sessionID, onEvent); err != nil {
		return nil, err
	}
	args := claudeLaunchArgs(model, sessionID, instructions)
	// The launch context is scoped to startup only. Execution.Launch cancels it
	// after publishing a healthy session, so it must not own the long-lived CLI.
	cmd := exec.Command(executable, args...)
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
	binding.SessionID = sessionID
	p := &Process{cmd: cmd, guard: processctl.Attach(cmd, nil), input: input, output: output, done: make(chan struct{}), exitBind: binding}
	p.waitDone = make(chan struct{})
	p.workers.Go(func() { p.waitProcess() })
	p.workers.Go(func() { p.readEvents(onEvent) })
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(err, p.Close())
	}
	return p, nil
}

// validateLaunch 校验 Claude 启动上下文、命令参数与事件回调，不做默认降级。
func validateLaunch(ctx context.Context, executable, cwd, model, sessionID string, onEvent func(Event)) error {
	if contextIsNil(ctx) {
		return errLaunchContextRequired
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if executable == "" || cwd == "" || model == "" || sessionID == "" || onEvent == nil {
		return errors.New("Claude process requires executable, cwd, model, session and event sink")
	}
	return nil
}

func claudeLaunchArgs(model, sessionID, instructions string) []string {
	args := []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json",
		"--verbose", "--replay-user-messages", "--model", model, "--session-id", sessionID, "--permission-mode", "dontAsk"}
	if instructions != "" {
		args = append(args, "--append-system-prompt", instructions)
	}
	return args
}

// waitProcess 独立回收 exec.Cmd，避免 stdout EOF 时读循环永久卡在 Wait。
func (p *Process) waitProcess() {
	p.recordFailure(p.cmd.Wait())
	close(p.waitDone)
}

// BindExitFence 补齐 Claude 进程退出通知的 agent 和 launch fence。
func (p *Process) BindExitFence(agentID, sessionID string, launchSeq uint64) error {
	if p == nil {
		return contract.ErrMissingCLIProcessExitIdentity
	}
	p.exitMu.Lock()
	defer p.exitMu.Unlock()
	select {
	case <-p.done:
		return errors.New("Claude process has already exited")
	default:
	}
	if agentID == "" || sessionID == "" || launchSeq == 0 {
		return contract.ErrMissingCLIProcessExitIdentity
	}
	p.exitBind.AgentID, p.exitBind.SessionID, p.exitBind.LaunchSeq = agentID, sessionID, launchSeq
	return nil
}

// readEvents 限制单帧大小；stdout EOF 但进程仍存活时主动回收进程。
func (p *Process) readEvents(onEvent func(Event)) {
	scanner := bufio.NewScanner(p.output)
	scanner.Buffer(make([]byte, 4096), 4*1024*1024)
	protocolFailure := false
	for scanner.Scan() {
		event, err := DecodeEvent(scanner.Bytes())
		if err != nil {
			p.recordFailure(err)
			protocolFailure = true
			break
		}
		p.mu.Lock()
		p.eventCallbackActive = true
		p.mu.Unlock()
		func() {
			defer func() {
				p.mu.Lock()
				p.eventCallbackActive = false
				p.mu.Unlock()
			}()
			onEvent(event)
		}()
	}
	if err := scanner.Err(); err != nil {
		p.recordFailure(err)
		p.stopAndWait()
	} else if protocolFailure {
		p.stopAndWait()
	} else {
		p.reapAfterOutputEOF()
	}
	close(p.done)
	p.emitExitAsync()
	// A reentrant Close returns before the reader exits. The reader owns the
	// final resource release in that case; resourceOnce keeps this idempotent
	// with the normal Close path.
	if err := p.finishResources(); err != nil {
		p.recordFailure(err)
	}
}

// reapAfterOutputEOF 给自然退出一个短暂窗口；若进程仍存活则终止并等待回收。
// 正常多轮协议不会关闭 stdout，因此不会进入该路径。
func (p *Process) reapAfterOutputEOF() {
	const eofExitGrace = 100 * time.Millisecond
	select {
	case <-p.waitDone:
		return
	case <-time.After(eofExitGrace):
	}
	p.recordFailure(errors.New("Claude stream-json stdout closed before process exit"))
	p.stopAndWait()
}

// stopAndWait 请求进程退出并在固定窗口后强制回收，确保 reader 不留下孤儿 Wait。
func (p *Process) stopAndWait() {
	p.recordFailure(processctl.RequestStop(p.cmd, p.guard))
	select {
	case <-p.waitDone:
		return
	case <-time.After(3 * time.Second):
	}
	p.recordFailure(processctl.ForceStop(p.cmd, p.guard))
	<-p.waitDone
}

func (p *Process) emitExit() {
	event, sink, ok := p.takeExitEvent()
	if !ok {
		return
	}
	defer func() { _ = recover() }()
	sink(event)
}

// emitExitAsync keeps process lifecycle callbacks off the reader/Wait worker.
// A callback may synchronously close the process, which waits for that worker.
func (p *Process) emitExitAsync() {
	event, sink, ok := p.takeExitEvent()
	if !ok {
		return
	}
	safego.Go(context.Background(), nil, "standalone.claudewire.exit-callback", func(context.Context) {
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
		p.exitMu.Unlock()
		if !binding.Valid() {
			return
		}
		p.mu.Lock()
		unexpected := !p.closed
		err := p.failure
		p.mu.Unlock()
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

// Send 把完整用户消息写成一行，失败不会自动重发。
func (p *Process) Send(ctx context.Context, sessionID, requestID, text string) error {
	if err := requireContext(ctx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := EncodeUser(sessionID, requestID, text)
	if err != nil {
		return err
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		return errors.New("Claude process is closed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	raw = append(raw, '\n')
	n, err := p.input.Write(raw)
	if err == nil && n != len(raw) {
		return io.ErrShortWrite
	}
	return err
}

func requireContext(ctx context.Context) error {
	if contextIsNil(ctx) {
		return errContextRequired
	}
	return nil
}

func contextIsNil(ctx context.Context) bool {
	if ctx == nil {
		return true
	}
	value := reflect.ValueOf(ctx)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if value.IsNil() {
			return true
		}
	}
	return false
}

// Running 依据实际进程读取/等待循环，不依据 session 元数据。
func (p *Process) Running() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

// Done 返回 reader 与底层进程均退出后关闭的通知通道。
func (p *Process) Done() <-chan struct{} {
	if p == nil {
		return nil
	}
	return p.done
}

// Failure 返回已记录协议或进程错误，不暴露 stderr 中的敏感正文。
func (p *Process) Failure() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.failure
}

func (p *Process) recordFailure(err error) {
	if err == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failure = errors.Join(p.failure, err)
}

// Close 关闭输入并终止本实例的进程组，等待读取循环结束后释放全部管道。
// 若从事件回调重入，则立即请求强制停止并由读取循环完成收尾，避免等待自身。
func (p *Process) Close() error {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closed = true
		p.mu.Unlock()
		p.closeErr = closeProcessPipe(p.input)
		if p.Running() {
			p.closeErr = errors.Join(p.closeErr, processctl.RequestStop(p.cmd, p.guard))
		}
		p.mu.Lock()
		eventCallbackActive := p.eventCallbackActive
		p.mu.Unlock()
		if eventCallbackActive {
			// readEvents is synchronously executing the callback and will finish
			// the stop/reap path after the callback returns. Waiting here would
			// wait for this goroutine to return from Close, causing self-deadlock.
			// Force-stop now so the reader cannot remain blocked on an uncooperative
			// child after the callback returns; waitProcess still reaps it.
			p.closeErr = errors.Join(p.closeErr, processctl.ForceStop(p.cmd, p.guard))
			return
		}
		select {
		case <-p.done:
		case <-time.After(3 * time.Second):
			p.closeErr = errors.Join(p.closeErr, processctl.ForceStop(p.cmd, p.guard))
			<-p.done
		}
		p.workers.Wait()
		p.closeErr = errors.Join(p.closeErr, p.finishResources())
	})
	return p.closeErr
}

// finishResources closes process-owned output and control resources exactly
// once. readEvents calls it after Done closes so a reentrant Close can return
// without waiting for its own reader worker.
func (p *Process) finishResources() error {
	p.resourceOnce.Do(func() {
		if p.waitDone != nil {
			<-p.waitDone
		}
		if p.output != nil {
			p.resourceErr = closeProcessPipe(p.output)
		}
		if p.guard != nil {
			p.guard.Close()
		}
	})
	return p.resourceErr
}

// closeProcessPipe 接受 exec.Cmd.Wait 已关闭的管道，只消除明确的重复关闭，不吞其他错误。
func closeProcessPipe(pipe io.Closer) error {
	err := pipe.Close()
	if errors.Is(err, os.ErrClosed) {
		return nil
	}
	return err
}

// PID 返回当前 CLI 子进程 PID；进程未启动或已不可用时 fail-fast。
func (p *Process) PID() (int, error) {
	if p == nil || p.cmd == nil || p.cmd.Process == nil || p.cmd.Process.Pid <= 1 {
		return 0, errors.New("claude process PID unavailable")
	}
	return p.cmd.Process.Pid, nil
}
