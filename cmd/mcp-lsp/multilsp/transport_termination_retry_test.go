package multilsp

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
)

type retryingTerminationProcessTreeOwner struct {
	countingProcessTreeOwner
}

func (o *retryingTerminationProcessTreeOwner) Terminate() error {
	if call := o.terminateCalls.Add(1); call == 1 {
		return hiddenexec.ErrProcessTreeCleanupPending
	}
	return nil
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
