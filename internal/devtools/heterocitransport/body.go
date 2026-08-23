package heterocitransport

import (
	"context"
	"errors"
	"sync"
)

type responseBody struct {
	inner    ResponseBody
	closeMu  sync.Once
	closeErr error
}

// Read 在委托给 requester 前先观察 context 取消状态。
func (body *responseBody) Read(ctx context.Context, destination []byte) (int, error) {
	if body == nil || isNilValue(body.inner) {
		return 0, ErrInvalidResponse
	}
	if ctx == nil {
		return 0, errors.Join(ErrInvalidRequest, errors.New("response read context is required"))
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return body.inner.Read(ctx, destination)
}

// Close 至多调用一次 requester body 的 Close，并稳定返回其结果。
func (body *responseBody) Close() error {
	if body == nil || isNilValue(body.inner) {
		return ErrInvalidResponse
	}
	body.closeMu.Do(func() {
		body.closeErr = body.inner.Close()
	})
	return body.closeErr
}
