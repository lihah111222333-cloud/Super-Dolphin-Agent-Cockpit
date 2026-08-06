package observability

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/idgen"
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

// QueryTailReaderFunc 让普通函数适配 tail 查询接口，便于测试注入。
type QueryTailReaderFunc func(context.Context, Query) (QueryResult, error)

// QueryTraceEvents 调用底层函数执行 trace tail 查询。
func (f QueryTailReaderFunc) QueryTraceEvents(ctx context.Context, query Query) (QueryResult, error) {
	return f(ctx, query)
}

// Service 负责 trace 采样、内存索引、可选 JSONL 写入和落盘 tail 回读。
// tail 查询有并发上限和同查询合并，避免 UI 重复查询压垮落盘读取路径。
type Service struct {
	enabled             bool
	disabledReason      string
	index               *Index
	sampler             *Sampler
	sanitizer           Sanitizer
	sink                serviceSink
	tail                queryTailReader
	tailSem             chan struct{}
	tailTimeoutMS       int
	inflight            map[Query]*tailCall
	inflightMu          sync.Mutex
	recordErrorWarnings *recordErrorWarningLimiter
}

// ServiceOption 在创建 observability Service 时注入 sink、tail reader 或采样器。
// 该类型只用于构造期配置，运行中不应改变服务并发和持久化边界。
type ServiceOption func(*Service)

// tailCall 记录正在执行的 tail 查询，复用相同 Query 的并发调用结果。
type tailCall struct {
	ready  chan struct{}
	result QueryResult
	err    error
}

// NewService 创建启用状态的 observability 服务，并应用测试或运行时注入选项。
func NewService(cfg Config, options ...ServiceOption) *Service {
	cfg = normalizeServiceConfig(cfg)
	svc := &Service{enabled: true, index: NewIndex(cfg), sampler: NewSampler(), sanitizer: NewSanitizer(cfg), tailSem: make(chan struct{}, cfg.QueryTailMaxConcurrency), tailTimeoutMS: cfg.QueryTailTimeoutMS, inflight: map[Query]*tailCall{}, recordErrorWarnings: newRecordErrorWarningLimiter()}
	for _, option := range options {
		option(svc)
	}
	return svc
}

// NewDisabledService 创建禁用状态的服务，仍保留索引和状态查询能力。
func NewDisabledService(cfg Config) *Service {
	cfg = normalizeServiceConfig(cfg)
	reason := cfg.DisabledReason
	if reason == "" {
		reason = "observability tracing disabled"
	}
	return &Service{enabled: false, disabledReason: reason, index: NewIndex(cfg), sampler: NewSampler(), sanitizer: NewSanitizer(cfg), tailSem: make(chan struct{}, cfg.QueryTailMaxConcurrency), tailTimeoutMS: cfg.QueryTailTimeoutMS, inflight: map[Query]*tailCall{}, recordErrorWarnings: newRecordErrorWarningLimiter()}
}

// WithSink 注入 trace 持久化 sink。
func WithSink(sink serviceSink) ServiceOption { return func(s *Service) { s.sink = sink } }

// WithTailReader 注入 JSONL tail 查询读取器。
func WithTailReader(reader queryTailReader) ServiceOption {
	return func(s *Service) { s.tail = reader }
}

// WithSampler 注入采样器；nil 时保留默认采样器。
func WithSampler(sampler *Sampler) ServiceOption {
	return func(s *Service) {
		if sampler != nil {
			s.sampler = sampler
		}
	}
}

// ServiceStatus 是暴露给诊断接口的 observability 运行状态。
type ServiceStatus struct {
	Enabled           bool   `json:"enabled"`
	DisabledReason    string `json:"disabled_reason,omitempty"`
	SchemaVersion     int    `json:"schema_version"`
	IndexTraceKeys    int    `json:"index_trace_keys"`
	SinkEventsWritten int64  `json:"sink_events_written"`
	SinkWriteErrors   int64  `json:"sink_write_errors"`
}

// Status 返回当前服务状态与 sink 写入统计。
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

// Enabled 返回 trace 记录是否启用。
func (s *Service) Enabled() bool { return s.enabled }

// Record 对事件脱敏、补全线程相关 trace，再按采样策略写入索引和 sink。
// 禁用时直接返回 nil，写入 sink 失败会返回错误供调用方告警。
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

// correlateTraceByThread 在缺少 trace_id 的事件上复用同线程最新 trace 上下文。
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
		event.SpanID = idgen.NewID("span")
	}
	return event
}

// Query 先查内存索引，必要时再读取 JSONL tail，并合并去重后的结果。
func (s *Service) Query(ctx context.Context, query Query) QueryResult {
	if !s.enabled {
		return QueryResult{Source: QuerySourceMemory}
	}
	memory := s.index.Query(query)
	if !query.IncludeTail || s.tail == nil {
		return memory
	}
	tail, err := s.queryTail(ctx, query)
	if err != nil {
		return queryResultWithTailFailure(memory, tail, err)
	}
	if len(tail.Events) == 0 {
		memory.copyTailDiagnosticsFrom(tail)
		return memory
	}
	if len(memory.Events) == 0 {
		tail.Source = QuerySourceJSONLTail
		return tail
	}
	return mergeQueryResults(memory, tail, query.Limit)
}

// queryResultWithTailFailure 保留内存查询结果，同时把 tail 读取失败暴露给上层。
func queryResultWithTailFailure(memory QueryResult, tail QueryResult, err error) QueryResult {
	result := memory
	result.copyTailDiagnosticsFrom(tail)
	if errors.Is(err, context.DeadlineExceeded) {
		result.TailTimedOut = true
	}
	result.TailDecodeErrors = append(result.TailDecodeErrors, TailDecodeError{
		Error: err.Error(),
		Metadata: map[string]any{
			"kind": "tail_read_error",
		},
	})
	return result
}

// mergeQueryResults 合并内存和 tail 结果，按时间排序并保留 tail 诊断信息。
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

// appendUniqueTraceEvent 按稳定去重键追加事件，缺少关键字段的事件保持原样。
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

// traceEventDedupeKey 生成跨内存索引和 JSONL tail 的事件去重键。
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

// queryTail 合并相同 Query 的并发 tail 读取，只有首个调用实际读文件。
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

// queryTailFresh 绕过 inflight 合并，供需要独立读取 tail 的测试使用。
func (s *Service) queryTailFresh(ctx context.Context, query Query) (QueryResult, error) {
	return s.readTail(ctx, query)
}

// readTail 在并发信号量和超时约束下读取 JSONL tail。
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

// tailCall 创建或复用同一 Query 的正在执行 tail 读取。
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

// finishTailCall 清理已完成的 tail 读取占位。
func (s *Service) finishTailCall(query Query) {
	s.inflightMu.Lock()
	defer s.inflightMu.Unlock()
	delete(s.inflight, query)
}

// normalizeServiceConfig 补齐 observability 服务运行所需的内部默认值。
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

// copyTailDiagnosticsFrom 把 tail 读取诊断复制到合并后的查询结果。
func (r *QueryResult) copyTailDiagnosticsFrom(tail QueryResult) {
	r.TailDecodeErrors = tail.TailDecodeErrors
	r.TailFilesScanned = tail.TailFilesScanned
	r.TailBytesRead = tail.TailBytesRead
	r.TailDurationMS = tail.TailDurationMS
	r.TailTimedOut = tail.TailTimedOut
	r.TailTruncated = tail.TailTruncated
}
