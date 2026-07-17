package appupdaterecovery

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"
)

func TestComputeReleaseDigestContextCancelsBlockedChunk(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "release-")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("release-content"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = computeReleaseDigestContextWithOps(ctx, file.Name(), releaseDigestOps{
		readChunk: func(ctx context.Context, _ io.Reader, _ []byte) (int, error) {
			<-ctx.Done()
			return 0, context.Cause(ctx)
		},
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("computeReleaseDigestContextWithOps() error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("blocked digest cancellation elapsed %s", elapsed)
	}
}

func TestComputeReleaseDigestContextRejectsAlreadyCancelledWalk(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(context.Canceled)
	if _, err := ComputeReleaseDigestContext(ctx, root); !errors.Is(err, context.Canceled) {
		t.Fatalf("ComputeReleaseDigestContext() error = %v, want canceled", err)
	}
}
