// Package idgen 生成 agent 与通用实体使用的短 ID。
package idgen

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync/atomic"
	"time"
)

// NewID 生成带前缀、毫秒时间戳和随机后缀的通用 ID。
func NewID(prefix string) string {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	return fmt.Sprintf("%s_%d_%s", prefix, time.Now().UnixMilli(), hex.EncodeToString(buf))
}

var lastAgentIDValue atomic.Uint64

// NewAgentID 生成根 agent ID，并保证进程内单调递增。
// 并发启动可能落在同一时钟刻度内，因此不能只依赖墙上时间去重。
func NewAgentID() string {
	return fmt.Sprintf("agent_%d", nextAgentIDValue())
}

// nextAgentIDValue 返回严格大于上次值的纳秒时间戳候选。
func nextAgentIDValue() uint64 {
	for {
		now := uint64(time.Now().UnixNano())
		last := lastAgentIDValue.Load()
		candidate := now
		if candidate <= last {
			candidate = last + 1
		}
		if lastAgentIDValue.CompareAndSwap(last, candidate) {
			return candidate
		}
	}
}

// NewChildAgentID 通过父 ID 和调用方提供的序号生成子 agent ID。
// 序号由持久化层或调度方决定，本函数只负责稳定拼接。
func NewChildAgentID(parentID string, seq int) string {
	return fmt.Sprintf("%s-%d", parentID, seq)
}
