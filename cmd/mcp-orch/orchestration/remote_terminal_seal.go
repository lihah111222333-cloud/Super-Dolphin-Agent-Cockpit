package orchestration

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

type remoteTerminalTurnRef struct {
	threadID string
	turnID   string
}

type remoteTerminalTruth struct {
	eventID     string
	fingerprint [sha256.Size]byte
}

const defaultRemoteTerminalSealCapacity = 4096

type remoteTerminalSealEntry struct {
	truth             remoteTerminalTruth
	sequence          uint64
	inFlight          bool
	retryable         bool
	clearWhenReleased bool
}

// remoteTerminalSealStats exposes bounded-seal accounting for logs and regression tests.
type remoteTerminalSealStats struct {
	Capacity          int
	Entries           int
	InFlight          int
	CapacityEvictions uint64
	LifecycleClears   uint64
	LifecycleDeferred uint64
}

type remoteTerminalAcceptance struct {
	accepted bool
	ref      remoteTerminalTurnRef
	evicted  bool
}

// acceptRemoteTurnTerminal 封存每个 TurnRef 的首个 canonical event，直到所属 thread
// 被归档或被有界的确定性淘汰回收。处理中条目不得淘汰，避免生命周期尚未收敛时被冲突终态抢占。
func (c *turnController) acceptRemoteTurnTerminal(terminal turndto.TurnTerminalV2) (remoteTerminalAcceptance, error) {
	if c == nil {
		return remoteTerminalAcceptance{}, errors.New("turn controller is required")
	}
	truth, err := remoteTerminalTruthFor(terminal)
	if err != nil {
		return remoteTerminalAcceptance{}, err
	}
	ref := remoteTerminalTurnRef{threadID: terminal.ThreadID, turnID: terminal.TurnID}
	c.remoteTerminalMu.Lock()
	defer c.remoteTerminalMu.Unlock()
	if c.remoteTerminalSeal == nil {
		c.remoteTerminalSeal = make(map[remoteTerminalTurnRef]remoteTerminalSealEntry)
	}
	first, exists := c.remoteTerminalSeal[ref]
	if !exists {
		if c.remoteTerminalNext == ^uint64(0) {
			return remoteTerminalAcceptance{}, errors.New("remote terminal seal sequence exhausted")
		}
		evicted := false
		if len(c.remoteTerminalSeal) >= c.remoteTerminalSealLimitLocked() {
			if !c.evictOldestRemoteTerminalSealLocked() {
				return remoteTerminalAcceptance{}, errors.New("remote terminal seal capacity exhausted by in-flight terminals")
			}
			evicted = true
		}
		c.remoteTerminalNext++
		c.remoteTerminalSeal[ref] = remoteTerminalSealEntry{truth: truth, sequence: c.remoteTerminalNext, inFlight: true}
		return remoteTerminalAcceptance{accepted: true, ref: ref, evicted: evicted}, nil
	}
	if first.truth == truth {
		if first.retryable {
			first.inFlight, first.retryable = true, false
			c.remoteTerminalSeal[ref] = first
			return remoteTerminalAcceptance{accepted: true, ref: ref}, nil
		}
		return remoteTerminalAcceptance{}, nil
	}
	return remoteTerminalAcceptance{}, fmt.Errorf("remote terminal truth conflict: first_event_id=%q conflicting_event_id=%q", first.truth.eventID, truth.eventID)
}

func remoteTerminalTruthFor(terminal turndto.TurnTerminalV2) (remoteTerminalTruth, error) {
	encoded, err := json.Marshal(terminal)
	if err != nil {
		return remoteTerminalTruth{}, fmt.Errorf("marshal remote terminal fingerprint: %w", err)
	}
	return remoteTerminalTruth{eventID: terminal.EventID, fingerprint: sha256.Sum256(encoded)}, nil
}

func (c *turnController) remoteTerminalSealLimitLocked() int {
	if c.remoteTerminalCapacity > 0 {
		return c.remoteTerminalCapacity
	}
	return defaultRemoteTerminalSealCapacity
}

// evictOldestRemoteTerminalSealLocked 确定性淘汰最早且已完成处理的封存条目。
func (c *turnController) evictOldestRemoteTerminalSealLocked() bool {
	var oldestRef remoteTerminalTurnRef
	var oldestSequence uint64
	found := false
	for ref, entry := range c.remoteTerminalSeal {
		if entry.inFlight {
			continue
		}
		if !found || entry.sequence < oldestSequence {
			oldestRef, oldestSequence, found = ref, entry.sequence, true
		}
	}
	if !found {
		return false
	}
	delete(c.remoteTerminalSeal, oldestRef)
	c.remoteTerminalEvictions++
	return true
}

// releaseRemoteTurnTerminal 在 terminal 生命周期处理结束后解除条目的淘汰保护。
func (c *turnController) releaseRemoteTurnTerminal(acceptance remoteTerminalAcceptance) {
	if c == nil || !acceptance.accepted {
		return
	}
	c.remoteTerminalMu.Lock()
	defer c.remoteTerminalMu.Unlock()
	entry, exists := c.remoteTerminalSeal[acceptance.ref]
	if !exists || !entry.inFlight {
		return
	}
	if entry.clearWhenReleased {
		delete(c.remoteTerminalSeal, acceptance.ref)
		c.remoteTerminalLifecycleClears++
		return
	}
	entry.inFlight = false
	c.remoteTerminalSeal[acceptance.ref] = entry
}

// retryRemoteTurnTerminal 在提交失败后标记可重试，保持首个 truth 并拒绝冲突终态。
func (c *turnController) retryRemoteTurnTerminal(acceptance remoteTerminalAcceptance) {
	if c == nil || !acceptance.accepted {
		return
	}
	c.remoteTerminalMu.Lock()
	defer c.remoteTerminalMu.Unlock()
	entry, exists := c.remoteTerminalSeal[acceptance.ref]
	if exists && entry.inFlight {
		if entry.clearWhenReleased {
			delete(c.remoteTerminalSeal, acceptance.ref)
			c.remoteTerminalLifecycleClears++
			return
		}
		entry.inFlight = false
		entry.retryable = true
		c.remoteTerminalSeal[acceptance.ref] = entry
	}
}

// clearRemoteTerminalsForThread 在归档时回收该 thread 的已完成封存；处理中 terminal
// 延后到 handler 释放后清理，避免重新打开 target-changed 防护窗口。
func (c *turnController) clearRemoteTerminalsForThread(threadID string) remoteTerminalSealStats {
	if c == nil || strings.TrimSpace(threadID) == "" {
		return remoteTerminalSealStats{}
	}
	c.remoteTerminalMu.Lock()
	defer c.remoteTerminalMu.Unlock()
	for ref, entry := range c.remoteTerminalSeal {
		if ref.threadID != threadID {
			continue
		}
		if entry.inFlight {
			entry.clearWhenReleased = true
			c.remoteTerminalSeal[ref] = entry
			c.remoteTerminalLifecycleDeferred++
			continue
		}
		delete(c.remoteTerminalSeal, ref)
		c.remoteTerminalLifecycleClears++
	}
	return c.remoteTerminalSealStatsLocked()
}

func (c *turnController) remoteTerminalSealStats() remoteTerminalSealStats {
	if c == nil {
		return remoteTerminalSealStats{}
	}
	c.remoteTerminalMu.Lock()
	defer c.remoteTerminalMu.Unlock()
	return c.remoteTerminalSealStatsLocked()
}

func (c *turnController) remoteTerminalSealStatsLocked() remoteTerminalSealStats {
	stats := remoteTerminalSealStats{
		Capacity:          c.remoteTerminalSealLimitLocked(),
		Entries:           len(c.remoteTerminalSeal),
		CapacityEvictions: c.remoteTerminalEvictions,
		LifecycleClears:   c.remoteTerminalLifecycleClears,
		LifecycleDeferred: c.remoteTerminalLifecycleDeferred,
	}
	for _, entry := range c.remoteTerminalSeal {
		if entry.inFlight {
			stats.InFlight++
		}
	}
	return stats
}
