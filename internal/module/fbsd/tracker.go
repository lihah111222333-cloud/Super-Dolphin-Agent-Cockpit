package fbsd

import (
	"context"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// 默认 tracker 调参（spec 未明示，留实现层默认）。
const (
	defaultEventBuffer = 1024
	// defaultSaveInterval 是后台节流写盘频率。worker 每 dirty 状态 + 滴答触发
	// 一次 SaveStats，避免每次 Record 都写盘（IO 放大）；harness 崩溃最坏丢
	// 这一窗口内的打点。
	defaultSaveInterval = 30 * time.Second
)

// Tracker 在内存维护 workspace + global stats，异步落盘。
//
// Record 是 hot path（每次 skill 调用），必须 non-blocking：channel 满时
// drop 当前事件不阻塞 caller。
//
// Flush 同步等待 pending events drain + 写盘完成；fx OnStop 钩子调用一次。
// Flush 后 Record 仍可调（events channel 未关），但 worker 已退出，event
// 不会被消费——caller 应保证 Flush 之后不再 Record。
type Tracker struct {
	wsPath, globPath string
	enabled          bool
	saveInterval     time.Duration

	mu        sync.Mutex
	wsStats   Stats
	globStats Stats

	events chan CallEvent
	stop   chan struct{} // close to signal worker
	done   chan struct{} // closed when worker exits
}

// NewTracker 构造 Tracker。enabled=false 时 Record/Flush 走 no-op 路径，
// 不启动 worker、不读盘。enabled=true 时立即 LoadStats，启动后台 worker。
func NewTracker(wsPath, globPath string, enabled bool) (*Tracker, error) {
	return newTrackerWithInterval(wsPath, globPath, enabled, defaultSaveInterval)
}

// newTrackerWithInterval 暴露 saveInterval 参数给测试，避免等真正的 30s。
func newTrackerWithInterval(wsPath, globPath string, enabled bool, interval time.Duration) (*Tracker, error) {
	t := &Tracker{
		wsPath:       wsPath,
		globPath:     globPath,
		enabled:      enabled,
		saveInterval: interval,
		wsStats:      Stats{},
		globStats:    Stats{},
		events:       make(chan CallEvent, defaultEventBuffer),
		stop:         make(chan struct{}),
		done:         make(chan struct{}),
	}
	if !enabled {
		// disabled tracker：不读盘、不启动 worker；done/stop 立即可用让 Flush no-op
		close(t.done)
		return t, nil
	}
	ws, err := LoadStats(wsPath)
	if err != nil {
		return nil, err
	}
	gl, err := LoadStats(globPath)
	if err != nil {
		return nil, err
	}
	t.wsStats = ws
	t.globStats = gl
	go t.run()
	return t, nil
}

// Enabled 报告 feature flag 是否打开。nil receiver → false。
func (t *Tracker) Enabled() bool {
	return t != nil && t.enabled
}

// Record 异步打点。enabled=false / receiver=nil / 空 name → no-op；channel
// 满时 drop 不阻塞 caller。
func (t *Tracker) Record(name, anchor string) {
	if t == nil || !t.enabled || name == "" {
		return
	}
	select {
	case t.events <- CallEvent{SkillName: name, Anchor: anchor, At: time.Now()}:
	default:
		// channel 满 → 丢弃当前事件；spec §9.6 允许有损（统计不需要 100% 精确）
	}
}

// Flush 通知 worker 停止 + drain 剩余 events + 写盘。fx OnStop 钩子调用。
// nil receiver / disabled → no-op 立即返回。重复 Flush 安全（stop 仅 close-once）。
func (t *Tracker) Flush(ctx context.Context) error {
	if t == nil || !t.enabled {
		return nil
	}
	select {
	case <-t.stop:
		// 已 close
	default:
		close(t.stop)
	}
	select {
	case <-t.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Snapshot 返回 wsStats + globStats 的 deep copy，给 tier 分配读快照用。
// 调用方修改返回值不会影响 tracker 内部。
func (t *Tracker) Snapshot() (ws, glob Stats) {
	if t == nil {
		return Stats{}, Stats{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return cloneStats(t.wsStats), cloneStats(t.globStats)
}

// run 是后台 worker：消费 events、节流写盘、收到 stop 后 drain + 最后写盘退出。
func (t *Tracker) run() {
	defer close(t.done)
	tick := time.NewTicker(t.saveInterval)
	defer tick.Stop()
	dirty := false
	for {
		select {
		case ev := <-t.events:
			t.applyEvent(ev)
			dirty = true
		case <-tick.C:
			if dirty {
				t.persistAll()
				dirty = false
			}
		case <-t.stop:
			t.drainAndPersist(dirty)
			return
		}
	}
}

// drainAndPersist 在收到 stop 后把 events channel 剩余事件全部应用，再写盘一次。
func (t *Tracker) drainAndPersist(dirty bool) {
	for {
		select {
		case ev := <-t.events:
			t.applyEvent(ev)
			dirty = true
		default:
			if dirty {
				t.persistAll()
			}
			return
		}
	}
}

func (t *Tracker) applyEvent(ev CallEvent) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, target := range []Stats{t.wsStats, t.globStats} {
		s := target[ev.SkillName]
		if s == nil {
			s = &SkillStats{SectionCalls: map[string]int{}}
			target[ev.SkillName] = s
		}
		s.Calls = append(s.Calls, ev.At)
		if ev.Anchor != "" {
			s.SectionCalls[ev.Anchor]++
		}
	}
}

// persistAll 把 wsStats + globStats 写到对应文件；锁内执行避免与 Snapshot/applyEvent 冲突。
func (t *Tracker) persistAll() {
	t.mu.Lock()
	wsCopy := cloneStats(t.wsStats)
	glCopy := cloneStats(t.globStats)
	t.mu.Unlock()
	_ = SaveStats(t.wsPath, wsCopy)
	_ = SaveStats(t.globPath, glCopy)
}

func cloneStats(in Stats) Stats {
	out := make(Stats, len(in))
	for k, v := range in {
		if v == nil {
			continue
		}
		c := *v
		c.Calls = append([]time.Time(nil), v.Calls...)
		c.SectionCalls = make(map[string]int, len(v.SectionCalls))
		for kk, vv := range v.SectionCalls {
			c.SectionCalls[kk] = vv
		}
		out[k] = &c
	}
	return out
}

func (t *Tracker) DisclosureSnapshot() contract.SkillDisclosureSnapshot {
	cfg := EnvTierConfig()
	wsStats, glStats := t.Snapshot()
	return contract.SkillDisclosureSnapshot{
		Workspace: disclosureStats(wsStats),
		Global:    disclosureStats(glStats),
		Config: contract.SkillDisclosureConfig{
			HalfLife:       cfg.HalfLife,
			FrozenDuration: cfg.FrozenDuration,
			WSMinCalls:     cfg.WSMinCalls,
			WSWeight:       cfg.WSWeight,
		},
	}
}

func disclosureStats(stats Stats) contract.SkillDisclosureStats {
	out := make(contract.SkillDisclosureStats, len(stats))
	for name, stat := range stats {
		if stat == nil {
			continue
		}
		out[name] = &contract.SkillDisclosureSkillStats{Calls: append([]time.Time(nil), stat.Calls...)}
	}
	return out
}
