package multilsp

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

// 本文件只验证不依赖平台进程树能力的请求超时语义；需要真实子进程所有权的
// 测试位于带显式平台 build tag 的 companion，避免 FreeBSD 误选不支持的清理路径。

type bufferWriteCloser struct{ bytes.Buffer }

func (*bufferWriteCloser) Close() error { return nil }

func TestDefaultLSPRequestTimeoutIsSixtySeconds(t *testing.T) {
	if defaultRequestTimeout != 60*time.Second {
		t.Fatalf("defaultRequestTimeout = %s, want 60s per LSP step", defaultRequestTimeout)
	}
}

func TestTransportResponseDeadlinePreservesHealthyTransport(t *testing.T) {
	writer := &bufferWriteCloser{}
	tr := &transport{
		stdin:   writer,
		pending: map[string]chan pendingResult{},
		done:    make(chan struct{}),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := tr.request(ctx, "textDocument/definition", map[string]string{"uri": "file:///slow.go"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("request() error = %v, want context deadline exceeded", err)
	}
	if tr.closed.Load() {
		t.Fatal("response deadline closed transport; slow server progress must survive for retry")
	}
	tr.pendingMu.Lock()
	pendingCount := len(tr.pending)
	tr.pendingMu.Unlock()
	if pendingCount != 0 {
		t.Fatalf("pending requests after response deadline = %d, want 0", pendingCount)
	}
	if writer.Len() == 0 {
		t.Fatal("request was not written before response deadline")
	}
}
