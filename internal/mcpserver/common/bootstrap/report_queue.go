package bootstrap

import (
	"context"
	"errors"
	"log"
	"strings"

	"github.com/creachadair/jrpc2"

	"github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

func (c *Client) normalizeReportRequest(req mcp.ReportRequest) mcp.ReportRequest {
	req.ReportID = strings.TrimSpace(req.ReportID)
	if req.ReportID == "" {
		req.ReportID = generateID("ctl_report")
	}
	req.Lease = mcp.LeaseKey{}
	req.Report = cloneReportEnvelope(req.Report)
	if req.Report.Type == "" {
		req.Report.Type = guessReportVariant(req)
	}
	return req
}

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
		return errors.New("bootstrap: durable report queue is full")
	}
	c.reportQueue = append(c.reportQueue, cloneReportRequest(req))
	return nil
}

func (c *Client) flushQueuedReports(ctx context.Context) {
	conn, degraded := c.currentConn()
	if conn == nil || degraded {
		return
	}
	c.flushQueuedReportsWithConn(ctx, conn, c.currentLease())
}

func (c *Client) flushQueuedReportsWithConn(ctx context.Context, conn *jrpc2.Client, lease mcp.LeaseKey) {
	for _, req := range c.snapshotQueuedReports() {
		if _, err := c.sendReportWithConn(ctx, conn, lease, req); err != nil {
			if isTransportErr(err) {
				return
			}
			log.Printf("bootstrap report replay dropped: instance=%s report_id=%s err=%v", c.instanceID, req.ReportID, err)
		}
		c.dropQueuedReport(req.ReportID)
	}
}

func (c *Client) sendReportWithConn(ctx context.Context, conn *jrpc2.Client, lease mcp.LeaseKey, req mcp.ReportRequest) (*mcp.ReportResponse, error) {
	if conn == nil {
		return nil, errors.New("bootstrap: nil rpc client")
	}
	req = cloneReportRequest(req)
	req.Lease = lease
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

func queuedReportResponse(req mcp.ReportRequest) *mcp.ReportResponse {
	return &mcp.ReportResponse{
		Accepted:        false,
		CanonicalStatus: "queued_offline",
		AppliedVariant:  guessReportVariant(req),
	}
}

func guessReportVariant(req mcp.ReportRequest) string {
	if strings.TrimSpace(req.Report.Type) != "" {
		return strings.TrimSpace(req.Report.Type)
	}
	switch {
	case req.Report.Runtime != nil:
		return "runtime"
	case req.Report.Completion != nil:
		return "completion"
	case req.Report.Progress != nil:
		return "progress"
	case req.Report.Diagnostic != nil:
		return "diagnostic"
	default:
		return "completion"
	}
}

func cloneReportRequest(req mcp.ReportRequest) mcp.ReportRequest {
	req.Report = cloneReportEnvelope(req.Report)
	return req
}

func cloneReportEnvelope(in mcp.ReportEnvelope) mcp.ReportEnvelope {
	out := in
	if in.Runtime != nil {
		copied := *in.Runtime
		out.Runtime = &copied
	}
	if in.Completion != nil {
		copied := *in.Completion
		copied.Metadata = cloneRaw(copied.Metadata)
		out.Completion = &copied
	}
	if in.Progress != nil {
		copied := *in.Progress
		out.Progress = &copied
	}
	if in.Diagnostic != nil {
		copied := *in.Diagnostic
		copied.Details = cloneRaw(copied.Details)
		out.Diagnostic = &copied
	}
	return out
}

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
