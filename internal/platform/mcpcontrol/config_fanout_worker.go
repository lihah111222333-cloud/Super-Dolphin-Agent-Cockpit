package mcpcontrol

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
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
	configFanoutReleaseLimit     = 64
	lspReleaseDispatchAttempts   = 3
	lspReleaseRetryDelay         = 50 * time.Millisecond
)

type configFanoutRequest struct {
	topic    string
	payload  map[string]any
	degraded bool
	dropped  int64

	releaseKey  string
	releaseOnly bool
}

// configFanoutWorker 串行处理配置变更事件到 ToolNotifier 的 fanout。
// 普通配置只入队；release 使用有界去重和显式背压，Stop(ctx) 会等待关键释放请求排空。
type configFanoutWorker struct {
	notifier configFanoutNotifier
	versions configVersionSource
	logger   *pkglogger.Logger

	fanoutCtx     context.Context
	fanoutCancel  context.CancelFunc
	releaseCtx    context.Context
	releaseCancel context.CancelFunc

	mu           sync.Mutex
	queue        []configFanoutRequest
	releaseKeys  map[string]struct{}
	releaseSpace *sync.Cond
	stopping     bool

	wake chan struct{}

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}
	stopErr   error

	enqueuedTotal  atomic.Int64
	processedTotal atomic.Int64
}

// newConfigFanoutWorker 创建配置 fanout worker 并为 peer 通知准备可取消 context。
func newConfigFanoutWorker(notifier configFanoutNotifier, versions configVersionSource, logger *pkglogger.Logger) *configFanoutWorker {
	if logger == nil {
		logger = pkglogger.Get()
	}
	ctx, cancel := context.WithCancel(context.Background())
	releaseCtx, releaseCancel := context.WithCancel(context.Background())
	worker := &configFanoutWorker{
		notifier:      notifier,
		versions:      versions,
		logger:        logger,
		fanoutCtx:     ctx,
		fanoutCancel:  cancel,
		releaseCtx:    releaseCtx,
		releaseCancel: releaseCancel,
		releaseKeys:   make(map[string]struct{}),
		wake:          make(chan struct{}, 1),
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
	worker.releaseSpace = sync.NewCond(&worker.mu)
	return worker
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
// 普通配置保持非阻塞压缩；release 达到唯一 scope 上限时阻塞施加背压，Stop 会唤醒并拒绝新请求。
func (w *configFanoutWorker) Enqueue(topic string, payload map[string]any) {
	if w == nil {
		return
	}
	if strings.TrimSpace(topic) == "" {
		return
	}
	req := configFanoutRequest{topic: topic, payload: payload}
	req.releaseKey = configFanoutReleaseKey(req)
	w.mu.Lock()
	for req.releaseKey != "" && !w.stopping {
		if _, exists := w.releaseKeys[req.releaseKey]; exists {
			w.mu.Unlock()
			return
		}
		if len(w.releaseKeys) < configFanoutReleaseLimit {
			w.releaseKeys[req.releaseKey] = struct{}{}
			break
		}
		w.releaseSpace.Wait()
	}
	if w.stopping {
		w.mu.Unlock()
		return
	}
	w.enqueueLocked(req)
	w.mu.Unlock()
	w.enqueuedTotal.Add(1)
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// enqueueLocked 在持锁状态下写入队列，满载时只压缩可丢弃的普通配置通知。
// agent/thread stop 同时承载资源释放意图，必须逐条保留，不能被 latest-only 语义覆盖。
func (w *configFanoutWorker) enqueueLocked(req configFanoutRequest) {
	if isLSPReleaseFanoutRequest(req) {
		w.queue = append(w.queue, req)
		return
	}
	degraded, discardable := configFanoutQueueState(w.queue)
	if degraded > 0 {
		w.queue = compactConfigFanoutQueue(w.queue, configFanoutDegradedRequest(req, degraded+1), req)
		return
	}
	if discardable >= configFanoutPendingLimit {
		w.queue = compactConfigFanoutQueue(w.queue, configFanoutDegradedRequest(req, int64(discardable)), req)
		return
	}
	w.queue = append(w.queue, req)
}

func isLSPReleaseFanoutRequest(req configFanoutRequest) bool {
	if req.releaseKey != "" {
		return true
	}
	_, ok := releaseScopeRequestFromConfigPayload(req.payload)
	return ok
}

func configFanoutReleaseKey(req configFanoutRequest) string {
	releaseReq, ok := releaseScopeRequestFromConfigPayload(req.payload)
	if !ok {
		return ""
	}
	releaseReq = normalizeLSPReleaseScopeRequest(releaseReq)
	return strings.Join([]string{
		releaseReq.ScopeKind,
		releaseReq.AgentID,
		releaseReq.ThreadID,
		releaseReq.ManagerKey,
	}, "\x00")
}

func configFanoutQueueState(queue []configFanoutRequest) (int64, int) {
	var degraded int64
	discardable := 0
	for _, queued := range queue {
		if isLSPReleaseFanoutRequest(queued) {
			continue
		}
		discardable++
		if queued.degraded {
			degraded = max(degraded, queued.dropped)
		}
	}
	return degraded, discardable
}

func compactConfigFanoutQueue(queue []configFanoutRequest, degraded, latest configFanoutRequest) []configFanoutRequest {
	compacted := make([]configFanoutRequest, 0, len(queue)+2)
	for _, queued := range queue {
		if isLSPReleaseFanoutRequest(queued) {
			compacted = append(compacted, queued)
		}
	}
	return append(compacted, degraded, latest)
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

// Stop 原子封闭入队入口、取消普通 fanout，并用独立 release context 排空资源释放请求。
// 等待时间受调用方 ctx 或 configFanoutWorkerDrainGrace 限制，重复调用只执行一次关闭动作。
func (w *configFanoutWorker) Stop(ctx context.Context) error {
	if w == nil {
		return nil
	}
	w.stopOnce.Do(func() {
		w.mu.Lock()
		w.stopping = true
		close(w.stopCh)
		w.releaseSpace.Broadcast()
		w.mu.Unlock()
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
			w.stopErr = waitCtx.Err()
			w.releaseCancel()
		}
		w.releaseCancel()
	})
	return w.stopErr
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
			if !w.drainPending() {
				return
			}
		}
	}
}

// drainPending 在锁内取出当前批次，再释放锁执行 peer 通知。
// FIFO 顺序很重要：configVersion 在 worker 中串行递增，peer 看到的版本顺序必须和事件入队顺序一致。
func (w *configFanoutWorker) drainPending() bool {
	for {
		w.mu.Lock()
		if len(w.queue) == 0 {
			w.mu.Unlock()
			return true
		}
		reqs := w.queue
		w.queue = nil
		w.mu.Unlock()
		retryPending := false
		for _, req := range reqs {
			if w.dispatch(req) {
				w.processedTotal.Add(1)
				w.completeRelease(req)
				continue
			}
			if w.releaseCtx.Err() != nil {
				return false
			}
			req.releaseOnly = true
			w.requeueRelease(req)
			retryPending = true
		}
		if retryPending {
			timer := time.NewTimer(lspReleaseRetryDelay)
			select {
			case <-w.releaseCtx.Done():
				timer.Stop()
				return false
			case <-timer.C:
			}
		}
	}
}

// dispatch 序列化单个配置变更并通知匹配 peer。
// 普通配置失败只记录日志；LSP release 只有确认 drained 后才算完成，否则保留 key 并重排。
func (w *configFanoutWorker) dispatch(req configFanoutRequest) bool {
	if !req.releaseOnly {
		raw, err := json.Marshal(req.payload)
		if err != nil {
			if w.logger != nil {
				w.logger.Warn("mcp config change marshal failed", "topic", req.topic, "err", err)
			}
		} else {
			configVersion := w.versions.advanceConfigVersion()
			scope := configChangeSelectorScope(req.payload)
			w.notifyConfigChanged(req, scope, configVersion, raw)
		}
	}
	if req.releaseKey == "" {
		return true
	}
	return w.dispatchLSPReleaseFromPayload(req)
}

func (w *configFanoutWorker) completeRelease(req configFanoutRequest) {
	if req.releaseKey == "" {
		return
	}
	w.mu.Lock()
	delete(w.releaseKeys, req.releaseKey)
	w.releaseSpace.Broadcast()
	w.mu.Unlock()
}

func (w *configFanoutWorker) requeueRelease(req configFanoutRequest) {
	w.mu.Lock()
	w.queue = append(w.queue, req)
	w.mu.Unlock()
}

// notifyConfigChanged 发送普通配置通知；失败保持可观测但不阻断后续队列项。
func (w *configFanoutWorker) notifyConfigChanged(req configFanoutRequest, scope *dto.SelectorScope, configVersion int64, raw json.RawMessage) {
	if err := w.notifier.NotifyConfigChanged(w.fanoutCtx, req.topic, scope, configVersion, raw); err != nil {
		if w.logger != nil {
			w.logger.Warn("mcp config change notify failed", "topic", req.topic, "config_version", configVersion, "err", err)
		}
	}
}

// dispatchLSPReleaseFromPayload 把 stop 事件转换为 LSP 释放请求；失败或 deferred 返回 false 触发重排。
func (w *configFanoutWorker) dispatchLSPReleaseFromPayload(req configFanoutRequest) bool {
	dispatcher, ok := w.notifier.(lspReleaseScopeDispatcher)
	if !ok {
		if w.logger != nil {
			w.logger.Warn("mcp lsp release-scope dispatcher unavailable", "topic", req.topic)
		}
		return false
	}
	releaseReq, shouldDispatch := releaseScopeRequestFromConfigPayload(req.payload)
	if !shouldDispatch {
		return true
	}
	result, err := w.dispatchLSPReleaseScope(dispatcher, releaseReq)
	if err != nil {
		if w.logger != nil {
			w.logger.Warn("mcp lsp release-scope dispatch failed", "topic", req.topic, "err", err)
		}
		return false
	}
	if releaseReq.Drain && !result.Drained && w.logger != nil {
		w.logger.Info("mcp lsp release-scope deferred",
			"topic", req.topic,
			"matched_managers", result.MatchedManagers,
			"busy_leases", result.BusyLeases,
		)
	}
	return !releaseReq.Drain || result.Drained
}

// dispatchLSPReleaseScope 对瞬态控制面失败执行有界重试，ctx 取消时立即停止。
func (w *configFanoutWorker) dispatchLSPReleaseScope(dispatcher lspReleaseScopeDispatcher, req dto.LSPReleaseScopeRequest) (dto.LSPReleaseScopeResult, error) {
	var result dto.LSPReleaseScopeResult
	var err error
	for attempt := 1; attempt <= lspReleaseDispatchAttempts; attempt++ {
		result, err = dispatcher.DispatchLSPReleaseScope(w.releaseCtx, req)
		if err == nil {
			return result, nil
		}
		if attempt == lspReleaseDispatchAttempts {
			break
		}
		timer := time.NewTimer(lspReleaseRetryDelay)
		select {
		case <-w.releaseCtx.Done():
			timer.Stop()
			return dto.LSPReleaseScopeResult{}, w.releaseCtx.Err()
		case <-timer.C:
		}
	}
	return dto.LSPReleaseScopeResult{}, err
}
