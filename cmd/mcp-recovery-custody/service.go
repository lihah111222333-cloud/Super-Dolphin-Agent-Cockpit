package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody/authorityhttp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody/rpcwire"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody/sqliteauthority"
)

// dependencies are deliberately injected. The candidate refuses to start when
// an external evidence finalizer or receipt verifier is absent.
type dependencies struct {
	Finalizer sqliteauthority.EvidenceFinalizer
	Verifier  recoverycustody.ReceiptVerifier
}

type custodyService struct {
	authority        *sqliteauthority.Authority
	lease            *sqliteauthority.KernelAuthorityLease
	server           *http.Server
	configuredListen string
	endpoint         atomic.Value
	ready            atomic.Bool
	closed           atomic.Bool
	workers          sync.WaitGroup
	handlers         sync.WaitGroup
	handlerMu        sync.Mutex
	handlersClosed   bool
	closeOnce        sync.Once
	closeErr         error
}

// newService 在内核租约下打开私有 authority 数据库并构造 loopback HTTP 服务；此处不启动监听器或 goroutine。
func newService(ctx context.Context, cfg serviceConfig, deps dependencies) (*custodyService, error) {
	if err := validateServiceConfig(cfg); err != nil {
		return nil, err
	}
	if dependencyNil(deps.Finalizer) || dependencyNil(deps.Verifier) {
		return nil, errors.New("recovery custody external evidence finalizer and receipt verifier are required")
	}
	credential, err := readCredential(cfg.CredentialFile)
	if err != nil {
		return nil, err
	}
	authority, lease, err := openAuthority(ctx, cfg, deps)
	if err != nil {
		return nil, err
	}
	authorityCfg := cfg.Authority.domain()
	identity := rpcwire.AuthorityIdentity{
		InstanceID: cfg.InstanceID, AuthorityID: authorityCfg.AuthorityID,
		AuthorityClusterID: authorityCfg.AuthorityClusterID, AuthorityProfile: authorityCfg.AuthorityProfile,
		AuthorityGeneration: authorityCfg.AuthorityGeneration, AuthorityProfileGeneration: authorityCfg.AuthorityProfileGeneration,
		AuthorityProfileDigest: authorityCfg.AuthorityProfileDigest, SecurityDomainID: authorityCfg.SecurityDomainID,
	}
	verifiedBackend, err := recoverycustody.NewVerifiedClient(authority, deps.Verifier)
	if err != nil {
		_ = authority.Close()
		_ = lease.Close()
		return nil, fmt.Errorf("construct recovery custody verified backend: %w", err)
	}
	handler, err := authorityhttp.NewHandler(authorityhttp.Config{
		Backend: verifiedBackend, Identity: identity, BearerToken: credential,
		MaxBodyBytes: cfg.MaxBodyBytes, MaxConcurrent: cfg.MaxConcurrent,
	})
	if err != nil {
		_ = authority.Close()
		_ = lease.Close()
		return nil, fmt.Errorf("construct recovery custody HTTP handler: %w", err)
	}
	mux := http.NewServeMux()
	mux.Handle("/rpc", handler)
	service := &custodyService{authority: authority, lease: lease, configuredListen: cfg.Listen,
		server: &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}}
	mux.HandleFunc("/healthz", service.health)
	mux.HandleFunc("/health", service.health)
	mux.HandleFunc("/readyz", service.readyHandler)
	mux.HandleFunc("/ready", service.readyHandler)
	service.server.Handler = service.trackHandler(mux)
	return service, nil
}

func openAuthority(ctx context.Context, cfg serviceConfig, deps dependencies) (*sqliteauthority.Authority, *sqliteauthority.KernelAuthorityLease, error) {
	authorityCfg := cfg.Authority.domain()
	lease, err := sqliteauthority.AcquireKernelAuthorityLease(ctx, authorityCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("acquire recovery custody authority lease: %w", err)
	}
	if cfg.Bootstrap {
		if err := sqliteauthority.Bootstrap(ctx, authorityCfg, lease); err != nil {
			_ = lease.Close()
			return nil, nil, fmt.Errorf("bootstrap recovery custody authority: %w", err)
		}
	}
	authority, err := sqliteauthority.OpenAuthority(ctx, authorityCfg, lease, deps.Finalizer, deps.Verifier)
	if err != nil {
		_ = lease.Close()
		return nil, nil, fmt.Errorf("open recovery custody authority: %w", err)
	}
	return authority, lease, nil
}

// serve 绑定配置的 loopback endpoint，并在 context 取消后按顺序排空监听器、数据库和内核租约。
func (s *custodyService) serve(ctx context.Context) error {
	return s.serveInternal(ctx, true)
}

// serveInternal runs the HTTP listener and optionally leaves authority cleanup
// to an owning supervisor.  The latter is required when another runner still
// depends on the authority during coordinated shutdown.
func (s *custodyService) serveInternal(ctx context.Context, closeAfter bool) error {
	return s.serveInternalWithShutdownTimeout(ctx, closeAfter, 10*time.Second)
}

func (s *custodyService) serveInternalWithShutdownTimeout(ctx context.Context, closeAfter bool, shutdownTimeout time.Duration) error {
	if s == nil || s.server == nil || s.lease == nil || s.authority == nil || s.closed.Load() {
		return errors.New("recovery custody service is not initialized")
	}
	if closeAfter {
		defer func() { _ = s.close() }()
	}
	listener, err := net.Listen("tcp4", s.configuredListen)
	if err != nil {
		return fmt.Errorf("listen recovery custody loopback endpoint: %w", err)
	}
	serveErr := make(chan error, 1)
	s.endpoint.Store(listener.Addr().String())
	s.ready.Store(true)
	s.workers.Go(func() { serveErr <- s.server.Serve(listener) })
	defer func() {
		s.ready.Store(false)
		_ = listener.Close()
		s.workers.Wait()
	}()
	select {
	case err := <-serveErr:
		return normalizeServeError(err)
	case <-ctx.Done():
		return s.shutdown(serveErr, shutdownTimeout)
	}
}

func normalizeServeError(err error) error {
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *custodyService) trackHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !s.beginHandler() {
			http.Error(writer, "recovery custody service is shutting down", http.StatusServiceUnavailable)
			return
		}
		defer s.handlers.Done()
		next.ServeHTTP(writer, request)
	})
}

func (s *custodyService) beginHandler() bool {
	s.handlerMu.Lock()
	defer s.handlerMu.Unlock()
	if s.handlersClosed {
		return false
	}
	s.handlers.Add(1)
	return true
}

func (s *custodyService) closeHandlerAdmission() {
	s.handlerMu.Lock()
	s.handlersClosed = true
	s.handlerMu.Unlock()
}

func (s *custodyService) waitHandlers() {
	s.handlers.Wait()
}

func (s *custodyService) shutdown(serveErr <-chan error, timeout time.Duration) error {
	s.closeHandlerAdmission()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	shutdownErr := s.server.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		shutdownErr = errors.Join(shutdownErr, s.server.Close())
	}
	s.waitHandlers()
	return errors.Join(shutdownErr, normalizeServeError(<-serveErr))
}

// close releases authority resources after Serve returns; it is idempotent for
// callers that need to clean up a failed startup path.
func (s *custodyService) close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		s.closeHandlerAdmission()
		serverErr := s.server.Close()
		s.waitHandlers()
		s.closeErr = errors.Join(serverErr, s.authority.Close(), s.lease.Close())
	})
	return s.closeErr
}

func (s *custodyService) health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.closed.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func (s *custodyService) readyHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.closed.Load() || !s.ready.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ready"}`))
}

func (s *custodyService) endpointAddress() string {
	value := s.endpoint.Load()
	if value == nil {
		return ""
	}
	return value.(string)
}

func dependencyNil(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

// processDependencies is intentionally absent from this candidate: no local
// finalizer or verifier may be mistaken for an authoritative external proof.
func processDependencies() (dependencies, error) {
	return dependencies{}, errors.New("recovery custody external dependencies are not configured")
}

var _ recoverycustody.Client = (*sqliteauthority.Authority)(nil)
