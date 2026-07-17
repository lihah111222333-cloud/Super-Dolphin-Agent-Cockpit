package appupdaterecovery

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"
)

const (
	blockingReleaseFilesystemHelperEnv      = "SUPER_DOLPHIN_TEST_BLOCKING_FS_HELPER"
	blockingReleaseFilesystemHelperFIFOEnv  = "SUPER_DOLPHIN_TEST_BLOCKING_FS_FIFO"
	blockingReleaseFilesystemHelperStartEnv = "SUPER_DOLPHIN_TEST_BLOCKING_FS_STARTED"
	blockingReleaseFilesystemHelperDoneEnv  = "SUPER_DOLPHIN_TEST_BLOCKING_FS_FINISHED"
)

func TestMain(m *testing.M) {
	if os.Getenv(blockingReleaseFilesystemHelperEnv) == "1" {
		if err := runBlockingReleaseFilesystemHelperProcess(); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		os.Exit(0)
	}
	if handled, err := RunReleaseFilesystemHelperIfRequested(os.Stdin, os.Stdout); handled {
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runBlockingReleaseFilesystemHelperProcess() error {
	started := os.Getenv(blockingReleaseFilesystemHelperStartEnv)
	if err := os.WriteFile(started, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		return fmt.Errorf("write blocking helper start marker: %w", err)
	}
	file, err := os.Open(os.Getenv(blockingReleaseFilesystemHelperFIFOEnv))
	if err != nil {
		return fmt.Errorf("open blocking helper FIFO: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close blocking helper FIFO: %w", err)
	}
	if err := os.WriteFile(os.Getenv(blockingReleaseFilesystemHelperDoneEnv), []byte("finished"), 0o600); err != nil {
		return fmt.Errorf("write blocking helper finish marker: %w", err)
	}
	return nil
}

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

func TestReleaseFilesystemHelperProtocolRejectsUnknownField(t *testing.T) {
	raw := `{"version":1,"operation":"digest","path":"/tmp/release","unexpected":true}`
	if _, err := decodeReleaseFilesystemHelperRequest(strings.NewReader(raw)); err == nil {
		t.Fatal("decodeReleaseFilesystemHelperRequest() accepted unknown field")
	}
}

func TestReleaseFilesystemHelperProtocolRejectsOversizedInput(t *testing.T) {
	raw := `{"version":1,"operation":"digest","path":"/tmp/release"}`
	raw += strings.Repeat(" ", releaseFilesystemHelperMaxRequestBytes-len(raw)+1)
	if _, err := decodeReleaseFilesystemHelperRequest(strings.NewReader(raw)); err == nil {
		t.Fatal("decodeReleaseFilesystemHelperRequest() accepted oversized input")
	}
}

func TestPrepareReleaseFilesystemHelperStagesExecutesAndCleans(t *testing.T) {
	previous, hadPrevious := os.LookupEnv(releaseFilesystemHelperExecutableEnv)
	cleanup, err := PrepareReleaseFilesystemHelper()
	if err != nil {
		t.Fatal(err)
	}
	cleaned := false
	t.Cleanup(func() {
		if !cleaned {
			if err := cleanup(); err != nil {
				t.Errorf("cleanup staged release filesystem helper: %v", err)
			}
		}
	})
	staged := os.Getenv(releaseFilesystemHelperExecutableEnv)
	if staged == "" {
		t.Fatal("PrepareReleaseFilesystemHelper() did not publish staged executable")
	}
	if _, err := ComputeReleaseDigestContext(t.Context(), t.TempDir()); err != nil {
		t.Fatalf("execute staged release filesystem helper: %v", err)
	}
	if err := cleanup(); err != nil {
		t.Fatalf("cleanup staged release filesystem helper: %v", err)
	}
	cleaned = true
	if _, err := os.Stat(staged); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staged release filesystem helper remains after cleanup: %v", err)
	}
	current, hasCurrent := os.LookupEnv(releaseFilesystemHelperExecutableEnv)
	if current != previous || hasCurrent != hadPrevious {
		t.Fatalf("helper executable environment after cleanup = %q, %v, want %q, %v", current, hasCurrent, previous, hadPrevious)
	}
}

func TestReleaseFilesystemHelperProtocolRejectsValueAndError(t *testing.T) {
	raw := `{"version":1,"operation":"digest","value":"value","error":{"code":"filesystem","message":"failure"}}`
	if _, err := decodeReleaseFilesystemHelperResponse([]byte(raw)); err == nil {
		t.Fatal("decodeReleaseFilesystemHelperResponse() accepted value and error")
	}
}

func TestReleaseFilesystemHelperPreservesNotExistClassification(t *testing.T) {
	_, err := ComputeReleaseDigestContext(t.Context(), t.TempDir()+"/missing")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ComputeReleaseDigestContext() error = %v, want not exist", err)
	}
}
