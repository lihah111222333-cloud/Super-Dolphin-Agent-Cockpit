package cron

import (
	"math/rand"
	"time"
)

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
