package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

type failingRoundTripper struct{}

func (f failingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("dial failed")
}

func startFlusherForTest(t *testing.T, run func(context.Context) error) (context.Context, context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	finished := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		defer close(finished)
		done <- run(ctx)
	})
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
			wg.Wait()
		case <-time.After(time.Second):
			t.Fatal("flusher goroutine did not stop")
		}
	})
	return ctx, cancel, done
}

func flusherPostFailureLog(t *testing.T, cfg ChannelConfig) string {
	t.Helper()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	resolver := &testResolver{known: map[string]ChannelConfig{"signed": cfg}}
	n := NewNotifier(logger, resolver, 1)
	client := NewWebhookClient(WebhookClientConfig{})
	client.HTTPClient().Transport = failingRoundTripper{}
	f := NewFlusher(logger, n, client, NewRenderer(), 100*time.Millisecond)
	f.now = func() time.Time { return time.UnixMilli(1_700_000_000_000) }
	f.handle(context.Background(), contract.NotifyRequest{
		ChannelAlias: "signed",
		Message: contract.NotifyMessage{
			Title: "redact",
			Body:  "body",
			Level: contract.NotifyLevelWarn,
		},
	})
	return buf.String()
}

func TestFlusherLogsRedactSignedDingtalkURL(t *testing.T) {
	t.Parallel()
	logLine := flusherPostFailureLog(t, ChannelConfig{
		Platform: PlatformDingtalk,
		URL:      "https://oapi.dingtalk.com/robot/send?access_token=token-secret",
		Secret:   "dingtalk-secret",
	})
	for _, leaked := range []string{"timestamp=", "sign=", "token-secret", "access_token="} {
		if strings.Contains(logLine, leaked) {
			t.Fatalf("log leaked %q: %s", leaked, logLine)
		}
	}
	if !strings.Contains(logLine, "notify: post failed") {
		t.Fatalf("log missing post failure / redacted marker: %s", logLine)
	}
}

func TestFlusherLogsRedactSlackBearerURL(t *testing.T) {
	t.Parallel()
	logLine := flusherPostFailureLog(t, ChannelConfig{
		Platform: PlatformSlack,
		URL:      "https://hooks.slack.com/services/T000/B000/SECRETXYZ",
	})
	for _, leaked := range []string{"/services/T000/B000/SECRETXYZ", "T000", "B000", "SECRETXYZ"} {
		if strings.Contains(logLine, leaked) {
			t.Fatalf("log leaked Slack bearer path %q: %s", leaked, logLine)
		}
	}
	if !strings.Contains(logLine, "/services/redacted") {
		t.Fatalf("log missing Slack redaction markers: %s", logLine)
	}
}

// startSlackTLSServer returns a TLS httptest server that accepts POSTs
// and records the bodies it receives. We use the AllowPrivateCIDR opt-
// in because httptest binds 127.0.0.1.
func startSlackTLSServer(t *testing.T) (*httptest.Server, *[]string, *sync.Mutex) {
	t.Helper()
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	return srv, &bodies, &mu
}

func newTLSClient(srv *httptest.Server) *WebhookClient {
	c := NewWebhookClient(WebhookClientConfig{
		Timeout:          2 * time.Second,
		AllowPrivateCIDR: true,
	})
	c.HTTPClient().Transport.(*http.Transport).TLSClientConfig =
		srv.Client().Transport.(*http.Transport).TLSClientConfig
	return c
}

// TestFlusherSendsQueuedRequest verifies the full path: enqueue a
// Slack request, run the flusher briefly, observe the server body
// carries the rendered block kit payload.
func TestFlusherSendsQueuedRequest(t *testing.T) {
	t.Parallel()
	srv, bodies, mu := startSlackTLSServer(t)
	defer srv.Close()
	resolver := &testResolver{known: map[string]ChannelConfig{
		"s": {Platform: PlatformSlack, URL: srv.URL},
	}}
	n := NewNotifier(slog.Default(), resolver, 4)
	f := NewFlusher(slog.Default(), n, newTLSClient(srv), NewRenderer(), 200*time.Millisecond)

	if err := n.TryEnqueue(context.Background(), contract.NotifyRequest{
		ChannelAlias: "s",
		Message:      contract.NotifyMessage{Title: "hello", Body: "world", Level: contract.NotifyLevelInfo},
	}); err != nil {
		t.Fatalf("TryEnqueue: %v", err)
	}

	_, cancel, done := startFlusherForTest(t, f.Run)

	waitForFlusherBodies(bodies, mu, 1, 2*time.Second)
	waitForFlusherDelivered(t, f, 1, 2*time.Second)
	cancel()
	waitForFlusherDone(t, done, time.Second)

	mu.Lock()
	defer mu.Unlock()
	if len(*bodies) != 1 {
		t.Fatalf("server got %d bodies, want 1", len(*bodies))
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte((*bodies)[0]), &payload); err != nil {
		t.Fatalf("body not json: %v", err)
	}
	if _, ok := payload["blocks"].([]any); !ok {
		t.Fatalf("body missing blocks: %v", payload)
	}
	m := f.Metrics()
	if m.Sent != 1 || m.Delivered != 1 {
		t.Fatalf("metrics = %+v, want Sent=1 Delivered=1", m)
	}
}

func waitForFlusherBodies(bodies *[]string, mu *sync.Mutex, want int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(*bodies)
		mu.Unlock()
		if n >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForFlusherDone(t *testing.T, done <-chan error, timeout time.Duration) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatal("flusher did not exit on cancel")
	}
}

// TestFlusherDeliversBulkQueue verifies the main loop processes a
// full queue of requests and each body reaches the server. Shutdown
// is via ctx cancel after the server and flusher metrics have recorded
// everything; drain semantics (bounded ctx for in-flight POSTs) are exercised by
// TestFlusherDrainBoundedContext below.
func TestFlusherDeliversBulkQueue(t *testing.T) {
	t.Parallel()
	srv, bodies, mu := startSlackTLSServer(t)
	defer srv.Close()
	resolver := &testResolver{known: map[string]ChannelConfig{
		"s": {Platform: PlatformSlack, URL: srv.URL},
	}}
	n := NewNotifier(slog.Default(), resolver, 8)
	f := NewFlusher(slog.Default(), n, newTLSClient(srv), NewRenderer(), 5*time.Second)

	ctx, cancel, done := startFlusherForTest(t, f.Run)

	for range 3 {
		if err := n.TryEnqueue(ctx, contract.NotifyRequest{
			ChannelAlias: "s",
			Message:      contract.NotifyMessage{Title: "t", Body: "b"},
		}); err != nil {
			t.Fatalf("TryEnqueue: %v", err)
		}
	}

	waitForFlusherBodies(bodies, mu, 3, 5*time.Second)
	waitForFlusherDelivered(t, f, 3, 5*time.Second)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("flusher did not exit after cancel")
	}

	mu.Lock()
	got := len(*bodies)
	mu.Unlock()
	if got != 3 {
		t.Fatalf("expected 3 deliveries, got %d", got)
	}
	if m := f.Metrics(); m.Delivered != 3 {
		t.Fatalf("metrics Delivered = %d, want 3 (%+v)", m.Delivered, m)
	}
}

func waitForFlusherDelivered(t *testing.T, f *Flusher, want int64, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if f.Metrics().Delivered >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("flusher delivered %d, want %d", f.Metrics().Delivered, want)
}

// TestFlusherDrainBoundedContext exercises the shutdown drain path
// specifically — we pre-fill the queue, cancel before Run starts so
// the very first select picks ctx.Done, and let drain() deliver what
// it can within its timeout. The assertion is pessimistic: drain is
// allowed to finish early if the OS serializes quickly, but at least
// one request must land so we know the drain path is live.
func TestFlusherDrainBoundedContext(t *testing.T) {
	t.Parallel()
	srv, bodies, mu := startSlackTLSServer(t)
	defer srv.Close()
	resolver := &testResolver{known: map[string]ChannelConfig{
		"s": {Platform: PlatformSlack, URL: srv.URL},
	}}
	n := NewNotifier(slog.Default(), resolver, 4)
	f := NewFlusher(slog.Default(), n, newTLSClient(srv), NewRenderer(), 5*time.Second)

	for range 2 {
		if err := n.TryEnqueue(context.Background(), contract.NotifyRequest{
			ChannelAlias: "s",
			Message:      contract.NotifyMessage{Title: "drain", Body: "b"},
		}); err != nil {
			t.Fatalf("TryEnqueue: %v", err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = f.Run(ctx)

	mu.Lock()
	got := len(*bodies)
	mu.Unlock()
	if got < 1 {
		t.Fatalf("drain path must deliver at least 1 request, got %d", got)
	}
}

// TestFlusherResolveErrorIsLoggedNotFatal verifies a bad alias in the
// queue (should never happen since TryEnqueue pre-resolves, but
// defensive) merely bumps the resolveErrs counter.
func TestFlusherResolveErrorIsLoggedNotFatal(t *testing.T) {
	t.Parallel()
	n := NewNotifier(slog.Default(), &testResolver{known: map[string]ChannelConfig{}}, 4)
	// Inject a request directly past the TryEnqueue pre-resolution.
	n.queue <- contract.NotifyRequest{ChannelAlias: "ghost", Message: contract.NotifyMessage{Title: "x"}}
	f := NewFlusher(slog.Default(), n, NewWebhookClient(WebhookClientConfig{}), NewRenderer(), 200*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	timer := time.AfterFunc(50*time.Millisecond, cancel)
	t.Cleanup(func() {
		timer.Stop()
		cancel()
	})
	_ = f.Run(ctx)

	m := f.Metrics()
	if m.ResolveErrs != 1 || m.Delivered != 0 {
		t.Fatalf("metrics = %+v, want ResolveErrs=1 Delivered=0", m)
	}
}

// TestFlusherNilQueueSitsOnCtx covers the defensive nil-queue branch
// (e.g. misconfigured Fx wiring) — the Runner must still obey ctx so
// the RunGroup's shutdown contract is intact.
func TestFlusherNilQueueSitsOnCtx(t *testing.T) {
	t.Parallel()
	f := &Flusher{} // zero-value, no queue
	var done atomic.Bool
	_, cancel, doneCh := startFlusherForTest(t, func(ctx context.Context) error {
		_ = f.Run(ctx)
		done.Store(true)
		return nil
	})
	// Give the goroutine a moment; it must NOT finish before cancel.
	time.Sleep(20 * time.Millisecond)
	if done.Load() {
		t.Fatal("flusher with nil queue exited before ctx cancel")
	}
	cancel()
	select {
	case <-doneCh:
	case <-time.After(time.Second):
		t.Fatal("flusher did not exit after cancel")
	}
}

// sanity: render dispatcher reaches all three platforms.
func TestFlusherRenderDispatch(t *testing.T) {
	t.Parallel()
	f := &Flusher{renderer: NewRenderer(), now: func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }}
	for _, plat := range []Platform{PlatformDingtalk, PlatformFeishu, PlatformSlack} {
		cfg := ChannelConfig{Platform: plat, URL: "https://example.com/hook", Secret: "sec"}
		if plat == PlatformSlack {
			cfg.Secret = ""
		}
		_, body, _, err := f.render(cfg, contract.NotifyMessage{Title: "t", Body: "b"})
		if err != nil {
			t.Fatalf("render %s: %v", plat, err)
		}
		if !strings.Contains(string(body), "\"") {
			t.Fatalf("render %s produced no JSON body", plat)
		}
	}
	// Unsupported platform sentinel.
	if _, _, _, err := f.render(ChannelConfig{Platform: "pigeon"}, contract.NotifyMessage{}); err == nil {
		t.Fatal("render should reject unsupported platform")
	}
}
