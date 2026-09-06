package common

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	runtimesafe "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
)

func TestSessionCredentialDigestIsComparableAndAlwaysRedacted(t *testing.T) {
	const credential = "task-secret.NeverLeak_1234567890_ABC"
	digest, err := DigestSessionCredential(credential)
	if err != nil {
		t.Fatal(err)
	}
	if digest == (SessionCredentialDigest{}) {
		t.Fatal("DigestSessionCredential() returned zero digest")
	}
	registry := map[SessionCredentialDigest]string{digest: "registered"}
	if registry[digest] != "registered" {
		t.Fatal("SessionCredentialDigest is not usable as a comparable registry key")
	}
	encoded, err := json.Marshal(struct {
		Digest SessionCredentialDigest `json:"digest"`
	}{Digest: digest})
	if err != nil {
		t.Fatal(err)
	}
	textEncoded, err := digest.MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	outputs := []string{
		digest.String(), digest.GoString(), fmt.Sprint(digest), fmt.Sprintf("%v", digest),
		fmt.Sprintf("%#v", digest), fmt.Sprintf("%x", digest), fmt.Sprintf("%s", digest), string(encoded), string(textEncoded),
	}
	assertRedactedRepresentations(t, outputs, credential)
	if _, err := DigestSessionCredential(" invalid-secret "); err == nil || strings.Contains(err.Error(), "invalid-secret") {
		t.Fatalf("invalid credential error = %v, want secret-free rejection", err)
	}
}

func TestSessionCredentialSyntaxIsStrictAndBounded(t *testing.T) {
	cases := []struct {
		name       string
		credential string
		valid      bool
	}{
		{name: "minimum", credential: strings.Repeat("a", minSessionCredentialBytes), valid: true},
		{name: "maximum_with_tail_padding", credential: strings.Repeat("b", maxSessionCredentialBytes-2) + "==", valid: true},
		{name: "too_short", credential: strings.Repeat("c", minSessionCredentialBytes-1)},
		{name: "too_long", credential: strings.Repeat("d", maxSessionCredentialBytes+1)},
		{name: "padding_in_middle", credential: strings.Repeat("e", minSessionCredentialBytes) + "=x"},
		{name: "space", credential: strings.Repeat("f", minSessionCredentialBytes) + " "},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := DigestSessionCredential(testCase.credential)
			if (err == nil) != testCase.valid {
				t.Fatalf("DigestSessionCredential() error = %v, valid=%v", err, testCase.valid)
			}
		})
	}
}

func TestTaskSessionIdentityFieldGuardAndValidation(t *testing.T) {
	typeOfIdentity := reflect.TypeFor[TaskSessionIdentity]()
	gotFields := make([]string, 0, typeOfIdentity.NumField())
	for index := 0; index < typeOfIdentity.NumField(); index++ {
		gotFields = append(gotFields, typeOfIdentity.Field(index).Name)
	}
	if want := []string{"Principal", "Attribution"}; !reflect.DeepEqual(gotFields, want) {
		t.Fatalf("TaskSessionIdentity fields = %v, want %v", gotFields, want)
	}
	valid := validTaskSessionIdentity(t, "principal/task-a")
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	sameInstantDifferentLocation := valid
	sameInstantDifferentLocation.Principal.ExpiresAt = valid.Principal.ExpiresAt.In(time.FixedZone("same-instant", 9*60*60))
	if !valid.Equal(sameInstantDifferentLocation) {
		t.Fatal("TaskSessionIdentity.Equal compared time.Location identity instead of the expiry instant")
	}
	differentAuthContext := valid
	differentAuthContext.Principal.AuthContext += "/rotated"
	if valid.Equal(differentAuthContext) {
		t.Fatal("TaskSessionIdentity.Equal ignored a principal field")
	}
	invalidPrincipal := valid
	invalidPrincipal.Principal.PrincipalRef = ""
	if err := invalidPrincipal.Validate(); !errors.Is(err, ErrTaskSessionIdentityInvalid) {
		t.Fatalf("invalid principal error = %v", err)
	}
	invalidAttribution := valid
	invalidAttribution.Attribution.Audience = "forged"
	if err := invalidAttribution.Validate(); !errors.Is(err, ErrTaskSessionIdentityInvalid) {
		t.Fatalf("invalid attribution error = %v", err)
	}
}

func TestTaskHTTPAuthenticatorIsBoundedByRequestSlots(t *testing.T) {
	const credential = "task-capacity-secret.1234567890_ABCDE"
	authenticator := newTaskAuthenticatorFixture(t, map[string]TaskSessionIdentity{
		credential: validTaskSessionIdentity(t, "principal/capacity"),
	})
	server, err := NewLocalHTTPServer("orch", "test", testToolProvider{}, LocalHTTPProfile{
		WorkspaceRoot: t.TempDir(), TaskSessionAuthenticator: authenticator,
	}, newTestLoggerRuntime())
	if err != nil {
		t.Fatal(err)
	}
	server.requestSlots = make(chan struct{}, 1)
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseAuthenticator := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseAuthenticator)
	authenticator.setProbe(func() error {
		started <- struct{}{}
		<-release
		return nil
	})
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	runtimesafe.SafeGo(t.Context(), nil, "mcpserver.common.test.task-auth-capacity", func(context.Context) {
		firstDone <- serveTaskRequest(server, http.MethodPost, initializeTaskRequestBody, "", credential)
	})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first request did not enter authenticator")
	}
	second := serveTaskRequest(server, http.MethodPost, initializeTaskRequestBody, "", credential)
	if second.Code != http.StatusServiceUnavailable {
		t.Fatalf("second request status = %d, want bounded capacity rejection", second.Code)
	}
	if got := len(authenticator.eventsSnapshot()); got != 2 {
		t.Fatalf("events during saturation = %v, second request reached authenticator", authenticator.eventsSnapshot())
	}
	releaseAuthenticator()
	select {
	case first := <-firstDone:
		if first.Code != http.StatusOK {
			t.Fatalf("first request = %d %s", first.Code, first.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("first request did not finish")
	}
}

func TestLocalHTTPProfileRequiresExactlyOneAuthenticationMode(t *testing.T) {
	identity := validTaskSessionIdentity(t, "principal/task-a")
	authenticator := newTaskAuthenticatorFixture(t, map[string]TaskSessionIdentity{"task-a-secret.1234567890_ABCDEFGHIJK": identity})
	principal := identity.Principal
	var typedNil *taskAuthenticatorFixture
	cases := []struct {
		name    string
		profile LocalHTTPProfile
		wantErr bool
	}{
		{name: "operator", profile: LocalHTTPProfile{WorkspaceRoot: t.TempDir(), BearerToken: "operator-secret", CallerPrincipal: principal}},
		{name: "task", profile: LocalHTTPProfile{WorkspaceRoot: t.TempDir(), TaskSessionAuthenticator: authenticator}},
		{name: "neither", profile: LocalHTTPProfile{WorkspaceRoot: t.TempDir()}, wantErr: true},
		{name: "both", profile: LocalHTTPProfile{WorkspaceRoot: t.TempDir(), BearerToken: "operator-secret", TaskSessionAuthenticator: authenticator}, wantErr: true},
		{name: "task_and_operator_principal", profile: LocalHTTPProfile{WorkspaceRoot: t.TempDir(), TaskSessionAuthenticator: authenticator, CallerPrincipal: principal}, wantErr: true},
		{name: "typed_nil", profile: LocalHTTPProfile{WorkspaceRoot: t.TempDir(), TaskSessionAuthenticator: typedNil}, wantErr: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := NewLocalHTTPServer("orch", "test", testToolProvider{}, testCase.profile, newTestLoggerRuntime())
			if (err != nil) != testCase.wantErr {
				t.Fatalf("NewLocalHTTPServer() error = %v, wantErr=%v", err, testCase.wantErr)
			}
		})
	}
	if authenticator.validateCallCount() != 1 {
		t.Fatalf("authenticator Validate calls = %d, want task-only construction once", authenticator.validateCallCount())
	}
}

func TestTaskHTTPAuthenticationFreezesIdentityAndBindsToolContext(t *testing.T) {
	fixture := newTaskHTTPFixture(t)
	failed := serveTaskRequest(fixture.server, http.MethodPost, initializeTaskRequestBody, "", taskWrongCredential)
	assertHTTPStatus(t, failed, http.StatusUnauthorized)
	if activeHTTPSessionCount(fixture.server) != 0 {
		t.Fatal("wrong-token initialize allocated a session")
	}
	fixture.openSession(t)
	if !fixture.session.taskAuthOn || fixture.session.credentialDigest != fixture.digestA || !fixture.session.taskIdentity.Equal(fixture.identityA) {
		t.Fatal("session did not freeze task digest and identity pair")
	}
	tool := serveTaskRequest(fixture.server, http.MethodPost, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"demo_tool","arguments":{}}}`, fixture.sessionID, taskCredentialA)
	assertHTTPStatus(t, tool, http.StatusOK)
	assertObservedTaskIdentity(t, fixture.observed, fixture.identityA)
	forged := serveTaskRequest(fixture.server, http.MethodPost, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"demo_tool","_agentId":"forged","arguments":{}}}`, fixture.sessionID, taskCredentialA)
	if forged.Code != http.StatusOK || !strings.Contains(forged.Body.String(), `"error"`) || fixture.toolCalls.Load() != 1 {
		t.Fatalf("forged metadata = %d %s toolCalls=%d", forged.Code, forged.Body.String(), fixture.toolCalls.Load())
	}
	assertOutputOmitsSecrets(t, failed.Body.String()+forged.Body.String()+tool.Body.String()+fmt.Sprint(fixture.digestA))
	assertAuthenticatorOrder(t, fixture.authenticator.eventsSnapshot())
}

func TestTaskHTTPAuthenticationRevalidatesWrongRevokedExpiredAndReconnect(t *testing.T) {
	fixture := newTaskHTTPFixture(t)
	fixture.openSession(t)
	lastAccess := sessionLastAccess(fixture.session)
	assertFixtureRequestRejected(t, fixture, taskWrongCredential, lastAccess)
	assertFixtureRequestRejected(t, fixture, taskCredentialB, lastAccess)
	fixture.authenticator.setIdentity(fixture.digestA, validTaskSessionIdentity(t, "principal/rebound"))
	assertFixtureRequestRejected(t, fixture, taskCredentialA, lastAccess)
	fixture.authenticator.setIdentity(fixture.digestA, fixture.identityA)
	fixture.authenticator.setRevoked(fixture.digestA, true)
	assertFixtureRequestRejected(t, fixture, taskCredentialA, lastAccess)
	fixture.authenticator.setRevoked(fixture.digestA, false)
	expired := fixture.identityA
	expired.Principal.ExpiresAt = time.Now().UTC().Add(-time.Second)
	fixture.authenticator.setIdentity(fixture.digestA, expired)
	assertFixtureRequestRejected(t, fixture, taskCredentialA, lastAccess)
	fixture.authenticator.setIdentity(fixture.digestA, fixture.identityA)
	missing := serveTaskRequest(fixture.server, http.MethodPost, `{"jsonrpc":"2.0","id":4,"method":"ping"}`, "unknown-session", taskCredentialA)
	assertHTTPStatus(t, missing, http.StatusNotFound)
	reconnect := serveTaskRequest(fixture.server, http.MethodPost, `{"jsonrpc":"2.0","id":5,"method":"ping"}`, fixture.sessionID, taskCredentialA)
	assertHTTPStatus(t, reconnect, http.StatusOK)
	if !sessionLastAccess(fixture.session).After(lastAccess) {
		t.Fatal("authorized reconnect did not touch session")
	}
}

func TestTaskHTTPDeleteReauthenticatesBeforeRemoval(t *testing.T) {
	fixture := newTaskHTTPFixture(t)
	fixture.openSession(t)
	fixture.authenticator.setRevoked(fixture.digestA, true)
	assertDeletePreservesTaskSession(t, fixture, taskCredentialA)
	fixture.authenticator.setRevoked(fixture.digestA, false)
	assertDeletePreservesTaskSession(t, fixture, taskCredentialB)
	deleted := serveTaskRequest(fixture.server, http.MethodDelete, "", fixture.sessionID, taskCredentialA)
	assertHTTPStatus(t, deleted, http.StatusNoContent)
	if activeHTTPSessionCount(fixture.server) != 0 {
		t.Fatal("authorized DELETE did not remove session")
	}
}

func TestTaskSessionBindingIsAtomicAndOperatorNeverGetsAttribution(t *testing.T) {
	identity := validTaskSessionIdentity(t, "principal/task-a")
	taskSession := &httpMCPSession{taskAuthOn: true, taskIdentity: identity}
	original, err := BindCallerAttribution(context.Background(), identity.Attribution)
	if err != nil {
		t.Fatal(err)
	}
	got, err := taskSession.bindCallerPrincipal(original)
	if err == nil || got != original {
		t.Fatalf("atomic bind = (%v,%v), want original context and error", got, err)
	}
	if _, principalErr := RequireCallerPrincipal(got); !errors.Is(principalErr, ErrMissingCallerPrincipal) {
		t.Fatalf("failed atomic bind left principal behind: %v", principalErr)
	}

	operatorSession := &httpMCPSession{principalOn: true, principal: identity.Principal}
	operatorCtx, err := operatorSession.bindCallerPrincipal(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RequireCallerPrincipal(operatorCtx); err != nil {
		t.Fatal(err)
	}
	if _, err := RequireCallerAttribution(operatorCtx); !errors.Is(err, ErrCallerAttributionUnavailable) {
		t.Fatalf("operator session attribution error = %v, want unavailable", err)
	}
}

const (
	initializeTaskRequestBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"task-test","version":"1"}}}`
	taskCredentialA           = "task-a-secret.NeverLeak_1234567890ABC"
	taskCredentialB           = "task-b-secret.NeverLeak_1234567890ABC"
	taskWrongCredential       = "wrong-secret.NeverLeak_1234567890ABC"
)

type taskHTTPFixture struct {
	server        *HTTPServer
	authenticator *taskAuthenticatorFixture
	identityA     TaskSessionIdentity
	digestA       SessionCredentialDigest
	toolCalls     atomic.Int32
	observed      chan TaskSessionIdentity
	sessionID     string
	session       *httpMCPSession
}

func newTaskHTTPFixture(t *testing.T) *taskHTTPFixture {
	t.Helper()
	fixture := &taskHTTPFixture{
		identityA: validTaskSessionIdentity(t, "principal/task-a"),
		observed:  make(chan TaskSessionIdentity, 2),
	}
	identityB := validTaskSessionIdentity(t, "principal/task-b")
	fixture.authenticator = newTaskAuthenticatorFixture(t, map[string]TaskSessionIdentity{
		taskCredentialA: fixture.identityA,
		taskCredentialB: identityB,
	})
	provider := captureToolProvider{call: fixture.captureToolIdentity}
	server, err := NewLocalHTTPServer("orch", "test", provider, LocalHTTPProfile{
		WorkspaceRoot: t.TempDir(), TaskSessionAuthenticator: fixture.authenticator,
	}, newTestLoggerRuntime())
	if err != nil {
		t.Fatal(err)
	}
	fixture.server = server
	fixture.digestA, err = DigestSessionCredential(taskCredentialA)
	if err != nil {
		t.Fatal(err)
	}
	return fixture
}

func (f *taskHTTPFixture) captureToolIdentity(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
	f.toolCalls.Add(1)
	principal, principalErr := RequireCallerPrincipal(ctx)
	attribution, attributionErr := RequireCallerAttribution(ctx)
	if principalErr != nil || attributionErr != nil {
		return nil, errors.New("transport identity missing")
	}
	f.observed <- TaskSessionIdentity{Principal: principal, Attribution: attribution}
	return map[string]any{"ok": true}, nil
}

func (f *taskHTTPFixture) openSession(t *testing.T) {
	t.Helper()
	initialize := serveTaskRequest(f.server, http.MethodPost, initializeTaskRequestBody, "", taskCredentialA)
	assertHTTPStatus(t, initialize, http.StatusOK)
	f.sessionID = initialize.Header().Get(httpMCPHeaderSessionID)
	if f.sessionID == "" {
		t.Fatal("task initialize omitted session ID")
	}
	f.session = requireHTTPSessionForTest(t, f.server, f.sessionID)
	f.authenticator.setProbe(f.unlockedAuthenticationProbe)
	initialized := serveTaskRequest(f.server, http.MethodPost, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, f.sessionID, taskCredentialA)
	assertHTTPStatus(t, initialized, http.StatusAccepted)
}

func (f *taskHTTPFixture) unlockedAuthenticationProbe() error {
	if !f.server.sessionsMu.TryLock() {
		return errors.New("Authenticate called while sessionsMu is held")
	}
	f.server.sessionsMu.Unlock()
	if !f.session.mu.TryLock() {
		return errors.New("Authenticate called while session.mu is held")
	}
	f.session.mu.Unlock()
	return nil
}

func assertRedactedRepresentations(t *testing.T, outputs []string, secret string) {
	t.Helper()
	for _, output := range outputs {
		if strings.Contains(output, secret) || !strings.Contains(output, sessionCredentialRedaction) {
			t.Fatalf("digest representation %q is not fixed-redacted", output)
		}
	}
}

func assertHTTPStatus(t *testing.T, response *httptest.ResponseRecorder, want int) {
	t.Helper()
	if response.Code != want {
		t.Fatalf("HTTP status = %d, want %d; body=%s", response.Code, want, response.Body.String())
	}
}

func assertObservedTaskIdentity(t *testing.T, observed <-chan TaskSessionIdentity, want TaskSessionIdentity) {
	t.Helper()
	select {
	case got := <-observed:
		if !got.Equal(want) {
			t.Fatalf("tool context identity = %#v, want %#v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("tool provider did not observe task identity")
	}
}

func assertOutputOmitsSecrets(t *testing.T, output string) {
	t.Helper()
	for _, secret := range []string{taskCredentialA, taskCredentialB, taskWrongCredential} {
		if strings.Contains(output, secret) {
			t.Fatalf("raw credential leaked into output: %q", secret)
		}
	}
}

func assertAuthenticatorOrder(t *testing.T, events []string) {
	t.Helper()
	if len(events) < 2 || events[0] != "validate" || events[1] != "authenticate" {
		t.Fatalf("authenticator events = %v, want Validate before Authenticate", events)
	}
}

func assertFixtureRequestRejected(t *testing.T, fixture *taskHTTPFixture, credential string, lastAccess time.Time) {
	t.Helper()
	assertTaskRequestRejectedWithoutSideEffects(
		t, fixture.server, fixture.session, fixture.sessionID, credential,
		http.StatusUnauthorized, lastAccess, &fixture.toolCalls, fixture.toolCalls.Load(),
	)
}

func assertDeletePreservesTaskSession(t *testing.T, fixture *taskHTTPFixture, credential string) {
	t.Helper()
	response := serveTaskRequest(fixture.server, http.MethodDelete, "", fixture.sessionID, credential)
	assertHTTPStatus(t, response, http.StatusUnauthorized)
	if activeHTTPSessionCount(fixture.server) != 1 {
		t.Fatal("unauthorized DELETE removed task session")
	}
}

type taskAuthenticatorFixture struct {
	mu         sync.Mutex
	identities map[SessionCredentialDigest]TaskSessionIdentity
	revoked    map[SessionCredentialDigest]bool
	events     []string
	probe      func() error
}

func newTaskAuthenticatorFixture(t *testing.T, credentials map[string]TaskSessionIdentity) *taskAuthenticatorFixture {
	t.Helper()
	fixture := &taskAuthenticatorFixture{
		identities: make(map[SessionCredentialDigest]TaskSessionIdentity, len(credentials)),
		revoked:    make(map[SessionCredentialDigest]bool),
	}
	for credential, identity := range credentials {
		digest, err := DigestSessionCredential(credential)
		if err != nil {
			t.Fatal(err)
		}
		fixture.identities[digest] = identity
	}
	return fixture
}

func (f *taskAuthenticatorFixture) Validate() error {
	if f == nil {
		return errors.New("authenticator is nil")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "validate")
	if len(f.identities) == 0 {
		return errors.New("credential registry is empty")
	}
	return nil
}

func (f *taskAuthenticatorFixture) Authenticate(_ context.Context, digest SessionCredentialDigest) (TaskSessionIdentity, error) {
	f.mu.Lock()
	f.events = append(f.events, "authenticate")
	identity, ok := f.identities[digest]
	revoked := f.revoked[digest]
	probe := f.probe
	f.mu.Unlock()
	if probe != nil {
		if err := probe(); err != nil {
			return TaskSessionIdentity{}, err
		}
	}
	if !ok || revoked {
		return TaskSessionIdentity{}, errors.New("credential is not active")
	}
	return identity, nil
}

func (f *taskAuthenticatorFixture) setProbe(probe func() error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.probe = probe
}

func (f *taskAuthenticatorFixture) setRevoked(digest SessionCredentialDigest, revoked bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked[digest] = revoked
}

func (f *taskAuthenticatorFixture) setIdentity(digest SessionCredentialDigest, identity TaskSessionIdentity) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.identities[digest] = identity
}

func (f *taskAuthenticatorFixture) validateCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, event := range f.events {
		if event == "validate" {
			count++
		}
	}
	return count
}

func (f *taskAuthenticatorFixture) eventsSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.events...)
}

func validTaskSessionIdentity(t *testing.T, principalRef string) TaskSessionIdentity {
	t.Helper()
	principal, err := NewCallerPrincipal(
		principalRef,
		"scope/task",
		"task_http_session_v1",
		"auth/task-session",
		time.Now().UTC().Add(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	return TaskSessionIdentity{Principal: principal, Attribution: validCallerAttribution(t)}
}

func serveTaskRequest(server *HTTPServer, method, body, sessionID, credential string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "http://127.0.0.1:3000/mcp", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+credential)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		request.Header.Set(httpMCPHeaderSessionID, sessionID)
		request.Header.Set("MCP-Protocol-Version", "2025-11-25")
	}
	response := httptest.NewRecorder()
	server.handleMCP(response, request)
	return response
}

func assertTaskRequestRejectedWithoutSideEffects(
	t *testing.T,
	server *HTTPServer,
	session *httpMCPSession,
	sessionID string,
	credential string,
	wantStatus int,
	wantLastAccess time.Time,
	toolCalls *atomic.Int32,
	wantToolCalls int32,
) {
	t.Helper()
	response := serveTaskRequest(server, http.MethodPost, `{"jsonrpc":"2.0","id":90,"method":"tools/call","params":{"name":"demo_tool","arguments":{}}}`, sessionID, credential)
	if response.Code != wantStatus || sessionLastAccess(session) != wantLastAccess || activeHTTPSessionCount(server) != 1 {
		t.Fatalf("rejected request = %d lastAccess=%v sessions=%d", response.Code, sessionLastAccess(session), activeHTTPSessionCount(server))
	}
	if got := toolCalls.Load(); got != wantToolCalls {
		t.Fatalf("tool calls = %d, want %d", got, wantToolCalls)
	}
	assertOutputOmitsSecrets(t, response.Body.String())
}

func sessionLastAccess(session *httpMCPSession) time.Time {
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.lastAccess
}

func activeHTTPSessionCount(server *HTTPServer) int {
	server.sessionsMu.RLock()
	defer server.sessionsMu.RUnlock()
	return len(server.sessions)
}
