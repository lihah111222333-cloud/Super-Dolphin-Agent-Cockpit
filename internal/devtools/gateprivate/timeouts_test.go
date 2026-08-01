package gateprivate

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWithTimeoutPreservesParentCancellation(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	ctx, cancel := WithTimeout(parent, time.Minute)
	defer cancel()
	cancelParent()
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("timeout context error = %v, want canceled", ctx.Err())
	}
}
