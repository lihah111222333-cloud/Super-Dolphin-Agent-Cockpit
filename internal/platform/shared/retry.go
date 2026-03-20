package shared

import (
	"context"
	"math"
	"time"
)

func Retry(ctx context.Context, maxAttempts int, base time.Duration, fn func() error) error {
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err := fn(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if attempt == maxAttempts-1 {
			break
		}
		delay := time.Duration(float64(base) * math.Pow(2, float64(attempt)))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	return lastErr
}
