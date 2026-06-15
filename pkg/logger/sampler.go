package logger

import (
	"sync"
	"sync/atomic"
)

// Sampler gates high-frequency log entries by key.
// The first N occurrences are always emitted; afterwards only every M-th.
// Aligned with V2's shouldSampleLSPToolDoneInfo pattern.
type Sampler struct {
	firstN int64
	everyM int64
	counts sync.Map // key → *atomic.Int64
}

// NewSampler creates a sampler that emits the first firstN hits per key,
// then every everyM-th hit thereafter.
// NewSampler 创建sampler。
func NewSampler(firstN, everyM int) *Sampler {
	if firstN < 1 {
		firstN = 3
	}
	if everyM < 1 {
		everyM = 20
	}
	return &Sampler{firstN: int64(firstN), everyM: int64(everyM)}
}

// NewEverySampler creates a sampler that emits only every everyM-th hit per key.
// NewEverySampler 创建everysampler。
func NewEverySampler(everyM int) *Sampler {
	if everyM < 1 {
		everyM = 20
	}
	return &Sampler{firstN: 0, everyM: int64(everyM)}
}

// ShouldLog returns true if this occurrence of key should be logged.
// ShouldLog 判断日志是否可用。
func (s *Sampler) ShouldLog(key string) bool {
	actual, _ := s.counts.LoadOrStore(key, &atomic.Int64{})
	counter := actual.(*atomic.Int64)
	seq := counter.Add(1)
	if seq <= s.firstN {
		return true
	}
	return (seq-s.firstN)%s.everyM == 0
}
