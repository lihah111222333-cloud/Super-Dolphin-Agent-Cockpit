package shared

import (
	"context"
	"errors"
	"testing"
)

func TestNonNilContext(t *testing.T) {
	t.Run("nil returns background", func(t *testing.T) {
		ctx := NonNilContext(nil) //nolint:staticcheck // testing nil-ctx defense
		if ctx == nil {
			t.Fatal("expected background context")
		}
		if err := ctx.Err(); err != nil {
			t.Fatalf("expected nil err, got %v", err)
		}
	})

	t.Run("non nil preserved", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if got := NonNilContext(ctx); got != ctx {
			t.Fatal("expected original context")
		}
	})
}

func TestCheckCtx(t *testing.T) {
	if err := CheckCtx(nil); err != nil { //nolint:staticcheck // testing nil-ctx behavior
		t.Fatalf("expected nil err for nil context, got %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := CheckCtx(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled error, got %v", err)
	}
}
