package notify

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kelindar/event"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	taskdto "github.com/anthropic-ai/super-agent-v3/internal/dto/task"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	"github.com/anthropic-ai/super-agent-v3/internal/util/ctxutil"
	"github.com/anthropic-ai/super-agent-v3/internal/util/safego"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// dagNotifyDrainGrace 限制 DAGNotifier worker 的停止等待时间。
// 即使 store 查询卡住，fx.Lifecycle.OnStop 也不会无限挂起。
const dagNotifyDrainGrace = 10 * time.Second

// defaultDAGNotifyQueueCapacity 是 DAG 通知 worker 的内存队列默认容量。
const defaultDAGNotifyQueueCapacity = 1024

// dagNotifyProcessTimeout 限制单个通知事件的查库和入队周期。
// 某次 store 调用卡住时，后续 DAG 通知不会被永久阻塞。
const dagNotifyProcessTimeout = 5 * time.Second

// DAGNotifierOption 调整 DAGNotifier 的运行参数。
type DAGNotifierOption func(*DAGNotifier)

// WithDAGNotifyQueueCapacity 设置 DAG 通知队列容量，非正数直接报错。
func WithDAGNotifyQueueCapacity(capacity int) (DAGNotifierOption, error) {
	if capacity <= 0 {
		return nil, fmt.Errorf("notify(orch): dag notifier queue capacity must be positive, got %d", capacity)
	}
	return func(n *DAGNotifier) {
		n.queueCapacity = capacity
	}, nil
}

// dagNotifyRequest 是 bus 回调放入内存队列的工作单元。
type dagNotifyRequest struct {
	ev taskdto.TaskNodeStatusChanged
}

// DAGNotifier 保存 orch 侧 DAG 通知订阅器状态。
// bus 回调只做轻量校验和内存入队，单 worker goroutine 负责查库与 TryEnqueue，避免 dispatcher 回调线程执行同步 I/O。
type DAGNotifier struct {
	logger   *slog.Logger
	notifier contract.MessageNotifier
	store    taskdag.Store

	mu            sync.Mutex
	queue         []dagNotifyRequest
	queueCapacity int

	wake chan struct{}

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}

	skipped       atomic.Int64
	enqueueErrors atomic.Int64
	enqueued      atomic.Int64
	dropped       atomic.Int64
}

// NewDAGNotifier 装配 orch 侧 DAG 通知器。
// store 为空时仍允许启动，后续事件会记录并丢弃，避免通知模块反向阻断编排进程。
func NewDAGNotifier(logger *slog.Logger, notifier contract.MessageNotifier, store taskdag.Store, opts ...DAGNotifierOption) *DAGNotifier {
	if logger == nil {
		logger = pkglogger.Get()
	}
	n := &DAGNotifier{
		logger:        logger,
		notifier:      notifier,
		store:         store,
		queueCapacity: defaultDAGNotifyQueueCapacity,
		wake:          make(chan struct{}, 1),
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(n)
		}
	}
	return n
}

// Start 启动单个 worker goroutine；sync.Once 保证重复调用不会启动多个消费者。
func (n *DAGNotifier) Start() {
	if n == nil {
		return
	}
	n.startOnce.Do(func() {
		safego.Go(context.Background(), nil, "mcp-orch.notify.dagNotifier", func(context.Context) {
			n.runWorker()
		})
	})
}

// Stop 关闭入口并等待 worker drain 队列；等待时间受 ctx 和 dagNotifyDrainGrace 双重限制。
func (n *DAGNotifier) Stop(ctx context.Context) error {
	if n == nil {
		return nil
	}
	var firstErr error
	n.stopOnce.Do(func() {
		close(n.stopCh)
		waitCtx := ctx
		if waitCtx == nil {
			waitCtx = context.Background()
		}
		if deadline, ok := waitCtx.Deadline(); !ok || time.Until(deadline) > dagNotifyDrainGrace {
			var cancel context.CancelFunc
			waitCtx, cancel = ctxutil.WithTimeout(waitCtx, dagNotifyDrainGrace)
			defer cancel()
			_ = deadline
		}
		select {
		case <-n.doneCh:
		case <-waitCtx.Done():
			firstErr = waitCtx.Err()
		}
	})
	return firstErr
}

// Run 实现 platformrunner.Runner，把 worker 生命周期交给 RunGroup 管理。
func (n *DAGNotifier) Run(ctx context.Context) error {
	if n == nil {
		<-ctx.Done()
		return nil
	}
	n.Start()
	<-ctx.Done()
	cleanupCtx, cancel := ctxutil.WithTimeout(context.Background(), dagNotifyDrainGrace)
	defer cancel()
	return n.Stop(cleanupCtx)
}

// Subscribe 注册 DAG 节点状态事件订阅，并返回统一取消函数。
// 这里只订阅 orch dispatcher 上的 DAG 事件；core turn 终态事件走 hook consumer 的 NotifyTap 链路。
func (n *DAGNotifier) Subscribe(dispatcher *event.Dispatcher, logger *pkglogger.Logger) context.CancelFunc {
	if n == nil || dispatcher == nil || n.notifier == nil {
		return func() {}
	}
	cancels := []context.CancelFunc{
		platformbus.ResilientSubscribe(dispatcher, n.onNodeStatusChanged, logger),
	}
	return func() {
		for _, c := range cancels {
			c()
		}
	}
}

// onNodeStatusChanged 是 DAG 节点状态变更的 bus 回调。
// 回调线程只做轻量校验和入队，DB 查询与 TryEnqueue 统一交给 worker，避免阻塞 dispatcher。
func (n *DAGNotifier) onNodeStatusChanged(ev taskdto.TaskNodeStatusChanged) {
	if !isTerminalNodeStatus(ev.NewStatus) {
		return
	}
	if strings.TrimSpace(ev.DagKey) == "" || strings.TrimSpace(ev.NodeKey) == "" {
		return
	}
	select {
	case <-n.stopCh:
		return
	default:
	}
	n.mu.Lock()
	if len(n.queue) >= n.queueCapacity {
		n.mu.Unlock()
		n.dropped.Add(1)
		n.logger.Warn("notify(orch): dag notifier queue full; dropping event",
			slog.String("dag_key", strings.TrimSpace(ev.DagKey)),
			slog.String("node_key", strings.TrimSpace(ev.NodeKey)),
		)
		return
	}
	n.queue = append(n.queue, dagNotifyRequest{ev: ev})
	n.mu.Unlock()
	select {
	case n.wake <- struct{}{}:
	default:
	}
}

// processEvent 在 worker goroutine 内处理单个通知事件。
// 它负责带超时查 node/DAG、解析 alias 并入通知队列，失败只影响该事件。
func (n *DAGNotifier) processEvent(ev taskdto.TaskNodeStatusChanged) {
	dagKey := strings.TrimSpace(ev.DagKey)
	nodeKey := strings.TrimSpace(ev.NodeKey)
	ctx, cancel := ctxutil.WithTimeout(context.Background(), dagNotifyProcessTimeout)
	defer cancel()
	node := n.findNode(ctx, dagKey, nodeKey)
	dag := n.getDAG(ctx, dagKey)
	alias := resolveNodeAlias(node, dag)
	if alias == "" {
		n.skipped.Add(1)
		n.logger.Debug("notify(orch): no alias configured for dag node",
			slog.String("dag_key", dagKey),
			slog.String("node_key", nodeKey),
			slog.String("new_status", ev.NewStatus),
		)
		return
	}
	msg := contract.NotifyMessage{
		Title: nodeTerminalTitle(ev),
		Body:  buildNodeBody(ev, node, dag),
		Level: levelForNodeStatus(ev.NewStatus),
	}
	if err := n.notifier.TryEnqueue(ctx, contract.NotifyRequest{
		ChannelAlias: alias,
		Message:      msg,
	}); err != nil {
		n.enqueueErrors.Add(1)
		n.logger.Warn("notify(orch): enqueue failed",
			slog.String("dag_key", dagKey),
			slog.String("node_key", nodeKey),
			slog.String("alias", alias),
			slog.String("error", err.Error()),
		)
		return
	}
	n.enqueued.Add(1)
}

// runWorker 等待 wake 信号或 stop 信号，stop 时先 drain 剩余事件再退出。
func (n *DAGNotifier) runWorker() {
	defer close(n.doneCh)
	for {
		select {
		case <-n.stopCh:
			n.drainPending()
			return
		case <-n.wake:
			n.drainPending()
		}
	}
}

// drainPending 取出当前队列快照并逐条处理，处理期间释放锁允许新事件继续入队。
func (n *DAGNotifier) drainPending() {
	for {
		n.mu.Lock()
		if len(n.queue) == 0 {
			n.mu.Unlock()
			return
		}
		reqs := n.queue
		n.queue = nil
		n.mu.Unlock()
		for _, req := range reqs {
			n.processEvent(req.ev)
		}
	}
}

// findNode 读取事件对应的节点行。
// 当前 store 只暴露 ListNodes，未命中或查询失败都按无节点处理，避免通知 worker 阻断 DAG 状态推进。
func (n *DAGNotifier) findNode(ctx context.Context, dagKey, nodeKey string) *taskdag.Node {
	if n.store == nil {
		return nil
	}
	nodes, err := n.store.ListNodes(ctx, dagKey)
	if err != nil {
		n.logger.Debug("notify(orch): list nodes failed",
			slog.String("dag_key", dagKey),
			slog.String("error", err.Error()),
		)
		return nil
	}
	for i := range nodes {
		if nodes[i].NodeKey == nodeKey {
			out := nodes[i]
			return &out
		}
	}
	return nil
}

// getDAG 读取 DAG 行用于通知正文和 alias 解析；查询失败按 drop 处理。
func (n *DAGNotifier) getDAG(ctx context.Context, dagKey string) *taskdag.DAG {
	if n.store == nil {
		return nil
	}
	dag, err := n.store.GetDAG(ctx, dagKey)
	if err != nil {
		n.logger.Debug("notify(orch): get dag failed",
			slog.String("dag_key", dagKey),
			slog.String("error", err.Error()),
		)
		return nil
	}
	return dag
}

// buildNodeBody 组装节点终态通知正文。
// 正文只放关键定位字段，不展开 node.Result 或 dag.Metadata，降低用户数据误发风险。
func buildNodeBody(ev taskdto.TaskNodeStatusChanged, node *taskdag.Node, dag *taskdag.DAG) string {
	var b strings.Builder
	b.WriteString("DAG: ")
	b.WriteString(strings.TrimSpace(ev.DagKey))
	if dag != nil && strings.TrimSpace(dag.Title) != "" {
		b.WriteString(" (")
		b.WriteString(strings.TrimSpace(dag.Title))
		b.WriteString(")")
	}
	b.WriteString("\nNode: ")
	b.WriteString(strings.TrimSpace(ev.NodeKey))
	if node != nil && strings.TrimSpace(node.Title) != "" {
		b.WriteString(" (")
		b.WriteString(strings.TrimSpace(node.Title))
		b.WriteString(")")
	}
	b.WriteString("\nStatus: ")
	if old := strings.TrimSpace(ev.OldStatus); old != "" {
		b.WriteString(old)
		b.WriteString(" → ")
	}
	b.WriteString(strings.TrimSpace(ev.NewStatus))
	if turn := strings.TrimSpace(ev.ActiveTurnID); turn != "" {
		b.WriteString("\nTurn: ")
		b.WriteString(turn)
	}
	return b.String()
}

// levelForNodeStatus 把节点终态映射为通知级别。
// 失败和错误走 error，取消走 warn，其余完成态保持 info。
func levelForNodeStatus(status string) contract.NotifyLevel {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "error":
		return contract.NotifyLevelError
	case "cancelled", "canceled":
		return contract.NotifyLevelWarn
	default:
		return contract.NotifyLevelInfo
	}
}

// Metrics 是 DAG 通知订阅器的只读计数器快照。
// dashboard 或 metrics 端点读取它时不需要持有 worker 锁。
type Metrics struct {
	Skipped       int64
	Enqueued      int64
	EnqueueErrors int64
	Dropped       int64
}

// Metrics 返回订阅器计数器快照，供 dashboard 或 metrics 端点读取。
func (n *DAGNotifier) Metrics() Metrics {
	if n == nil {
		return Metrics{}
	}
	return Metrics{
		Skipped:       n.skipped.Load(),
		Enqueued:      n.enqueued.Load(),
		EnqueueErrors: n.enqueueErrors.Load(),
		Dropped:       n.dropped.Load(),
	}
}
