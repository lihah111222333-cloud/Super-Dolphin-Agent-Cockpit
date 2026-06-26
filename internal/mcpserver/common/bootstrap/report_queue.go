package bootstrap

import (
	"context"
	"errors"
	"strings"

	"github.com/creachadair/jrpc2"

	"github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/metrics"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// normalizeReportRequest 补全 ReportID、清空 lease 字段并推断 report 变体类型。
func (c *Client) normalizeReportRequest(req mcp.ReportRequest) mcp.ReportRequest {
	req.ReportID = strings.TrimSpace(req.ReportID)
	if req.ReportID == "" {
		req.ReportID = generateID("ctl_report")
	}
	req.InstanceID = ""
	req.Generation = 0
	req.Report = cloneReportEnvelope(req.Report)
	if req.Report.Type == "" {
		req.Report.Type = guessReportVariant(req)
	}
	return req
}

// enqueueReport 将报告加入离线队列，已存在同 ID 时更新；队列满则丢弃并计数。
func (c *Client) enqueueReport(req mcp.ReportRequest) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, queued := range c.reportQueue {
		if queued.ReportID == req.ReportID {
			c.reportQueue[i] = cloneReportRequest(req)
			return nil
		}
	}
	if len(c.reportQueue) >= c.reportQueueLimit {
		// 入队阶段的溢出单独计数；drain 阶段丢弃已有稳定日志锚点可关联。
		metrics.BootstrapReportQueueDropped.Inc()
		return errors.New("bootstrap: durable report queue is full")
	}
	c.reportQueue = append(c.reportQueue, cloneReportRequest(req))
	return nil
}

// flushQueuedReports 检查连接可用后委托给 flushQueuedReportsWithConn 发送队列。
func (c *Client) flushQueuedReports(ctx context.Context) {
	conn, degraded := c.currentConn()
	if conn == nil || degraded {
		return
	}
	c.flushQueuedReportsWithConn(ctx, conn, c.currentLease())
}

// flushQueuedReportsWithConn 用指定连接发送离线队列；传输断开时保留剩余报告等待下次重放。
func (c *Client) flushQueuedReportsWithConn(ctx context.Context, conn *jrpc2.Client, lease mcp.LeaseKey) {
	queued := c.snapshotQueuedReports()
	if len(queued) == 0 {
		return
	}
	// event 字段是 report queue drain 的稳定观测锚点，每次 drain 尝试只打一次。
	pkglogger.Info("bootstrap report queue drain",
		"event", "bootstrap.report_queue.drain",
		"instance_id", c.instanceID,
		"lease_key", lease,
		"queue_size", len(queued),
	)
	for _, req := range queued {
		if _, err := c.sendReportWithConn(ctx, conn, lease, req); err != nil {
			if isTransportErr(err) {
				return
			}
			pkglogger.Warn("bootstrap report replay dropped",
				"event", "bootstrap.report_queue.drain",
				"outcome", "dropped",
				"instance_id", c.instanceID,
				"lease_key", lease,
				"report_id", req.ReportID,
				"queue_size", len(queued),
				"error", err,
			)
		}
		c.dropQueuedReport(req.ReportID)
	}
}

// sendReportWithConn 绑定 lease 后发送报告，并补齐旧响应里的 Accepted/AppliedVariant 字段。
func (c *Client) sendReportWithConn(ctx context.Context, conn *jrpc2.Client, lease mcp.LeaseKey, req mcp.ReportRequest) (*mcp.ReportResponse, error) {
	if conn == nil {
		return nil, errors.New("bootstrap: nil rpc client")
	}
	req = cloneReportRequest(req)
	req.InstanceID = lease.InstanceID
	req.Generation = lease.Generation
	callCtx, cancel := withTimeoutIfNone(ctx, c.currentSendTimeout())
	defer cancel()
	var resp mcp.ReportResponse
	if err := conn.CallResult(callCtx, mcp.MethodReport, req, &resp); err != nil {
		return nil, err
	}
	if !resp.Accepted && resp.Success {
		resp.Accepted = true
	}
	if resp.AppliedVariant == "" {
		resp.AppliedVariant = guessReportVariant(req)
	}
	return &resp, nil
}

// finalReportRequest 在 Close() 时调用 FinalReport 回调并规范化结果，无回调时返回 nil。
func (c *Client) finalReportRequest() *mcp.ReportRequest {
	if c.cfg.FinalReport == nil {
		return nil
	}
	req := c.cfg.FinalReport()
	if req == nil {
		return nil
	}
	normalized := c.normalizeReportRequest(*req)
	return &normalized
}

// queuedReportResponse 构造离线排队状态的 ReportResponse。
func queuedReportResponse(req mcp.ReportRequest) *mcp.ReportResponse {
	return &mcp.ReportResponse{
		Accepted:        false,
		CanonicalStatus: "queued_offline",
		AppliedVariant:  guessReportVariant(req),
	}
}

// guessReportVariant 从 envelope 内容推断报告变体，作为旧调用未显式填 Type 时的兼容层。
func guessReportVariant(req mcp.ReportRequest) string {
	if strings.TrimSpace(req.Report.Type) != "" {
		return strings.TrimSpace(req.Report.Type)
	}
	switch {
	case req.Report.Runtime != nil:
		return mcp.ReportVariantRuntime
	case req.Report.Completion != nil:
		return mcp.ReportVariantCompletion
	case req.Report.Progress != nil:
		return mcp.ReportVariantProgress
	case req.Report.Diagnostic != nil:
		return mcp.ReportVariantDiagnostic
	default:
		return mcp.ReportVariantCompletion
	}
}

// cloneReportRequest 深拷贝 ReportRequest 及其内部 Envelope。
func cloneReportRequest(req mcp.ReportRequest) mcp.ReportRequest {
	req.Report = cloneReportEnvelope(req.Report)
	return req
}

// cloneReportEnvelope 深拷贝 ReportEnvelope 中各可变指针字段，防止并发修改。
func cloneReportEnvelope(in mcp.ReportEnvelope) mcp.ReportEnvelope {
	out := in
	if in.Runtime != nil {
		copied := *in.Runtime
		out.Runtime = &copied
	}
	if in.Completion != nil {
		copied := *in.Completion
		copied.Metadata = shared.CloneRawMessage(copied.Metadata)
		out.Completion = &copied
	}
	if in.Progress != nil {
		copied := *in.Progress
		out.Progress = &copied
	}
	if in.Diagnostic != nil {
		copied := *in.Diagnostic
		copied.Details = shared.CloneRawMessage(copied.Details)
		out.Diagnostic = &copied
	}
	return out
}

// snapshotQueuedReports 在读锁下深拷贝当前离线队列，供 flush 迭代使用。
func (c *Client) snapshotQueuedReports() []mcp.ReportRequest {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.reportQueue) == 0 {
		return nil
	}
	out := make([]mcp.ReportRequest, len(c.reportQueue))
	for i, req := range c.reportQueue {
		out[i] = cloneReportRequest(req)
	}
	return out
}

// dropQueuedReport 从离线队列中移除指定 report_id 的条目。
func (c *Client) dropQueuedReport(reportID string) {
	reportID = strings.TrimSpace(reportID)
	if reportID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, queued := range c.reportQueue {
		if queued.ReportID != reportID {
			continue
		}
		c.reportQueue = append(c.reportQueue[:i], c.reportQueue[i+1:]...)
		return
	}
}
