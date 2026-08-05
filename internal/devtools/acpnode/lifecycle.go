package acpnode

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

type clientWork struct {
	pending []*pendingCall
	cancels []context.CancelFunc
}

// lifecycleActionOwner keeps a non-cooperative Close, Interrupt, or Kill
// operation reachable after its bounded caller returns.
type lifecycleActionOwner struct {
	ownerState
	result chan error
}

func newLifecycleActionOwner() *lifecycleActionOwner {
	return &lifecycleActionOwner{ownerState: newOwnerState(), result: make(chan error, 1)}
}

func startLifecycleAction(action func() error) *lifecycleActionOwner {
	owner := newLifecycleActionOwner()
	go runLifecycleAction(owner, action)
	return owner
}

func runLifecycleAction(owner *lifecycleActionOwner, action func() error) {
	var err error
	if action == nil {
		err = fmt.Errorf("acp: nil lifecycle action")
	} else {
		err = action()
	}
	owner.result <- err
	owner.finish(err)
}

type lifecycleActionPendingError struct {
	owner *lifecycleActionOwner
}

// Error 返回生命周期 owner 仍未完成的稳定错误分类。
func (e *lifecycleActionPendingError) Error() string {
	return "acp: lifecycle action remains pending"
}

// Done 暴露生命周期操作的最终完成信号。
func (e *lifecycleActionPendingError) Done() <-chan struct{} {
	if e == nil || e.owner == nil {
		return closedChannel()
	}
	return e.owner.done
}

// Err 返回生命周期 owner 完成后的底层错误，未完成时返回 ErrOwnerPending。
func (e *lifecycleActionPendingError) Err() error {
	if e == nil || e.owner == nil {
		return ErrOwnerPending
	}
	return e.owner.Err()
}

// Join 等待生命周期 owner 完成并返回其底层错误。
func (e *lifecycleActionPendingError) Join() error {
	if e == nil || e.owner == nil {
		return ErrOwnerPending
	}
	return e.owner.Join()
}

// Unwrap 在 owner 完成后把底层生命周期错误接入 errors.Is/As。
func (e *lifecycleActionPendingError) Unwrap() error {
	err := e.Err()
	if errors.Is(err, ErrOwnerPending) {
		return nil
	}
	return err
}

func (o *lifecycleActionOwner) pendingError() error {
	if o == nil {
		return nil
	}
	select {
	case <-o.Done():
		return nil
	default:
		return &lifecycleActionPendingError{owner: o}
	}
}

// Generation 返回当前协议代际，供调用方区分旧响应和新会话。
func (c *Client) Generation() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.generation
}

// Done 返回客户端终止信号，关闭后不会再产生新的协议工作。
func (c *Client) Done() <-chan struct{} { return c.done }

// Err 返回客户端记录的首个或合并后的终止原因。
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.terminalErr
}

// WaitErr 等待子进程结束，并保留随后发现的 stderr 或协议污染错误。
func (c *Client) WaitErr() error {
	c.startWait()
	<-c.waitDone
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.terminalErr != nil {
		return c.terminalErr
	}
	return c.waitErr
}

func (c *Client) terminate(cause error) {
	c.mu.Lock()
	c.addFailureLocked(cause)
	if c.terminated {
		c.mu.Unlock()
		return
	}
	c.terminated = true
	c.closed = true
	c.generation++
	pendingErr := c.terminalErr
	if pendingErr == nil {
		pendingErr = io.EOF
	}
	work := c.takeWorkLocked()
	c.mu.Unlock()
	resolveClientWork(work, pendingErr)
}

func (c *Client) addFailureLocked(err error) {
	if err == nil {
		return
	}
	if c.failureErr == nil {
		c.failureErr = err
		c.refreshTerminalErrorLocked()
		return
	}
	if errors.Is(c.failureErr, err) {
		return
	}
	c.failureErr = errors.Join(c.failureErr, err)
	c.refreshTerminalErrorLocked()
}

func (c *Client) refreshTerminalErrorLocked() {
	switch {
	case c.failureErr == nil:
		c.terminalErr = c.waitErr
	case c.waitErr == nil:
		c.terminalErr = c.failureErr
	default:
		c.terminalErr = errors.Join(c.failureErr, c.waitErr)
	}
}

func (c *Client) takeWorkLocked() clientWork {
	work := clientWork{
		pending: make([]*pendingCall, 0, len(c.pending)),
		cancels: make([]context.CancelFunc, 0, len(c.reverseCancels)),
	}
	for key, call := range c.pending {
		delete(c.pending, key)
		c.addTombstoneLocked(key)
		work.pending = append(work.pending, call)
	}
	for key, cancel := range c.reverseCancels {
		delete(c.reverseCancels, key)
		work.cancels = append(work.cancels, cancel)
	}
	if !c.updatesClosed {
		close(c.updates)
		c.updatesClosed = true
	}
	return work
}

func resolveClientWork(work clientWork, result error) {
	for _, call := range work.pending {
		call.result <- pendingResult{err: result}
	}
	for _, cancel := range work.cancels {
		cancel()
	}
}

func (c *Client) waitReverse(timeout time.Duration) error {
	c.mu.Lock()
	done := c.reverseDone
	c.mu.Unlock()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		c.reverseWG.Wait()
		return nil
	case <-timer.C:
		return ErrShutdownTimeout
	}
}

func (c *Client) fail(err error) {
	if err == nil {
		err = fmt.Errorf("acp: protocol failure")
	}
	c.terminate(err)
	c.startShutdown()
}

func (c *Client) startShutdown() {
	c.closeMu.Lock()
	started := c.closeStarted
	c.closeMu.Unlock()
	if !started {
		go c.closeAfterFailure()
	}
}

func (c *Client) closeAfterFailure() {
	if err := c.Close(); err != nil {
		c.mu.Lock()
		c.addFailureLocked(err)
		c.mu.Unlock()
	}
}

// Close 串行执行输入输出流、子进程和反向请求的有界关闭流程。
func (c *Client) Close() error {
	c.closeMu.Lock()
	if c.closeStarted {
		done := c.closeDone
		c.closeMu.Unlock()
		<-done
		c.closeMu.Lock()
		err := c.closeErr
		c.closeMu.Unlock()
		return err
	}
	c.closeStarted = true
	c.closeMu.Unlock()
	err := c.closeInternal()
	c.closeMu.Lock()
	c.closeErr = err
	close(c.closeDone)
	c.closeMu.Unlock()
	return err
}

// closeInternal 执行只允许发生一次的关闭步骤并 join 所有生命周期 owner。
func (c *Client) closeInternal() error {
	work := c.rejectClientWork()
	resolveClientWork(work, ErrClientClosed)
	var errs []error
	if err := c.closeStdin(); err != nil {
		errs = append(errs, err)
	}
	if err := c.closeStreams(); err != nil {
		errs = append(errs, err)
	}
	if err := c.shutdownProcess(errs); err != nil {
		errs = append(errs, err)
	}
	if err := c.waitReverse(c.cfg.ShutdownTimeout); err != nil {
		errs = append(errs, err)
	}
	if err := c.joinTrackedOwners(c.cfg.ShutdownTimeout); err != nil {
		errs = append(errs, err)
	}
	c.mu.Lock()
	lateStreamFailureErr := c.lateStreamFailureErr
	c.mu.Unlock()
	if lateStreamFailureErr != nil && !errors.Is(errors.Join(errs...), lateStreamFailureErr) {
		errs = append(errs, lateStreamFailureErr)
	}
	return errors.Join(errs...)
}

func (c *Client) rejectClientWork() clientWork {
	c.closeWriteAdmission()
	c.mu.Lock()
	c.closed = true
	work := c.takeWorkLocked()
	c.mu.Unlock()
	return work
}

// shutdownProcess 按等待、打断、强杀顺序回收子进程并报告超时。
func (c *Client) shutdownProcess(errs []error) error {
	if c.waitFor(c.cfg.ShutdownTimeout) {
		return c.shutdownResult(errs)
	}
	if err := c.interrupt(); err != nil {
		errs = append(errs, err)
	}
	if c.waitFor(c.cfg.ShutdownTimeout) {
		return c.shutdownResult(errs)
	}
	if err := c.kill(); err != nil {
		errs = append(errs, err)
	}
	c.startWait()
	if !c.waitFor(c.cfg.ShutdownTimeout) {
		return errors.Join(append(errs, ErrShutdownTimeout)...)
	}
	return c.shutdownResult(errs)
}

func (c *Client) shutdownResult(errs []error) error {
	c.startWait()
	<-c.waitDone
	c.mu.Lock()
	waitErr := c.waitErr
	terminalErr := c.terminalErr
	c.mu.Unlock()
	if waitErr != nil && (terminalErr == nil || !errors.Is(terminalErr, waitErr)) {
		errs = append(errs, waitErr)
	}
	return errors.Join(errs...)
}

func (c *Client) closeStdin() error {
	c.stdinOnce.Do(func() { c.stdinErr = closeCloserBoundedTracked(c.p.Stdin(), c.cfg.ShutdownTimeout, c.trackOwner) })
	return c.stdinErr
}

func closeCloserBounded(closer io.Closer, timeout time.Duration) error {
	return closeCloserBoundedTracked(closer, timeout, nil)
}

func closeCloserBoundedTracked(closer io.Closer, timeout time.Duration, track func(trackedOwner)) error {
	if closer == nil {
		return nil
	}
	owner := newLifecycleActionOwner()
	if track != nil {
		track(owner)
	}
	go runLifecycleAction(owner, closer.Close)
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-owner.result:
		return err
	case <-timer.C:
		return errors.Join(ErrShutdownTimeout, &lifecycleActionPendingError{owner: owner})
	}
}

func (c *Client) closeStreams() error {
	var errs []error
	c.stdoutOnce.Do(func() {
		c.stdoutErr = closeCloserBoundedTracked(c.p.Stdout(), c.cfg.ShutdownTimeout, c.trackOwner)
	})
	if c.stdoutErr != nil {
		errs = append(errs, c.stdoutErr)
	}
	c.stderrOnce.Do(func() {
		c.stderrErr = closeCloserBoundedTracked(c.p.Stderr(), c.cfg.ShutdownTimeout, c.trackOwner)
	})
	if c.stderrErr != nil {
		errs = append(errs, c.stderrErr)
	}
	return errors.Join(errs...)
}

func (c *Client) interrupt() error {
	c.interruptOnce.Do(func() {
		c.interruptErr = boundedLifecycleActionTracked(c.p.Interrupt, c.cfg.ShutdownTimeout, c.trackOwner)
	})
	return c.interruptErr
}

func (c *Client) kill() error {
	c.killOnce.Do(func() {
		c.killErr = boundedLifecycleActionTracked(c.p.Kill, c.cfg.ShutdownTimeout, c.trackOwner)
	})
	return c.killErr
}

func boundedLifecycleAction(action func() error, timeout time.Duration) error {
	return boundedLifecycleActionTracked(action, timeout, nil)
}

func boundedLifecycleActionTracked(action func() error, timeout time.Duration, track func(trackedOwner)) error {
	owner := newLifecycleActionOwner()
	if track != nil {
		track(owner)
	}
	go runLifecycleAction(owner, action)
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-owner.result:
		return err
	case <-timer.C:
		return errors.Join(ErrShutdownTimeout, &lifecycleActionPendingError{owner: owner})
	}
}

func (c *Client) waitFor(timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-c.waitDone:
		return true
	case <-timer.C:
		return false
	}
}
