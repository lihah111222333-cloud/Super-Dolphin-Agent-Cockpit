package notify

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/sidecar/orch/orchestration"
	"github.com/anthropic-ai/super-agent-v3/internal/sidecar/orch/store/taskdag"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// DispatchRetryAlertNotifier describes notify integration data.
type DispatchRetryAlertNotifier struct {
	logger   *slog.Logger
	notifier contract.MessageNotifier
	store    taskdag.Store
}

// NewDispatchRetryAlertNotifier 创建dispatch重试alertnotifier。
func NewDispatchRetryAlertNotifier(logger *slog.Logger, notifier contract.MessageNotifier, store taskdag.Store) *DispatchRetryAlertNotifier {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &DispatchRetryAlertNotifier{logger: logger, notifier: notifier, store: store}
}

func provideDispatchRetryAlertSink(logger *slog.Logger, notifier contract.MessageNotifier, store taskdag.Store) orchestration.DispatchRetryAlertSink {
	return NewDispatchRetryAlertNotifier(logger, notifier, store)
}

// AlertDispatchRetry 处理alertdispatch重试。
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

// buildDispatchRetryAlertBody 构建dispatch重试alert正文。
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
	b.WriteString(fmt.Sprintf("%d", alert.AttemptCount))
	if alert.RetryCount > 0 {
		b.WriteString("\nProcess retry count: ")
		b.WriteString(fmt.Sprintf("%d", alert.RetryCount))
	}
	if errText := strings.TrimSpace(alert.LastError); errText != "" {
		b.WriteString("\nLast error: ")
		b.WriteString(errText)
	}
	return b.String()
}

var _ orchestration.DispatchRetryAlertSink = (*DispatchRetryAlertNotifier)(nil)
