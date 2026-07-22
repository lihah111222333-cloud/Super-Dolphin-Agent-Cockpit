package wails

import (
	"context"
	"errors"
	"sync"
)

// ActivationReadiness 持有同一 Wails activation epoch 的原生启动和前端 RPC ready 状态。
type ActivationReadiness struct {
	mu              sync.Mutex
	epoch           uint64
	frontendEpoch   uint64
	applicationDone chan struct{}
	frontendDone    chan struct{}
}

// NewActivationReadiness 创建尚未激活的 Wails readiness owner。
func NewActivationReadiness() *ActivationReadiness {
	return &ActivationReadiness{
		applicationDone: make(chan struct{}),
		frontendDone:    make(chan struct{}),
	}
}

// MarkApplicationStarted 只记录首次真实 Wails ApplicationStarted 事件，并开启 epoch 1。
func (readiness *ActivationReadiness) MarkApplicationStarted() {
	if readiness == nil {
		return
	}
	readiness.mu.Lock()
	defer readiness.mu.Unlock()
	if readiness.epoch != 0 {
		return
	}
	readiness.epoch = 1
	close(readiness.applicationDone)
}

// Wait 阻塞到 Wails 发出 ApplicationStarted，或 owner context 结束。
func (readiness *ActivationReadiness) Wait(ctx context.Context) error {
	if readiness == nil || readiness.applicationDone == nil {
		return errors.New("wails activation readiness is required")
	}
	if ctx == nil {
		return errors.New("wails activation context is required")
	}
	select {
	case <-readiness.applicationDone:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// WaitForFrontendReady 阻塞到当前 epoch 的前端完成真实 RPC ready，或 owner context 结束。
func (readiness *ActivationReadiness) WaitForFrontendReady(ctx context.Context) error {
	if readiness == nil || readiness.frontendDone == nil {
		return errors.New("wails frontend readiness is required")
	}
	if ctx == nil {
		return errors.New("wails frontend readiness context is required")
	}
	select {
	case <-readiness.frontendDone:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// CurrentEpoch 返回当前 native ApplicationStarted 所属的 readiness epoch。
func (readiness *ActivationReadiness) CurrentEpoch() (uint64, error) {
	if readiness == nil {
		return 0, errors.New("wails activation readiness is required")
	}
	readiness.mu.Lock()
	defer readiness.mu.Unlock()
	if readiness.epoch == 0 {
		return 0, errors.New("wails ApplicationStarted is required before frontend readiness")
	}
	return readiness.epoch, nil
}

// MarkFrontendReady 只接受当前 activation epoch 的前端 RPC ready；同 epoch 重复信号幂等。
func (readiness *ActivationReadiness) MarkFrontendReady(epoch uint64) error {
	if readiness == nil {
		return errors.New("wails frontend readiness is required")
	}
	if epoch == 0 {
		return errors.New("wails frontend readiness epoch is required")
	}
	readiness.mu.Lock()
	defer readiness.mu.Unlock()
	if readiness.epoch == 0 {
		return errors.New("wails ApplicationStarted is required before frontend readiness")
	}
	if epoch != readiness.epoch {
		return errors.New("wails frontend readiness epoch does not match current activation")
	}
	if readiness.frontendEpoch == 0 {
		readiness.frontendEpoch = epoch
		close(readiness.frontendDone)
		return nil
	}
	if readiness.frontendEpoch != epoch {
		return errors.New("wails frontend readiness epoch does not match committed activation")
	}
	return nil
}

// Activated 返回一次性 ApplicationStarted 转换是否已经发生。
func (readiness *ActivationReadiness) Activated() bool {
	if readiness == nil {
		return false
	}
	readiness.mu.Lock()
	defer readiness.mu.Unlock()
	return readiness.epoch != 0
}

// FrontendReady 返回当前 activation epoch 是否已经收到有效前端 RPC ready。
func (readiness *ActivationReadiness) FrontendReady() bool {
	if readiness == nil {
		return false
	}
	readiness.mu.Lock()
	defer readiness.mu.Unlock()
	return readiness.frontendEpoch != 0
}
