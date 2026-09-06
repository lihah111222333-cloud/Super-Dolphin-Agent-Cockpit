package taskhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

const (
	taskBearer     = "task-http-secret.NeverLeak_1234567890_ABC"
	operatorBearer = "operator-http-secret.NeverLeak_1234567890"
	protocol       = "2025-11-25"
)

type authenticatorFixture struct {
	mu       sync.Mutex
	identity map[common.SessionCredentialDigest]common.TaskSessionIdentity
}

func (f *authenticatorFixture) Validate() error {
	if f == nil || f.identity == nil {
		return errors.New("authenticator fixture is invalid")
	}
	return nil
}

func (f *authenticatorFixture) Authenticate(
	ctx context.Context,
	digest common.SessionCredentialDigest,
) (common.TaskSessionIdentity, error) {
	if err := ctx.Err(); err != nil {
		return common.TaskSessionIdentity{}, err
	}
	f.mu.Lock()
	identity, exists := f.identity[digest]
	f.mu.Unlock()
	if !exists {
		return common.TaskSessionIdentity{}, errors.New("credential unavailable")
	}
	return identity, nil
}

func TestServerConfigFieldGuard(t *testing.T) {
	typeOfConfig := reflect.TypeFor[ServerConfig]()
	fields := make([]string, 0, typeOfConfig.NumField())
	for index := range typeOfConfig.NumField() {
		fields = append(fields, typeOfConfig.Field(index).Name)
	}
	want := []string{"WorkspaceRoot", "Authenticator", "Name", "Version", "AllowedTools", "Logger"}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("ServerConfig fields = %v, want %v", fields, want)
	}
}

func TestNewServerRequiresEveryExplicitConfigField(t *testing.T) {
	authenticator := newAuthenticatorFixture(t, taskBearer, validTaskIdentity(t, "task/config"))
	var typedNilAuthenticator *authenticatorFixture
	valid := ServerConfig{
		WorkspaceRoot: t.TempDir(), Authenticator: authenticator,
		Name: "mcp-orch-task", Version: "test", AllowedTools: []string{}, Logger: testLogger(),
	}
	tests := []struct {
		name   string
		mutate func(*ServerConfig)
	}{
		{name: "workspace", mutate: func(config *ServerConfig) { config.WorkspaceRoot = "" }},
		{name: "relative workspace", mutate: func(config *ServerConfig) { config.WorkspaceRoot = "relative" }},
		{name: "authenticator", mutate: func(config *ServerConfig) { config.Authenticator = nil }},
		{name: "typed nil authenticator", mutate: func(config *ServerConfig) { config.Authenticator = typedNilAuthenticator }},
		{name: "name", mutate: func(config *ServerConfig) { config.Name = "" }},
		{name: "version", mutate: func(config *ServerConfig) { config.Version = "" }},
		{name: "allowed tools", mutate: func(config *ServerConfig) { config.AllowedTools = nil }},
		{name: "logger", mutate: func(config *ServerConfig) { config.Logger = nil }},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			config := valid
			testCase.mutate(&config)
			_, err := NewServer(t.Context(), &providerFixture{}, config)
			if !errors.Is(err, ErrServerConfigInvalid) {
				t.Fatalf("NewServer() error = %v, want ErrServerConfigInvalid", err)
			}
		})
	}
}

func TestServeLocalRejectsNonLoopbackOrNonTCPListenerBeforeServe(t *testing.T) {
	server, err := NewServer(t.Context(), &providerFixture{}, ServerConfig{
		WorkspaceRoot: t.TempDir(),
		Authenticator: newAuthenticatorFixture(t, taskBearer, validTaskIdentity(t, "task/listener")),
		Name:          "task-listener",
		Version:       "test",
		AllowedTools:  []string{},
		Logger:        testLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	var typedNilListener *rejectingListener
	if err := server.ServeLocal(t.Context(), typedNilListener); err == nil {
		t.Fatal("ServeLocal() accepted a typed nil listener")
	}
	tests := []struct {
		name string
		addr net.Addr
	}{
		{name: "unspecified IPv4", addr: &net.TCPAddr{IP: net.IPv4zero, Port: 1234}},
		{name: "unspecified IPv6", addr: &net.TCPAddr{IP: net.IPv6unspecified, Port: 1234}},
		{name: "non loopback", addr: &net.TCPAddr{IP: net.ParseIP("192.0.2.1"), Port: 1234}},
		{name: "unix", addr: &net.UnixAddr{Name: "/tmp/taskhttp-test.sock", Net: "unix"}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			listener := &rejectingListener{addr: testCase.addr}
			if err := server.ServeLocal(t.Context(), listener); !errors.Is(err, ErrListenerInvalid) {
				t.Fatalf("ServeLocal() error = %v, want ErrListenerInvalid", err)
			}
			if listener.acceptCalls != 0 {
				t.Fatalf("rejected listener Accept calls = %d, want 0", listener.acceptCalls)
			}
		})
	}
}

type rejectingListener struct {
	addr        net.Addr
	acceptCalls int
}

func (l *rejectingListener) Accept() (net.Conn, error) {
	l.acceptCalls++
	return nil, errors.New("unexpected Accept call")
}

func (*rejectingListener) Close() error { return nil }

func (l *rejectingListener) Addr() net.Addr { return l.addr }

func TestOperatorAndTaskServersRejectCrossTokenProfileAndBindTaskIdentity(t *testing.T) {
	identity := validTaskIdentity(t, "task/isolated")
	observed := make(chan common.TaskSessionIdentity, 1)
	taskURL := newTaskIsolationEndpoint(t, identity, observed)
	operatorURL := newOperatorIsolationEndpoint(t)
	assertWrongBearerRejected(t, taskURL, operatorBearer, "operator token on task server")
	assertWrongBearerRejected(t, operatorURL, taskBearer, "task token on operator server")
	taskSession := openSession(t, taskURL, taskBearer)
	operatorSession := openSession(t, operatorURL, operatorBearer)
	assertCrossSessionRejected(t, taskURL, taskBearer, operatorSession, 20, "operator profile session on task server")
	assertCrossSessionRejected(t, operatorURL, operatorBearer, taskSession, 21, "task profile session on operator server")
	callTaskRead(t, taskURL, taskSession)
	assertTaskIdentityObserved(t, observed, identity)
}

// TestTaskSessionDeletePreservesAnotherSession verifies that deleting one
// task-authenticated HTTP session cancels only that session's work. A second
// session remains usable while an in-flight tool call is still running; this
// is the deterministic unit-level boundary needed by ST-03 without starting a
// provider or reading any credential from the environment.
func TestTaskSessionDeletePreservesAnotherSession(t *testing.T) {
	scenario := newTaskSessionDeleteScenario(t)
	deleteTaskSessionA(t, scenario)
	scenario.releaseB()
	responseB := <-scenario.resultB
	assertTaskCallSucceeded(t, responseB)
	assertTaskIdentityObserved(t, scenario.observed, scenario.identityB)
	assertDeletedSessionUnavailable(t, scenario)
}

type taskSessionDeleteScenario struct {
	endpoint, bearerA, bearerB string
	sessionA, sessionB         string
	identityB                  common.TaskSessionIdentity
	observed                   chan common.TaskSessionIdentity
	resultB                    chan rpcResponse
	released                   chan struct{}
	releaseB                   func()
}

func newTaskSessionDeleteScenario(t *testing.T) *taskSessionDeleteScenario {
	t.Helper()
	identityA := validTaskIdentity(t, "task/delete-a")
	identityB := validTaskIdentity(t, "task/delete-b")
	const bearerA = "task-delete-a-secret.NeverLeak_1234567890"
	const bearerB = "task-delete-b-secret.NeverLeak_1234567890"
	authenticator := newTaskSessionDeleteAuthenticator(t, bearerA, identityA, bearerB, identityB)
	startedB := make(chan struct{})
	releaseB := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseB) }) })
	observed := make(chan common.TaskSessionIdentity, 2)
	server, err := NewServer(t.Context(), &providerFixture{
		tools: testDefinitions("read"),
		call:  taskSessionDeleteProvider(identityB, startedB, releaseB, observed),
	}, ServerConfig{
		WorkspaceRoot: t.TempDir(), Authenticator: authenticator,
		Name: "task-delete-isolation", Version: "test", AllowedTools: []string{"read"}, Logger: testLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	endpoint := runServer(t, server)
	sessionA := openSessionWithBearer(t, endpoint, bearerA)
	sessionB := openSessionWithBearer(t, endpoint, bearerB)
	resultB := startTaskSessionDeleteCall(t, endpoint, bearerB, sessionB, startedB)
	return &taskSessionDeleteScenario{
		endpoint: endpoint, bearerA: bearerA, bearerB: bearerB,
		sessionA: sessionA, sessionB: sessionB, identityB: identityB,
		observed: observed, resultB: resultB,
		released: releaseB,
		releaseB: func() { releaseOnce.Do(func() { close(releaseB) }) },
	}
}

func newTaskSessionDeleteAuthenticator(
	t *testing.T,
	bearerA string,
	identityA common.TaskSessionIdentity,
	bearerB string,
	identityB common.TaskSessionIdentity,
) *authenticatorFixture {
	t.Helper()
	digestA, err := common.DigestSessionCredential(bearerA)
	if err != nil {
		t.Fatal(err)
	}
	digestB, err := common.DigestSessionCredential(bearerB)
	if err != nil {
		t.Fatal(err)
	}
	return &authenticatorFixture{identity: map[common.SessionCredentialDigest]common.TaskSessionIdentity{
		digestA: identityA,
		digestB: identityB,
	}}
}

func taskSessionDeleteProvider(
	identityB common.TaskSessionIdentity,
	startedB, releaseB chan struct{},
	observed chan<- common.TaskSessionIdentity,
) func(context.Context, string, json.RawMessage) (any, error) {
	return func(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
		principal, principalErr := common.RequireCallerPrincipal(ctx)
		attribution, attributionErr := common.RequireCallerAttribution(ctx)
		if principalErr != nil || attributionErr != nil {
			return nil, errors.New("task transport identity missing")
		}
		observed <- common.TaskSessionIdentity{Principal: principal, Attribution: attribution}
		if attribution.TaskID != identityB.Attribution.TaskID {
			return map[string]any{"ok": true}, nil
		}
		close(startedB)
		select {
		case <-releaseB:
			return map[string]any{"ok": true}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func startTaskSessionDeleteCall(
	t *testing.T,
	endpoint, bearer, session string,
	started <-chan struct{},
) chan rpcResponse {
	t.Helper()
	result := make(chan rpcResponse, 1)
	safego.Go(t.Context(), nil, "taskhttp.test.session-delete-call", func(context.Context) {
		result <- postRPC(t, endpoint, bearer, session,
			`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"read","arguments":{}}}`,
			t.Context())
	})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("session B tool call did not start")
	}
	return result
}

func deleteTaskSessionA(t *testing.T, scenario *taskSessionDeleteScenario) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodDelete, scenario.endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+scenario.bearerA)
	request.Header.Set("Mcp-Session-Id", scenario.sessionA)
	request.Header.Set("MCP-Protocol-Version", protocol)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE session A status = %d, want %d", response.StatusCode, http.StatusNoContent)
	}
	select {
	case <-time.After(100 * time.Millisecond):
	case <-scenario.releaseSignal():
		t.Fatal("session A deletion unexpectedly released session B")
	}
}

func (s *taskSessionDeleteScenario) releaseSignal() <-chan struct{} {
	return s.released
}

func assertTaskCallSucceeded(t *testing.T, response rpcResponse) {
	t.Helper()
	if response.status != http.StatusOK || strings.Contains(response.body, `"error"`) {
		t.Fatalf("session B tool call after session A DELETE = %d %s", response.status, response.body)
	}
}

func assertDeletedSessionUnavailable(t *testing.T, scenario *taskSessionDeleteScenario) {
	t.Helper()
	removed := postRPC(t, scenario.endpoint, scenario.bearerA, scenario.sessionA, pingBody(9), t.Context())
	if removed.status != http.StatusNotFound {
		t.Fatalf("deleted session A remained usable: status=%d body=%s", removed.status, removed.body)
	}
	remaining := postRPC(t, scenario.endpoint, scenario.bearerB, scenario.sessionB, pingBody(10), t.Context())
	if remaining.status != http.StatusOK {
		t.Fatalf("session B ping after session A DELETE = %d %s", remaining.status, remaining.body)
	}
}

func openSessionWithBearer(t *testing.T, endpoint, bearer string) string {
	t.Helper()
	initialize := postRPC(t, endpoint, bearer, "", initializeBody(), t.Context())
	if initialize.status != http.StatusOK || initialize.session == "" {
		t.Fatalf("initialize = %d %s session=%q", initialize.status, initialize.body, initialize.session)
	}
	initialized := postRPC(t, endpoint, bearer, initialize.session, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, t.Context())
	if initialized.status != http.StatusAccepted {
		t.Fatalf("initialized = %d %s", initialized.status, initialized.body)
	}
	return initialize.session
}

func newTaskIsolationEndpoint(
	t *testing.T,
	identity common.TaskSessionIdentity,
	observed chan<- common.TaskSessionIdentity,
) string {
	t.Helper()
	base := &providerFixture{tools: testDefinitions("read"), call: captureTaskIdentity(observed)}
	server, err := NewServer(t.Context(), base, ServerConfig{
		WorkspaceRoot: t.TempDir(), Authenticator: newAuthenticatorFixture(t, taskBearer, identity),
		Name: "task-server", Version: "test", AllowedTools: []string{"read"}, Logger: testLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return runServer(t, server)
}

func captureTaskIdentity(observed chan<- common.TaskSessionIdentity) func(context.Context, string, json.RawMessage) (any, error) {
	return func(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
		principal, principalErr := common.RequireCallerPrincipal(ctx)
		attribution, attributionErr := common.RequireCallerAttribution(ctx)
		if principalErr != nil || attributionErr != nil {
			return nil, errors.New("task transport identity missing")
		}
		scope, scopeOK := common.ToolScopeFromContext(ctx)
		if !scopeOK || scope.AgentID != attribution.TaskID {
			return nil, errors.New("task transport scope missing attribution task")
		}
		observed <- common.TaskSessionIdentity{Principal: principal, Attribution: attribution}
		return map[string]any{"ok": true}, nil
	}
}

func newOperatorIsolationEndpoint(t *testing.T) string {
	t.Helper()
	principal, err := common.NewCallerPrincipal(
		"operator/local", "workspace/operator", "operator_v1", "auth/operator",
		time.Now().UTC().Add(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	server, err := common.NewLocalHTTPServer(
		"operator-server", "test", &providerFixture{tools: testDefinitions("operator_read")},
		common.LocalHTTPProfile{WorkspaceRoot: t.TempDir(), BearerToken: operatorBearer, CallerPrincipal: principal},
		testLogger(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return runServer(t, server)
}

func assertWrongBearerRejected(t *testing.T, endpoint, bearer, label string) {
	t.Helper()
	response := postRPC(t, endpoint, bearer, "", initializeBody(), t.Context())
	if response.status != http.StatusUnauthorized {
		t.Fatalf("%s = %d %s", label, response.status, response.body)
	}
}

func assertCrossSessionRejected(t *testing.T, endpoint, bearer, session string, id int, label string) {
	t.Helper()
	response := postRPC(t, endpoint, bearer, session, pingBody(id), t.Context())
	if response.status != http.StatusNotFound {
		t.Fatalf("%s = %d %s", label, response.status, response.body)
	}
}

func callTaskRead(t *testing.T, endpoint, session string) {
	t.Helper()
	response := postRPC(t, endpoint, taskBearer, session, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"read","arguments":{}}}`, t.Context())
	if response.status != http.StatusOK || strings.Contains(response.body, `"error"`) {
		t.Fatalf("task tools/call = %d %s", response.status, response.body)
	}
}

func assertTaskIdentityObserved(
	t *testing.T,
	observed <-chan common.TaskSessionIdentity,
	want common.TaskSessionIdentity,
) {
	t.Helper()
	select {
	case got := <-observed:
		if !got.Equal(want) {
			t.Fatalf("task tool identity = %#v, want %#v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("task tool call did not observe principal and attribution")
	}
}

func TestEmptyAllowlistSupportsAuthenticatedHandshakeWithoutSendOrWait(t *testing.T) {
	identity := validTaskIdentity(t, "task/empty")
	base := &providerFixture{tools: testDefinitions("send_message", "wait_for_agents")}
	server, err := NewServer(t.Context(), base, ServerConfig{
		WorkspaceRoot: t.TempDir(), Authenticator: newAuthenticatorFixture(t, taskBearer, identity),
		Name: "task-empty", Version: "test", AllowedTools: []string{}, Logger: testLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	serverURL := runServer(t, server)
	session := openSession(t, serverURL, taskBearer)
	listed := postRPC(t, serverURL, taskBearer, session, `{"jsonrpc":"2.0","id":4,"method":"tools/list"}`, t.Context())
	if listed.status != http.StatusOK || !strings.Contains(listed.body, `"tools":[]`) {
		t.Fatalf("empty tools/list = %d %s", listed.status, listed.body)
	}
	for index, name := range []string{"send_message", "wait_for_agents"} {
		body := `{"jsonrpc":"2.0","id":` + string(rune('5'+index)) + `,"method":"tools/call","params":{"name":"` + name + `","arguments":{}}}`
		response := postRPC(t, serverURL, taskBearer, session, body, t.Context())
		if response.status != http.StatusOK || !strings.Contains(response.body, ErrToolNotAllowed.Error()) {
			t.Fatalf("disabled %s call = %d %s", name, response.status, response.body)
		}
	}
	if _, names := base.counts(); len(names) != 0 {
		t.Fatalf("empty server forwarded send/wait: %v", names)
	}
}

func TestAuthenticationAndToolCallsObserveRequestCancellation(t *testing.T) {
	identity := validTaskIdentity(t, "task/cancel")

	t.Run("authentication", func(t *testing.T) {
		authenticator := newAuthenticatorFixture(t, taskBearer, identity)
		requestCtx, cancel := context.WithCancel(t.Context())
		cancel()
		digest, err := common.DigestSessionCredential(taskBearer)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := authenticator.Authenticate(requestCtx, digest); !errors.Is(err, context.Canceled) {
			t.Fatalf("Authenticate() error = %v, want context.Canceled", err)
		}
	})

	t.Run("tool", func(t *testing.T) {
		entered := make(chan struct{}, 1)
		canceled := make(chan struct{})
		base := &providerFixture{
			tools: testDefinitions("read"),
			call: func(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
				entered <- struct{}{}
				<-ctx.Done()
				close(canceled)
				return nil, ctx.Err()
			},
		}
		server, err := NewServer(t.Context(), base, ServerConfig{
			WorkspaceRoot: t.TempDir(), Authenticator: newAuthenticatorFixture(t, taskBearer, identity),
			Name: "task-tool-cancel", Version: "test", AllowedTools: []string{"read"}, Logger: testLogger(),
		})
		if err != nil {
			t.Fatal(err)
		}
		serverURL := runServer(t, server)
		session := openSession(t, serverURL, taskBearer)
		requestCtx, cancel := context.WithCancel(t.Context())
		finished := make(chan struct{})
		var waitGroup sync.WaitGroup
		waitGroup.Go(func() {
			defer close(finished)
			_ = postRPCError(serverURL, taskBearer, session, `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"read","arguments":{}}}`, requestCtx)
		})
		<-entered
		cancel()
		select {
		case <-canceled:
		case <-time.After(time.Second):
			t.Fatal("tool provider did not observe request cancellation")
		}
		<-finished
		waitGroup.Wait()
	})
}

type localServer interface {
	ServeLocal(context.Context, net.Listener) error
}

func runServer(t *testing.T, server localServer) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveCtx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	var waitGroup sync.WaitGroup
	waitGroup.Go(func() { done <- server.ServeLocal(serveCtx, listener) })
	t.Cleanup(func() {
		cancel()
		select {
		case serveErr := <-done:
			if serveErr != nil {
				t.Errorf("ServeLocal() error = %v", serveErr)
			}
		case <-time.After(6 * time.Second):
			t.Error("ServeLocal() did not stop after cancellation")
		}
		waitGroup.Wait()
	})
	return "http://" + listener.Addr().String() + "/mcp"
}

type rpcResponse struct {
	status  int
	body    string
	session string
}

func postRPC(t *testing.T, endpoint, bearer, session, body string, ctx context.Context) rpcResponse {
	t.Helper()
	response, err := doRPC(endpoint, bearer, session, body, ctx)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func postRPCError(endpoint, bearer, session, body string, ctx context.Context) error {
	_, err := doRPC(endpoint, bearer, session, body, ctx)
	return err
}

func doRPC(endpoint, bearer, session, body string, ctx context.Context) (rpcResponse, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewBufferString(body))
	if err != nil {
		return rpcResponse{}, err
	}
	request.Header.Set("Authorization", "Bearer "+bearer)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	if session != "" {
		request.Header.Set("Mcp-Session-Id", session)
		request.Header.Set("MCP-Protocol-Version", protocol)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return rpcResponse{}, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return rpcResponse{}, err
	}
	return rpcResponse{
		status: response.StatusCode, body: string(responseBody), session: response.Header.Get("Mcp-Session-Id"),
	}, nil
}

func openSession(t *testing.T, endpoint, bearer string) string {
	t.Helper()
	initialize := postRPC(t, endpoint, bearer, "", initializeBody(), t.Context())
	if initialize.status != http.StatusOK || initialize.session == "" {
		t.Fatalf("initialize = %d %s session=%q", initialize.status, initialize.body, initialize.session)
	}
	initialized := postRPC(t, endpoint, bearer, initialize.session, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, t.Context())
	if initialized.status != http.StatusAccepted {
		t.Fatalf("initialized = %d %s", initialized.status, initialized.body)
	}
	return initialize.session
}

func initializeBody() string {
	return `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"taskhttp-test","version":"1"}}}`
}

func pingBody(id int) string {
	encoded, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": "ping"})
	return string(encoded)
}

func newAuthenticatorFixture(
	t *testing.T,
	bearer string,
	identity common.TaskSessionIdentity,
) *authenticatorFixture {
	t.Helper()
	digest, err := common.DigestSessionCredential(bearer)
	if err != nil {
		t.Fatal(err)
	}
	return &authenticatorFixture{identity: map[common.SessionCredentialDigest]common.TaskSessionIdentity{digest: identity}}
}

func validTaskIdentity(t *testing.T, taskID string) common.TaskSessionIdentity {
	t.Helper()
	principal, err := common.NewCallerPrincipal(
		"principal/"+taskID, "scope/"+taskID, "unified_v2_task", "auth/"+taskID,
		time.Now().UTC().Add(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	attribution, err := common.NewCallerAttribution(
		taskID,
		common.ExecutionFence{
			AgentID: taskID, ReservationID: "reservation/" + taskID, ReservationGeneration: 1,
			IncarnationID: "incarnation/" + taskID, IncarnationGeneration: 1,
			BindingVersion: 1, BindingDigest: strings.Repeat("a", 64), SourceFenceRef: "fence/" + taskID,
		},
		common.IssuerBinding{
			Kind: common.CallerAttributionIssuerManagedRuntime, BindingRef: "issuer/" + taskID,
			Generation: 1, Digest: strings.Repeat("b", 64),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return common.TaskSessionIdentity{Principal: principal, Attribution: attribution}
}

func testLogger() *pkglogger.Runtime {
	return pkglogger.NewRuntime(pkglogger.RuntimeConfig{
		Mode: pkglogger.Development, ServiceName: "taskhttp-test", ServiceVersion: "test", ExitFunc: func(int) {},
	})
}
