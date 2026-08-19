package multilsp

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
)

func waitForTransportExitForTest(t *testing.T, tr *transport) {
	t.Helper()
	select {
	case <-tr.done:
	case <-time.After(time.Second):
		t.Fatal("transport wait did not complete")
	}
}

func assertTransportClosedForTest(t *testing.T, tr *transport) {
	t.Helper()
	if !tr.closed.Load() {
		t.Fatal("transport remained open after the language server exited")
	}
}

func assertPendingFailedForTest(t *testing.T, pending chan pendingResult) {
	t.Helper()
	select {
	case result := <-pending:
		if result.err == nil {
			t.Fatal("pending request completed successfully after language server exit")
		}
		if !errors.Is(result.err, ErrTransportClosed) {
			t.Fatalf("pending request error = %v, want ErrTransportClosed", result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("pending request was not failed after language server exit")
	}
}

func TestTransportDoesNotCloseOnOrdinaryStderrWarning(t *testing.T) {
	stderr := &limitedBuffer{limit: stderrLimitBytes}
	_, _ = stderr.Write([]byte("warning: tsserver using fallback configuration\n"))
	tr := &transport{stderr: stderr, pending: map[string]chan pendingResult{}}
	if err := tr.checkFatalChildCrash(); err != nil {
		t.Fatalf("ordinary stderr warning returned error: %v", err)
	}
	if tr.closed.Load() {
		t.Fatal("ordinary stderr warning closed transport")
	}
}

type retryingTerminationProcessTreeOwner struct {
	countingProcessTreeOwner
}

func (o *retryingTerminationProcessTreeOwner) Terminate() error {
	if call := o.terminateCalls.Add(1); call == 1 {
		return hiddenexec.ErrProcessTreeCleanupPending
	}
	return nil
}

func (o *retryingTerminationProcessTreeOwner) Remaining() ([]hiddenexec.ProcessIdentity, error) {
	if o.terminateCalls.Load() < 2 {
		return []hiddenexec.ProcessIdentity{{PID: 2}}, nil
	}
	return nil, nil
}

// TestTransportCloseRetriesProcessTreeTerminationAfterFailure 锁定终止失败后的 owner 保留与下一次收敛。
func TestTransportCloseRetriesProcessTreeTerminationAfterFailure(t *testing.T) {
	owner := &retryingTerminationProcessTreeOwner{}
	tr := newTestTransportWithExitedProcess()
	tr.processTree = owner

	firstErr := tr.Close()
	if !errors.Is(firstErr, hiddenexec.ErrProcessTreeCleanupPending) {
		t.Fatalf("first Close() error = %v, want CleanupPending", firstErr)
	}
	assertTransportOwnerCalls(t, owner, 1, 0)

	if err := tr.Close(); err != nil {
		t.Fatalf("second Close() error = %v, want successful convergence", err)
	}
	assertTransportOwnerCalls(t, owner, 2, 1)
	if err := tr.Close(); err != nil {
		t.Fatalf("completed Close() error = %v, want nil", err)
	}
	assertTransportOwnerCalls(t, owner, 2, 1)
}

// TestTransportReleasedOwnerDoesNotRetryTermination 锁定已释放 owner 不因历史 pending 再次触发终止或释放。
func TestTransportReleasedOwnerDoesNotRetryTermination(t *testing.T) {
	owner := &countingProcessTreeOwner{}
	tr := newTestTransportWithExitedProcess()
	tr.processTree = owner
	tr.treeReleaseMu.Lock()
	tr.treeReleased = true
	tr.treeReleaseMu.Unlock()
	tr.terminationMu.Lock()
	tr.terminationErr = hiddenexec.ErrProcessTreeCleanupPending
	tr.terminationComplete = false
	tr.terminationMu.Unlock()

	if err, _ := tr.terminateProcessTreeAttempt(); err != nil {
		t.Fatalf("released owner terminate attempt = %v, want nil", err)
	}
	if err := tr.Close(); err != nil {
		t.Fatalf("released owner Close() = %v, want nil", err)
	}
	if got := owner.terminateCalls.Load(); got != 0 {
		t.Fatalf("released owner Terminate() calls = %d, want zero", got)
	}
	if got := owner.releaseCalls.Load(); got != 0 {
		t.Fatalf("released owner Release() calls = %d, want zero", got)
	}
}

type dynamicRemainingProcessTreeOwner struct {
	countingProcessTreeOwner
	remainingCalls atomic.Int32
}

func (o *dynamicRemainingProcessTreeOwner) Remaining() ([]hiddenexec.ProcessIdentity, error) {
	if call := o.remainingCalls.Add(1); call == 1 {
		return []hiddenexec.ProcessIdentity{{PID: 2}}, nil
	}
	return nil, nil
}

// TestTransportCloseRetriesTerminationAfterDynamicRemaining 锁定终止成功后 late descendant 的再次终止。
func TestTransportCloseRetriesTerminationAfterDynamicRemaining(t *testing.T) {
	owner := &dynamicRemainingProcessTreeOwner{}
	tr := newTestTransportWithExitedProcess()
	tr.processTree = owner

	if err := tr.Close(); !errors.Is(err, hiddenexec.ErrProcessTreeRemaining) {
		t.Fatalf("first Close() error = %v, want remaining-owner error", err)
	}
	assertTransportOwnerCalls(t, owner, 1, 0)
	if err := tr.Close(); err != nil {
		t.Fatalf("second Close() error = %v, want successful convergence", err)
	}
	assertTransportOwnerCalls(t, owner, 2, 1)
	if err := tr.Close(); err != nil {
		t.Fatalf("completed Close() error = %v, want nil", err)
	}
	assertTransportOwnerCalls(t, owner, 2, 1)
}

type terminationFailureOwner struct {
	countingProcessTreeOwner
}

func (o *terminationFailureOwner) Terminate() error {
	if call := o.terminateCalls.Add(1); call == 1 {
		return hiddenexec.ErrProcessTreeCleanupPending
	}
	return nil
}

func (o *terminationFailureOwner) Remaining() ([]hiddenexec.ProcessIdentity, error) {
	if o.terminateCalls.Load() < 2 {
		return []hiddenexec.ProcessIdentity{{PID: 2}}, nil
	}
	return nil, nil
}

type convergedTerminationFailureOwner struct {
	terminationFailureOwner
}

func (o *convergedTerminationFailureOwner) Remaining() ([]hiddenexec.ProcessIdentity, error) {
	return nil, nil
}

// TestTransportCloseReleasesExitedVerifiedTreeAfterBlockedSignal 锁定 Darwin 零信号失败后的自然退出收敛：
// cmd.Wait 已完成且 exact owner 证明零成员时，不得继续误报 CleanupPending。
func TestTransportCloseReleasesExitedVerifiedTreeAfterBlockedSignal(t *testing.T) {
	owner := &convergedTerminationFailureOwner{}
	tr := newTestTransportWithExitedProcess()
	tr.processTree = owner

	if err := tr.Close(); err != nil {
		t.Fatalf("Close() after verified natural exit = %v, want successful convergence", err)
	}
	assertTransportOwnerCalls(t, owner, 1, 1)
	if !tr.closeComplete {
		t.Fatal("Close() did not latch completion after verified natural exit")
	}
}

type noRemainingProcessTreeOwner struct {
	terminateCalls atomic.Int32
	releaseCalls   atomic.Int32
}

func (o *noRemainingProcessTreeOwner) Terminate() error {
	o.terminateCalls.Add(1)
	return nil
}

func (o *noRemainingProcessTreeOwner) Release() error {
	o.releaseCalls.Add(1)
	return nil
}

func (o *noRemainingProcessTreeOwner) RSSBytes() (uint64, error) { return 0, nil }

func (o *noRemainingProcessTreeOwner) PrepareShutdown() error { return nil }

// TestTransportCloseRetainsOwnerWhenNaturalExitCannotProveRemaining 锁定自然退出缺少 Remaining 证据时不得释放 owner。
func TestTransportCloseRetainsOwnerWhenNaturalExitCannotProveRemaining(t *testing.T) {
	owner := &noRemainingProcessTreeOwner{}
	tr := newTestTransportWithExitedProcess()
	tr.processTree = owner

	err := tr.Close()
	if !errors.Is(err, hiddenexec.ErrProcessTreeCleanupPending) {
		t.Fatalf("Close() without Remaining = %v, want CleanupPending", err)
	}
	if got := owner.releaseCalls.Load(); got != 0 {
		t.Fatalf("Release() calls without Remaining = %d, want zero", got)
	}
	if tr.closeComplete {
		t.Fatal("Close() marked completion without Remaining evidence")
	}
}

// TestTransportCloseRetainsOwnerWhenTerminationFails 锁定终止失败时不得提前 Release。
func TestTransportCloseRetainsOwnerWhenTerminationFails(t *testing.T) {
	owner := &terminationFailureOwner{}
	tr := newTestTransportWithExitedProcess()
	tr.processTree = owner

	if err := tr.Close(); !errors.Is(err, hiddenexec.ErrProcessTreeCleanupPending) {
		t.Fatalf("first Close() error = %v, want CleanupPending", err)
	}
	assertTransportOwnerCalls(t, owner, 1, 0)
	if err := tr.Close(); err != nil {
		t.Fatalf("second Close() error = %v, want success", err)
	}
	assertTransportOwnerCalls(t, owner, 2, 1)
}

type cleanupTerminationFailureOwner struct {
	terminateCalls atomic.Int32
	releaseCalls   atomic.Int32
}

func (o *cleanupTerminationFailureOwner) Terminate() error {
	if call := o.terminateCalls.Add(1); call == 1 {
		return hiddenexec.ErrProcessTreeCleanupPending
	}
	return nil
}

func (o *cleanupTerminationFailureOwner) Wait(context.Context) error { return nil }

func (o *cleanupTerminationFailureOwner) Release() error {
	o.releaseCalls.Add(1)
	return nil
}

// TestCleanupProcessTreeOwnerRetainsOwnerWhenTerminationFails 锁定启动失败清理不得释放未终止 owner。
func TestCleanupProcessTreeOwnerRetainsOwnerWhenTerminationFails(t *testing.T) {
	owner := &cleanupTerminationFailureOwner{}
	firstErr := cleanupProcessTreeOwner(owner)
	if !errors.Is(firstErr, hiddenexec.ErrProcessTreeCleanupPending) {
		t.Fatalf("first cleanupProcessTreeOwner() error = %v, want CleanupPending", firstErr)
	}
	if got := owner.releaseCalls.Load(); got != 0 {
		t.Fatalf("first cleanup Release() calls = %d, want retained owner", got)
	}
	if err := cleanupProcessTreeOwner(owner); err != nil {
		t.Fatalf("second cleanupProcessTreeOwner() error = %v, want success", err)
	}
	if got := owner.terminateCalls.Load(); got != 2 {
		t.Fatalf("cleanup Terminate() calls = %d, want retry", got)
	}
	if got := owner.releaseCalls.Load(); got != 1 {
		t.Fatalf("cleanup Release() calls = %d, want 1", got)
	}
}

// TestTerminateProcessTreeRetainsOwnerOnTerminationFailure 锁定非 Close 终止路径同样不得提前 Release。
func TestTerminateProcessTreeRetainsOwnerOnTerminationFailure(t *testing.T) {
	owner := &terminationFailureOwner{}
	tr := newTestTransportWithExitedProcess()
	tr.processTree = owner

	if err := tr.terminateProcessTree(); !errors.Is(err, hiddenexec.ErrProcessTreeCleanupPending) {
		t.Fatalf("first terminateProcessTree() error = %v, want CleanupPending", err)
	}
	assertTransportOwnerCalls(t, owner, 1, 0)
	if err := tr.terminateProcessTree(); err != nil {
		t.Fatalf("second terminateProcessTree() error = %v, want success", err)
	}
	assertTransportOwnerCalls(t, owner, 2, 1)
}

// TestTransportCloseWithoutProcessTreeOwnerDoesNotComplete 锁定 owner 缺失时 Close 不能伪造成功。
func TestTransportCloseWithoutProcessTreeOwnerDoesNotComplete(t *testing.T) {
	tr := newTestTransportWithExitedProcess()
	tr.processTree = nil

	if err := tr.Close(); !errors.Is(err, hiddenexec.ErrProcessTreeOwnerMissing) {
		t.Fatalf("first Close() error = %v, want owner-missing", err)
	}
	if tr.closeComplete {
		t.Fatal("Close() marked transport complete without a process-tree owner")
	}
	if err := tr.Close(); !errors.Is(err, hiddenexec.ErrProcessTreeOwnerMissing) {
		t.Fatalf("retry Close() error = %v, want owner-missing", err)
	}
}

func assertTransportOwnerCalls(t *testing.T, owner interface {
	terminateCallCount() int32
	releaseCallCount() int32
}, terminate, release int32) {
	t.Helper()
	if got := owner.terminateCallCount(); got != terminate {
		t.Fatalf("Terminate() calls = %d, want %d", got, terminate)
	}
	if got := owner.releaseCallCount(); got != release {
		t.Fatalf("Release() calls = %d, want %d", got, release)
	}
}

func (o *retryingTerminationProcessTreeOwner) terminateCallCount() int32 {
	return o.terminateCalls.Load()
}
func (o *retryingTerminationProcessTreeOwner) releaseCallCount() int32 { return o.releaseCalls.Load() }
func (o *dynamicRemainingProcessTreeOwner) terminateCallCount() int32  { return o.terminateCalls.Load() }
func (o *dynamicRemainingProcessTreeOwner) releaseCallCount() int32    { return o.releaseCalls.Load() }
func (o *terminationFailureOwner) terminateCallCount() int32           { return o.terminateCalls.Load() }
func (o *terminationFailureOwner) releaseCallCount() int32             { return o.releaseCalls.Load() }
func (o *convergedTerminationFailureOwner) terminateCallCount() int32 {
	return o.terminateCalls.Load()
}
func (o *convergedTerminationFailureOwner) releaseCallCount() int32 {
	return o.releaseCalls.Load()
}
