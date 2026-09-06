package taskruntime

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone/taskhttp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

type identityReaderStub struct{}

func (identityReaderStub) Get(context.Context, string) (identity.Reservation, error) {
	return identity.Reservation{}, errors.New("not configured")
}
func (identityReaderStub) GetIncarnation(context.Context, string) (identity.Incarnation, error) {
	return identity.Incarnation{}, errors.New("not configured")
}
func (identityReaderStub) GetCurrent(context.Context, string) (identity.CurrentBinding, error) {
	return identity.CurrentBinding{}, errors.New("not configured")
}

type toolProviderStub struct{}

func (toolProviderStub) ListTools(context.Context) ([]common.MCPTool, error) { return nil, nil }
func (toolProviderStub) CallTool(context.Context, string, json.RawMessage) (any, error) {
	return nil, nil
}

func newTestOwner(t *testing.T) *Owner {
	t.Helper()
	owner, err := New(Config{
		WorkspaceRoot:    t.TempDir(),
		BaseToolProvider: toolProviderStub{},
		AllowedTools:     []string{},
		Logger:           pkglogger.NewRuntime(pkglogger.RuntimeConfig{ServiceName: "taskruntime-test"}),
		IdentityResolver: identityReaderStub{},
		Name:             "orch",
		Version:          "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	return owner
}

func assertLoopbackEndpoint(t *testing.T, endpoint Endpoint) {
	t.Helper()
	if !strings.HasPrefix(endpoint.URL, "http://127.0.0.1:") || !strings.HasSuffix(endpoint.URL, "/mcp") {
		t.Fatalf("endpoint URL = %q", endpoint.URL)
	}
	if endpoint.InstanceID == "" || strings.Contains(endpoint.String(), "bearer") {
		t.Fatalf("invalid endpoint identity/string: %#v %q", endpoint, endpoint.String())
	}
	raw, err := json.Marshal(endpoint)
	if err != nil || strings.Contains(strings.ToLower(string(raw)), "token") || strings.Contains(strings.ToLower(string(raw)), "bearer") {
		t.Fatalf("endpoint JSON leaks credential fields: %s", raw)
	}
}

func TestStartLoopbackEndpointAndClose(t *testing.T) {
	owner := newTestOwner(t)
	endpoint, err := owner.Start(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	assertLoopbackEndpoint(t, endpoint)
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestStartRejectsNonLoopbackListenerAndTypedNil(t *testing.T) {
	owner := newTestOwner(t)
	listener, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Start(context.Background(), listener); err == nil {
		t.Fatal("Start accepted non-loopback listener")
	}
	// A rejected listener must not poison the owner state: a later valid start
	// is still allowed, while the failed listener has already been closed.
	if _, err := owner.Start(context.Background(), 0); err != nil {
		t.Fatalf("Start after rejected listener = %v", err)
	}
	var nilListener *net.TCPListener
	if _, err := owner.Start(context.Background(), net.Listener(nilListener)); err == nil {
		t.Fatal("Start accepted typed-nil listener")
	}
	_ = owner.Close()
}

func TestDuplicateStartDoesNotCloseOwnedListenerButClosesRejectedFreshListener(t *testing.T) {
	owner := newTestOwner(t)
	owned, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := owner.Start(context.Background(), owned)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()

	if _, err := owner.Start(context.Background(), owned); !errors.Is(err, ErrAlreadyStarted) {
		t.Fatalf("duplicate Start(same listener) = %v, want ErrAlreadyStarted", err)
	}
	// The first listener remains usable after the duplicate call. A successful
	// TCP handshake proves the duplicate path did not close the owner listener.
	conn, err := net.DialTimeout("tcp4", strings.TrimPrefix(strings.TrimSuffix(endpoint.URL, "/mcp"), "http://"), time.Second)
	if err != nil {
		t.Fatalf("owned listener was closed by duplicate Start: %v", err)
	}
	_ = conn.Close()

	fresh, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Start(context.Background(), fresh); !errors.Is(err, ErrAlreadyStarted) {
		t.Fatalf("duplicate Start(fresh listener) = %v, want ErrAlreadyStarted", err)
	}
	if _, err := fresh.Accept(); err == nil {
		t.Fatal("rejected fresh listener remained open after duplicate Start")
	}
}

func TestConcurrentStartHasSingleOwnerAndPreservesListener(t *testing.T) {
	owner := newTestOwner(t)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			<-start
			_, err := owner.Start(context.Background(), listener)
			results <- err
		})
	}
	close(start)
	workers.Wait()
	var successes, duplicates int
	for range 2 {
		switch err := <-results; {
		case err == nil:
			successes++
		case errors.Is(err, ErrAlreadyStarted):
			duplicates++
		default:
			t.Fatalf("concurrent Start() error = %v", err)
		}
	}
	if successes != 1 || duplicates != 1 {
		t.Fatalf("concurrent Start outcomes successes=%d duplicates=%d", successes, duplicates)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCloseClosesOwnedListener(t *testing.T) {
	owner := newTestOwner(t)
	base, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener := &closeTrackingListener{Listener: base}
	if _, err := owner.Start(context.Background(), listener); err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if listener.closeCount() == 0 {
		t.Fatal("Close() did not close the task-owned listener")
	}
}

type closeTrackingListener struct {
	net.Listener
	mu     sync.Mutex
	closes int
}

func (l *closeTrackingListener) Close() error {
	l.mu.Lock()
	l.closes++
	l.mu.Unlock()
	return l.Listener.Close()
}

func (l *closeTrackingListener) closeCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.closes
}

func TestTaskCredentialLifecycleRequiresStartedOwnerAndRejectsAfterClose(t *testing.T) {
	owner := newTestOwner(t)
	identity := common.TaskSessionIdentity{}
	if err := owner.Activate(context.Background(), "lease", identity); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("Activate before Start = %v, want ErrNotStarted", err)
	}
	if err := owner.Revoke(context.Background(), "lease"); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("Revoke before Start = %v, want ErrNotStarted", err)
	}
	if _, err := owner.Start(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if err := owner.Activate(context.Background(), "lease", identity); !errors.Is(err, ErrClosed) {
		t.Fatalf("Activate after Close = %v, want ErrClosed", err)
	}
	if err := owner.Revoke(context.Background(), "lease"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Revoke after Close = %v, want ErrClosed", err)
	}
}

func TestPendingCredentialRequiresStartAndIsOneShot(t *testing.T) {
	owner := newTestOwner(t)
	if _, _, err := owner.IssuePending(context.Background(), "lease", time.Now().UTC().Add(time.Minute)); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("IssuePending before Start = %v, want ErrNotStarted", err)
	}
	if _, err := owner.Start(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	issued, endpoint, err := owner.IssuePending(context.Background(), "lease", time.Now().UTC().Add(time.Minute))
	if err != nil || issued == nil || endpoint == "" {
		t.Fatalf("IssuePending() = (%v,%q,%v)", issued, endpoint, err)
	}
	if _, err := issued.TakeBearer(); err != nil {
		t.Fatal(err)
	}
	if _, err := issued.TakeBearer(); err == nil {
		t.Fatal("credential holder returned bearer twice")
	}
	_ = owner.Close()
}

func TestNewRejectsUnboundTaskCapabilityAllowlist(t *testing.T) {
	for _, name := range []string{"launch_agent", "manage_direct_peer", "list_agents", "get_agent_report", "stop_agent", "wait_agents", "send_message"} {
		t.Run(name, func(t *testing.T) {
			config := Config{
				WorkspaceRoot:    t.TempDir(),
				BaseToolProvider: toolProviderStub{},
				AllowedTools:     []string{name},
				Logger:           pkglogger.NewRuntime(pkglogger.RuntimeConfig{ServiceName: "taskruntime-test"}),
				IdentityResolver: identityReaderStub{},
				Name:             "orch",
				Version:          "test",
			}
			_, err := New(config)
			if !errors.Is(err, taskhttp.ErrTaskCapabilityManifestRequired) {
				t.Fatalf("New(%q) error = %v, want ErrTaskCapabilityManifestRequired", name, err)
			}
		})
	}
}
