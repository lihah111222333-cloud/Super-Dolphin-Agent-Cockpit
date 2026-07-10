package notify

import (
	"context"
	"log/slog"
	"strconv"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// DispatchRetryAlertNotifier 把 DAG 派发重试阈值事件转换成平台通知。
type DispatchRetryAlertNotifier struct {
	logger   *slog.Logger
	notifier contract.MessageNotifier
	store    taskdag.DAGDetailStore
}

// NewDispatchRetryAlertNotifier 创建派发重试告警器；logger 为空时使用全局 logger。
// notifier 或 store 可以为空，运行时会按 drop 语义跳过，不阻断 DAG 调度主路径。
func NewDispatchRetryAlertNotifier(logger *slog.Logger, notifier contract.MessageNotifier, store taskdag.DAGDetailStore) *DispatchRetryAlertNotifier {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &DispatchRetryAlertNotifier{logger: logger, notifier: notifier, store: store}
}

// provideDispatchRetryAlertSink 把通知器收窄为 orchestration.DispatchRetryAlertSink 端口。
func provideDispatchRetryAlertSink(logger *slog.Logger, notifier contract.MessageNotifier, store taskdag.DAGDetailStore) orchestration.DispatchRetryAlertSink {
	return NewDispatchRetryAlertNotifier(logger, notifier, store)
}

// AlertDispatchRetry 在节点配置或 DAG metadata 指定通知别名时发送重试告警。
// 缺 notifier、缺 dag/node key 或未配置 alias 都按可观测 drop 处理，不让通知失败影响调度。
func (n *DispatchRetryAlertNotifier) AlertDispatchRetry(ctx context.Context, alert orchestration.DispatchRetryAlert) error {
	if n == nil || n.notifier == nil {
		return nil
	}
	dagKey := strings.TrimSpace(alert.DagKey)
	nodeKey := strings.TrimSpace(alert.NodeKey)
	if dagKey == "" || nodeKey == "" {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	node := n.findNode(ctx, dagKey, nodeKey)
	dag := n.getDAG(ctx, dagKey)
	alias := resolveNodeAlias(node, dag)
	if alias == "" {
		n.logger.Debug("notify(orch): no alias configured for dispatch retry alert",
			slog.String("dag_key", dagKey),
			slog.String("node_key", nodeKey))
		return nil
	}
	msg := contract.NotifyMessage{
		Title: "DAG node retry threshold: " + nodeKey,
		Body:  buildDispatchRetryAlertBody(alert, node, dag),
		Level: contract.NotifyLevelWarn,
	}
	return n.notifier.TryEnqueue(ctx, contract.NotifyRequest{
		ChannelAlias: alias,
		Message:      msg,
	})
}

// findNode 读取告警节点，用于补标题和解析 node.config.notify_channel。
func (n *DispatchRetryAlertNotifier) findNode(ctx context.Context, dagKey, nodeKey string) *taskdag.Node {
	if n.store == nil {
		return nil
	}
	nodes, err := n.store.ListNodes(ctx, dagKey)
	if err != nil {
		n.logger.Debug("notify(orch): list nodes failed for retry alert",
			slog.String("dag_key", dagKey),
			slog.String("error", err.Error()))
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

// getDAG 读取告警所属 DAG，用于补标题和解析 dag.metadata.notify_channel。
func (n *DispatchRetryAlertNotifier) getDAG(ctx context.Context, dagKey string) *taskdag.DAG {
	if n.store == nil {
		return nil
	}
	dag, err := n.store.GetDAG(ctx, dagKey)
	if err != nil {
		n.logger.Debug("notify(orch): get dag failed for retry alert",
			slog.String("dag_key", dagKey),
			slog.String("error", err.Error()))
		return nil
	}
	return dag
}

// buildDispatchRetryAlertBody 构建派发重试告警正文，只包含定位和错误摘要。
func buildDispatchRetryAlertBody(alert orchestration.DispatchRetryAlert, node *taskdag.Node, dag *taskdag.DAG) string {
	var b strings.Builder
	b.WriteString("DAG: ")
	b.WriteString(strings.TrimSpace(alert.DagKey))
	if dag != nil && strings.TrimSpace(dag.Title) != "" {
		b.WriteString(" (")
		b.WriteString(strings.TrimSpace(dag.Title))
		b.WriteString(")")
	}
	b.WriteString("\nNode: ")
	b.WriteString(strings.TrimSpace(alert.NodeKey))
	if node != nil && strings.TrimSpace(node.Title) != "" {
		b.WriteString(" (")
		b.WriteString(strings.TrimSpace(node.Title))
		b.WriteString(")")
	}
	b.WriteString("\nRetry attempts: ")
	b.WriteString(strconv.FormatInt(int64(alert.AttemptCount), 10))
	if alert.RetryCount > 0 {
		b.WriteString("\nProcess retry count: ")
		b.WriteString(strconv.FormatInt(alert.RetryCount, 10))
	}
	if errText := strings.TrimSpace(alert.LastError); errText != "" {
		b.WriteString("\nLast error: ")
		b.WriteString(errText)
	}
	return b.String()
}

var _ orchestration.DispatchRetryAlertSink = (*DispatchRetryAlertNotifier)(nil)
