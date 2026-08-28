//go:build darwin

package appupdaterecovery

import (
	"context"
	"errors"
	"net"
	"os"
	"sync"
	"testing"
	"time"
)

func TestRollbackRestartReadinessRetriesRefusedPublishedEndpoint(t *testing.T) {
	endpoint := newRefusedRollbackRestartEndpoint(t)
	token := "rollback-restart-readiness"
	exact := currentRollbackRestartExactProcess(t, endpoint, token)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	result := make(chan rollbackEndpointWaitResult, 1)
	var workers sync.WaitGroup
	workers.Go(func() {
		identity, err := waitRollbackRestartEndpoint(ctx, exact)
		result <- rollbackEndpointWaitResult{endpoint: identity, err: err}
	})
	t.Cleanup(func() {
		cancel()
		workers.Wait()
	})
	select {
	case early := <-result:
		t.Fatalf("wait returned before listener became ready: %v", early.err)
	case <-time.After(3 * rollbackRestartEndpointPoll):
	}
	startReadyRollbackRestartEndpoint(t, endpoint, token)
	assertRollbackRestartEndpointReady(t, ctx, result, endpoint)
}

func TestRollbackRestartReadinessRefusedEndpointHonorsDeadline(t *testing.T) {
	endpoint := newRefusedRollbackRestartEndpoint(t)
	exact := currentRollbackRestartExactProcess(t, endpoint, "rollback-restart-deadline")
	ctx, cancel := context.WithTimeout(t.Context(), 3*rollbackRestartEndpointPoll)
	defer cancel()
	started := time.Now()
	_, err := waitRollbackRestartEndpoint(ctx, exact)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("deadline wait elapsed = %v, want <= 1s", elapsed)
	}
}

func newRefusedRollbackRestartEndpoint(t *testing.T) string {
	t.Helper()
	file, err := os.CreateTemp("/tmp", "sd-rollback-ready-")
	if err != nil {
		t.Fatalf("create refused rollback endpoint path: %v", err)
	}
	endpoint := file.Name() + ".sock"
	if err := file.Close(); err != nil {
		t.Fatalf("close refused rollback endpoint path: %v", err)
	}
	if err := os.Remove(file.Name()); err != nil {
		t.Fatalf("remove refused rollback endpoint path: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Remove(endpoint); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Errorf("remove refused rollback endpoint: %v", err)
		}
	})
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: endpoint, Net: "unix"})
	if err != nil {
		t.Fatalf("listen refused rollback endpoint: %v", err)
	}
	listener.SetUnlinkOnClose(false)
	if err := os.Chmod(endpoint, 0o600); err != nil {
		_ = listener.Close()
		t.Fatalf("chmod refused rollback endpoint: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("close refused rollback listener: %v", err)
	}
	return endpoint
}
