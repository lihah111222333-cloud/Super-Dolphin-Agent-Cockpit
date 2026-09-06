package childissuer

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/taskprincipal"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

type resolverStub struct {
	mu        sync.Mutex
	parent    taskprincipal.TaskPrincipal
	responses []taskprincipal.TaskPrincipal
	err       error
	calls     int
}

func (s *resolverStub) RequireCurrent(context.Context) (taskprincipal.TaskPrincipal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.err != nil {
		return taskprincipal.TaskPrincipal{}, s.err
	}
	if index := s.calls - 1; index >= 0 && index < len(s.responses) {
		return s.responses[index], nil
	}
	return s.parent, nil
}

type delegateStub struct {
	mu                   sync.Mutex
	identity             common.TaskSessionIdentity
	err                  error
	calls                int
	observed             IssueRequest
	observedContextValue any
}

func (s *delegateStub) IssueChildIdentity(ctx context.Context, request IssueRequest) (common.TaskSessionIdentity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.observed = request
	s.observedContextValue = ctx.Value(contextProbeKey{})
	if s.err != nil {
		return common.TaskSessionIdentity{}, s.err
	}
	return s.identity, nil
}

type fixture struct {
	parent   taskprincipal.TaskPrincipal
	request  Request
	issuer   *CandidateIssuer
	resolver *resolverStub
	delegate *delegateStub
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	now := time.Now().UTC()
	parentFence := common.ExecutionFence{
		AgentID: "parent-task", ReservationID: "reservation/parent", ReservationGeneration: 3,
		IncarnationID: "incarnation/parent", IncarnationGeneration: 4, BindingVersion: 2,
		BindingDigest: digest('1'), SourceFenceRef: "fence/parent",
	}
	parentIssuer := common.IssuerBinding{
		Kind: common.CallerAttributionIssuerProviderBridge, BindingRef: "issuer/parent",
		Generation: 5, Digest: digest('2'),
	}
	parentAttribution, err := common.NewCallerAttribution(parentFence.AgentID, parentFence, parentIssuer)
	if err != nil {
		t.Fatal(err)
	}
	parentPrincipal, err := common.NewCallerPrincipal(
		"principal/parent", "scope/run", "task_session_v1", "auth/parent", now.Add(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	parent := taskprincipal.TaskPrincipal{
		Principal: parentPrincipal, Attribution: parentAttribution,
		Binding: currentBinding(parentFence, "managed_agent", "external/parent", "receipt/parent"),
	}

	childFence := common.ExecutionFence{
		AgentID: "child-task", ReservationID: "reservation/child", ReservationGeneration: 1,
		IncarnationID: "incarnation/child", IncarnationGeneration: 1, BindingVersion: 1,
		BindingDigest: digest('3'), SourceFenceRef: "fence/child",
	}
	childReservation := identity.Reservation{
		ReservationID: childFence.ReservationID,
		CommandID:     stringPointer("command/child"), AgentID: childFence.AgentID,
		IdentityKind: "managed_agent", SourceKind: "first_launch",
		CallerPrincipalRef: parent.Principal.PrincipalRef, ScopeRef: parent.Principal.ScopeRef,
		ParentAgentID: parent.Attribution.TaskID, IdempotencyNamespace: "child/issue",
		IdempotencyKeyDigest: digest('4'), RequestDigest: digest('5'),
		SourceFenceRef: childFence.SourceFenceRef, TargetFenceRef: "target/child",
		ExpectedBindingVersion: 0, ReservationGeneration: childFence.ReservationGeneration,
		State: "activated", SchemaVersion: identitySchemaVersion,
		CreatedAt: "2035-01-02T03:04:05Z", UpdatedAt: "2035-01-02T03:04:05Z",
	}
	childIncarnation := identity.Incarnation{
		IncarnationID: childFence.IncarnationID, ReservationID: childFence.ReservationID,
		AgentID: childFence.AgentID, IdentityKind: childReservation.IdentityKind,
		ExternalIdentityRef: "external/child", ExternalEpoch: "epoch/child",
		SourceFenceRef: childFence.SourceFenceRef, IdentityDigest: digest('6'),
		IncarnationGeneration: childFence.IncarnationGeneration, SchemaVersion: identitySchemaVersion,
		CreatedAt: "2035-01-02T03:04:05Z",
	}
	childBinding := currentBinding(childFence, childReservation.IdentityKind, childIncarnation.ExternalIdentityRef, "receipt/child")
	childBinding.ExternalIdentityRef = childIncarnation.ExternalIdentityRef
	issuerBinding := common.IssuerBinding{
		Kind: common.CallerAttributionIssuerManagedRuntime, BindingRef: "issuer/child",
		Generation: 7, Digest: digest('b'),
	}
	childPrincipal, err := common.NewCallerPrincipal(
		"principal/child", parent.Principal.ScopeRef, "managed_task_v1", "auth/child", now.Add(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	childAttribution, err := common.NewCallerAttribution(childFence.AgentID, childFence, issuerBinding)
	if err != nil {
		t.Fatal(err)
	}
	childIdentity := common.TaskSessionIdentity{Principal: childPrincipal, Attribution: childAttribution}
	resolver := &resolverStub{parent: parent}
	delegate := &delegateStub{identity: childIdentity}
	issuer, err := NewCandidateIssuer(resolver, delegate)
	if err != nil {
		t.Fatal(err)
	}
	return fixture{
		parent:  parent,
		request: Request{ChildReservation: childReservation, ChildIncarnation: childIncarnation, ChildBinding: childBinding, Issuer: issuerBinding, IssuerNonceDigest: digest('a')},
		issuer:  issuer, resolver: resolver, delegate: delegate,
	}
}

func TestCandidateIssuerBindsVerifiedParentAndChildFences(t *testing.T) {
	fixture := newFixture(t)
	candidate, err := fixture.issuer.Issue(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	assertCandidateIdentity(t, candidate, fixture)
	assertCandidateCallBindings(t, fixture)
	if fixture.delegate.observedContextValue != nil {
		t.Fatal("delegate inherited caller context values")
	}
	if err := candidate.Validate(); err != nil {
		t.Fatalf("Candidate.Validate() error = %v", err)
	}
}

type contextProbeKey struct{}

func TestCandidateIssuerHidesParentTransportContextFromDelegate(t *testing.T) {
	fixture := newFixture(t)
	ctx := context.WithValue(context.Background(), contextProbeKey{}, "caller-metadata")
	if _, err := fixture.issuer.Issue(ctx, fixture.request); err != nil {
		t.Fatal(err)
	}
	if fixture.delegate.observedContextValue != nil {
		t.Fatal("delegate observed parent transport context value")
	}
}

// assertCandidateIdentity 检查候选身份、双端 fence 与 issuer tuple。
func assertCandidateIdentity(t *testing.T, candidate Candidate, fixture fixture) {
	t.Helper()
	if got := candidate.SessionIdentity(); !got.Equal(fixture.delegate.identity) {
		t.Fatalf("SessionIdentity() = %#v, want delegate identity", got)
	}
	if candidate.ParentFence != fixture.parent.Attribution.Source {
		t.Fatalf("parent fence = %#v, want resolver fence", candidate.ParentFence)
	}
	if candidate.ChildFence != executionFence(fixture.request.ChildBinding) {
		t.Fatalf("child fence = %#v, want binding fence", candidate.ChildFence)
	}
	if candidate.Issuer != fixture.request.Issuer {
		t.Fatalf("candidate issuer = %#v, want request issuer", candidate.Issuer)
	}
	if candidate.IssuerNonceDigest != fixture.request.IssuerNonceDigest {
		t.Fatalf("candidate nonce digest = %q, want request digest", candidate.IssuerNonceDigest)
	}
}

// assertCandidateCallBindings 检查 resolver/delegate 各只被调用一次且收到 verified parent。
func assertCandidateCallBindings(t *testing.T, fixture fixture) {
	t.Helper()
	if fixture.delegate.calls != 1 {
		t.Fatalf("delegate calls = %d, want one", fixture.delegate.calls)
	}
	if fixture.resolver.calls != 2 {
		t.Fatalf("resolver calls = %d, want pre/post parent verification", fixture.resolver.calls)
	}
	if !reflect.DeepEqual(fixture.delegate.observed.Parent, fixture.parent) {
		t.Fatal("delegate did not receive resolver-verified parent")
	}
}

func TestCandidateIssuerRejectsParentRolloverAfterDelegate(t *testing.T) {
	fixture := newFixture(t)
	rolled := fixture.parent
	rolled.Binding.BindingVersion++
	rolled.Binding.BindingDigest = digest('9')
	attribution, err := common.NewCallerAttribution(
		rolled.Binding.AgentID, executionFence(rolled.Binding), rolled.Attribution.Issuer,
	)
	if err != nil {
		t.Fatal(err)
	}
	rolled.Attribution = attribution
	fixture.resolver.responses = []taskprincipal.TaskPrincipal{fixture.parent, rolled}
	if _, err := fixture.issuer.Issue(context.Background(), fixture.request); !errors.Is(err, ErrParentChildConflict) {
		t.Fatalf("parent rollover Issue() error = %v, want ErrParentChildConflict", err)
	}
	if fixture.delegate.calls != 1 {
		t.Fatalf("delegate calls after parent rollover = %d, want one", fixture.delegate.calls)
	}
}

func TestCandidateIssuerStableChildKeyRejectsParentOrIssuerRollover(t *testing.T) {
	fixture := newFixture(t)
	if _, err := fixture.issuer.Issue(context.Background(), fixture.request); err != nil {
		t.Fatal(err)
	}
	rolled := fixture.parent
	rolled.Binding.BindingVersion++
	rolled.Binding.BindingDigest = digest('9')
	attribution, err := common.NewCallerAttribution(
		rolled.Binding.AgentID, executionFence(rolled.Binding), rolled.Attribution.Issuer,
	)
	if err != nil {
		t.Fatal(err)
	}
	rolled.Attribution = attribution
	fixture.resolver.parent = rolled
	fixture.request.IssuerNonceDigest = digest('c')
	if _, err := fixture.issuer.Issue(context.Background(), fixture.request); !errors.Is(err, ErrChildAlreadyIssued) {
		t.Fatalf("parent rollover retry = %v, want ErrChildAlreadyIssued", err)
	}

	fixture = newFixture(t)
	if _, err := fixture.issuer.Issue(context.Background(), fixture.request); err != nil {
		t.Fatal(err)
	}
	fixture.request.Issuer.Generation++
	fixture.request.Issuer.Digest = digest('d')
	fixture.request.IssuerNonceDigest = digest('e')
	if _, err := fixture.issuer.Issue(context.Background(), fixture.request); !errors.Is(err, ErrChildAlreadyIssued) {
		t.Fatalf("issuer rollover retry = %v, want ErrChildAlreadyIssued", err)
	}
}

func TestDecodeRequestIsStrictAndCanonical(t *testing.T) {
	fixture := newFixture(t)
	payload, err := json.Marshal(fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeRequest(payload)
	if err != nil {
		t.Fatalf("DecodeRequest(valid) error = %v", err)
	}
	if !reflect.DeepEqual(decoded, fixture.request) {
		t.Fatalf("DecodeRequest(valid) = %#v, want %#v", decoded, fixture.request)
	}
	invalid := []struct {
		name string
		json string
	}{
		{name: "duplicate", json: `{"issuer_nonce_digest":"` + fixture.request.IssuerNonceDigest + `","issuer_nonce_digest":"` + fixture.request.IssuerNonceDigest + `"}`},
		{name: "unknown", json: `{"unknown":1}`},
		{name: "null", json: `{"issuer_nonce_digest":null}`},
		{name: "trailing", json: string(payload) + ` {}`},
		{name: "array", json: `[]`},
		{name: "whitespace", json: " " + string(payload)},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecodeRequest([]byte(tc.json)); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("DecodeRequest(%s) error = %v, want ErrInvalidRequest", tc.name, err)
			}
		})
	}
}

func TestCandidateIssuerRejectsSameNonceReplayAndConcurrentSecondWinner(t *testing.T) {
	fixture := newFixture(t)
	if _, err := fixture.issuer.Issue(context.Background(), fixture.request); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.issuer.Issue(context.Background(), fixture.request); !errors.Is(err, ErrIssuerReplay) {
		t.Fatalf("second Issue() error = %v, want ErrIssuerReplay", err)
	}

	fixture = newFixture(t)
	const attempts = 24
	start := make(chan struct{})
	results := make(chan error, attempts)
	var goroutines sync.WaitGroup
	for range attempts {
		goroutines.Go(func() {
			<-start
			_, err := fixture.issuer.Issue(context.Background(), fixture.request)
			results <- err
		})
	}
	close(start)
	winners := 0
	for range attempts {
		if err := <-results; err == nil {
			winners++
		} else if !errors.Is(err, ErrIssuerReplay) {
			t.Fatalf("concurrent Issue() error = %v, want replay for losers", err)
		}
	}
	goroutines.Wait()
	if winners != 1 || fixture.delegate.calls != 1 {
		t.Fatalf("concurrent winners=%d delegate calls=%d, want one each", winners, fixture.delegate.calls)
	}
}

func TestCandidateIssuerStableChildKeyRejectsNewNonce(t *testing.T) {
	fixture := newFixture(t)
	if _, err := fixture.issuer.Issue(context.Background(), fixture.request); err != nil {
		t.Fatal(err)
	}
	fixture.request.IssuerNonceDigest = digest('c')
	if _, err := fixture.issuer.Issue(context.Background(), fixture.request); !errors.Is(err, ErrChildAlreadyIssued) {
		t.Fatalf("new nonce for existing child = %v, want ErrChildAlreadyIssued", err)
	}
	if fixture.delegate.calls != 1 {
		t.Fatalf("delegate calls after child replay = %d, want one", fixture.delegate.calls)
	}
}

func TestCandidateIssuerScopesNonceByChildIdentity(t *testing.T) {
	fixture := newFixture(t)
	if _, err := fixture.issuer.Issue(context.Background(), fixture.request); err != nil {
		t.Fatal(err)
	}
	retargetChild(t, &fixture, "child-two")
	if _, err := fixture.issuer.Issue(context.Background(), fixture.request); err != nil {
		t.Fatalf("same nonce for distinct child = %v, want success", err)
	}
	if fixture.delegate.calls != 2 {
		t.Fatalf("delegate calls for distinct children = %d, want two", fixture.delegate.calls)
	}
}

func TestCandidateIssuerRejectsOperatorParentBeforeDelegate(t *testing.T) {
	fixture := newFixture(t)
	fixture.parent.Principal.Profile = "standalone_http_local_v1"
	fixture.resolver.parent = fixture.parent
	if _, err := fixture.issuer.Issue(context.Background(), fixture.request); !errors.Is(err, ErrOperatorPrincipal) {
		t.Fatalf("operator parent Issue() error = %v, want ErrOperatorPrincipal", err)
	}
	if fixture.delegate.calls != 0 {
		t.Fatalf("operator parent reached delegate %d times", fixture.delegate.calls)
	}
}

func TestCandidateIssuerRejectsForeignParentChildScopeAndObservation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*fixture)
		want   error
	}{
		{name: "foreign parent", mutate: func(f *fixture) { f.request.ChildReservation.ParentAgentID = "parent/other" }, want: ErrParentChildConflict},
		{name: "same agent", mutate: makeSameAgentRequest, want: ErrParentChildConflict},
		{name: "caller mismatch", mutate: func(f *fixture) { f.request.ChildReservation.CallerPrincipalRef = "principal/other" }, want: ErrParentChildConflict},
		{name: "scope mismatch", mutate: func(f *fixture) { f.request.ChildReservation.ScopeRef = "scope/other" }, want: ErrParentChildConflict},
		{name: "shared issuer binding", mutate: func(f *fixture) { f.request.Issuer = f.parent.Attribution.Issuer }, want: ErrParentChildConflict},
		{name: "observation source", mutate: func(f *fixture) {
			f.request.ChildReservation.SourceKind = "a2a_observation"
			f.request.ChildReservation.CommandID = nil
		}, want: ErrInvalidRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newFixture(t)
			tc.mutate(&fixture)
			if _, err := fixture.issuer.Issue(context.Background(), fixture.request); !errors.Is(err, tc.want) {
				t.Fatalf("Issue() error = %v, want %v", err, tc.want)
			}
			if fixture.delegate.calls != 0 {
				t.Fatalf("invalid request reached delegate %d times", fixture.delegate.calls)
			}
		})
	}
}

func makeSameAgentRequest(f *fixture) {
	parent := f.parent.Binding
	f.request.ChildReservation.AgentID = parent.AgentID
	f.request.ChildReservation.ReservationID = parent.ReservationID
	f.request.ChildReservation.SourceFenceRef = parent.SourceFenceRef
	f.request.ChildReservation.ReservationGeneration = parent.ReservationGeneration
	f.request.ChildIncarnation.AgentID = parent.AgentID
	f.request.ChildIncarnation.ReservationID = parent.ReservationID
	f.request.ChildIncarnation.IncarnationID = parent.IncarnationID
	f.request.ChildIncarnation.SourceFenceRef = parent.SourceFenceRef
	f.request.ChildIncarnation.IncarnationGeneration = parent.IncarnationGeneration
	f.request.ChildIncarnation.ExternalIdentityRef = parent.ExternalIdentityRef
	f.request.ChildBinding.AgentID = parent.AgentID
	f.request.ChildBinding.ReservationID = parent.ReservationID
	f.request.ChildBinding.IncarnationID = parent.IncarnationID
	f.request.ChildBinding.SourceFenceRef = parent.SourceFenceRef
	f.request.ChildBinding.ReservationGeneration = parent.ReservationGeneration
	f.request.ChildBinding.IncarnationGeneration = parent.IncarnationGeneration
	f.request.ChildBinding.BindingVersion = parent.BindingVersion
	f.request.ChildBinding.BindingDigest = parent.BindingDigest
	f.request.ChildBinding.ExternalIdentityRef = parent.ExternalIdentityRef
}

func retargetChild(t *testing.T, fixture *fixture, agentID string) {
	t.Helper()
	request := &fixture.request
	request.ChildReservation.AgentID = agentID
	request.ChildReservation.ReservationID = "reservation/" + agentID
	request.ChildReservation.IdempotencyKeyDigest = digest('d')
	request.ChildReservation.RequestDigest = digest('e')
	request.ChildReservation.SourceFenceRef = "fence/" + agentID
	request.ChildReservation.TargetFenceRef = "target/" + agentID
	commandID := "command/" + agentID
	request.ChildReservation.CommandID = &commandID
	request.ChildIncarnation.AgentID = agentID
	request.ChildIncarnation.ReservationID = request.ChildReservation.ReservationID
	request.ChildIncarnation.IncarnationID = "incarnation/" + agentID
	request.ChildIncarnation.ExternalIdentityRef = "external/" + agentID
	request.ChildIncarnation.ExternalEpoch = "epoch/" + agentID
	request.ChildIncarnation.SourceFenceRef = request.ChildReservation.SourceFenceRef
	request.ChildIncarnation.IdentityDigest = digest('f')
	request.ChildBinding.AgentID = agentID
	request.ChildBinding.ReservationID = request.ChildReservation.ReservationID
	request.ChildBinding.IncarnationID = request.ChildIncarnation.IncarnationID
	request.ChildBinding.ExternalIdentityRef = request.ChildIncarnation.ExternalIdentityRef
	request.ChildBinding.SourceFenceRef = request.ChildReservation.SourceFenceRef
	request.ChildBinding.BindingDigest = digest('0')
	request.ChildBinding.ActiveProcessReceiptRef = "receipt/" + agentID
	request.IssuerNonceDigest = digest('a')
	childPrincipal, err := common.NewCallerPrincipal(
		"principal/"+agentID, fixture.parent.Principal.ScopeRef, "managed_task_v1", "auth/"+agentID, time.Now().UTC().Add(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	childAttribution, err := common.NewCallerAttribution(agentID, executionFence(request.ChildBinding), request.Issuer)
	if err != nil {
		t.Fatal(err)
	}
	fixture.delegate.identity = common.TaskSessionIdentity{Principal: childPrincipal, Attribution: childAttribution}
}

func TestCandidateIssuerRejectsStaleChildBindingAndIssuerTuple(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Request)
	}{
		{name: "revoked", mutate: func(r *Request) { r.ChildBinding.State = "revoked" }},
		{name: "future schema", mutate: func(r *Request) { r.ChildBinding.SchemaVersion = 2 }},
		{name: "binding digest", mutate: func(r *Request) { r.ChildBinding.BindingDigest = strings.Repeat("z", 64) }},
		{name: "incarnation fence", mutate: func(r *Request) { r.ChildIncarnation.SourceFenceRef = "fence/other" }},
		{name: "external epoch", mutate: func(r *Request) { r.ChildIncarnation.ExternalEpoch = "" }},
		{name: "native child without adoption proof", mutate: func(r *Request) {
			r.ChildReservation.IdentityKind = "native_subagent"
			r.ChildIncarnation.IdentityKind = "native_subagent"
			r.ChildBinding.IdentityKind = "native_subagent"
		}},
		{name: "issuer kind", mutate: func(r *Request) { r.Issuer.Kind = common.CallerAttributionIssuerA2AGateway }},
		{name: "issuer generation", mutate: func(r *Request) { r.Issuer.Generation = 0 }},
		{name: "issuer digest", mutate: func(r *Request) { r.Issuer.Digest = strings.ToUpper(r.Issuer.Digest) }},
		{name: "nonce uppercase", mutate: func(r *Request) { r.IssuerNonceDigest = strings.ToUpper(r.IssuerNonceDigest) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newFixture(t)
			tc.mutate(&fixture.request)
			if _, err := fixture.issuer.Issue(context.Background(), fixture.request); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("Issue() error = %v, want ErrInvalidRequest", err)
			}
			if fixture.delegate.calls != 0 {
				t.Fatalf("stale request reached delegate %d times", fixture.delegate.calls)
			}
		})
	}
}

func TestCandidateIssuerRejectsForeignOrReusedChildIdentity(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*fixture)
		want   error
	}{
		{name: "same principal", mutate: func(f *fixture) {
			f.delegate.identity.Principal.PrincipalRef = f.parent.Principal.PrincipalRef
		}, want: ErrParentChildConflict},
		{name: "shared auth context", mutate: func(f *fixture) {
			f.delegate.identity.Principal.AuthContext = f.parent.Principal.AuthContext
		}, want: ErrParentChildConflict},
		{name: "operator child", mutate: func(f *fixture) {
			f.delegate.identity.Principal.Profile = "operator"
		}, want: ErrParentChildConflict},
		{name: "foreign scope", mutate: func(f *fixture) {
			f.delegate.identity.Principal.ScopeRef = "scope/other"
		}, want: ErrParentChildConflict},
		{name: "foreign source", mutate: func(f *fixture) {
			f.delegate.identity.Attribution.TaskID = "child/other"
		}, want: ErrChildIdentityInvalid},
		{name: "foreign issuer", mutate: func(f *fixture) {
			f.delegate.identity.Attribution.Issuer.BindingRef = "issuer/other"
		}, want: ErrChildIdentityInvalid},
		{name: "foreign audience", mutate: func(f *fixture) {
			f.delegate.identity.Attribution.Audience = "mcp-orch/legacy"
		}, want: ErrChildIdentityInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newFixture(t)
			tc.mutate(&fixture)
			if _, err := fixture.issuer.Issue(context.Background(), fixture.request); !errors.Is(err, tc.want) {
				t.Fatalf("Issue() error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestCandidateIssuerCollapsesDelegateErrorsAndRedactsCandidate(t *testing.T) {
	fixture := newFixture(t)
	fixture.delegate.err = errors.New("provider secret must never escape")
	assertDelegateFailureConsumesNonce(t, fixture)

	fixture = newFixture(t)
	assertCandidateRedaction(t, fixture)
}

// assertDelegateFailureConsumesNonce 锁定失败 attempt 不可用同 nonce 重发。
func assertDelegateFailureConsumesNonce(t *testing.T, fixture fixture) {
	t.Helper()
	if _, err := fixture.issuer.Issue(context.Background(), fixture.request); !errors.Is(err, ErrIssuerUnavailable) || strings.Contains(err.Error(), "provider secret") {
		t.Fatalf("delegate error = %v, want redacted ErrIssuerUnavailable", err)
	}
	if _, err := fixture.issuer.Issue(context.Background(), fixture.request); !errors.Is(err, ErrIssuerReplay) {
		t.Fatalf("retry after unknown delegate attempt = %v, want ErrIssuerReplay", err)
	}
	if fixture.delegate.calls != 1 {
		t.Fatalf("delegate calls after failed retry = %d, want one", fixture.delegate.calls)
	}
}

// assertCandidateRedaction 检查候选和 delegate request 不暴露 auth context。
func assertCandidateRedaction(t *testing.T, fixture fixture) {
	t.Helper()
	candidate, err := fixture.issuer.Issue(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "auth/child") || strings.Contains(candidate.String(), "auth/child") {
		t.Fatalf("candidate leaked auth context: json=%s string=%s", encoded, candidate)
	}
	assertSafeJSONFields(t, string(encoded), []string{"parent_principal_ref", "parent_scope_ref", "parent_profile", "child_identity_kind", "child_source_kind", "child_external_epoch"})
	if !strings.Contains(string(encoded), digestText("auth/parent")) {
		t.Fatalf("candidate omitted parent auth context digest: %s", encoded)
	}
	requestJSON, err := json.Marshal(fixture.delegate.observed)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(requestJSON), "auth/parent") || strings.Contains(fixture.delegate.observed.String(), "auth/parent") {
		t.Fatalf("delegate request leaked parent auth context: json=%s string=%s", requestJSON, fixture.delegate.observed)
	}
	assertSafeJSONFields(t, string(requestJSON), []string{"parent_principal_ref", "parent_scope_ref", "parent_profile", "child_reservation", "child_incarnation", "child_binding"})
	if !strings.Contains(string(requestJSON), digestText("auth/parent")) {
		t.Fatalf("delegate request omitted parent auth context digest: %s", requestJSON)
	}
}

// assertSafeJSONFields 检查 JSON 保留非秘密关联字段且不依赖隐式缺省。
func assertSafeJSONFields(t *testing.T, payload string, fields []string) {
	t.Helper()
	for _, field := range fields {
		if !strings.Contains(payload, `"`+field+`"`) {
			t.Fatalf("JSON omitted required safe field %q: %s", field, payload)
		}
	}
}

func TestCandidateIssuerRejectsNilDependenciesAndContext(t *testing.T) {
	if issuer, err := NewCandidateIssuer(nil, &delegateStub{}); issuer != nil || !errors.Is(err, ErrIssuerUnavailable) {
		t.Fatalf("nil parent constructor = (%#v,%v), want ErrIssuerUnavailable", issuer, err)
	}
	var typedNil *resolverStub
	if issuer, err := NewCandidateIssuer(typedNil, &delegateStub{}); issuer != nil || !errors.Is(err, ErrIssuerUnavailable) {
		t.Fatalf("typed nil parent constructor = (%#v,%v), want ErrIssuerUnavailable", issuer, err)
	}
	fixture := newFixture(t)
	if _, err := fixture.issuer.Issue(nil, fixture.request); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("nil context Issue() error = %v, want ErrInvalidRequest", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fixture.issuer.Issue(ctx, fixture.request); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Issue() error = %v, want context.Canceled", err)
	}
}

func currentBinding(fence common.ExecutionFence, kind, external, receipt string) identity.CurrentBinding {
	return identity.CurrentBinding{
		AgentID: fence.AgentID, BindingVersion: fence.BindingVersion, ReservationID: fence.ReservationID,
		IncarnationID: fence.IncarnationID, IdentityKind: kind, ReservationGeneration: fence.ReservationGeneration,
		IncarnationGeneration: fence.IncarnationGeneration, ExternalIdentityRef: external,
		SourceFenceRef: fence.SourceFenceRef, ActiveProcessReceiptRef: receipt, BindingDigest: fence.BindingDigest,
		State: "active", SchemaVersion: identitySchemaVersion, UpdatedAt: "2035-01-02T03:04:05Z",
	}
}

func stringPointer(value string) *string { return &value }

func digest(value byte) string { return strings.Repeat(string(value), 64) }
