package acpnode

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

type writeResult struct {
	n   int
	err error
}

// writeOwner retains ownership of a potentially non-cooperative Write until
// it returns, so a timeout never leaves an untracked goroutine behind.
type writeOwner struct {
	ownerState
	result chan writeResult
}

func (o *writeOwner) run(w io.Writer, payload []byte) {
	n, err := w.Write(payload)
	o.result <- writeResult{n: n, err: err}
	o.finish(err)
}

type closeOwner struct {
	ownerState
	result chan error
}

func (o *closeOwner) run(closeWriter func() error) {
	err := closeWriter()
	o.result <- err
	o.finish(err)
}

type writeOwnerPendingError struct {
	owner *writeOwner
}

// Error 返回协议 Write owner 仍未完成的稳定错误分类。
func (e *writeOwnerPendingError) Error() string {
	return "acp: protocol write owner remains pending"
}

// Done 暴露协议 Write owner 的最终完成信号。
func (e *writeOwnerPendingError) Done() <-chan struct{} {
	if e == nil || e.owner == nil {
		return closedChannel()
	}
	return e.owner.done
}

// Err 返回协议 Write owner 完成后的底层错误。
func (e *writeOwnerPendingError) Err() error {
	if e == nil || e.owner == nil {
		return ErrOwnerPending
	}
	return e.owner.Err()
}

// Join 等待协议 Write owner 完成并返回底层错误。
func (e *writeOwnerPendingError) Join() error {
	if e == nil || e.owner == nil {
		return ErrOwnerPending
	}
	return e.owner.Join()
}

// Unwrap 在 owner 完成后暴露其底层写入错误。
func (e *writeOwnerPendingError) Unwrap() error {
	err := e.Err()
	if errors.Is(err, ErrOwnerPending) {
		return nil
	}
	return err
}

type closeOwnerPendingError struct {
	owner *closeOwner
}

// Error 返回协议 Close owner 仍未完成的稳定错误分类。
func (e *closeOwnerPendingError) Error() string {
	return "acp: protocol close owner remains pending"
}

// Done 暴露协议 Close owner 的最终完成信号。
func (e *closeOwnerPendingError) Done() <-chan struct{} {
	if e == nil || e.owner == nil {
		return closedChannel()
	}
	return e.owner.done
}

// Err 返回协议 Close owner 完成后的底层错误。
func (e *closeOwnerPendingError) Err() error {
	if e == nil || e.owner == nil {
		return ErrOwnerPending
	}
	return e.owner.Err()
}

// Join 等待协议 Close owner 完成并返回底层错误。
func (e *closeOwnerPendingError) Join() error {
	if e == nil || e.owner == nil {
		return ErrOwnerPending
	}
	return e.owner.Join()
}

// Unwrap 在 owner 完成后暴露其底层关闭错误。
func (e *closeOwnerPendingError) Unwrap() error {
	err := e.Err()
	if errors.Is(err, ErrOwnerPending) {
		return nil
	}
	return err
}

func (o *writeOwner) pendingError() error {
	if o == nil {
		return nil
	}
	select {
	case <-o.Done():
		return nil
	default:
		return &writeOwnerPendingError{owner: o}
	}
}

func (o *closeOwner) pendingError() error {
	if o == nil {
		return nil
	}
	select {
	case <-o.Done():
		return nil
	default:
		return &closeOwnerPendingError{owner: o}
	}
}

// writeBytesBoundedContext 监督单次 Write，并确保阻塞子进程不能占住协议锁。
func writeBytesBoundedContext(ctx context.Context, w io.Writer, payload []byte, timeout time.Duration, closeWriter func() error) error {
	return writeBytesBoundedContextTracked(ctx, w, payload, timeout, closeWriter, nil)
}

// writeBytesBoundedContextTracked 追踪单次有界 Write 并返回可 join 的超时错误。
func writeBytesBoundedContextTracked(ctx context.Context, w io.Writer, payload []byte, timeout time.Duration, closeWriter func() error, track func(*writeOwner)) error {
	if ctx == nil {
		return fmt.Errorf("acp: nil write context")
	}
	if w == nil {
		return fmt.Errorf("acp: nil wire writer")
	}
	if timeout <= 0 {
		return fmt.Errorf("acp: invalid write timeout")
	}
	owner := &writeOwner{ownerState: newOwnerState(), result: make(chan writeResult, 1)}
	if track != nil {
		track(owner)
	}
	go owner.run(w, payload)
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case outcome := <-owner.result:
		if outcome.err != nil {
			return outcome.err
		}
		if outcome.n != len(payload) {
			return io.ErrShortWrite
		}
		return nil
	case <-ctx.Done():
		return abortBlockedWrite(ctx.Err(), closeWriter, owner, timeout)
	case <-timer.C:
		return abortBlockedWrite(ErrWriteTimeout, closeWriter, owner, timeout)
	}
}

func abortBlockedWrite(trigger error, closeWriter func() error, owner *writeOwner, timeout time.Duration) error {
	closeErr := boundedWriteClose(closeWriter, timeout)
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case outcome := <-owner.result:
		if outcome.err != nil {
			return errors.Join(trigger, closeErr, outcome.err)
		}
		if outcome.n != 0 {
			return errors.Join(trigger, closeErr, io.ErrShortWrite)
		}
		return errors.Join(trigger, closeErr)
	case <-timer.C:
		return errors.Join(trigger, closeErr, ErrWriteTimeout, &writeOwnerPendingError{owner: owner})
	}
}

func boundedWriteClose(closeWriter func() error, timeout time.Duration) error {
	if closeWriter == nil {
		return fmt.Errorf("acp: nil protocol close operation")
	}
	owner := &closeOwner{ownerState: newOwnerState(), result: make(chan error, 1)}
	go owner.run(closeWriter)
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-owner.result:
		return err
	case <-timer.C:
		return errors.Join(ErrShutdownTimeout, &closeOwnerPendingError{owner: owner})
	}
}
