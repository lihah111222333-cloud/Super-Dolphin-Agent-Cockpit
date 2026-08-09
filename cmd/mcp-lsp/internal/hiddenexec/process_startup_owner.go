package hiddenexec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

// startupProcessTree is a constrained owner retained when platform tree
// binding fails after Start. It holds the startup command and the single
// cmd.Wait result channel, so callers can retry root cleanup without losing
// the unreaped startup process. Unix callers also retain an immutable startup
// identity; the os.Process value itself is not a PID-reuse-safe handle.
// The identity is copied into the retained owner before any retry.
type startupProcessTree struct {
	startupProcessTreeState
}

// startupProcessTreeState keeps the wait/reap state separate from the
// controller's remaining operations so each file stays within the method guard.
type startupProcessTreeState struct {
	mu               sync.Mutex
	cmd              *exec.Cmd
	waitDone         chan error
	waitStarted      bool
	waitComplete     bool
	waitErr          error
	releaseHook      func() error
	terminateHook    func() error
	released         bool
	startupIdentity  ProcessIdentity
	identityKnown    bool
	identityRequired bool
	captureIdentity  func(int) (ProcessIdentity, error)
}

func newStartupProcessTreeWithRelease(cmd *exec.Cmd, waitDone chan error, releaseHook func() error) *ProcessTree {
	return newStartupProcessTreeWithIdentity(cmd, waitDone, ProcessIdentity{}, false, nil, false, releaseHook)
}

func newStartupProcessTreeWithIdentity(cmd *exec.Cmd, waitDone chan error, identity ProcessIdentity, identityKnown bool, captureIdentity func(int) (ProcessIdentity, error), identityRequired bool, releaseHook func() error) *ProcessTree {
	return &ProcessTree{controller: &startupProcessTree{startupProcessTreeState: startupProcessTreeState{
		cmd:              cmd,
		waitDone:         waitDone,
		waitStarted:      waitDone != nil,
		releaseHook:      releaseHook,
		startupIdentity:  identity,
		identityKnown:    identityKnown,
		identityRequired: identityRequired,
		captureIdentity:  captureIdentity,
	}}}
}

func (p *startupProcessTreeState) startWaitLocked() chan error {
	if p.waitStarted {
		return p.waitDone
	}
	p.waitDone = make(chan error, 1)
	p.waitStarted = true
	cmd := p.cmd
	safego.Go(context.Background(), nil, "mcp-lsp.hiddenexec.startup-owner-wait", func(context.Context) {
		p.waitDone <- cmd.Wait()
	})
	return p.waitDone
}

func (p *startupProcessTreeState) recordWaitResult(err error) error {
	p.mu.Lock()
	p.waitComplete = true
	p.waitErr = err
	p.mu.Unlock()
	var exitErr *exec.ExitError
	if err == nil || errors.As(err, &exitErr) || errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

func (p *startupProcessTreeState) waitResult(ctx context.Context) error {
	p.mu.Lock()
	if p.waitComplete {
		err := p.waitErr
		p.mu.Unlock()
		return p.recordWaitResult(err)
	}
	done := p.startWaitLocked()
	p.mu.Unlock()
	select {
	case err := <-done:
		return p.recordWaitResult(err)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// terminateExact 在平台授权后先验证不可变启动身份，再执行根进程动作并回收。
// Darwin 没有稳定的进程句柄时由平台实现 fail-closed，绝不退化为 PID kill。
func (p *startupProcessTreeState) terminateExact() error {
	p.mu.Lock()
	p.startWaitLocked()
	cmd, identity, identityKnown, identityRequired, captureIdentity := p.cmd, p.startupIdentity, p.identityKnown, p.identityRequired, p.captureIdentity
	terminateHook := p.terminateHook
	waitComplete := p.waitComplete
	p.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return errors.Join(ErrProcessTreeCleanupPending, errors.New("startup process owner is unavailable"))
	}
	var killErr error
	if !waitComplete {
		killErr = terminateOwnedStartupProcess(cmd, identity, identityKnown, identityRequired, captureIdentity, terminateHook)
	}
	if killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
		killErr = fmt.Errorf("kill exact startup process %d: %w", cmd.Process.Pid, killErr)
	}
	waitCtx, waitCancel := startupTerminationWaitContext(killErr)
	waitErr := p.waitResult(waitCtx)
	waitCancel()
	if waitErr != nil || killErr != nil {
		return errors.Join(ErrProcessTreeCleanupPending, killErr, waitErr)
	}
	return nil
}

// terminateOwnedStartupProcess 在同一动作边界内完成启动身份复验和平台精确终止。
func terminateOwnedStartupProcess(
	cmd *exec.Cmd,
	identity ProcessIdentity,
	identityKnown bool,
	identityRequired bool,
	captureIdentity func(int) (ProcessIdentity, error),
	terminateHook func() error,
) error {
	if identityRequired {
		if err := verifyStartupProcessIdentity(cmd.Process.Pid, identity, identityKnown, captureIdentity); err != nil {
			return errors.Join(ErrProcessTreeCleanupPending, err)
		}
	}
	if terminateHook != nil {
		return terminateHook()
	}
	return terminateStartupProcess(cmd)
}

func (p *startupProcessTreeState) terminate() error {
	p.mu.Lock()
	released := p.released
	p.mu.Unlock()
	if released {
		return errors.New("startup process-tree owner is released")
	}
	return p.terminateExact()
}

// release 仅在启动进程已等待完成且释放钩子成功时关闭 startup owner。
func (p *startupProcessTreeState) release() error {
	p.mu.Lock()
	if p.released {
		p.mu.Unlock()
		return nil
	}
	if !p.waitStarted {
		p.mu.Unlock()
		return ErrProcessTreeCleanupPending
	}
	complete := p.waitComplete
	p.mu.Unlock()
	if !complete {
		waitCtx, waitCancel := platformconfig.WithTimeout(context.Background(), startupOwnerWait)
		result := p.waitResult(waitCtx)
		waitCancel()
		if result != nil {
			return errors.Join(ErrProcessTreeCleanupPending, result)
		}
	}
	if p.releaseHook != nil {
		if err := p.releaseHook(); err != nil {
			return errors.Join(ErrProcessTreeCleanupPending, err)
		}
	}
	p.mu.Lock()
	p.released = true
	p.mu.Unlock()
	return nil
}

func (p *startupProcessTreeState) rssBytes() (uint64, error) {
	return 0, errors.Join(ErrProcessTreeCleanupPending, errors.New("startup process-tree RSS is unavailable without a bound tree owner"))
}

func (p *startupProcessTreeState) identity() (ProcessIdentity, error) {
	p.mu.Lock()
	identityRequired, identityKnown, identity := p.identityRequired, p.identityKnown, p.startupIdentity
	p.mu.Unlock()
	if identityRequired {
		if !identityKnown {
			return ProcessIdentity{}, errors.Join(ErrProcessTreeCleanupPending, ErrProcessTreeOwnerMissing, errors.New("startup process identity is unavailable"))
		}
		return identity, nil
	}
	return ProcessIdentity{}, errors.Join(ErrProcessTreeCleanupPending, errors.New("startup process-tree identity binding is incomplete"))
}

func (p *startupProcessTreeState) snapshot() (ProcessTreeSnapshot, error) {
	return ProcessTreeSnapshot{}, errors.Join(ErrProcessTreeCleanupPending, errors.New("startup process-tree snapshot is unavailable without a bound tree owner"))
}

// prepareShutdown 对未完成绑定的 startup owner 明确返回 CleanupPending，禁止假设后代已受控。
func (p *startupProcessTreeState) prepareShutdown() error {
	return errors.Join(ErrProcessTreeCleanupPending, errors.New("startup process-tree shutdown preparation is unavailable without a bound tree owner"))
}

// alive 以启动时身份重读结果报告 retained startup owner 是否仍可证明。
func (p *startupProcessTreeState) alive() (bool, error) {
	p.mu.Lock()
	cmd := p.cmd
	released := p.released
	identity, identityKnown, identityRequired, captureIdentity := p.startupIdentity, p.identityKnown, p.identityRequired, p.captureIdentity
	p.mu.Unlock()
	if released {
		return false, errors.New("startup process-tree owner is released")
	}
	if cmd == nil || cmd.Process == nil {
		return false, errors.Join(ErrProcessTreeCleanupPending, errors.New("startup process owner is unavailable"))
	}
	if identityRequired {
		if err := verifyStartupProcessIdentity(cmd.Process.Pid, identity, identityKnown, captureIdentity); err != nil {
			return false, errors.Join(ErrProcessTreeCleanupPending, err)
		}
		return true, nil
	}
	return ProcessAlive(cmd.Process.Pid)
}

func verifyStartupProcessIdentity(pid int, expected ProcessIdentity, known bool, captureIdentity func(int) (ProcessIdentity, error)) error {
	if !known {
		return errors.Join(ErrProcessTreeIdentityMismatch, ErrProcessTreeOwnerMissing, errors.New("startup process identity was not captured"))
	}
	if captureIdentity == nil {
		return errors.Join(ErrProcessTreeIdentityMismatch, ErrProcessTreeOwnerMissing, errors.New("startup process identity probe is unavailable"))
	}
	current, err := captureIdentity(pid)
	if err != nil {
		return errors.Join(ErrProcessTreeIdentityMismatch, fmt.Errorf("re-read startup process identity for pid %d: %w", pid, err))
	}
	if !current.Equal(expected) {
		return fmt.Errorf("%w: startup process PID %d was reused", ErrProcessTreeIdentityMismatch, pid)
	}
	return nil
}

func (p *startupProcessTree) descendants() ([]ProcessIdentity, error) {
	return nil, errors.Join(ErrProcessTreeCleanupPending, errors.New("startup process-tree descendants are not bound"))
}

func (p *startupProcessTree) graceful(context.Context) error {
	return errors.Join(ErrProcessTreeCleanupPending, errors.New("startup process-tree owner has no safe graceful action"))
}

func (p *startupProcessTree) force(context.Context) error {
	return p.startupProcessTreeState.terminate()
}

func (p *startupProcessTree) wait(ctx context.Context) error {
	if ctx == nil {
		return ErrProcessTreeContextNil
	}
	p.mu.Lock()
	released := p.released
	p.mu.Unlock()
	if released {
		return errors.New("startup process-tree owner is released")
	}
	if err := p.startupProcessTreeState.waitResult(ctx); err != nil {
		return errors.Join(ErrProcessTreeCleanupPending, err)
	}
	return nil
}

// remaining 返回 startup owner 当前仍可由精确等待状态证明的成员。
func (p *startupProcessTree) remaining() ([]ProcessIdentity, error) {
	p.mu.Lock()
	if p.released {
		p.mu.Unlock()
		return nil, errors.New("startup process-tree owner is released")
	}
	waitStarted := p.waitStarted
	waitComplete := p.waitComplete
	done := p.waitDone
	p.mu.Unlock()
	if !waitStarted {
		return nil, ErrProcessTreeCleanupPending
	}
	if waitComplete {
		return nil, nil
	}
	select {
	case err := <-done:
		if err := p.recordWaitResult(err); err != nil {
			return nil, errors.Join(ErrProcessTreeCleanupPending, err)
		}
		return nil, nil
	default:
		return nil, ErrProcessTreeCleanupPending
	}
}

const startupOwnerWait = 3 * time.Second

func startupTerminationWaitContext(killErr error) (context.Context, context.CancelFunc) {
	if errors.Is(killErr, ErrProcessTreeCleanupPending) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		return ctx, cancel
	}
	return platformconfig.WithTimeout(context.Background(), startupOwnerWait)
}
