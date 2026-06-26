package logger

import (
	"sync"
	"sync/atomic"
)

// Sampler 按 key 抽样高频日志。
// 每个 key 的前 firstN 次必打，之后只保留每 everyM 次，避免热点日志刷屏。
type Sampler struct {
	firstN int64
	everyM int64
	counts sync.Map // key -> *atomic.Int64，按 key 独立计数。
}

// NewSampler 创建按 key 抽样的 sampler，非法参数会收敛到默认阈值。
func NewSampler(firstN, everyM int) *Sampler {
	if firstN < 1 {
		firstN = 3
	}
	if everyM < 1 {
		everyM = 20
	}
	return &Sampler{firstN: int64(firstN), everyM: int64(everyM)}
}

// NewEverySampler 创建只按 everyM 间隔输出的 sampler，不保留前几次直通窗口。
func NewEverySampler(everyM int) *Sampler {
	if everyM < 1 {
		everyM = 20
	}
	return &Sampler{firstN: 0, everyM: int64(everyM)}
}

// ShouldLog 记录一次 key 命中并返回该次是否应写日志。
func (s *Sampler) ShouldLog(key string) bool {
	actual, _ := s.counts.LoadOrStore(key, &atomic.Int64{})
	counter := actual.(*atomic.Int64)
	seq := counter.Add(1)
	if seq <= s.firstN {
		return true
	}
	return (seq-s.firstN)%s.everyM == 0
}
