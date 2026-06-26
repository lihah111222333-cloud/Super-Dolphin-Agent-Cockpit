package observability

import "sync"

// SamplerConfig 配置高频事件的采样间隔。
type SamplerConfig struct{ HighFrequencyKeepEvery int }

// Sampler 对高频 UI/state 事件采样，错误、panic 和慢事件始终保留。
type Sampler struct {
	mu        sync.Mutex
	keepEvery int
	dropped   int64
	seen      int64
}

// SampleDecision 表示事件是否保留，以及是否需要额外写入 dropped summary。
type SampleDecision struct {
	Keep    bool
	Summary *TraceEvent
}

// NewSampler 创建高频事件采样器，未配置时默认每 10 条保留 1 条。
func NewSampler(configs ...SamplerConfig) *Sampler {
	keepEvery := 10
	if len(configs) > 0 && configs[0].HighFrequencyKeepEvery > 0 {
		keepEvery = configs[0].HighFrequencyKeepEvery
	}
	return &Sampler{keepEvery: keepEvery}
}

// Decide 判断事件是否写入，并在采样点生成 dropped_count 汇总事件。
func (s *Sampler) Decide(event TraceEvent) SampleDecision {
	if mustKeep(event) || !highFrequency(event) {
		return SampleDecision{Keep: true}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen++
	if s.seen%int64(s.keepEvery) != 0 {
		s.dropped++
		return SampleDecision{}
	}
	summary := event
	summary.Status = StatusDroppedSummary
	summary.Metadata = map[string]any{"dropped_count": s.dropped}
	s.dropped = 0
	return SampleDecision{Keep: true, Summary: &summary}
}

// mustKeep 判断事件是否因错误、panic 或慢请求状态必须保留。
func mustKeep(event TraceEvent) bool {
	return event.Status == StatusError || event.Status == StatusPanic || event.Status == StatusSlow
}

// highFrequency 判断事件是否属于可采样的 UI/state 高频流。
func highFrequency(event TraceEvent) bool {
	return event.Kind == "ui_state" || event.Kind == "state" || event.Method == "ui/state" || event.Phase == "state"
}
