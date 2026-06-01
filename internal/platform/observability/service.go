package observability

import (
	"context"
	"sync"
	"time"
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
	cache          map[Query]QueryResult
	inflight       map[Query]*tailCall
	cacheMu        sync.Mutex
}

type ServiceOption func(*Service)

type tailCall struct {
	ready  chan struct{}
	result QueryResult
	err    error
}

func NewService(cfg Config, options ...ServiceOption) *Service {
	cfg = normalizeServiceConfig(cfg)
	svc := &Service{enabled: true, index: NewIndex(cfg), sampler: NewSampler(), sanitizer: NewSanitizer(cfg), tailSem: make(chan struct{}, cfg.QueryTailMaxConcurrency), tailTimeoutMS: cfg.QueryTailTimeoutMS, cache: map[Query]QueryResult{}, inflight: map[Query]*tailCall{}}
	for _, option := range options {
		option(svc)
	}
	return svc
}

func NewDisabledService(cfg Config) *Service {
	cfg = normalizeServiceConfig(cfg)
	reason := cfg.DisabledReason
	if reason == "" {
		reason = "observability tracing disabled"
	}
	return &Service{enabled: false, disabledReason: reason, index: NewIndex(cfg), sampler: NewSampler(), sanitizer: NewSanitizer(cfg), tailSem: make(chan struct{}, cfg.QueryTailMaxConcurrency), tailTimeoutMS: cfg.QueryTailTimeoutMS, cache: map[Query]QueryResult{}, inflight: map[Query]*tailCall{}}
}

func WithSink(sink serviceSink) ServiceOption { return func(s *Service) { s.sink = sink } }
func WithTailReader(reader queryTailReader) ServiceOption {
	return func(s *Service) { s.tail = reader }
}
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

func (s *Service) Enabled() bool { return s.enabled }

func (s *Service) Record(ctx context.Context, event TraceEvent) error {
	if !s.enabled {
		return nil
	}
	event = s.sanitizer.SanitizeEvent(event)
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
	return QueryResult{Source: QuerySourceMixed, Events: append(memory.Events, tail.Events...), Truncated: memory.Truncated || tail.Truncated}
}

func (s *Service) queryTail(ctx context.Context, query Query) (QueryResult, error) {
	if cached, ok := s.cachedTail(query); ok {
		return cached, nil
	}
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
	select {
	case s.tailSem <- struct{}{}:
		defer func() { <-s.tailSem }()
	case <-ctx.Done():
		call.result, call.err = QueryResult{Source: QuerySourceJSONLTail}, ctx.Err()
		return call.result, call.err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(s.tailTimeoutMS)*time.Millisecond)
	defer cancel()
	call.result, call.err = s.tail.QueryTraceEvents(ctx, query)
	if call.err == nil {
		s.storeTail(query, call.result)
	}
	return call.result, call.err
}

func (s *Service) tailCall(query Query) (*tailCall, bool) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if call := s.inflight[query]; call != nil {
		return call, false
	}
	call := &tailCall{ready: make(chan struct{})}
	s.inflight[query] = call
	return call, true
}

func (s *Service) finishTailCall(query Query) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	delete(s.inflight, query)
}

func (s *Service) cachedTail(query Query) (QueryResult, bool) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	result, ok := s.cache[query]
	return result, ok
}

func (s *Service) storeTail(query Query, result QueryResult) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if len(s.cache) >= 64 {
		s.cache = map[Query]QueryResult{}
	}
	s.cache[query] = result
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
