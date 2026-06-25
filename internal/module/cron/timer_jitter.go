// Package cron exposes the host-side CRUD surface and the scheduler runtime
// for scheduled agent tasks.
//
// 这里有两件事：service 先把坏配置挡住；scheduler 只跑已入库的 job，
// 靠 CAS 和 claim_token 防止重复推进。
package cron

import (
	"math/rand"
	"time"
)

// timerDelayWithJitter 在 interval 基础上叠加最多 25% 的随机抖动，避免多实例同时触发。
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
