package multilsp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
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

func TestTransportResponseDeadlineDropsLateResult(t *testing.T) {
	writer := &bufferWriteCloser{}
	tr := &transport{stdin: writer, pending: map[string]chan pendingResult{}, done: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := tr.request(ctx, "textDocument/definition", map[string]string{"uri": "file:///slow.go"})
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrLSPResponseTimeout) {
		t.Fatalf("request() error = %v, want explicit response timeout", err)
	}
	if writer.Len() == 0 {
		t.Fatal("request was not written before response deadline")
	}
	// transport assigns the first request ID synchronously. The write is framed
	// as LSP Content-Length bytes, so do not confuse it with raw JSON here.
	late, err := protocol.BuildSuccessResponse([]byte("1"), map[string]string{"late": "must be discarded"})
	if err != nil {
		t.Fatalf("build late response: %v", err)
	}
	raw, err := json.Marshal(late)
	if err != nil {
		t.Fatalf("marshal late response: %v", err)
	}
	if err := tr.handleResponse(raw); err != nil {
		t.Fatalf("handle late response: %v", err)
	}
	tr.pendingMu.Lock()
	pendingCount := len(tr.pending)
	tr.pendingMu.Unlock()
	if pendingCount != 0 {
		t.Fatalf("late response restored %d pending requests", pendingCount)
	}
}
