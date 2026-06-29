package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

const (
	// TierFast 快速操作超时（5秒）。
	TierFast = 5 * time.Second
	// TierNormal 普通操作超时（30秒）。
	TierNormal = 30 * time.Second
	// TierSlow 慢速操作超时（120秒）。
	TierSlow = 120 * time.Second
)

const defaultTimeoutWorkerLimit = 64

// Timeout 给请求套上超时控制。
func Timeout(limit time.Duration) Middleware {
	return timeoutWithWorkerLimit(limit, defaultTimeoutWorkerLimit)
}

// timeoutWithWorkerLimit 构造带有有界 worker 槽位的超时中间件，测试可缩小上限验证背压。
func timeoutWithWorkerLimit(limit time.Duration, workerLimit int) Middleware {
	if limit <= 0 {
		limit = TierNormal
	}
	if workerLimit <= 0 {
		workerLimit = 1
	}
	workerSem := make(chan struct{}, workerLimit)
	return func(next Handler) Handler {
		return func(ctx context.Context, params json.RawMessage) (any, error) {
			timeoutCtx, cancel := withToolTimeout(ctx, limit)
			defer cancel()
			if err := timeoutCtx.Err(); err != nil {
				return nil, err
			}
			if err := acquireTimeoutWorker(timeoutCtx, workerSem); err != nil {
				return nil, err
			}
			resultC := make(chan timeoutResult, 1)
			go func() {
				defer releaseTimeoutWorker(workerSem)
				result, err := callWithRecover(next, timeoutCtx, params)
				resultC <- timeoutResult{value: result, err: err}
			}()
			select {
			case result := <-resultC:
				return result.value, result.err
			case <-timeoutCtx.Done():
				return nil, newToolTimeoutError(limit, timeoutCtx.Err())
			}
		}
	}
}

// acquireTimeoutWorker 给异步 handler 分配有界槽位；满载时 fail-fast，避免超时请求无限堆积。
func acquireTimeoutWorker(ctx context.Context, workerSem chan struct{}) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case workerSem <- struct{}{}:
		return nil
	default:
		return newToolTimeoutCapacityError(cap(workerSem))
	}
}

func releaseTimeoutWorker(workerSem chan struct{}) {
	<-workerSem
}

// withToolTimeout 为工具请求创建超时上下文，遵守父上下文的已有截止时间。
func withToolTimeout(ctx context.Context, limit time.Duration) (context.Context, context.CancelFunc) {
	deadline := time.Now().Add(limit)
	if parentDeadline, ok := ctx.Deadline(); ok && parentDeadline.Before(deadline) {
		return context.WithDeadline(ctx, parentDeadline)
	}
	return context.WithDeadline(ctx, deadline)
}

// timeoutResult 持有 goroutine 执行结果，通过 channel 传回调用方。
type timeoutResult struct {
	value any
	err   error
}

// callWithRecover 调用 next 并在 panic 时返回错误，避免 goroutine 崩溃传播。
func callWithRecover(next Handler, ctx context.Context, params json.RawMessage) (value any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = common.NewPanicToolError(recovered)
		}
	}()
	return next(ctx, params)
}

// newToolTimeoutCapacityError 构造 timeout middleware 的容量错误，提醒调用方稍后重试。
func newToolTimeoutCapacityError(maxOutstanding int) error {
	return &common.CodedToolError{
		Err:       errors.New("tool timeout worker capacity exhausted"),
		Code:      "lsp_timeout_capacity",
		Retryable: true,
		Hint:      "next: retry after current LSP requests finish or narrow concurrent tool calls",
		Meta: map[string]any{
			"max_outstanding": maxOutstanding,
		},
	}
}

// newToolTimeoutError 构造工具超时的结构化错误，包含重试提示和超时时长元数据。
func newToolTimeoutError(limit time.Duration, err error) error {
	if errors.Is(err, context.Canceled) {
		return err
	}
	return &common.CodedToolError{
		Err:       fmt.Errorf("tool handler timed out after %s: %w", limit, context.DeadlineExceeded),
		Code:      "lsp_timeout",
		Retryable: true,
		Hint:      "next: narrow query/path/glob or reduce max_results after the language server finishes indexing",
		Meta: map[string]any{
			"timeout_ms": limit.Milliseconds(),
		},
	}
}

// ClampTimeout 把超时时间限制在允许范围内。
func ClampTimeout(requestedSeconds int, fallback time.Duration, ceiling time.Duration) time.Duration {
	if fallback <= 0 {
		fallback = TierNormal
	}
	if ceiling <= 0 {
		ceiling = fallback
	}
	if requestedSeconds <= 0 {
		return fallback
	}
	requested := time.Duration(requestedSeconds) * time.Second
	if requested > ceiling {
		return ceiling
	}
	return requested
}
