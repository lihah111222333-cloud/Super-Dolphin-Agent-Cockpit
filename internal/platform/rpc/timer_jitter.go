package rpc

import (
	"math/rand"
	"time"
)

// timerDelayWithJitter 为周期任务增加最多 25% 的正向抖动，避免多实例同时触发。
func timerDelayWithJitter(interval time.Duration) time.Duration {
	if interval <= 0 {
		return interval
	}
	maxJitter := interval / 4
	if maxJitter <= 0 {
		return interval
	}
	return interval + time.Duration(rand.Int63n(int64(maxJitter)))
}
