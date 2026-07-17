package wails

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

// ActivationReadiness 持有 Wails ApplicationStarted 触发的一次性激活状态。
type ActivationReadiness struct {
	once      sync.Once
	activated atomic.Bool
	done      chan struct{}
}

// NewActivationReadiness 创建尚未激活的 Wails readiness owner。
func NewActivationReadiness() *ActivationReadiness {
	return &ActivationReadiness{done: make(chan struct{})}
}

// MarkApplicationStarted 只记录首次真实 Wails ApplicationStarted 事件。
func (readiness *ActivationReadiness) MarkApplicationStarted() {
	if readiness == nil {
		return
	}
	readiness.once.Do(func() {
		readiness.activated.Store(true)
		close(readiness.done)
	})
}

// Wait 阻塞到 Wails 发出 ApplicationStarted，或 owner context 结束。
func (readiness *ActivationReadiness) Wait(ctx context.Context) error {
	if readiness == nil || readiness.done == nil {
		return errors.New("wails activation readiness is required")
	}
	if ctx == nil {
		return errors.New("wails activation context is required")
	}
	select {
	case <-readiness.done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// Activated 返回一次性 ApplicationStarted 转换是否已经发生。
func (readiness *ActivationReadiness) Activated() bool {
	return readiness != nil && readiness.activated.Load()
}
