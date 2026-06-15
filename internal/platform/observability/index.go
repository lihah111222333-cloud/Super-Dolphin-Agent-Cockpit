package observability

import "sync"

type Index struct {
	mu         sync.Mutex
	next       uint64
	cfg        Config
	order      []uint64
	events     map[uint64]TraceEvent
	traceRefs  map[string]*seqRing
	threadRefs map[string]*seqRing
	slowRefs   seqRing
	errorRefs  seqRing
}

type seqRing struct {
	cap  int
	seqs []uint64
}

// NewIndex 创建索引。
func NewIndex(cfg Config) *Index {
	cfg = normalizeIndexConfig(cfg)
	return &Index{cfg: cfg, events: make(map[uint64]TraceEvent, cfg.IndexMaxEvents), traceRefs: map[string]*seqRing{}, threadRefs: map[string]*seqRing{}, slowRefs: seqRing{cap: cfg.IndexMaxSlowEvents}, errorRefs: seqRing{cap: cfg.IndexMaxErrorEvents}}
}

// Add 添加平台observability。
func (i *Index) Add(event TraceEvent) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.next++
	seq := i.next
	i.events[seq] = event
	i.order = append(i.order, seq)
	if len(i.order) > i.cfg.IndexMaxEvents {
		i.evict(i.order[0])
		i.order = i.order[1:]
	}
	if event.TraceID != "" {
		i.appendKeyRef(i.traceRefs, event.TraceID, i.cfg.IndexMaxTraceEvents, seq)
	}
	if event.ThreadID != "" {
		i.appendKeyRef(i.threadRefs, event.ThreadID, i.cfg.IndexMaxThreadEvents, seq)
	}
	if event.Status == StatusSlow {
		i.slowRefs.append(seq)
	}
	if event.Status == StatusError || event.Status == StatusPanic {
		i.errorRefs.append(seq)
	}
}

// Query 处理查询。
func (i *Index) Query(query Query) QueryResult {
	i.mu.Lock()
	defer i.mu.Unlock()
	seqs := i.querySeqs(query)
	seqs = i.filterSeqs(seqs, query)
	events, truncated := i.eventsForSeqs(seqs, query.Limit)
	return QueryResult{Source: QuerySourceMemory, Events: events, Truncated: truncated}
}

// TraceKeyCount 处理trace键count。
func (i *Index) TraceKeyCount() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return len(i.traceRefs)
}

// LatestTraceContextByThread 按线程处理latesttrace上下文。
func (i *Index) LatestTraceContextByThread(threadID string) (TraceContext, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	ring := i.threadRefs[threadID]
	if ring == nil {
		return TraceContext{}, false
	}
	ring.prune(i.events)
	for n := len(ring.seqs) - 1; n >= 0; n-- {
		event := i.events[ring.seqs[n]]
		if event.TraceID != "" {
			return TraceContext{TraceID: event.TraceID, SpanID: event.SpanID, ParentSpanID: event.ParentSpanID}, true
		}
	}
	return TraceContext{}, false
}

func (i *Index) querySeqs(query Query) []uint64 {
	switch {
	case query.TraceID != "":
		return i.keySeqs(i.traceRefs, query.TraceID)
	case query.ThreadID != "":
		return i.keySeqs(i.threadRefs, query.ThreadID)
	case query.Slow:
		return append([]uint64(nil), i.slowRefs.seqs...)
	case query.Errors:
		return append([]uint64(nil), i.errorRefs.seqs...)
	default:
		return append([]uint64(nil), i.order...)
	}
}

func (i *Index) filterSeqs(seqs []uint64, query Query) []uint64 {
	out := seqs[:0]
	for _, seq := range seqs {
		event, ok := i.events[seq]
		if ok && matchesQuery(event, query) {
			out = append(out, seq)
		}
	}
	return out
}

func (i *Index) keySeqs(refs map[string]*seqRing, key string) []uint64 {
	ring := refs[key]
	if ring == nil {
		return nil
	}
	ring.prune(i.events)
	if len(ring.seqs) == 0 {
		delete(refs, key)
		return nil
	}
	return append([]uint64(nil), ring.seqs...)
}

func (i *Index) eventsForSeqs(seqs []uint64, limit int) ([]TraceEvent, bool) {
	truncated := false
	if limit > 0 && len(seqs) > limit {
		seqs = seqs[len(seqs)-limit:]
		truncated = true
	}
	out := make([]TraceEvent, 0, len(seqs))
	for _, seq := range seqs {
		if event, ok := i.events[seq]; ok {
			out = append(out, event)
		}
	}
	return out, truncated
}

func (i *Index) appendKeyRef(refs map[string]*seqRing, key string, cap int, seq uint64) {
	ring := refs[key]
	if ring == nil {
		ring = &seqRing{cap: cap}
		refs[key] = ring
	}
	ring.append(seq)
}

func (i *Index) evict(seq uint64) {
	event, ok := i.events[seq]
	if !ok {
		return
	}
	delete(i.events, seq)
	i.removeKeyRef(i.traceRefs, event.TraceID, seq)
	i.removeKeyRef(i.threadRefs, event.ThreadID, seq)
	if event.Status == StatusSlow {
		i.slowRefs.remove(seq)
	}
	if event.Status == StatusError || event.Status == StatusPanic {
		i.errorRefs.remove(seq)
	}
}

func (i *Index) removeKeyRef(refs map[string]*seqRing, key string, seq uint64) {
	if key == "" {
		return
	}
	if ring := refs[key]; ring != nil {
		ring.remove(seq)
		if len(ring.seqs) == 0 {
			delete(refs, key)
		}
	}
}

func (r *seqRing) append(seq uint64) {
	if r.cap <= 0 {
		return
	}
	r.seqs = append(r.seqs, seq)
	if len(r.seqs) > r.cap {
		r.seqs = r.seqs[len(r.seqs)-r.cap:]
	}
}

func (r *seqRing) remove(seq uint64) {
	for n, value := range r.seqs {
		if value == seq {
			copy(r.seqs[n:], r.seqs[n+1:])
			r.seqs = r.seqs[:len(r.seqs)-1]
			return
		}
	}
}

func (r *seqRing) prune(events map[uint64]TraceEvent) {
	out := r.seqs[:0]
	for _, seq := range r.seqs {
		if _, ok := events[seq]; ok {
			out = append(out, seq)
		}
	}
	r.seqs = out
}

// normalizeIndexConfig 规范化索引配置。
func normalizeIndexConfig(cfg Config) Config {
	if cfg.IndexMaxEvents <= 0 {
		cfg.IndexMaxEvents = 5000
	}
	if cfg.IndexMaxTraceEvents <= 0 {
		cfg.IndexMaxTraceEvents = 128
	}
	if cfg.IndexMaxThreadEvents <= 0 {
		cfg.IndexMaxThreadEvents = 256
	}
	if cfg.IndexMaxSlowEvents <= 0 {
		cfg.IndexMaxSlowEvents = 500
	}
	if cfg.IndexMaxErrorEvents <= 0 {
		cfg.IndexMaxErrorEvents = 500
	}
	return cfg
}
