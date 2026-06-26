package observability

import "sync"

// Index 是进程内 trace 事件索引，按 trace/thread/slow/error 多维保留有限事件窗口。
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

// seqRing 保存索引引用的事件序号窗口，cap 控制每个 key 最多保留多少条。
type seqRing struct {
	cap  int
	seqs []uint64
}

// NewIndex 创建内存索引并补齐缺省容量，所有内部 map 从这里初始化。
func NewIndex(cfg Config) *Index {
	cfg = normalizeIndexConfig(cfg)
	return &Index{cfg: cfg, events: make(map[uint64]TraceEvent, cfg.IndexMaxEvents), traceRefs: map[string]*seqRing{}, threadRefs: map[string]*seqRing{}, slowRefs: seqRing{cap: cfg.IndexMaxSlowEvents}, errorRefs: seqRing{cap: cfg.IndexMaxErrorEvents}}
}

// Add 写入一条事件并更新各维度索引；超过全局容量时同步驱逐旧事件引用。
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

// Query 根据 Query 选择最窄索引读取事件，再应用过滤和 limit。
func (i *Index) Query(query Query) QueryResult {
	i.mu.Lock()
	defer i.mu.Unlock()
	seqs := i.querySeqs(query)
	seqs = i.filterSeqs(seqs, query)
	events, truncated := i.eventsForSeqs(seqs, query.Limit)
	return QueryResult{Source: QuerySourceMemory, Events: events, Truncated: truncated}
}

// TraceKeyCount 返回当前仍有事件引用的 trace key 数量。
func (i *Index) TraceKeyCount() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return len(i.traceRefs)
}

// LatestTraceContextByThread 返回指定 thread 最近一条带 traceID 的上下文。
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

// querySeqs 按 query 选择初始序号集合，优先使用最具体的索引。
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

// filterSeqs 在候选序号上应用完整 Query 条件，并原地复用切片容量。
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

// keySeqs 读取指定 key 的 ring，并清理已被全局事件表驱逐的旧序号。
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

// eventsForSeqs 按 limit 返回最近事件，truncated 表示候选集合被裁剪。
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

// appendKeyRef 把事件序号追加到某个 key 的 ring，必要时创建 ring。
func (i *Index) appendKeyRef(refs map[string]*seqRing, key string, cap int, seq uint64) {
	ring := refs[key]
	if ring == nil {
		ring = &seqRing{cap: cap}
		refs[key] = ring
	}
	ring.append(seq)
}

// evict 从全局事件表和所有二级索引中移除指定序号。
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

// removeKeyRef 从指定 key 的 ring 中移除序号，ring 为空时删除 key。
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

// append 向 ring 追加序号，超出容量时保留最新窗口。
func (r *seqRing) append(seq uint64) {
	if r.cap <= 0 {
		return
	}
	r.seqs = append(r.seqs, seq)
	if len(r.seqs) > r.cap {
		r.seqs = r.seqs[len(r.seqs)-r.cap:]
	}
}

// remove 从 ring 中删除指定序号，保持其余序号相对顺序。
func (r *seqRing) remove(seq uint64) {
	for n, value := range r.seqs {
		if value == seq {
			copy(r.seqs[n:], r.seqs[n+1:])
			r.seqs = r.seqs[:len(r.seqs)-1]
			return
		}
	}
}

// prune 丢弃已不存在于全局事件表的序号引用。
func (r *seqRing) prune(events map[uint64]TraceEvent) {
	out := r.seqs[:0]
	for _, seq := range r.seqs {
		if _, ok := events[seq]; ok {
			out = append(out, seq)
		}
	}
	r.seqs = out
}

// normalizeIndexConfig 为索引容量补默认值，避免零值配置导致索引完全不保留事件。
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
