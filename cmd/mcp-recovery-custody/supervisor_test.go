package main

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody/reconcilecoordinator"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody/sqliteauthority"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
)

func TestDependencyFactoriesAreExplicitAndFailClosed(t *testing.T) {
	cfg := testConfig(t)
	if _, err := (dependencyFactories{}).resolve(t.Context(), cfg); err == nil {
		t.Fatal("empty dependency factories unexpectedly resolved")
	}
	factories := dependencyFactories{
		EvidenceFinalizer: func(context.Context, serviceConfig) (sqliteauthority.EvidenceFinalizer, error) {
			return nil, nil
		},
		ReceiptVerifier: func(context.Context, serviceConfig) (recoverycustody.ReceiptVerifier, error) {
			return testVerifier{}, nil
		},
	}
	if _, err := factories.resolve(t.Context(), cfg); err == nil {
		t.Fatal("nil finalizer factory result unexpectedly resolved")
	}
}

func TestSupervisorConstructionRunsStartupScanBeforeRun(t *testing.T) {
	requireLoopback(t)
	supervisor := newTestSupervisor(t)
	t.Cleanup(func() { _ = supervisor.close() })
	if supervisor.service.ready.Load() || supervisor.service.endpointAddress() != "" {
		t.Fatal("supervisor became ready before Run")
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := startSupervisor(ctx, supervisor, "recovery-custody.test.startup")
	waitSupervisorReady(t, supervisor)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("supervisor.Run() = %v", err)
	}
	if !supervisor.service.closed.Load() {
		t.Fatal("supervisor did not close authority after runner shutdown")
	}
}

func TestSupervisorRejectsSharedOrMissingCoordinatorStateWithoutMutation(t *testing.T) {
	serviceCfg := testConfig(t)
	coordinatorCfg, parent := coordinatorCandidateConfig(t)
	coordinatorCfg.Path = filepath.Join(parent, "missing.sqlite")
	factories := dependencyFactories{
		EvidenceFinalizer: func(context.Context, serviceConfig) (sqliteauthority.EvidenceFinalizer, error) {
			return testFinalizer{}, nil
		},
		ReceiptVerifier: func(context.Context, serviceConfig) (recoverycustody.ReceiptVerifier, error) {
			return testVerifier{}, nil
		},
	}
	if supervisor, err := newRecoveryCustodySupervisor(t.Context(), supervisorConfig{Service: serviceCfg, Coordinator: coordinatorCfg}, factories); supervisor != nil || err == nil {
		t.Fatalf("missing coordinator result = (%v, %v)", supervisor, err)
	}
	shared := coordinatorCfg
	shared.Path = serviceCfg.Authority.Path
	if supervisor, err := newRecoveryCustodySupervisor(t.Context(), supervisorConfig{Service: serviceCfg, Coordinator: shared}, factories); supervisor != nil || err == nil {
		t.Fatalf("shared coordinator result = (%v, %v)", supervisor, err)
	}
}

func TestSupervisorRunRejectsNilContext(t *testing.T) {
	var supervisor *recoveryCustodySupervisor
	if err := supervisor.Run(context.Background()); err == nil {
		t.Fatal("nil supervisor unexpectedly ran")
	}
	if err := (&recoveryCustodySupervisor{}).Run(nil); !errors.Is(err, recoverycustody.ErrContextRequired) {
		t.Fatalf("nil context error = %v", err)
	}
}

func TestRunSupervisorWorkerDeliversOneTerminalResult(t *testing.T) {
	errRunner := errors.New("runner failure")
	tests := []struct {
		name      string
		run       func(context.Context) error
		wantError bool
	}{
		{name: "error", run: func(context.Context) error { return errRunner }, wantError: true},
		{name: "panic", run: func(context.Context) error { // archguard:ignore panic_count -- verifies worker panic conversion.
			panic("runner panic")
		}, wantError: true},
		{name: "goexit", run: func(context.Context) error { runtime.Goexit(); return nil }, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			done := make(chan error, 2)
			runtimesafe.SafeGo(t.Context(), nil, "recovery-custody.test.runner-"+test.name, func(ctx context.Context) {
				runSupervisorWorker(ctx, done, test.run)
			})
			err := <-done
			if !test.wantError || err == nil {
				t.Fatalf("runner result = %v, want terminal error", err)
			}
			if test.name == "error" && !errors.Is(err, errRunner) {
				t.Fatalf("runner error = %v, want %v", err, errRunner)
			}
			select {
			case err := <-done:
				t.Fatalf("runner delivered duplicate terminal result: %v", err)
			default:
			}
		})
	}
}

func TestCancellationClassificationPreservesJoinedFailure(t *testing.T) {
	realFailure := errors.New("coordinator failed")
	joined := errors.Join(context.Canceled, realFailure)
	if isCancellation(joined) {
		t.Fatal("joined cancellation and failure was classified as pure cancellation")
	}
	if err := normalizeSupervisorCoordinatorError(joined); !errors.Is(err, realFailure) {
		t.Fatalf("normalized joined error = %v, want retained failure", err)
	}
	if err := normalizeSupervisorCoordinatorError(context.Canceled); err != nil {
		t.Fatalf("normalized direct cancellation = %v, want nil", err)
	}
}

// TestSupervisorRejectsConcurrentRun keeps one supervisor from attaching the
// same http.Server and authority to two independent runner lifecycles. Without
// this boundary, canceling either caller shuts down both listeners and can
// release the shared authority while the other caller is still active.
func TestSupervisorRejectsConcurrentRun(t *testing.T) {
	requireLoopback(t)
	supervisor := newTestSupervisor(t)
	firstCtx, cancelFirst := context.WithCancel(t.Context())
	firstDone := startSupervisor(firstCtx, supervisor, "recovery-custody.test.first-run")
	t.Cleanup(func() {
		cancelFirst()
		waitSupervisorStop(t, firstDone, "first")
	})
	waitSupervisorReady(t, supervisor)

	secondCtx, cancelSecond := context.WithCancel(t.Context())
	defer cancelSecond()
	secondDone := startSupervisor(secondCtx, supervisor, "recovery-custody.test.second-run")
	select {
	case err := <-secondDone:
		if err == nil {
			t.Fatal("concurrent supervisor Run unexpectedly succeeded")
		}
	case <-time.After(250 * time.Millisecond):
		cancelSecond()
		waitSupervisorStop(t, secondDone, "second")
		t.Fatal("concurrent supervisor Run was not rejected promptly")
	}
}

func newTestSupervisor(t *testing.T) *recoveryCustodySupervisor {
	t.Helper()
	serviceCfg := testConfig(t)
	coordinatorCfg, _ := coordinatorCandidateConfig(t)
	if err := reconcilecoordinator.Bootstrap(t.Context(), coordinatorCfg); err != nil {
		t.Fatal(err)
	}
	supervisor, err := newRecoveryCustodySupervisor(t.Context(), supervisorConfig{
		Service: serviceCfg, Coordinator: coordinatorCfg,
	}, testDependencyFactories())
	if err != nil {
		t.Fatal(err)
	}
	return supervisor
}

func testDependencyFactories() dependencyFactories {
	return dependencyFactories{
		EvidenceFinalizer: func(context.Context, serviceConfig) (sqliteauthority.EvidenceFinalizer, error) {
			return testFinalizer{}, nil
		},
		ReceiptVerifier: func(context.Context, serviceConfig) (recoverycustody.ReceiptVerifier, error) {
			return testVerifier{}, nil
		},
	}
}

func requireLoopback(t *testing.T) {
	t.Helper()
	probe, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("loopback listeners unavailable: %v", err)
	}
	_ = probe.Close()
}

func startSupervisor(ctx context.Context, supervisor *recoveryCustodySupervisor, label string) <-chan error {
	done := make(chan error, 1)
	runtimesafe.SafeGo(ctx, nil, label, func(ctx context.Context) {
		done <- supervisor.Run(ctx)
	})
	return done
}

func waitSupervisorReady(t *testing.T, supervisor *recoveryCustodySupervisor) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !supervisor.service.ready.Load() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !supervisor.service.ready.Load() {
		t.Fatal("supervisor did not become ready")
	}
}

func waitSupervisorStop(t *testing.T, done <-chan error, name string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Errorf("%s supervisor run did not stop", name)
	}
}
