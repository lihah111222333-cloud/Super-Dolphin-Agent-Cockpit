package thread

import (
	"context"
	"strings"
	"time"

	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	"github.com/anthropic-ai/super-agent-v3/internal/util"
	"github.com/anthropic-ai/super-agent-v3/internal/util/idgen"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

func (s *service) publishThreadStarted(state threadState) {
	if s == nil || s.emitStarted == nil {
		return
	}
	event := newThreadEvent(threadEventStartedKind, state.PublicThreadID, threadEventFields{State: state})
	if event == nil {
		return
	}
	s.emitStarted(event.(threaddto.Started))
}

func (s *service) publishThreadLaunched(state threadState) {
	if s == nil || s.emitLaunched == nil {
		return
	}
	event := newThreadEvent(threadEventLaunchedKind, state.PublicThreadID, threadEventFields{State: state})
	if event == nil {
		return
	}
	s.emitLaunched(event.(threaddto.Launched))
}

func (s *service) publishThreadStopped(threadID, agentID, status, reason string) {
	// Only WARN for non-intentional / suspect statuses; normal stops use Info.
	if status == statusArchived {
		pkglogger.Warn("thread: publishThreadStopped ARCHIVED",
			"thread_id", threadID,
			"agent_id", agentID,
			"status", status,
			"reason", reason,
			"caller", archiveCallerStack(),
		)
	}
	if s == nil || s.emitStopped == nil {
		return
	}
	event := newThreadEvent(threadEventStoppedKind, threadID, threadEventFields{
		AgentID: agentID,
		Status:  status,
		Reason:  reason,
	})
	if event == nil {
		return
	}
	s.emitStopped(event.(threaddto.Stopped))
}

func (s *service) publishMessagesPage(threadID string, totalCount, pages int) {
	if s == nil || s.emitMessagesPage == nil {
		return
	}
	event := newThreadEvent(threadEventMessagesPageKind, threadID, threadEventFields{
		TotalCount: totalCount,
		Pages:      pages,
	})
	if event == nil {
		return
	}
	s.emitMessagesPage(event.(threaddto.MessagesPage))
}

type threadTraceSpan struct {
	ctx       context.Context
	trace     observability.TraceContext
	kind      string
	threadID  string
	agentID   string
	code      observability.CodeAnchor
	metadata  map[string]any
	startedAt time.Time
}

func (s *service) beginThreadTraceSpan(
	ctx context.Context,
	kind, threadID, agentID string,
	code observability.CodeAnchor,
	metadata map[string]any,
) threadTraceSpan {
	ctx = util.NonNilContext(ctx)
	trace, ok := observability.TraceFromContext(ctx)
	parentSpanID := ""
	if ok {
		parentSpanID = trace.SpanID
	}
	if trace.TraceID == "" {
		trace.TraceID = idgen.NewID("trace")
	}
	trace.ParentSpanID = parentSpanID
	trace.SpanID = idgen.NewID("span")
	span := threadTraceSpan{
		ctx:       observability.ContextWithTrace(ctx, trace),
		trace:     trace,
		kind:      kind,
		threadID:  strings.TrimSpace(threadID),
		agentID:   strings.TrimSpace(agentID),
		code:      code,
		metadata:  metadata,
		startedAt: time.Now(),
	}
	s.recordThreadTraceEvent(span, "begin", observability.StatusOK, 0, "")
	return span
}

func (s *service) finishThreadTraceSpan(span threadTraceSpan, err error) {
	status := observability.StatusOK
	message := ""
	phase := "done"
	if err != nil {
		status = observability.StatusError
		message = err.Error()
		phase = "error"
	}
	s.recordThreadTraceEvent(span, phase, status, time.Since(span.startedAt).Milliseconds(), message)
}

func (s *service) recordThreadTraceEvent(
	span threadTraceSpan,
	phase string,
	status observability.Status,
	durationMS int64,
	message string,
) {
	if s == nil || s.tracing == nil {
		return
	}
	event := observability.TraceEvent{
		SchemaVersion: observability.SchemaVersion,
		Timestamp:     time.Now(),
		TraceID:       span.trace.TraceID,
		SpanID:        span.trace.SpanID,
		ParentSpanID:  span.trace.ParentSpanID,
		Kind:          span.kind,
		Phase:         phase,
		Method:        span.kind,
		ThreadID:      span.threadID,
		AgentID:       span.agentID,
		DurationMS:    durationMS,
		Status:        status,
		Error:         message,
		Code:          span.code,
		Metadata:      span.metadata,
	}
	if err := s.tracing.Record(span.ctx, event); err != nil && s.logger != nil {
		s.logger.Warn("thread trace record failed", "kind", span.kind, "phase", phase, "error", err)
	}
}
