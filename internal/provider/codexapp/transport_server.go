package codexapp

import "context"

// transportServer bridges *transport to the SpawnedServer interface the
// pool uses. Fields are intentionally narrow — the pool only cares
// about the WebSocket URL, process liveness, and an orderly shutdown;
// every other *transport method stays private to codexapp.
type transportServer struct {
	t *transport
}

// wrapTransport produces a SpawnedServer view over a running
// *transport. The caller must keep the transport alive until Close
// is invoked; the pool releases entries via this contract when a
// codexHome is evicted.
func wrapTransport(t *transport) SpawnedServer {
	return &transportServer{t: t}
}

// ServerURL reads the currently-bound WebSocket address. The read is
// guarded by the transport's RWMutex because spawn writes the URL
// asynchronously after the child process advertises it on stderr.
func (s *transportServer) ServerURL() string {
	if s == nil || s.t == nil {
		return ""
	}
	s.t.stateMu.RLock()
	defer s.t.stateMu.RUnlock()
	return s.t.serverURL
}

// Close tears the transport down, closing the WebSocket and draining
// the underlying child process. The provided ctx is currently not
// forwarded because shutdownTransport manages its own timeouts; the
// SpawnedServer contract still accepts it so future upgrades can
// honour a caller deadline.
func (s *transportServer) Close(_ context.Context) error {
	if s == nil || s.t == nil {
		return nil
	}
	return s.t.shutdownTransport(true)
}

// Alive reports whether the underlying process is still running. A
// remote transport (local=false, a test-only mode) is always alive
// from the pool's perspective because the pool only owns local
// child processes.
func (s *transportServer) Alive() bool {
	if s == nil || s.t == nil {
		return false
	}
	return s.t.processRunning()
}

// Static compile-time check: transportServer must satisfy the pool's
// SpawnedServer interface. A breakage here surfaces at build time,
// not at the first Acquire.
var _ SpawnedServer = (*transportServer)(nil)
