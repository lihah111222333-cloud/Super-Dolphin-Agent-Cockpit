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
// binding fails after Start. It holds the exact os.Process handle and the
// single cmd.Wait result channel, so callers can retry root cleanup without
// reconstructing a PID or losing the unreaped startup process.
type startupProcessTree struct {
	startupProcessTreeState
}

// startupProcessTreeState keeps the wait/reap state separate from the
// controller's remaining operations so each file stays within the method guard.
type startupProcessTreeState struct {
	mu           sync.Mutex
	cmd          *exec.Cmd
	waitDone     chan error
	waitStarted  bool
	waitComplete bool
	waitErr      error
	releaseHook  func() error
	released     bool
}

func newStartupProcessTree(cmd *exec.Cmd, waitDone chan error) *ProcessTree {
	return newStartupProcessTreeWithRelease(cmd, waitDone, nil)
}

func newStartupProcessTreeWithRelease(cmd *exec.Cmd, waitDone chan error, releaseHook func() error) *ProcessTree {
	return &ProcessTree{controller: &startupProcessTree{startupProcessTreeState: startupProcessTreeState{
		cmd:         cmd,
		waitDone:    waitDone,
		waitStarted: waitDone != nil,
		releaseHook: releaseHook,
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

// terminateExact 通过启动时保存的 os.Process 句柄终止并回收未完成绑定的根进程。
func (p *startupProcessTreeState) terminateExact() error {
	p.mu.Lock()
	p.startWaitLocked()
	cmd := p.cmd
	p.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return errors.Join(ErrProcessTreeCleanupPending, errors.New("startup process owner is unavailable"))
	}
	var killErr error
	if cmd.ProcessState == nil {
		killErr = cmd.Process.Kill()
	}
	if killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
		killErr = fmt.Errorf("kill exact startup process %d: %w", cmd.Process.Pid, killErr)
	}
	waitCtx, waitCancel := platformconfig.WithTimeout(context.Background(), startupOwnerWait)
	waitErr := p.waitResult(waitCtx)
	waitCancel()
	if waitErr != nil || killErr != nil {
		return errors.Join(ErrProcessTreeCleanupPending, killErr, waitErr)
	}
	return nil
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
	return ProcessIdentity{}, errors.Join(ErrProcessTreeCleanupPending, errors.New("startup process-tree identity binding is incomplete"))
}

func (p *startupProcessTreeState) snapshot() (ProcessTreeSnapshot, error) {
	return ProcessTreeSnapshot{}, errors.Join(ErrProcessTreeCleanupPending, errors.New("startup process-tree snapshot is unavailable without a bound tree owner"))
}

// prepareShutdown 对未完成绑定的 startup owner 明确返回 CleanupPending，禁止假设后代已受控。
func (p *startupProcessTreeState) prepareShutdown() error {
	return errors.Join(ErrProcessTreeCleanupPending, errors.New("startup process-tree shutdown preparation is unavailable without a bound tree owner"))
}

func (p *startupProcessTreeState) alive() (bool, error) {
	p.mu.Lock()
	cmd := p.cmd
	released := p.released
	p.mu.Unlock()
	if released {
		return false, errors.New("startup process-tree owner is released")
	}
	if cmd == nil || cmd.Process == nil {
		return false, errors.Join(ErrProcessTreeCleanupPending, errors.New("startup process owner is unavailable"))
	}
	return ProcessAlive(cmd.Process.Pid)
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
