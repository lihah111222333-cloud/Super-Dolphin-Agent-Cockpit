package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody/sqliteauthority"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
)

type testFinalizer struct{}

func (testFinalizer) Finalize(context.Context, sqliteauthority.FinalizationRequest) (sqliteauthority.FinalizedEvidence, error) {
	return sqliteauthority.FinalizedEvidence{}, sqliteauthority.ErrInvalidEvidence
}
func (testFinalizer) Query(context.Context, sqliteauthority.FinalizationRequest) (sqliteauthority.FinalizationObservation, error) {
	return sqliteauthority.FinalizationObservation{}, sqliteauthority.ErrInvalidEvidence
}

type testVerifier struct{}

func (testVerifier) VerifyReceipt(context.Context, recoverycustody.SubmissionHandle, recoverycustody.ExternalReceipt) error {
	return nil
}

func TestParseArgsRequiresAbsoluteConfig(t *testing.T) {
	if _, err := parseArgs(nil); err == nil {
		t.Fatal("parseArgs accepted missing config")
	}
	if _, err := parseArgs([]string{"--config", "relative.json"}); err == nil {
		t.Fatal("parseArgs accepted relative config")
	}
}

func TestValidateServiceConfigRejectsNonLoopback(t *testing.T) {
	cfg := testConfig(t)
	cfg.Listen = "0.0.0.0:9000"
	if err := validateServiceConfig(cfg); err == nil {
		t.Fatal("validateServiceConfig accepted wildcard listener")
	}
}

func TestValidateServiceConfigRequiresExternalServiceMode(t *testing.T) {
	cfg := testConfig(t)
	cfg.Authority.ExternalService = false
	if err := validateServiceConfig(cfg); err == nil {
		t.Fatal("validateServiceConfig accepted generic authority path mode")
	}
	cfg = testConfig(t)
	cfg.Authority.ProductSQLitePath = "/tmp/product.db"
	if err := validateServiceConfig(cfg); err == nil {
		t.Fatal("validateServiceConfig accepted product path in external service mode")
	}
}

func TestServiceConfigOmitsGenericDatabasePaths(t *testing.T) {
	raw, err := json.Marshal(testConfig(t).Authority)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"product_sqlite_path", "workspace_root"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("service authority JSON contains generic path field %q: %s", forbidden, raw)
		}
	}
}

func TestReadCredentialRejectsWrongModeAndRedacts(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "credential")
	if err := os.WriteFile(path, []byte("secret-value"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readCredential(path); err == nil {
		t.Fatal("readCredential accepted group-readable credential")
	} else if strings.Contains(err.Error(), "secret-value") {
		t.Fatal("credential error leaked secret")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	credential, err := readCredential(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := credential.String(); got != "[REDACTED]" {
		t.Fatalf("credential.String() = %q", got)
	}
}

func TestProcessDependenciesFailsClosed(t *testing.T) {
	if _, err := processDependencies(); err == nil {
		t.Fatal("processDependencies unexpectedly provided local authority dependencies")
	}
}

// TestRunMissingExternalDependenciesHasNoStartupSideEffects locks the
// fail-closed ordering of the real CLI entrypoint. A complete private config
// and credential must still stop at the missing external evidence dependency,
// before Bootstrap/OpenAuthority, listener binding, lock acquisition, or any
// ready artifact can mutate the configured state directory.
func TestRunMissingExternalDependenciesHasNoStartupSideEffects(t *testing.T) {
	cfg := testConfig(t)
	configPath := filepath.Join(filepath.Dir(cfg.CredentialFile), "config.json")
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal valid service config: %v", err)
	}
	if err := os.WriteFile(configPath, raw, 0o600); err != nil {
		t.Fatalf("write valid service config: %v", err)
	}
	rootPath := filepath.Dir(cfg.CredentialFile)
	authorityDir := filepath.Dir(cfg.Authority.Path)
	rootBefore := directoryNames(t, rootPath)
	authorityBefore := directoryNames(t, authorityDir)

	err = run([]string{"--config", configPath})
	if err == nil {
		t.Fatal("run() unexpectedly started without external evidence dependencies")
	}
	if got := err.Error(); got != "recovery custody external dependencies are not configured" {
		t.Fatalf("run() error = %q, want stable missing-dependency failure", got)
	}
	if got := directoryNames(t, rootPath); !reflect.DeepEqual(got, rootBefore) {
		t.Fatalf("state root changed before dependency failure: before=%v after=%v", rootBefore, got)
	}
	if got := directoryNames(t, authorityDir); !reflect.DeepEqual(got, authorityBefore) {
		t.Fatalf("authority directory changed before dependency failure: before=%v after=%v", authorityBefore, got)
	}
}

func directoryNames(t *testing.T, path string) []string {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatalf("read startup state directory %s: %v", path, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func TestDependencyNilRejectsTypedNil(t *testing.T) {
	var finalizer *testFinalizer
	if !dependencyNil(finalizer) {
		t.Fatal("dependencyNil accepted typed nil finalizer")
	}
}

// TestAuthorityConfigWriterFenceRoundTrip 防止服务 JSON mapper 丢失 writer token。
func TestAuthorityConfigWriterFenceRoundTrip(t *testing.T) {
	cfg := testConfig(t)
	domain := cfg.Authority.domain()
	if got := authorityJSONFromDomain(domain).domain(); !reflect.DeepEqual(got, domain) {
		t.Fatalf("authority config round-trip lost fields: got %+v, want %+v", got, domain)
	}
}

// TestAuthorityConfigFieldSetMatchesJSONMapper 动态检查 authority Config 与
// 服务 JSON carrier 的字段集合，防止新增 fence 字段静默丢失。
func TestAuthorityConfigFieldSetMatchesJSONMapper(t *testing.T) {
	domainType := reflect.TypeFor[sqliteauthority.Config]()
	jsonType := reflect.TypeFor[authorityConfigJSON]()
	for field := range domainType.Fields() {
		if _, ok := jsonType.FieldByName(field.Name); !ok {
			t.Errorf("authority JSON mapper missing domain field %s", field.Name)
		}
	}
	for field := range jsonType.Fields() {
		if _, ok := domainType.FieldByName(field.Name); !ok {
			t.Errorf("authority domain config missing JSON field %s", field.Name)
		}
	}
}

func TestServiceLifecycleAndHealth(t *testing.T) {
	cfg := testConfig(t)
	service, err := newService(t.Context(), cfg, dependencies{Finalizer: testFinalizer{}, Verifier: testVerifier{}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = service.close() }()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Go(func() { done <- service.serve(ctx) })
	deadline := time.Now().Add(3 * time.Second)
	for !service.ready.Load() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !service.ready.Load() {
		t.Fatal("service did not become ready")
	}
	response, err := http.Get("http://" + service.endpointAddress() + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", response.StatusCode)
	}
	_ = response.Body.Close()
	cancel()
	wg.Wait()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("serve() = %v", err)
	}
	recorder := httptest.NewRecorder()
	service.health(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("closed health status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
}

// TestServiceShutdownWaitsForActiveHandlers prevents the service from closing
// its authority after the graceful HTTP deadline while a request is still using
// it. The handler remains blocked after request cancellation so the test can
// prove that service return and resource release both wait for handler exit.
func TestServiceShutdownWaitsForActiveHandlers(t *testing.T) {
	service, cfg, barrier, cancelRun, done, requestDone := startBarrierService(t, 25*time.Millisecond)
	cancelRun()
	assertHandlerCancellationBeforeServiceExit(t, service, cfg, barrier, done)
	barrier.release()
	if err := <-done; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error = %v, want context deadline exceeded", err)
	}
	<-requestDone
}

// TestServiceCloseWaitsForActiveHandlers covers the direct close path, which
// must not release the authority lease while an admitted handler is still
// blocked after request cancellation.
func TestServiceCloseWaitsForActiveHandlers(t *testing.T) {
	service, cfg, barrier, _, serveDone, requestDone := startBarrierService(t, 10*time.Second)
	closeDone := make(chan error, 1)
	runtimesafe.SafeGo(t.Context(), nil, "recovery-custody.test.handler-close", func(context.Context) {
		closeDone <- service.close()
	})
	assertHandlerCancellationBeforeServiceExit(t, service, cfg, barrier, closeDone)
	barrier.release()
	if err := <-closeDone; err != nil {
		t.Fatalf("close() error = %v", err)
	}
	if err := <-serveDone; err != nil {
		t.Fatalf("serveInternalWithShutdownTimeout() error = %v", err)
	}
	<-requestDone
}

type activeHandlerBarrier struct {
	entered  chan struct{}
	canceled chan struct{}
	releaseC chan struct{}
	finished chan struct{}
	once     sync.Once
}

func newActiveHandlerBarrier() *activeHandlerBarrier {
	return &activeHandlerBarrier{entered: make(chan struct{}), canceled: make(chan struct{}), releaseC: make(chan struct{}), finished: make(chan struct{})}
}

func (b *activeHandlerBarrier) ServeHTTP(_ http.ResponseWriter, request *http.Request) {
	close(b.entered)
	<-request.Context().Done()
	close(b.canceled)
	<-b.releaseC
	close(b.finished)
}

func (b *activeHandlerBarrier) release() {
	b.once.Do(func() { close(b.releaseC) })
}

func startBarrierService(t *testing.T, shutdownTimeout time.Duration) (*custodyService, serviceConfig, *activeHandlerBarrier, context.CancelFunc, <-chan error, <-chan struct{}) {
	t.Helper()
	requireLoopback(t)
	cfg := testConfig(t)
	service, err := newService(t.Context(), cfg, dependencies{Finalizer: testFinalizer{}, Verifier: testVerifier{}})
	if err != nil {
		t.Fatal(err)
	}
	barrier := newActiveHandlerBarrier()
	service.server.Handler = service.trackHandler(barrier)
	runCtx, cancelRun := context.WithCancel(t.Context())
	t.Cleanup(cancelRun)
	done := make(chan error, 1)
	runtimesafe.SafeGo(t.Context(), nil, "recovery-custody.test.handler-lifecycle", func(context.Context) {
		done <- service.serveInternalWithShutdownTimeout(runCtx, true, shutdownTimeout)
	})
	waitServiceReady(t, service)
	requestCtx, cancelRequest := context.WithCancel(t.Context())
	t.Cleanup(cancelRequest)
	requestDone := make(chan struct{})
	runtimesafe.SafeGo(t.Context(), nil, "recovery-custody.test.barrier-request", func(context.Context) {
		request, requestErr := http.NewRequestWithContext(requestCtx, http.MethodGet, "http://"+service.endpointAddress()+"/healthz", nil)
		if requestErr == nil {
			response, responseErr := http.DefaultClient.Do(request)
			if responseErr == nil {
				_ = response.Body.Close()
			}
		}
		close(requestDone)
	})
	select {
	case <-barrier.entered:
	case <-time.After(3 * time.Second):
		barrier.release()
		t.Fatal("barrier handler did not start")
	}
	t.Cleanup(barrier.release)
	return service, cfg, barrier, cancelRun, done, requestDone
}

func assertHandlerCancellationBeforeServiceExit(t *testing.T, service *custodyService, cfg serviceConfig, barrier *activeHandlerBarrier, done <-chan error) {
	t.Helper()
	select {
	case <-barrier.canceled:
	case err := <-done:
		t.Fatalf("service returned before active handler cancellation: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("active handler request was not canceled before service exit observation")
	}
	// The timer is only a bounded negative-observation window. Handler state is
	// established by its cancellation/release channels and the kernel lease check
	// below, rather than by elapsed time.
	observationTimer := time.NewTimer(250 * time.Millisecond)
	defer observationTimer.Stop()
	select {
	case err := <-done:
		t.Fatalf("service returned before active handler release: %v", err)
	case <-observationTimer.C:
	}
	select {
	case <-barrier.finished:
		t.Fatal("active handler finished while its release barrier remained closed")
	default:
	}
	assertAuthorityLeaseStillHeld(t, cfg)
}

func assertAuthorityLeaseStillHeld(t *testing.T, cfg serviceConfig) {
	t.Helper()
	lease, err := sqliteauthority.AcquireKernelAuthorityLease(t.Context(), cfg.Authority.domain())
	if err == nil {
		_ = lease.Close()
		t.Fatal("authority lease was released while an active handler was blocked")
	}
	if !errors.Is(err, sqliteauthority.ErrLeaseRejected) {
		t.Fatalf("second authority lease error = %v, want ErrLeaseRejected from held kernel lock", err)
	}
}

func waitServiceReady(t *testing.T, service *custodyService) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !service.ready.Load() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !service.ready.Load() {
		t.Fatal("service did not become ready")
	}
}

func testConfig(t *testing.T) serviceConfig {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	authorityDir := filepath.Join(root, "authority")
	if err := os.Mkdir(authorityDir, 0o700); err != nil {
		t.Fatal(err)
	}
	credentialPath := filepath.Join(root, "credential")
	if err := os.WriteFile(credentialPath, []byte("test-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	digest := func(seed string) string { sum := sha256.Sum256([]byte(seed)); return hex.EncodeToString(sum[:]) }
	domain := sqliteauthority.Config{SchemaVersion: 1, Path: filepath.Join(authorityDir, "authority.db"), ExternalService: true,
		StoreID: "store-1", AuthorityID: "authority-1", AuthorityClusterID: "cluster-1", AuthorityProfile: "profile-1", AuthorityGeneration: 1,
		OwnerID: "owner-1", OwnerGeneration: 1, OwnerFenceDigest: digest("owner"), AuthorityProfileGeneration: 1, AuthorityProfileDigest: digest("profile"),
		SecurityDomainID: "security-1", SecurityTimeLineageDigest: digest("time"), SecurityTimeGeneration: 1, SecurityTimeRevision: 1,
		RetentionPolicyDigest: digest("retention"), HeadID: "head-1", InitialHeadRootDigest: digest("head"), AntiRollbackGeneration: 1,
		AntiRollbackAnchorDigest: digest("anchor"), AntiRollbackAttestationRef: "anchor://initial", WriterEpoch: 1,
		DeploymentFence: "deployment-1"}
	return serviceConfig{SchemaVersion: 1, InstanceID: "instance-1", Listen: "127.0.0.1:0", CredentialFile: credentialPath,
		Bootstrap: true, MaxBodyBytes: 1 << 20, MaxConcurrent: 4,
		Authority: authorityJSONFromDomain(domain)}
}
