package observability

import "sync"

type SamplerConfig struct{ HighFrequencyKeepEvery int }

type Sampler struct {
	mu        sync.Mutex
	keepEvery int
	dropped   int64
	seen      int64
}

type SampleDecision struct {
	Keep    bool
	Summary *TraceEvent
}

// NewSampler 创建sampler。
func NewSampler(configs ...SamplerConfig) *Sampler {
	keepEvery := 10
	if len(configs) > 0 && configs[0].HighFrequencyKeepEvery > 0 {
		keepEvery = configs[0].HighFrequencyKeepEvery
	}
	return &Sampler{keepEvery: keepEvery}
}

// Decide 处理decide。
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

func mustKeep(event TraceEvent) bool {
	return event.Status == StatusError || event.Status == StatusPanic || event.Status == StatusSlow
}

func highFrequency(event TraceEvent) bool {
	return event.Kind == "ui_state" || event.Kind == "state" || event.Method == "ui/state" || event.Phase == "state"
}
