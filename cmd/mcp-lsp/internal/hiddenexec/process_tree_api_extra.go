package hiddenexec

import (
	"context"
	"errors"
)

// Descendants 返回当前已证明属于 owner 的后代，并在返回前重新绑定其身份。
func (p *ProcessTree) Descendants() ([]ProcessIdentity, error) {
	if p == nil || p.controller == nil {
		return nil, errors.New("process-tree owner is nil")
	}
	return p.controller.descendants()
}

// Graceful 执行一次有界、经 action-time 身份复核的优雅关闭动作。
// Windows 明确不虚构 TERM 阶段，会返回平台错误且不发送信号。
func (p *ProcessTree) Graceful(ctx context.Context) error {
	if p == nil || p.controller == nil {
		return errors.New("process-tree owner is nil")
	}
	if ctx == nil {
		return ErrProcessTreeContextNil
	}
	return p.controller.graceful(ctx)
}

// Force 执行一次有界、经 action-time 身份复核的强制关闭动作。
func (p *ProcessTree) Force(ctx context.Context) error {
	if p == nil || p.controller == nil {
		return errors.New("process-tree owner is nil")
	}
	if ctx == nil {
		return ErrProcessTreeContextNil
	}
	return p.controller.force(ctx)
}

// Wait 等待 owner 成员全部退出；超时不释放 owner，调用方可继续重试或进入 CleanupPending。
func (p *ProcessTree) Wait(ctx context.Context) error {
	if p == nil || p.controller == nil {
		return errors.New("process-tree owner is nil")
	}
	if ctx == nil {
		return ErrProcessTreeContextNil
	}
	return p.controller.wait(ctx)
}

// Remaining 返回当前仍存活且通过身份复核的成员；身份不完整时返回错误而非空集合。
func (p *ProcessTree) Remaining() ([]ProcessIdentity, error) {
	if p == nil || p.controller == nil {
		return nil, errors.New("process-tree owner is nil")
	}
	return p.controller.remaining()
}
