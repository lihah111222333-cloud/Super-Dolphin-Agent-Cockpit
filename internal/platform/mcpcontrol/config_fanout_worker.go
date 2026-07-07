package mcpcontrol

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// configFanoutNotifier 是 worker 需要的 ToolNotifier 最小能力。
// 测试只需 fake NotifyConfigChanged，不必实现其他通知方法。
type configFanoutNotifier interface {
	NotifyConfigChanged(ctx context.Context, topic string, scope *dto.SelectorScope, configVersion int64, payload json.RawMessage) error
}

// config fanout 队列与退化通知常量。
const (
	configFanoutWorkerDrainGrace = 10 * time.Second
	configFanoutPendingLimit     = 4
	configFanoutDegradedEvent    = "platform/queue/degraded"
	configFanoutDegradedQueueID  = "config_fanout"
)

type configFanoutRequest struct {
	topic    string
	payload  map[string]any
	degraded bool
	dropped  int64
}

// configFanoutWorker 串行处理配置变更事件到 ToolNotifier 的 fanout。
// bus callback 只入队，worker 负责推进版本、marshal payload 和调用 peer；Stop(ctx) 会取消 fanoutCtx 并等待队列排空。
type configFanoutWorker struct {
	notifier configFanoutNotifier
	versions configVersionSource
	logger   *pkglogger.Logger

	fanoutCtx    context.Context
	fanoutCancel context.CancelFunc

	mu    sync.Mutex
	queue []configFanoutRequest

	wake chan struct{}

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}

	enqueuedTotal  atomic.Int64
	processedTotal atomic.Int64
}

// newConfigFanoutWorker 创建配置 fanout worker 并为 peer 通知准备可取消 context。
func newConfigFanoutWorker(notifier configFanoutNotifier, versions configVersionSource, logger *pkglogger.Logger) *configFanoutWorker {
	if logger == nil {
		logger = pkglogger.Get()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &configFanoutWorker{
		notifier:     notifier,
		versions:     versions,
		logger:       logger,
		fanoutCtx:    ctx,
		fanoutCancel: cancel,
		wake:         make(chan struct{}, 1),
		stopCh:       make(chan struct{}),
		doneCh:       make(chan struct{}),
	}
}

// Start 启动单 worker goroutine。
// notifier 或 versions 缺失时立即关闭 doneCh，Enqueue 保持廉价丢弃，避免半初始化 fanout。
func (w *configFanoutWorker) Start() {
	if w == nil {
		return
	}
	w.startOnce.Do(func() {
		if w.notifier == nil || w.versions == nil {
			close(w.doneCh)
			return
		}
		var workerWG sync.WaitGroup
		workerWG.Go(func() {
			defer func() {
				if rec := recover(); rec != nil {
					pkglogger.Error("mcpcontrol: recovered config_fanout_worker panic", "panic", rec)
				}
			}()
			w.runWorker()
		})
	})
}

// Enqueue 记录一次配置变更 fanout 请求。
// 它适合 bus callback 调用，只做非阻塞唤醒；Stop 后的新请求会被丢弃以封住关闭入口。
func (w *configFanoutWorker) Enqueue(topic string, payload map[string]any) {
	if w == nil {
		return
	}
	if strings.TrimSpace(topic) == "" {
		return
	}
	select {
	case <-w.stopCh:
		return
	default:
	}
	w.mu.Lock()
	w.enqueueLocked(configFanoutRequest{topic: topic, payload: payload})
	w.mu.Unlock()
	w.enqueuedTotal.Add(1)
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// enqueueLocked 在持锁状态下写入队列，满载时压缩为退化通知和最新配置。
func (w *configFanoutWorker) enqueueLocked(req configFanoutRequest) {
	if len(w.queue) > 0 && w.queue[0].degraded {
		w.queue = []configFanoutRequest{configFanoutDegradedRequest(req, w.queue[0].dropped+1), req}
		return
	}
	if len(w.queue) >= configFanoutPendingLimit {
		dropped := int64(len(w.queue))
		w.queue = []configFanoutRequest{configFanoutDegradedRequest(req, dropped), req}
		return
	}
	w.queue = append(w.queue, req)
}

// configFanoutDegradedRequest 构造配置 fanout 队列压缩后的显式退化请求。
func configFanoutDegradedRequest(latest configFanoutRequest, dropped int64) configFanoutRequest {
	return configFanoutRequest{
		topic:    latest.topic,
		degraded: true,
		dropped:  dropped,
		payload: map[string]any{
			"event":   configFanoutDegradedEvent,
			"queue":   configFanoutDegradedQueueID,
			"dropped": dropped,
			"mode":    "latest_only",
		},
	}
}

// FanoutCtx 返回传给 NotifyConfigChanged 的可取消 context。
// 暴露它是为了测试关闭取消链路，不允许外部直接修改 worker 内部状态。
func (w *configFanoutWorker) FanoutCtx() context.Context {
	if w == nil {
		return context.Background()
	}
	return w.fanoutCtx
}

// Stop 关闭入队入口、取消 fanoutCtx 并等待 worker 排空已接收请求。
// 等待时间受调用方 ctx 或 configFanoutWorkerDrainGrace 限制，重复调用只执行一次关闭动作。
func (w *configFanoutWorker) Stop(ctx context.Context) error {
	if w == nil {
		return nil
	}
	var firstErr error
	w.stopOnce.Do(func() {
		close(w.stopCh)
		w.fanoutCancel()
		waitCtx := ctx
		if waitCtx == nil {
			waitCtx = context.Background()
		}
		if deadline, ok := waitCtx.Deadline(); !ok || time.Until(deadline) > configFanoutWorkerDrainGrace {
			var cancel context.CancelFunc
			waitCtx, cancel = platformconfig.WithTimeout(waitCtx, configFanoutWorkerDrainGrace)
			defer cancel()
			_ = deadline
		}
		select {
		case <-w.doneCh:
		case <-waitCtx.Done():
			firstErr = waitCtx.Err()
		}
	})
	return firstErr
}

// EnqueuedTotal 返回已接收的配置变更请求数量。
func (w *configFanoutWorker) EnqueuedTotal() int64 { return w.enqueuedTotal.Load() }

// ProcessedTotal 返回已完成 fanout 的配置变更请求数量。
func (w *configFanoutWorker) ProcessedTotal() int64 { return w.processedTotal.Load() }

// runWorker 监听 wake/stop 信号并串行排空队列。
// stop 信号到达后仍会处理已经入队的请求，随后关闭 doneCh。
func (w *configFanoutWorker) runWorker() {
	defer close(w.doneCh)
	for {
		select {
		case <-w.stopCh:
			w.drainPending()
			return
		case <-w.wake:
			w.drainPending()
		}
	}
}

// drainPending 在锁内取出当前批次，再释放锁执行 peer 通知。
// FIFO 顺序很重要：configVersion 在 worker 中串行递增，peer 看到的版本顺序必须和事件入队顺序一致。
func (w *configFanoutWorker) drainPending() {
	for {
		w.mu.Lock()
		if len(w.queue) == 0 {
			w.mu.Unlock()
			return
		}
		reqs := w.queue
		w.queue = nil
		w.mu.Unlock()
		for _, req := range reqs {
			w.dispatch(req)
			w.processedTotal.Add(1)
		}
	}
}

// dispatch 序列化单个配置变更并通知匹配 peer。
// payload 无法 marshal 或 peer 通知失败只记录日志，不阻断后续配置事件；LSP scope 释放是同一事件的附加通知。
func (w *configFanoutWorker) dispatch(req configFanoutRequest) {
	raw, err := json.Marshal(req.payload)
	if err != nil {
		if w.logger != nil {
			w.logger.Warn("mcp config change marshal failed", "topic", req.topic, "err", err)
		}
		return
	}
	configVersion := w.versions.advanceConfigVersion()
	scope := configChangeSelectorScope(req.payload)
	if err := w.notifier.NotifyConfigChanged(w.fanoutCtx, req.topic, scope, configVersion, raw); err != nil {
		if w.logger != nil {
			w.logger.Warn("mcp config change notify failed", "topic", req.topic, "config_version", configVersion, "err", err)
		}
	}
	if dispatcher, ok := w.notifier.(lspReleaseScopeDispatcher); ok {
		if releaseReq, shouldDispatch := releaseScopeRequestFromConfigPayload(req.payload); shouldDispatch {
			if _, err := dispatcher.DispatchLSPReleaseScope(w.fanoutCtx, releaseReq); err != nil && w.logger != nil {
				w.logger.Warn("mcp lsp release-scope dispatch failed", "topic", req.topic, "err", err)
			}
		}
	}
}
