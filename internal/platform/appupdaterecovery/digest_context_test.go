package appupdaterecovery

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"go.uber.org/goleak"
)

func TestComputeReleaseDigestContextCancelsBlockedChunk(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
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
	ops, release := newBlockedReleaseDigestOps(t)
	defer release()
	started := time.Now()
	_, err = computeReleaseDigestContextWithOps(ctx, file.Name(), ops)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("computeReleaseDigestContextWithOps() error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("blocked digest cancellation elapsed %s", elapsed)
	}
}

func newBlockedReleaseDigestOps(t *testing.T) (releaseDigestOps, func()) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	ops := defaultReleaseDigestOps()
	ops.openFile = func(string) (releaseDigestFile, error) { return reader, nil }
	return ops, func() {
		_ = reader.Close()
		_ = writer.Close()
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
