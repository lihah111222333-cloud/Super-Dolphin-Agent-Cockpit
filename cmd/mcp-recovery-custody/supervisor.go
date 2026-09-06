package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody/reconcilecoordinator"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody/sqliteauthority"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
)

// EvidenceFinalizerFactory constructs the external evidence finalizer.
type EvidenceFinalizerFactory func(context.Context, serviceConfig) (sqliteauthority.EvidenceFinalizer, error)

// ReceiptVerifierFactory constructs the external terminal receipt verifier.
type ReceiptVerifierFactory func(context.Context, serviceConfig) (recoverycustody.ReceiptVerifier, error)

type dependencyFactories struct {
	EvidenceFinalizer EvidenceFinalizerFactory
	ReceiptVerifier   ReceiptVerifierFactory
}

func (f dependencyFactories) resolve(ctx context.Context, cfg serviceConfig) (dependencies, error) {
	if ctx == nil {
		return dependencies{}, recoverycustody.ErrContextRequired
	}
	if f.EvidenceFinalizer == nil || f.ReceiptVerifier == nil {
		return dependencies{}, errors.New("recovery custody evidence dependency factories are required")
	}
	finalizer, err := f.EvidenceFinalizer(ctx, cfg)
	if err != nil {
		return dependencies{}, fmt.Errorf("construct recovery custody evidence finalizer: %w", err)
	}
	if dependencyNil(finalizer) {
		return dependencies{}, errors.New("recovery custody evidence finalizer factory returned nil")
	}
	verifier, err := f.ReceiptVerifier(ctx, cfg)
	if err != nil {
		return dependencies{}, fmt.Errorf("construct recovery custody receipt verifier: %w", err)
	}
	if dependencyNil(verifier) {
		return dependencies{}, errors.New("recovery custody receipt verifier factory returned nil")
	}
	return dependencies{Finalizer: finalizer, Verifier: verifier}, nil
}

// supervisorConfig keeps coordinator state explicit and separate from the
// authority database.  Coordinator.Open performs schema and owner-fence
// verification; this layer never bootstraps or migrates either database.
type supervisorConfig struct {
	Service     serviceConfig
	Coordinator reconcilecoordinator.Config
}

type recoveryCustodySupervisor struct {
	service     *custodyService
	coordinator *reconcilecoordinator.Coordinator
	runStarted  atomic.Bool
	closeOnce   sync.Once
	closeErr    error
}

// newRecoveryCustodySupervisor constructs the complete service-owned seam.
// Dependencies are resolved before opening any SQLite state, and the finite
// startup scan completes before either HTTP or coordinator runner starts.
func newRecoveryCustodySupervisor(ctx context.Context, cfg supervisorConfig, factories dependencyFactories) (*recoveryCustodySupervisor, error) {
	if ctx == nil {
		return nil, recoverycustody.ErrContextRequired
	}
	if err := validateServiceConfig(cfg.Service); err != nil {
		return nil, err
	}
	if err := cfg.Coordinator.Validate(); err != nil {
		return nil, err
	}
	if err := validateSupervisorPaths(cfg.Service.Authority.Path, cfg.Coordinator.Path); err != nil {
		return nil, err
	}
	deps, err := factories.resolve(ctx, cfg.Service)
	if err != nil {
		return nil, err
	}
	service, err := newService(ctx, cfg.Service, deps)
	if err != nil {
		return nil, err
	}
	coordinator, err := reconcilecoordinator.Open(ctx, cfg.Coordinator, service.authority)
	if err != nil {
		_ = service.close()
		return nil, fmt.Errorf("open recovery custody reconcile coordinator: %w", err)
	}
	supervisor := &recoveryCustodySupervisor{service: service, coordinator: coordinator}
	if err := coordinator.StartupScan(ctx); err != nil {
		_ = supervisor.close()
		return nil, fmt.Errorf("scan recovery custody active resolutions: %w", err)
	}
	return supervisor, nil
}

func validateSupervisorPaths(authorityPath, coordinatorPath string) error {
	if err := validateSupervisorPath("authority", authorityPath); err != nil {
		return err
	}
	if err := validateSupervisorPath("coordinator", coordinatorPath); err != nil {
		return err
	}
	if authorityPath == coordinatorPath {
		return errors.New("recovery custody authority and coordinator paths must differ")
	}
	return validateSupervisorDatabaseIdentity(authorityPath, coordinatorPath)
}

func validateSupervisorDatabaseIdentity(authorityPath, coordinatorPath string) error {
	authorityInfo, err := os.Lstat(authorityPath)
	if errors.Is(err, os.ErrNotExist) {
		// The service may explicitly bootstrap its authority database during
		// construction; OpenAuthority still verifies the resulting identity.
		return nil
	}
	if err != nil || authorityInfo.Mode()&os.ModeSymlink != 0 || !authorityInfo.Mode().IsRegular() {
		return errors.New("recovery custody authority database identity is unavailable")
	}
	coordinatorInfo, err := os.Lstat(coordinatorPath)
	if err == nil && os.SameFile(authorityInfo, coordinatorInfo) {
		return errors.New("recovery custody authority and coordinator paths must differ")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("recovery custody coordinator database identity is unavailable")
	}
	return nil
}

func validateSupervisorPath(name, path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("recovery custody %s path must be absolute and canonical", name)
	}
	return nil
}

// Run starts the coordinator runner and HTTP service only after construction's
// schema/identity checks and StartupScan. It starts one controlled lifecycle
// and closes coordinator state and authority only after both runners return.
func (s *recoveryCustodySupervisor) Run(ctx context.Context) error {
	if ctx == nil {
		return recoverycustody.ErrContextRequired
	}
	if s == nil || s.service == nil || s.coordinator == nil {
		return errors.New("recovery custody supervisor is not initialized")
	}
	if !s.runStarted.CompareAndSwap(false, true) {
		return errors.New("recovery custody supervisor Run may only be called once")
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	coordinatorDone := make(chan error, 1)
	serviceDone := make(chan error, 1)
	runtimesafe.SafeGo(runCtx, nil, "recovery-custody.supervisor.coordinator", func(ctx context.Context) {
		runSupervisorWorker(ctx, coordinatorDone, s.coordinator.Run)
	})
	runtimesafe.SafeGo(runCtx, nil, "recovery-custody.supervisor.service", func(ctx context.Context) {
		runSupervisorWorker(ctx, serviceDone, func(ctx context.Context) error {
			return s.service.serveInternal(ctx, false)
		})
	})

	var serviceErr, coordinatorErr error
	select {
	case serviceErr = <-serviceDone:
		cancel()
		coordinatorErr = <-coordinatorDone
	case coordinatorErr = <-coordinatorDone:
		cancel()
		serviceErr = <-serviceDone
	}
	closeErr := s.close()
	if ctx.Err() != nil && isCancellation(serviceErr, coordinatorErr) {
		return closeErr
	}
	return errors.Join(serviceErr, normalizeSupervisorCoordinatorError(coordinatorErr), closeErr)
}

func runSupervisorWorker(ctx context.Context, done chan<- error, run func(context.Context) error) {
	result := errors.New("recovery custody supervisor runner exited without returning")
	defer func() {
		if recovered := recover(); recovered != nil {
			result = errors.New("recovery custody supervisor runner panicked")
		}
		done <- result
	}()
	result = run(ctx)
}

// serve is the lifecycle-shaped entrypoint used by command composition; Run
// remains the public runner contract for callers that use a runner group.
func (s *recoveryCustodySupervisor) serve(ctx context.Context) error {
	return s.Run(ctx)
}

func normalizeSupervisorCoordinatorError(err error) error {
	if isOnlyCancellation(err) {
		return nil
	}
	return err
}

func isCancellation(errs ...error) bool {
	for _, err := range errs {
		if !isOnlyCancellation(err) {
			return false
		}
	}
	return true
}

func isOnlyCancellation(err error) bool {
	if err == nil {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		causes := joined.Unwrap()
		if len(causes) == 0 {
			return false
		}
		for _, cause := range causes {
			if !isOnlyCancellation(cause) {
				return false
			}
		}
		return true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return isOnlyCancellation(wrapped.Unwrap())
	}
	return errors.Is(err, context.Canceled)
}

func (s *recoveryCustodySupervisor) close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.closeErr = errors.Join(s.coordinator.Close(), s.service.close())
	})
	return s.closeErr
}
