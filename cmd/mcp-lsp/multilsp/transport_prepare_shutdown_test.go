package multilsp

import (
	"context"
	"errors"
	"testing"
)

// TestTransportPrepareProcessTreeShutdownUsesExactOwner 验证关闭协议只调用 transport 持有的 exact owner。
func TestTransportPrepareProcessTreeShutdownUsesExactOwner(t *testing.T) {
	owner := &countingProcessTreeOwner{}
	tr := &transport{processTree: owner}

	if err := tr.prepareProcessTreeShutdown(); err != nil {
		t.Fatalf("prepareProcessTreeShutdown() error = %v", err)
	}
	if got := owner.prepareCalls.Load(); got != 1 {
		t.Fatalf("PrepareShutdown() calls = %d, want 1", got)
	}
}

// TestTransportPrepareProcessTreeShutdownPropagatesOwnerError 验证 owner 无法安全入册时关闭请求立即失败。
func TestTransportPrepareProcessTreeShutdownPropagatesOwnerError(t *testing.T) {
	want := errors.New("owner preparation failed")
	owner := &countingProcessTreeOwner{prepareErr: want}
	tr := &transport{processTree: owner}

	err := tr.prepareProcessTreeShutdown()
	if !errors.Is(err, want) {
		t.Fatalf("prepareProcessTreeShutdown() error = %v, want %v", err, want)
	}
	if got := owner.prepareCalls.Load(); got != 1 {
		t.Fatalf("PrepareShutdown() calls = %d, want 1", got)
	}
}

// TestClientShutdownUninitializedPreparesExactOwner 验证未初始化 client 也必须先完成 owner 入册。
func TestClientShutdownUninitializedPreparesExactOwner(t *testing.T) {
	owner := &countingProcessTreeOwner{}
	c := &client{transport: &transport{processTree: owner}}

	if err := c.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if got := owner.prepareCalls.Load(); got != 1 {
		t.Fatalf("PrepareShutdown() calls = %d, want 1", got)
	}
	if !c.isShutdown() {
		t.Fatal("Shutdown() did not mark uninitialized client as shutdown")
	}
}

// TestClientShutdownPreparationFailureBlocksProtocolPath 验证准备失败时不发送协议并保持未关闭状态。
func TestClientShutdownPreparationFailureBlocksProtocolPath(t *testing.T) {
	want := errors.New("owner preparation failed")
	owner := &countingProcessTreeOwner{prepareErr: want}
	c := &client{
		transport:   &transport{processTree: owner},
		initialized: true,
	}

	err := c.Shutdown(context.Background())
	if !errors.Is(err, want) {
		t.Fatalf("Shutdown() error = %v, want %v", err, want)
	}
	if got := owner.prepareCalls.Load(); got != 1 {
		t.Fatalf("PrepareShutdown() calls = %d, want 1", got)
	}
	if c.isShutdown() {
		t.Fatal("Shutdown() marked client shutdown after preparation failure")
	}
}
