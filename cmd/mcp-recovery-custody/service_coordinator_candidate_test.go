package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody/reconcilecoordinator"
)

var (
	errServiceCoordinatorCandidate       = errors.New("test-owned recovery custody coordinator candidate is invalid")
	errServiceCoordinatorSharedAuthority = errors.New("test-owned recovery custody coordinator cannot share the authority database")
)

// serviceCoordinatorAuthorityAdapter is test-owned: production service code
// must not import or start reconcilecoordinator. It exposes only the durable
// active-resolution scan and exact-handle reconcile methods required by the
// coordinator, never Submit, Query, or terminal-proof construction.
type serviceCoordinatorAuthorityAdapter struct {
	authority reconcilecoordinator.Authority
}

// ListActiveResolutionHandles forwards only the coordinator startup scan.
func (a serviceCoordinatorAuthorityAdapter) ListActiveResolutionHandles(ctx context.Context) ([]recoverycustody.SubmissionHandle, error) {
	if err := validateServiceCoordinatorAdapter(ctx, a.authority); err != nil {
		return nil, err
	}
	return a.authority.ListActiveResolutionHandles(ctx)
}

// ReconcileResolution forwards only the exact handle reconciliation operation.
func (a serviceCoordinatorAuthorityAdapter) ReconcileResolution(ctx context.Context, handle recoverycustody.SubmissionHandle) (recoverycustody.Outcome, error) {
	if err := validateServiceCoordinatorAdapter(ctx, a.authority); err != nil {
		return recoverycustody.Outcome{}, err
	}
	return a.authority.ReconcileResolution(ctx, handle)
}

func validateServiceCoordinatorAdapter(ctx context.Context, authority reconcilecoordinator.Authority) error {
	if ctx == nil {
		return recoverycustody.ErrContextRequired
	}
	if dependencyNil(authority) {
		return errServiceCoordinatorCandidate
	}
	return nil
}

var _ reconcilecoordinator.Authority = (*serviceCoordinatorAuthorityAdapter)(nil)

// serviceCoordinatorWiringCandidate only opens an explicitly bootstrapped
// coordinator for an already dependency-gated service. It intentionally has no
// Serve/Run method, so this candidate cannot enable a production runner.
type serviceCoordinatorWiringCandidate struct {
	coordinator *reconcilecoordinator.Coordinator
}

// newServiceCoordinatorWiringCandidate keeps service and coordinator database
// ownership separate while leaving Bootstrap and process lifecycle explicit.
func newServiceCoordinatorWiringCandidate(
	ctx context.Context,
	service *custodyService,
	authorityPath string,
	config reconcilecoordinator.Config,
) (*serviceCoordinatorWiringCandidate, error) {
	if ctx == nil {
		return nil, recoverycustody.ErrContextRequired
	}
	if service == nil || service.authority == nil || service.closed.Load() {
		return nil, errServiceCoordinatorCandidate
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if err := validateServiceCoordinatorPaths(authorityPath, config.Path); err != nil {
		return nil, err
	}
	authority := serviceCoordinatorAuthorityAdapter{authority: service.authority}
	coordinator, err := reconcilecoordinator.Open(ctx, config, authority)
	if err != nil {
		return nil, err
	}
	return &serviceCoordinatorWiringCandidate{coordinator: coordinator}, nil
}

func validateServiceCoordinatorPaths(authorityPath, coordinatorPath string) error {
	if err := validateServiceCoordinatorPath("authority", authorityPath); err != nil {
		return err
	}
	if err := validateServiceCoordinatorPath("coordinator", coordinatorPath); err != nil {
		return err
	}
	if authorityPath == coordinatorPath {
		return errServiceCoordinatorSharedAuthority
	}
	authorityInfo, err := serviceCoordinatorFileInfo(authorityPath)
	if err != nil {
		return err
	}
	coordinatorInfo, err := os.Lstat(coordinatorPath)
	if err == nil && os.SameFile(authorityInfo, coordinatorInfo) {
		return errServiceCoordinatorSharedAuthority
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: coordinator database identity is unavailable", errServiceCoordinatorCandidate)
	}
	return nil
}

func validateServiceCoordinatorPath(name, path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("%w: %s path must be absolute and canonical", errServiceCoordinatorCandidate, name)
	}
	return nil
}

func serviceCoordinatorFileInfo(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: authority database identity is unavailable", errServiceCoordinatorCandidate)
	}
	return info, nil
}

// startupScan exposes only the finite scan step for this test candidate; the
// long-running coordinator runner remains deliberately unrepresented here.
func (c *serviceCoordinatorWiringCandidate) startupScan(ctx context.Context) error {
	if c == nil || c.coordinator == nil {
		return errServiceCoordinatorCandidate
	}
	if ctx == nil {
		return recoverycustody.ErrContextRequired
	}
	return c.coordinator.StartupScan(ctx)
}

func (c *serviceCoordinatorWiringCandidate) close() error {
	if c == nil || c.coordinator == nil {
		return nil
	}
	return c.coordinator.Close()
}

func coordinatorCandidateConfig(t *testing.T) (reconcilecoordinator.Config, string) {
	t.Helper()
	root := t.TempDir()
	parent := filepath.Join(root, "coordinator-state")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatalf("mkdir coordinator state: %v", err)
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		t.Fatalf("chmod coordinator state: %v", err)
	}
	path := filepath.Join(parent, "coordinator.sqlite")
	return reconcilecoordinator.Config{
		Path: path, OwnerID: "custody-service-coordinator", OwnerEpoch: 1,
		OwnerToken: strings.Repeat("a", 64), ClaimTTL: time.Minute,
		RetryBase: time.Second, RetryMax: time.Minute, MaxAttempts: 3,
		RunnerPeriod: time.Second, Now: func() time.Time { return time.Unix(900, 0).UTC() },
	}, parent
}

// TestServiceCoordinatorWiringCandidateUsesExplicitServiceAuthority verifies
// the finite service-owned wiring shape without starting HTTP or a runner.
func TestServiceCoordinatorWiringCandidateUsesExplicitServiceAuthority(t *testing.T) {
	serviceConfig := testConfig(t)
	service, err := newService(t.Context(), serviceConfig, dependencies{Finalizer: testFinalizer{}, Verifier: testVerifier{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.close() })
	coordinatorConfig, _ := coordinatorCandidateConfig(t)
	if err := reconcilecoordinator.Bootstrap(t.Context(), coordinatorConfig); err != nil {
		t.Fatal(err)
	}
	candidate, err := newServiceCoordinatorWiringCandidate(t.Context(), service, serviceConfig.Authority.Path, coordinatorConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = candidate.close() })
	if err := candidate.startupScan(t.Context()); err != nil {
		t.Fatalf("startupScan() error = %v", err)
	}
	jobs, err := candidate.coordinator.ListJobs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatalf("startupScan() jobs = %+v, want no active resolutions", jobs)
	}
	if service.ready.Load() || service.endpointAddress() != "" {
		t.Fatalf("candidate changed service listener state: ready=%v endpoint=%q", service.ready.Load(), service.endpointAddress())
	}
}

// TestServiceCoordinatorWiringCandidateRejectsInvalidInputsBeforeOpen covers
// nil service, invalid config, and missing coordinator state without creating
// a database or starting an HTTP listener.
func TestServiceCoordinatorWiringCandidateRejectsInvalidInputsBeforeOpen(t *testing.T) {
	config, parent := coordinatorCandidateConfig(t)
	before := directoryNames(t, parent)
	if candidate, err := newServiceCoordinatorWiringCandidate(t.Context(), nil, filepath.Join(parent, "authority.sqlite"), config); err == nil || candidate != nil {
		t.Fatalf("nil service result = (%v, %v), want error and nil candidate", candidate, err)
	}
	invalid := config
	invalid.Path = "relative/coordinator.sqlite"
	if candidate, err := newServiceCoordinatorWiringCandidate(t.Context(), nil, filepath.Join(parent, "authority.sqlite"), invalid); err == nil || candidate != nil {
		t.Fatalf("invalid config result = (%v, %v), want error and nil candidate", candidate, err)
	}
	if after := directoryNames(t, parent); !reflect.DeepEqual(after, before) {
		t.Fatalf("candidate rejection changed coordinator state: before=%v after=%v", before, after)
	}
}

// TestServiceCoordinatorWiringCandidateRejectsUnbootstrappedCoordinator keeps
// Open read-only: a missing coordinator database must not be created implicitly.
func TestServiceCoordinatorWiringCandidateRejectsUnbootstrappedCoordinator(t *testing.T) {
	serviceConfig := testConfig(t)
	service, err := newService(t.Context(), serviceConfig, dependencies{Finalizer: testFinalizer{}, Verifier: testVerifier{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.close() })
	config, parent := coordinatorCandidateConfig(t)
	before := directoryNames(t, parent)
	if candidate, err := newServiceCoordinatorWiringCandidate(t.Context(), service, serviceConfig.Authority.Path, config); err == nil || candidate != nil {
		t.Fatalf("missing coordinator result = (%v, %v), want error and nil candidate", candidate, err)
	}
	if after := directoryNames(t, parent); !reflect.DeepEqual(after, before) {
		t.Fatalf("missing coordinator rejection changed state: before=%v after=%v", before, after)
	}
}

// TestServiceCoordinatorWiringCandidateRejectsSharedAuthorityPath prevents a
// test adapter from turning the authority SQLite file into coordinator state.
func TestServiceCoordinatorWiringCandidateRejectsSharedAuthorityPath(t *testing.T) {
	serviceConfig := testConfig(t)
	service, err := newService(t.Context(), serviceConfig, dependencies{Finalizer: testFinalizer{}, Verifier: testVerifier{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.close() })
	config, _ := coordinatorCandidateConfig(t)
	config.Path = serviceConfig.Authority.Path
	if candidate, err := newServiceCoordinatorWiringCandidate(t.Context(), service, serviceConfig.Authority.Path, config); !errors.Is(err, errServiceCoordinatorSharedAuthority) || candidate != nil {
		t.Fatalf("shared authority result = (%v, %v), want stable shared-path error", candidate, err)
	}
}

// TestServiceCoordinatorAuthorityAdapterContractIsNarrow locks the two-method
// contract so future wiring cannot accidentally gain Submit/Query authority.
func TestServiceCoordinatorAuthorityAdapterContractIsNarrow(t *testing.T) {
	typeOfAdapter := reflect.TypeFor[serviceCoordinatorAuthorityAdapter]()
	if typeOfAdapter.NumMethod() != 2 {
		t.Fatalf("adapter method count = %d, want 2", typeOfAdapter.NumMethod())
	}
	for index := 0; index < typeOfAdapter.NumMethod(); index++ {
		method := typeOfAdapter.Method(index).Name
		if method != "ListActiveResolutionHandles" && method != "ReconcileResolution" {
			t.Fatalf("unexpected adapter method %q", method)
		}
	}
}

// TestServiceCoordinatorAuthorityAdapterRejectsNilAuthority checks the
// adapter's fail-fast boundary without constructing any finalizer/verifier.
func TestServiceCoordinatorAuthorityAdapterRejectsNilAuthority(t *testing.T) {
	adapter := serviceCoordinatorAuthorityAdapter{}
	if _, err := adapter.ListActiveResolutionHandles(t.Context()); !errors.Is(err, errServiceCoordinatorCandidate) {
		t.Fatalf("nil authority scan error = %v", err)
	}
	if _, err := adapter.ListActiveResolutionHandles(nil); !errors.Is(err, recoverycustody.ErrContextRequired) {
		t.Fatalf("nil context scan error = %v", err)
	}
}

// TestServiceCoordinatorProductionSurfaceRemainsUnwired ensures this
// test-owned candidate cannot silently become production service composition.
func TestServiceCoordinatorProductionSurfaceRemainsUnwired(t *testing.T) {
	for _, path := range []string{"service.go", "main.go"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read production service file %s: %v", path, err)
		}
		if strings.Contains(string(data), "reconcilecoordinator") || strings.Contains(string(data), "newServiceCoordinatorWiringCandidate") {
			t.Fatalf("production service file %s references test-only coordinator wiring", path)
		}
	}
	mainData, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mainData), "deps, err := processDependencies()") {
		t.Fatal("production run no longer requires processDependencies()")
	}
}
