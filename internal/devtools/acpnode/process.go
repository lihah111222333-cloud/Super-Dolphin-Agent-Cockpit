package acpnode

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

var ErrStartupTimeout = errors.New("acp: process startup timeout")
var ErrStartupCleanupTimeout = errors.New("acp: process startup cleanup timeout")

// Process is the narrow, injectable child-process boundary used by the
// development harness. It deliberately exposes no provider, auth, or storage
// capability.
type Process interface {
	Stdin() io.WriteCloser
	Stdout() io.ReadCloser
	Stderr() io.ReadCloser
	Wait() error
	Interrupt() error
	Kill() error
}

// ProcessFactory 创建严格受 LaunchConfig 约束的 ACP 子进程。
type ProcessFactory interface {
	Start(context.Context, LaunchConfig) (Process, error)
}

// DefaultProcessFactory 返回不经过 shell 的标准子进程工厂。
func DefaultProcessFactory() ProcessFactory { return osProcessFactory{} }

func boundedContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	timer := time.AfterFunc(timeout, cancel)
	return ctx, func() {
		timer.Stop()
		cancel()
	}
}

type processStartResult struct {
	process Process
	err     error
}

// processActionOwner retains ownership of a non-cooperative lifecycle call
// after its caller's bounded wait expires.
type processActionOwner struct {
	ownerState
	result chan error
}

func newProcessActionOwner() *processActionOwner {
	return &processActionOwner{ownerState: newOwnerState(), result: make(chan error, 1)}
}

func startProcessActionOwner(action func() error) *processActionOwner {
	owner := newProcessActionOwner()
	launchACP(context.Background(), "acp process lifecycle action", func() { runProcessActionOwner(owner, action) })
	return owner
}

func runProcessActionOwner(owner *processActionOwner, action func() error) {
	var err error
	if action == nil {
		err = fmt.Errorf("acp: nil process lifecycle action")
	} else {
		err = action()
	}
	owner.result <- err
	owner.finish(err)
}

type processActionPendingError struct {
	owner *processActionOwner
}

// Error 返回进程生命周期 owner 仍未完成的稳定错误分类。
func (e *processActionPendingError) Error() string {
	return "acp: process lifecycle action remains pending"
}

// Done 暴露进程生命周期 owner 的最终完成信号。
func (e *processActionPendingError) Done() <-chan struct{} {
	if e == nil || e.owner == nil {
		return closedChannel()
	}
	return e.owner.done
}

// Err 返回进程生命周期 owner 完成后的底层错误。
func (e *processActionPendingError) Err() error {
	if e == nil || e.owner == nil {
		return ErrOwnerPending
	}
	return e.owner.Err()
}

// Join 等待进程生命周期 owner 完成并返回底层错误。
func (e *processActionPendingError) Join() error {
	if e == nil || e.owner == nil {
		return ErrOwnerPending
	}
	return e.owner.Join()
}

// Unwrap 在 owner 完成后暴露其底层进程错误。
func (e *processActionPendingError) Unwrap() error {
	err := e.Err()
	if errors.Is(err, ErrOwnerPending) {
		return nil
	}
	return err
}

func (o *processActionOwner) pendingError() error {
	if o == nil {
		return nil
	}
	select {
	case <-o.Done():
		return nil
	default:
		return &processActionPendingError{owner: o}
	}
}

// lateStartOwner 持有超时后的工厂结果和清理生命周期，避免把迟到工作遗留为无主 goroutine。
type lateStartOwner struct {
	result <-chan processStartResult
	done   chan struct{}

	mu          sync.Mutex
	cleanupErr  error
	cleanupDone bool
}

// newLateStartOwner 创建可等待的迟到启动 owner。
func newLateStartOwner(result <-chan processStartResult) *lateStartOwner {
	return &lateStartOwner{result: result, done: make(chan struct{})}
}

// finish 消费迟到工厂结果并记录完整清理错误链。
func (o *lateStartOwner) finish(outcome processStartResult, timeout time.Duration) {
	var errs []error
	if outcome.err != nil {
		errs = append(errs, outcome.err)
	}
	if outcome.process == nil {
		if outcome.err == nil {
			errs = append(errs, fmt.Errorf("acp: late process factory returned nil process"))
		}
	} else if err := cleanupProcess(outcome.process, timeout); err != nil {
		errs = append(errs, err)
	}
	o.mu.Lock()
	o.cleanupErr = errors.Join(errs...)
	o.cleanupDone = true
	close(o.done)
	o.mu.Unlock()
}

// currentErr 返回迟到 owner 的实时清理状态。
func (o *lateStartOwner) currentErr() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.cleanupDone {
		return o.cleanupErr
	}
	return ErrStartupCleanupTimeout
}

// lateStartError keeps late cleanup errors observable after startProcess returns.
type lateStartError struct{ owner *lateStartOwner }

// Error 返回迟到启动 owner 的当前错误摘要。
func (e *lateStartError) Error() string {
	if e == nil || e.owner == nil {
		return "acp: late process startup cleanup unavailable"
	}
	err := e.owner.currentErr()
	if err == nil {
		return "acp: late process startup cleanup complete"
	}
	return "acp: late process startup cleanup: " + err.Error()
}

// Unwrap 暴露迟到启动 owner 的实时清理错误链。
func (e *lateStartError) Unwrap() error {
	if e == nil || e.owner == nil {
		return nil
	}
	return e.owner.currentErr()
}

// Is 让迟到启动错误参与标准 errors.Is 匹配。
func (e *lateStartError) Is(target error) bool {
	if e == nil || e.owner == nil {
		return false
	}
	return errors.Is(e.owner.currentErr(), target)
}

// Done 暴露迟到启动 owner 的有界完成信号。
func (e *lateStartError) Done() <-chan struct{} {
	if e == nil || e.owner == nil {
		return closedChannel()
	}
	return e.owner.done
}

// Err 返回迟到清理当前错误，未完成时返回 ErrStartupCleanupTimeout。
func (e *lateStartError) Err() error {
	if e == nil || e.owner == nil {
		return ErrStartupCleanupTimeout
	}
	return e.owner.currentErr()
}

func startProcessCall(ctx context.Context, factory ProcessFactory, cfg LaunchConfig, result chan<- processStartResult) {
	process, err := factory.Start(ctx, cfg)
	result <- processStartResult{process: process, err: err}
}

// startProcess 在启动超时后等待可取消工厂，并对部分进程执行有界清理。
func startProcess(cfg LaunchConfig, factory ProcessFactory) (Process, error) {
	if factory == nil {
		return nil, fmt.Errorf("acp: nil process factory")
	}
	result := make(chan processStartResult, 1)
	owner := newLateStartOwner(result)
	ctx, cancel := boundedContext(context.Background(), cfg.StartupTimeout)
	defer cancel()
	launchACP(ctx, "acp process start", func() { startProcessCall(ctx, factory, cfg, result) })
	select {
	case outcome := <-result:
		if ctx.Err() != nil {
			return nil, errors.Join(ErrStartupTimeout, cleanupStartupOutcome(outcome, cfg.ShutdownTimeout))
		}
		if outcome.err != nil {
			if outcome.process == nil {
				return nil, outcome.err
			}
			return nil, errors.Join(outcome.err, cleanupProcess(outcome.process, cfg.ShutdownTimeout))
		}
		if outcome.process == nil {
			return nil, fmt.Errorf("acp: process factory returned nil process")
		}
		return outcome.process, nil
	case <-ctx.Done():
		cleanupTimer := time.NewTimer(cfg.ShutdownTimeout)
		defer cleanupTimer.Stop()
		select {
		case outcome := <-result:
			return nil, errors.Join(ErrStartupTimeout, cleanupStartupOutcome(outcome, cfg.ShutdownTimeout))
		case <-cleanupTimer.C:
			late := &lateStartError{owner: owner}
			launchACP(context.Background(), "acp late process cleanup", func() { cleanupLateStartOwner(owner, cfg.ShutdownTimeout) })
			return nil, errors.Join(ErrStartupTimeout, ErrStartupCleanupTimeout, late)
		}
	}
}

// cleanupLateStartOwner 让迟到工厂和其返回进程共享同一个 owner 生命周期。
func cleanupLateStartOwner(owner *lateStartOwner, timeout time.Duration) {
	owner.finish(<-owner.result, timeout)
}

// cleanupStartupOutcome 清理启动窗口内返回的进程并保留工厂错误。
func cleanupStartupOutcome(outcome processStartResult, timeout time.Duration) error {
	if outcome.process == nil {
		if outcome.err != nil {
			return outcome.err
		}
		return fmt.Errorf("acp: process factory returned nil process")
	}
	return errors.Join(outcome.err, cleanupProcess(outcome.process, timeout))
}

// cleanupProcess 关闭输入、终止进程并合并所有可观察的清理错误。
func cleanupProcess(process Process, timeout time.Duration) error {
	if process == nil {
		return nil
	}
	var errs []error
	if stdin := process.Stdin(); stdin != nil {
		errs = appendProcessActionError(errs, timeout, stdin.Close)
	}
	errs = appendProcessActionError(errs, timeout, process.Kill)
	waitOwner := startProcessActionOwner(process.Wait)
	var done bool
	errs, done = appendWaitActionError(errs, waitOwner, timeout)
	if done {
		return errors.Join(errs...)
	}
	errs = appendProcessActionError(errs, timeout, process.Interrupt)
	errs, done = appendWaitActionError(errs, waitOwner, timeout)
	if done {
		return errors.Join(errs...)
	}
	return errors.Join(errs...)
}

func appendProcessActionError(errs []error, timeout time.Duration, action func() error) []error {
	if err := runProcessAction(timeout, action); err != nil {
		return append(errs, err)
	}
	return errs
}

func appendWaitActionError(errs []error, owner *processActionOwner, timeout time.Duration) ([]error, bool) {
	done, err := awaitProcessAction(owner, timeout)
	if err != nil {
		errs = append(errs, err)
	}
	return errs, done
}

func runProcessAction(timeout time.Duration, action func() error) error {
	returnProcess := startProcessActionOwner(action)
	_, err := awaitProcessAction(returnProcess, timeout)
	return err
}

func awaitProcessAction(owner *processActionOwner, timeout time.Duration) (bool, error) {
	if owner == nil {
		return true, fmt.Errorf("acp: nil process lifecycle owner")
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-owner.result:
		return true, err
	case <-timer.C:
		return false, errors.Join(ErrStartupCleanupTimeout, &processActionPendingError{owner: owner})
	}
}

type osProcessFactory struct{}

// Start 创建带显式环境和可取消上下文的直接子进程。
func (osProcessFactory) Start(ctx context.Context, c LaunchConfig) (Process, error) {
	if ctx == nil {
		return nil, fmt.Errorf("acp: nil process startup context")
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	// exec.Command passes argv directly to the kernel. No shell or string
	// interpolation is involved in this boundary. The startup context is
	// checked around creation; it must not own the child after Start returns.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cmd := exec.Command(c.Executable, c.Args...)
	cmd.Dir = c.CWD
	cmd.Env = append([]string(nil), c.Env...)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, closeFailedProcess(err, in)
	}
	errout, err := cmd.StderrPipe()
	if err != nil {
		return nil, closeFailedProcess(err, in, out)
	}
	if err := cmd.Start(); err != nil {
		return nil, closeFailedProcess(err, in, out, errout)
	}
	if err := ctx.Err(); err != nil {
		killErr := cmd.Process.Kill()
		waitErr := cmd.Wait()
		return nil, errors.Join(err, killErr, waitErr)
	}
	return &osProcess{cmd: cmd, in: in, out: out, errout: errout}, nil
}

func closeFailedProcess(primary error, closers ...io.Closer) error {
	errs := []error{primary}
	for _, closer := range closers {
		if err := closer.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

type osProcess struct {
	cmd          *exec.Cmd
	in           io.WriteCloser
	out, errout  io.ReadCloser
	waitOnce     sync.Once
	waitErr      error
	interrupt    sync.Once
	interruptErr error
	kill         sync.Once
	killErr      error
}

// Stdin 返回子进程标准输入流。
func (p *osProcess) Stdin() io.WriteCloser { return p.in }

// Stdout 返回子进程标准输出流。
func (p *osProcess) Stdout() io.ReadCloser { return p.out }

// Stderr 返回子进程标准错误流。
func (p *osProcess) Stderr() io.ReadCloser { return p.errout }

// Wait 等待子进程并缓存终态错误。
func (p *osProcess) Wait() error {
	p.waitOnce.Do(func() { p.waitErr = p.cmd.Wait() })
	return p.waitErr
}

// Interrupt 向子进程发送可恢复的中断信号。
func (p *osProcess) Interrupt() error {
	p.interrupt.Do(func() { p.interruptErr = p.cmd.Process.Signal(os.Interrupt) })
	return p.interruptErr
}

// Kill 强制结束子进程并缓存调用结果。
func (p *osProcess) Kill() error {
	p.kill.Do(func() { p.killErr = p.cmd.Process.Kill() })
	return p.killErr
}
