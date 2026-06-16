package observability

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
)

type serviceSink interface {
	Append(context.Context, TraceEvent) error
}

type serviceSinkStats interface {
	Stats() SinkStats
}

type queryTailReader interface {
	QueryTraceEvents(context.Context, Query) (QueryResult, error)
}

type QueryTailReaderFunc func(context.Context, Query) (QueryResult, error)

// QueryTraceEvents 处理查询trace事件。
func (f QueryTailReaderFunc) QueryTraceEvents(ctx context.Context, query Query) (QueryResult, error) {
	return f(ctx, query)
}

type Service struct {
	enabled        bool
	disabledReason string
	index          *Index
	sampler        *Sampler
	sanitizer      Sanitizer
	sink           serviceSink
	tail           queryTailReader
	tailSem        chan struct{}
	tailTimeoutMS  int
	inflight       map[Query]*tailCall
	inflightMu     sync.Mutex
}

type ServiceOption func(*Service)

type tailCall struct {
	ready  chan struct{}
	result QueryResult
	err    error
}

// NewService 创建服务。
func NewService(cfg Config, options ...ServiceOption) *Service {
	cfg = normalizeServiceConfig(cfg)
	svc := &Service{enabled: true, index: NewIndex(cfg), sampler: NewSampler(), sanitizer: NewSanitizer(cfg), tailSem: make(chan struct{}, cfg.QueryTailMaxConcurrency), tailTimeoutMS: cfg.QueryTailTimeoutMS, inflight: map[Query]*tailCall{}}
	for _, option := range options {
		option(svc)
	}
	return svc
}

// NewDisabledService 创建disabled服务。
func NewDisabledService(cfg Config) *Service {
	cfg = normalizeServiceConfig(cfg)
	reason := cfg.DisabledReason
	if reason == "" {
		reason = "observability tracing disabled"
	}
	return &Service{enabled: false, disabledReason: reason, index: NewIndex(cfg), sampler: NewSampler(), sanitizer: NewSanitizer(cfg), tailSem: make(chan struct{}, cfg.QueryTailMaxConcurrency), tailTimeoutMS: cfg.QueryTailTimeoutMS, inflight: map[Query]*tailCall{}}
}

// WithSink 设置sink。
func WithSink(sink serviceSink) ServiceOption { return func(s *Service) { s.sink = sink } }

// WithTailReader 设置tail读取器。
func WithTailReader(reader queryTailReader) ServiceOption {
	return func(s *Service) { s.tail = reader }
}

// WithSampler 设置sampler。
func WithSampler(sampler *Sampler) ServiceOption {
	return func(s *Service) {
		if sampler != nil {
			s.sampler = sampler
		}
	}
}

type ServiceStatus struct {
	Enabled           bool   `json:"enabled"`
	DisabledReason    string `json:"disabled_reason,omitempty"`
	SchemaVersion     int    `json:"schema_version"`
	IndexTraceKeys    int    `json:"index_trace_keys"`
	SinkEventsWritten int64  `json:"sink_events_written"`
	SinkWriteErrors   int64  `json:"sink_write_errors"`
}

// Status 处理状态。
func (s *Service) Status() ServiceStatus {
	status := ServiceStatus{Enabled: s.enabled, DisabledReason: s.disabledReason, SchemaVersion: SchemaVersion}
	if s.index != nil {
		status.IndexTraceKeys = s.index.TraceKeyCount()
	}
	if statsSink, ok := s.sink.(serviceSinkStats); ok {
		stats := statsSink.Stats()
		status.SinkEventsWritten = stats.EventsWritten
		status.SinkWriteErrors = stats.WriteErrors
	}
	return status
}

// Enabled 判断平台observability是否启用。
func (s *Service) Enabled() bool { return s.enabled }

// Record 记录平台observability。
func (s *Service) Record(ctx context.Context, event TraceEvent) error {
	if !s.enabled {
		return nil
	}
	event = s.sanitizer.SanitizeEvent(event)
	event = s.correlateTraceByThread(event)
	decision := s.sampler.Decide(event)
	if decision.Summary != nil {
		summary := s.sanitizer.SanitizeEvent(*decision.Summary)
		s.index.Add(summary)
		if s.sink != nil {
			if err := s.sink.Append(ctx, summary); err != nil {
				return err
			}
		}
	}
	if !decision.Keep {
		return nil
	}
	s.index.Add(event)
	if s.sink != nil {
		return s.sink.Append(ctx, event)
	}
	return nil
}

// correlateTraceByThread 按线程处理correlatetrace。
func (s *Service) correlateTraceByThread(event TraceEvent) TraceEvent {
	if event.TraceID != "" || event.ThreadID == "" || s == nil || s.index == nil {
		return event
	}
	trace, ok := s.index.LatestTraceContextByThread(event.ThreadID)
	if !ok || trace.TraceID == "" {
		return event
	}
	event.TraceID = trace.TraceID
	if event.ParentSpanID == "" {
		event.ParentSpanID = trace.SpanID
	}
	if event.SpanID == "" {
		event.SpanID = kernel.NewID("span")
	}
	return event
}

// Query 处理查询。
func (s *Service) Query(ctx context.Context, query Query) QueryResult {
	if !s.enabled {
		return QueryResult{Source: QuerySourceMemory}
	}
	memory := s.index.Query(query)
	if !query.IncludeTail || s.tail == nil {
		return memory
	}
	tail, err := s.queryTail(ctx, query)
	if err != nil || len(tail.Events) == 0 {
		return memory
	}
	if len(memory.Events) == 0 {
		tail.Source = QuerySourceJSONLTail
		return tail
	}
	return mergeQueryResults(memory, tail, query.Limit)
}

// mergeQueryResults 合并查询结果。
func mergeQueryResults(memory QueryResult, tail QueryResult, limit int) QueryResult {
	combined := make([]TraceEvent, 0, len(memory.Events)+len(tail.Events))
	seen := make(map[string]struct{}, len(memory.Events)+len(tail.Events))
	for _, event := range memory.Events {
		combined = appendUniqueTraceEvent(combined, seen, event)
	}
	for _, event := range tail.Events {
		combined = appendUniqueTraceEvent(combined, seen, event)
	}
	sort.SliceStable(combined, func(i, j int) bool {
		left, right := combined[i].Timestamp, combined[j].Timestamp
		if left.IsZero() || right.IsZero() {
			return false
		}
		return left.Before(right)
	})
	truncated := memory.Truncated || tail.Truncated
	if limit > 0 && len(combined) > limit {
		combined = combined[len(combined)-limit:]
		truncated = true
	}
	result := QueryResult{Source: QuerySourceMixed, Events: combined, Truncated: truncated}
	result.copyTailDiagnosticsFrom(tail)
	return result
}

func appendUniqueTraceEvent(events []TraceEvent, seen map[string]struct{}, event TraceEvent) []TraceEvent {
	key := traceEventDedupeKey(event)
	if key == "" {
		return append(events, event)
	}
	if _, ok := seen[key]; ok {
		return events
	}
	seen[key] = struct{}{}
	return append(events, event)
}

// traceEventDedupeKey 处理trace事件去重键。
func traceEventDedupeKey(event TraceEvent) string {
	if event.TraceID == "" && event.SpanID == "" && event.CallID == "" && event.TurnID == "" && event.Method == "" {
		return ""
	}
	parts := []string{
		event.TraceID,
		event.SpanID,
		event.ParentSpanID,
		event.Kind,
		event.Phase,
		event.Method,
		event.ThreadID,
		event.AgentID,
		event.TurnID,
		event.CallID,
		event.ToolName,
		event.ClientKind,
		event.ClientRoute,
		strconv.FormatInt(event.DurationMS, 10),
		string(event.Status),
		event.Error,
		event.Code.File,
		event.Code.Function,
		strconv.Itoa(event.Code.Line),
	}
	if !event.Timestamp.IsZero() {
		parts = append(parts, event.Timestamp.UTC().Format(time.RFC3339Nano))
	}
	return strings.Join(parts, "\x00")
}

func (s *Service) queryTail(ctx context.Context, query Query) (QueryResult, error) {
	call, owner := s.tailCall(query)
	if !owner {
		select {
		case <-call.ready:
			return call.result, call.err
		case <-ctx.Done():
			return QueryResult{Source: QuerySourceJSONLTail}, ctx.Err()
		}
	}
	defer close(call.ready)
	defer s.finishTailCall(query)
	call.result, call.err = s.readTail(ctx, query)
	return call.result, call.err
}

func (s *Service) queryTailFresh(ctx context.Context, query Query) (QueryResult, error) {
	return s.readTail(ctx, query)
}

func (s *Service) readTail(ctx context.Context, query Query) (QueryResult, error) {
	startedAt := time.Now()
	select {
	case s.tailSem <- struct{}{}:
		defer func() { <-s.tailSem }()
	case <-ctx.Done():
		return QueryResult{Source: QuerySourceJSONLTail, TailDurationMS: durationMillis(time.Since(startedAt)), TailTimedOut: errors.Is(ctx.Err(), context.DeadlineExceeded)}, ctx.Err()
	}
	ctx, cancel := platformconfig.WithTimeout(ctx, time.Duration(s.tailTimeoutMS)*time.Millisecond)
	defer cancel()
	result, err := s.tail.QueryTraceEvents(ctx, query)
	result.TailDurationMS = durationMillis(time.Since(startedAt))
	result.TailTimedOut = result.TailTimedOut || errors.Is(err, context.DeadlineExceeded)
	return result, err
}

func (s *Service) tailCall(query Query) (*tailCall, bool) {
	s.inflightMu.Lock()
	defer s.inflightMu.Unlock()
	if call := s.inflight[query]; call != nil {
		return call, false
	}
	call := &tailCall{ready: make(chan struct{})}
	s.inflight[query] = call
	return call, true
}

func (s *Service) finishTailCall(query Query) {
	s.inflightMu.Lock()
	defer s.inflightMu.Unlock()
	delete(s.inflight, query)
}

func normalizeServiceConfig(cfg Config) Config {
	cfg = normalizeIndexConfig(cfg)
	if cfg.MetadataMaxBytes <= 0 {
		cfg.MetadataMaxBytes = 4096
	}
	if cfg.StringMaxBytes <= 0 {
		cfg.StringMaxBytes = deriveStringMaxBytes(8192)
	}
	if cfg.QueryTailMaxConcurrency <= 0 {
		cfg.QueryTailMaxConcurrency = 1
	}
	if cfg.QueryTailTimeoutMS <= 0 {
		cfg.QueryTailTimeoutMS = 750
	}
	return cfg
}

func (r *QueryResult) copyTailDiagnosticsFrom(tail QueryResult) {
	r.TailDecodeErrors = tail.TailDecodeErrors
	r.TailFilesScanned = tail.TailFilesScanned
	r.TailBytesRead = tail.TailBytesRead
	r.TailDurationMS = tail.TailDurationMS
	r.TailTimedOut = tail.TailTimedOut
	r.TailTruncated = tail.TailTruncated
}
