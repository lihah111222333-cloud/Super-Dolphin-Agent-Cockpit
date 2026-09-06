package tasktransport

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func TestAuthorizationCandidateAcceptsBoundSendWithZeroEffects(t *testing.T) {
	fixture := newAuthorizationFixture(t, ActionSend)
	decision, err := Evaluate(fixture.ctx, fixture.request, fixture.now)
	if err != nil || decision.Status != "authorized" || decision.Authoritative || !decision.Effects.IsZero() {
		t.Fatalf("Evaluate(valid send)=(%#v,%v), want authorized zero-effect decision", decision, err)
	}
	if err := decision.Validate(); err != nil {
		t.Fatalf("decision validation: %v", err)
	}
}

func TestAuthorizationCandidateDecisionCannotBecomeAuthoritative(t *testing.T) {
	fixture := newAuthorizationFixture(t, ActionSend)
	decision, err := Evaluate(fixture.ctx, fixture.request, fixture.now)
	if err != nil {
		t.Fatal(err)
	}
	decision.Authoritative = true
	if !errors.Is(decision.Validate(), ErrAuthorizationInvalid) {
		t.Fatalf("authoritative decision validation error = %v, want ErrAuthorizationInvalid", decision.Validate())
	}
	decision.Authoritative = false
	decision.DecisionKind = "authorization"
	if !errors.Is(decision.Validate(), ErrAuthorizationInvalid) {
		t.Fatalf("non-structural decision validation error = %v, want ErrAuthorizationInvalid", decision.Validate())
	}
}

func TestBuildPreflightRequestProjectsOnlyServerSnapshots(t *testing.T) {
	fixture := newAuthorizationFixture(t, ActionSend)
	principal, err := common.RequireCallerPrincipal(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	attribution, err := common.RequireCallerAttribution(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	spec := PreflightSpec{
		SchemaVersion: fixture.request.SchemaVersion, RequestID: fixture.request.RequestID,
		Action: fixture.request.Action, Grant: fixture.request.Grant, GrantRef: fixture.request.GrantRef,
		Parent: fixture.request.Parent, ContentRef: fixture.request.ContentRef,
		ResourceReservationRef: fixture.request.ResourceReservationRef,
		IntentDigest:           fixture.request.IntentDigest, RequestedAt: fixture.request.RequestedAt,
	}
	built, err := BuildPreflightRequest(principal, attribution, spec, fixture.now)
	if err != nil {
		t.Fatalf("BuildPreflightRequest() error = %v", err)
	}
	if !sameCaller(built.Caller, fixture.request.Caller) || built.Caller.TaskID != attribution.TaskID {
		t.Fatalf("built caller = %#v, want exact server snapshot projection", built.Caller)
	}
}

func TestBuildPreflightRequestRejectsOperatorMissingAndExpiredSnapshots(t *testing.T) {
	fixture := newAuthorizationFixture(t, ActionSend)
	principal, err := common.RequireCallerPrincipal(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	attribution, err := common.RequireCallerAttribution(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	spec := PreflightSpec{
		SchemaVersion: fixture.request.SchemaVersion, RequestID: fixture.request.RequestID,
		Action: fixture.request.Action, Grant: fixture.request.Grant, GrantRef: fixture.request.GrantRef,
		Parent: fixture.request.Parent, ContentRef: fixture.request.ContentRef,
		ResourceReservationRef: fixture.request.ResourceReservationRef,
		IntentDigest:           fixture.request.IntentDigest, RequestedAt: fixture.request.RequestedAt,
	}
	operator, err := common.NewCallerPrincipal(principal.PrincipalRef, principal.ScopeRef, "operator", principal.AuthContext, fixture.now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildAuthorizationRequest(operator, attribution, spec, fixture.now); !errors.Is(err, ErrOperatorPrincipal) {
		t.Fatalf("operator build error = %v, want ErrOperatorPrincipal", err)
	}
	expired, err := common.NewCallerPrincipal(principal.PrincipalRef, principal.ScopeRef, principal.Profile, principal.AuthContext, fixture.now.Add(-time.Second))
	if err == nil {
		t.Fatal("expired principal constructor unexpectedly succeeded")
	}
	_ = expired
	if _, err := BuildPreflightRequest(common.CallerPrincipal{}, attribution, spec, fixture.now); !errors.Is(err, ErrCallerUnavailable) {
		t.Fatalf("missing principal build error = %v, want ErrCallerUnavailable", err)
	}
	forgedAttribution := attribution
	forgedAttribution.TaskID = "task/foreign"
	if _, err := BuildPreflightRequest(principal, forgedAttribution, spec, fixture.now); !errors.Is(err, ErrCallerUnavailable) {
		t.Fatalf("foreign attribution build error = %v, want ErrCallerUnavailable", err)
	}
}

func TestAuthorizationCandidateRejectsActionReuseBeforeEffects(t *testing.T) {
	for _, action := range []Action{ActionDiscover, ActionRead, ActionSend} {
		t.Run(string(action), func(t *testing.T) {
			fixture := newAuthorizationFixture(t, ActionSend)
			fixture.request.Action = action
			if action == ActionSend {
				fixture.request.Grant.Action = ActionDiscover
			}
			if action == ActionRead {
				fixture.request.Cursor = validCursor(fixture.request)
				fixture.request.ContentRef, fixture.request.ResourceReservationRef = "", ""
			}
			if action == ActionDiscover {
				fixture.request.ContentRef, fixture.request.ResourceReservationRef = "", ""
			}
			decision, err := Evaluate(fixture.ctx, fixture.request, fixture.now)
			if !errors.Is(err, ErrGrantActionMismatch) || !decision.Effects.IsZero() {
				t.Fatalf("action reuse decision=%#v err=%v, want action mismatch and zero effects", decision, err)
			}
		})
	}
}

func TestAuthorizationCandidateRejectsForgedGrantDigestBeforeOwner(t *testing.T) {
	fixture := newAuthorizationFixture(t, ActionSend)
	fixture.request.GrantRef.GrantDigest = digest('0')
	decision, err := Evaluate(fixture.ctx, fixture.request, fixture.now)
	if !errors.Is(err, ErrGrantActionMismatch) || !decision.Effects.IsZero() {
		t.Fatalf("forged grant digest decision=%#v err=%v, want mismatch and zero effects", decision, err)
	}

	fixture = newAuthorizationFixture(t, ActionSend)
	fixture.request.Grant.GrantDigest = strings.ToUpper(fixture.request.Grant.GrantDigest)
	decision, err = Evaluate(fixture.ctx, fixture.request, fixture.now)
	if !errors.Is(err, ErrAuthorizationInvalid) || !decision.Effects.IsZero() {
		t.Fatalf("non-canonical grant digest decision=%#v err=%v, want invalid and zero effects", decision, err)
	}
}

func TestAuthorizationCandidateRequiresExplicitParentBinding(t *testing.T) {
	fixture := newAuthorizationFixture(t, ActionSend)
	fixture.request.Parent.ChildTaskID = "other-child"
	decision, err := Evaluate(fixture.ctx, fixture.request, fixture.now)
	if !errors.Is(err, ErrParentMismatch) || !decision.Effects.IsZero() {
		t.Fatalf("parent mismatch decision=%#v err=%v, want deny with zero effects", decision, err)
	}

	fixture = newAuthorizationFixture(t, ActionSend)
	fixture.request.Grant.Parent.ParentEvidenceRef = "other-proof"
	decision, err = Evaluate(fixture.ctx, fixture.request, fixture.now)
	if !errors.Is(err, ErrParentMismatch) || !decision.Effects.IsZero() {
		t.Fatalf("grant parent mismatch decision=%#v err=%v, want deny with zero effects", decision, err)
	}
}

func TestAuthorizationCandidateRevocationEpochAndExpiryAreFailClosed(t *testing.T) {
	fixture := newAuthorizationFixture(t, ActionSend)
	revocation := RevokeGrantRequest{SchemaVersion: AuthorizationSchemaVersion, GrantRef: fixture.request.Grant.Ref(), Caller: fixture.request.Caller, Parent: fixture.request.Parent, ReasonRef: "operator-revoke", OperationDigest: digest('e'), RequestedAt: fixture.now}
	revoked, err := revocation.Apply(fixture.request.Grant, fixture.now)
	if err != nil || revoked.Status != "revoked" || revoked.RevocationEpoch != fixture.request.Grant.RevocationEpoch+1 {
		t.Fatalf("Apply(revoke)=%#v err=%v, want next revoked epoch", revoked, err)
	}
	fixture.request.Grant, fixture.request.GrantRef = revoked, revoked.Ref()
	decision, err := Evaluate(fixture.ctx, fixture.request, fixture.now)
	if !errors.Is(err, ErrGrantRevoked) || !decision.Effects.IsZero() {
		t.Fatalf("revoked decision=%#v err=%v, want revoked and zero effects", decision, err)
	}

	fixture = newAuthorizationFixture(t, ActionSend)
	expired := fixture.request.Grant
	expired.Status, expired.ExpiredAt = "expired", timePointer(fixture.now)
	fixture.request.Grant, fixture.request.GrantRef = expired, expired.Ref()
	decision, err = Evaluate(fixture.ctx, fixture.request, fixture.now)
	if !errors.Is(err, ErrGrantExpired) || !decision.Effects.IsZero() {
		t.Fatalf("expired decision=%#v err=%v, want expired and zero effects", decision, err)
	}
}

func TestAuthorizationCandidateRejectsStaleCursorBeforeRead(t *testing.T) {
	fixture := newAuthorizationFixture(t, ActionRead)
	fixture.request.Cursor = validCursor(fixture.request)
	fixture.request.Grant.Action = ActionRead
	fixture.request.ContentRef, fixture.request.ResourceReservationRef = "", ""
	for _, mutate := range []struct {
		name string
		fn   func(*CursorClaim)
	}{
		{name: "foreign session", fn: func(cursor *CursorClaim) { cursor.SessionID = "foreign-session" }},
		{name: "foreign source", fn: func(cursor *CursorClaim) { cursor.SourceID = "task/foreign" }},
		{name: "old incarnation", fn: func(cursor *CursorClaim) { cursor.SourceEpoch = "old-incarnation" }},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			caseFixture := fixture
			cursor := *fixture.request.Cursor
			mutate.fn(&cursor)
			caseFixture.request.Cursor = &cursor
			decision, err := Evaluate(caseFixture.ctx, caseFixture.request, caseFixture.now)
			if !errors.Is(err, ErrCursorMismatch) || !decision.Effects.IsZero() {
				t.Fatalf("foreign cursor decision=%#v err=%v, want cursor mismatch and zero effects", decision, err)
			}
		})
	}

	fixture = newAuthorizationFixture(t, ActionRead)
	fixture.request.Cursor = validCursor(fixture.request)
	fixture.request.Grant.Action = ActionRead
	fixture.request.ContentRef, fixture.request.ResourceReservationRef = "", ""
	fixture.request.Cursor.AfterSequence = 2
	fixture.request.Cursor.AfterCursor = canonicalCursor(fixture.request.Cursor.SourceID, fixture.request.Cursor.SourceEpoch, fixture.request.Cursor.Lane, 1)
	decision, err := Evaluate(fixture.ctx, fixture.request, fixture.now)
	if !errors.Is(err, ErrCursorMismatch) || !decision.Effects.IsZero() {
		t.Fatalf("stale cursor decision=%#v err=%v, want cursor mismatch and zero effects", decision, err)
	}
}

func TestAuthorizationCandidateRejectsMissingOrMismatchedCallerWithoutEffects(t *testing.T) {
	fixture := newAuthorizationFixture(t, ActionSend)
	decision, err := Evaluate(context.Background(), fixture.request, fixture.now)
	if !errors.Is(err, ErrUnauthorized) || !decision.Effects.IsZero() {
		t.Fatalf("missing caller decision=%#v err=%v, want unauthorized zero effects", decision, err)
	}

	other := newAuthorizationFixture(t, ActionSend)
	decision, err = Evaluate(other.ctx, fixture.request, fixture.now)
	if !errors.Is(err, ErrUnauthorized) || !decision.Effects.IsZero() {
		t.Fatalf("mismatched caller decision=%#v err=%v, want unauthorized zero effects", decision, err)
	}
}

func TestAuthorizationCandidateRejectsFutureRequestedAtWithoutEffects(t *testing.T) {
	fixture := newAuthorizationFixture(t, ActionSend)
	fixture.request.RequestedAt = fixture.now.Add(time.Second)
	decision, err := Evaluate(fixture.ctx, fixture.request, fixture.now)
	if !errors.Is(err, ErrUnauthorized) || !decision.Effects.IsZero() {
		t.Fatalf("future requested_at decision=%#v err=%v, want unauthorized zero effects", decision, err)
	}
}

func TestAuthorizationCandidateCallerContextRejectsOperator(t *testing.T) {
	fixture := newAuthorizationFixture(t, ActionSend)
	operator, err := common.NewCallerPrincipal("operator", "operator-scope", "operator", "operator-auth", fixture.now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := common.BindCallerPrincipal(context.Background(), operator)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CallerBindingFromContext(ctx, fixture.now); !errors.Is(err, ErrCallerUnavailable) {
		t.Fatalf("operator CallerBindingFromContext() error=%v, want unavailable", err)
	}
}

func TestAuthorizationCandidateRevokeRejectsStaleReference(t *testing.T) {
	fixture := newAuthorizationFixture(t, ActionSend)
	revocation := RevokeGrantRequest{SchemaVersion: AuthorizationSchemaVersion, GrantRef: fixture.request.Grant.Ref(), Caller: fixture.request.Caller, Parent: fixture.request.Parent, ReasonRef: "revoke", OperationDigest: digest('f'), RequestedAt: fixture.now}
	revocation.GrantRef.RevocationEpoch++
	if _, err := revocation.Apply(fixture.request.Grant, fixture.now); !errors.Is(err, ErrRevocationConflict) {
		t.Fatalf("stale revocation error=%v, want ErrRevocationConflict", err)
	}
}

func TestAuthorizationCandidateRevokeUsesEvaluationTimeForCallerAndGrant(t *testing.T) {
	fixture := newAuthorizationFixture(t, ActionSend)
	revocation := RevokeGrantRequest{
		SchemaVersion:   AuthorizationSchemaVersion,
		GrantRef:        fixture.request.Grant.Ref(),
		Caller:          fixture.request.Caller,
		Parent:          fixture.request.Parent,
		ReasonRef:       "revoke",
		OperationDigest: digest('f'),
		RequestedAt:     fixture.now,
	}

	// The request was valid when created, but its caller has expired by the
	// time Apply evaluates it. The stale RequestedAt must not authorize revoke.
	evaluationNow := fixture.now.Add(2 * time.Hour)
	grant := fixture.request.Grant
	grant.ExpiresAt = evaluationNow.Add(time.Hour)
	if _, err := revocation.Apply(grant, evaluationNow); !errors.Is(err, ErrAuthorizationInvalid) {
		t.Fatalf("expired caller at evaluation error=%v, want ErrAuthorizationInvalid", err)
	}

	// Conversely, a grant that expires after the request was created must be
	// revalidated at Apply time, with no revoked snapshot returned.
	fixture = newAuthorizationFixture(t, ActionSend)
	revocation = RevokeGrantRequest{
		SchemaVersion:   AuthorizationSchemaVersion,
		GrantRef:        fixture.request.Grant.Ref(),
		Caller:          fixture.request.Caller,
		Parent:          fixture.request.Parent,
		ReasonRef:       "revoke",
		OperationDigest: digest('f'),
		RequestedAt:     fixture.now,
	}
	grant = fixture.request.Grant
	grant.ExpiresAt = fixture.now.Add(time.Hour)
	evaluationNow = fixture.now.Add(2 * time.Hour)
	revocation.Caller.ExpiresAt = evaluationNow.Add(time.Hour)
	if _, err := revocation.Apply(grant, evaluationNow); !errors.Is(err, ErrGrantExpired) {
		t.Fatalf("expired grant at evaluation error=%v, want ErrGrantExpired", err)
	}

	// A grant that is not issued yet is also invalid at the evaluation
	// boundary, even when the request carries an otherwise valid old timestamp.
	fixture = newAuthorizationFixture(t, ActionSend)
	revocation = RevokeGrantRequest{
		SchemaVersion:   AuthorizationSchemaVersion,
		GrantRef:        fixture.request.Grant.Ref(),
		Caller:          fixture.request.Caller,
		Parent:          fixture.request.Parent,
		ReasonRef:       "revoke",
		OperationDigest: digest('f'),
		RequestedAt:     fixture.now,
	}
	grant = fixture.request.Grant
	grant.IssuedAt = fixture.now.Add(time.Hour)
	grant.ExpiresAt = fixture.now.Add(3 * time.Hour)
	evaluationNow = fixture.now.Add(30 * time.Minute)
	if _, err := revocation.Apply(grant, evaluationNow); !errors.Is(err, ErrGrantNotYetValid) {
		t.Fatalf("not-yet-valid grant at evaluation error=%v, want ErrGrantNotYetValid", err)
	}

	// Once the grant is active, a request timestamp from before issuance is
	// still stale and cannot be replayed to revoke the newer grant.
	fixture = newAuthorizationFixture(t, ActionSend)
	revocation = RevokeGrantRequest{
		SchemaVersion:   AuthorizationSchemaVersion,
		GrantRef:        fixture.request.Grant.Ref(),
		Caller:          fixture.request.Caller,
		Parent:          fixture.request.Parent,
		ReasonRef:       "revoke",
		OperationDigest: digest('f'),
		RequestedAt:     fixture.now.Add(-2 * time.Second),
	}
	grant = fixture.request.Grant
	grant.IssuedAt = fixture.now.Add(-time.Second)
	if _, err := revocation.Apply(grant, fixture.now); !errors.Is(err, ErrRevocationConflict) {
		t.Fatalf("pre-issuance request timestamp error=%v, want ErrRevocationConflict", err)
	}
}

func TestAuthorizationCandidateJSONFieldsAreExplicitAndUnique(t *testing.T) {
	for _, value := range []any{CallerBinding{}, ParentBinding{}, GrantRef{}, GrantClaim{}, CursorClaim{}, PreflightSpec{}, AuthorizationRequest{}, EffectCounters{}, AuthorizationDecision{}, RevokeGrantRequest{}} {
		t.Run(reflect.TypeOf(value).Name(), func(t *testing.T) {
			fields := jsonFields(reflect.TypeOf(value))
			if len(fields) != reflect.TypeOf(value).NumField() {
				t.Fatalf("%s JSON fields=%v, want one tag per field", reflect.TypeOf(value).Name(), fields)
			}
			if _, err := json.Marshal(value); err != nil {
				t.Fatalf("json.Marshal(%s): %v", reflect.TypeOf(value).Name(), err)
			}
		})
	}
}

type authorizationFixture struct {
	ctx     context.Context
	request AuthorizationRequest
	now     time.Time
}

func newAuthorizationFixture(t *testing.T, action Action) authorizationFixture {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	parentFence := testFence("parent", "parent-inc", "a")
	childFence := testFence("child", "child-inc", "b")
	issuer := common.IssuerBinding{Kind: common.CallerAttributionIssuerProviderBridge, BindingRef: "issuer/parent", Generation: 1, Digest: digest('c')}
	attribution, err := common.NewCallerAttribution("parent", parentFence, issuer)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := common.NewCallerPrincipal("principal/parent", "caller/scope", "task_http_session_v1", "auth/parent", now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := common.BindCallerPrincipal(context.Background(), principal)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err = common.BindCallerAttribution(ctx, attribution)
	if err != nil {
		t.Fatal(err)
	}
	parent := ParentBinding{SchemaVersion: AuthorizationSchemaVersion, Relation: ParentChildRelation, ParentTaskID: "parent", ChildTaskID: "child", ParentScopeRef: "parent/scope", ChildScopeRef: "child/scope", ParentFence: parentFence, ChildFence: childFence, ParentEvidenceRef: "proof/parent-child", BindingDigest: digest('d')}
	grant := GrantClaim{SchemaVersion: AuthorizationSchemaVersion, GrantID: "grant/parent-child", Action: action, CallerPrincipalRef: principal.PrincipalRef, ScopeRef: principal.ScopeRef, SourceScopeRef: parent.ParentScopeRef, TargetScopeRef: parent.ChildScopeRef, Parent: parent, GrantRevision: 1, RevocationEpoch: 1, CapabilityEvidenceCursor: "capability/1", RequestDigest: digest('e'), GrantDigest: digest('f'), Status: "active", IssuedAt: now.Add(-time.Second), ExpiresAt: now.Add(time.Hour)}
	request := AuthorizationRequest{SchemaVersion: AuthorizationSchemaVersion, RequestID: "request/1", Action: action, Caller: CallerBinding{PrincipalRef: principal.PrincipalRef, AuthContextID: principal.AuthContext, ScopeRef: principal.ScopeRef, Profile: principal.Profile, TaskID: attribution.TaskID, SessionID: principal.AuthContext, BootEpoch: attribution.Issuer.BindingRef, Source: attribution.Source, Issuer: attribution.Issuer, ExpiresAt: principal.ExpiresAt}, Grant: grant, GrantRef: grant.Ref(), Parent: parent, ContentRef: "content/1", ResourceReservationRef: "resource/1", IntentDigest: digest('1'), RequestedAt: now}
	if action != ActionSend {
		request.ContentRef, request.ResourceReservationRef = "", ""
	}
	return authorizationFixture{ctx: ctx, request: request, now: now}
}

func validCursor(request AuthorizationRequest) *CursorClaim {
	return &CursorClaim{SchemaVersion: AuthorizationSchemaVersion, Domain: "security/task", SourceOwner: "event-owner", SourceOwnerGeneration: 2, SourceID: "task/parent", SourceEpoch: request.Parent.ParentFence.IncarnationID, Lane: "output", StreamID: "stream/1", SessionID: request.Caller.SessionID, BootEpoch: request.Caller.BootEpoch, SelectorDigest: digest('2'), Grant: request.Grant.Ref(), SourceFence: request.Parent.ParentFence, TargetFence: request.Parent.ChildFence}
}

func testFence(agent, incarnation string, digestChar string) common.ExecutionFence {
	return common.ExecutionFence{AgentID: agent, ReservationID: "reservation/" + agent, ReservationGeneration: 1, IncarnationID: incarnation, IncarnationGeneration: 1, BindingVersion: 1, BindingDigest: strings.Repeat(digestChar, 64), SourceFenceRef: "fence/" + agent}
}

func digest(char byte) string { return strings.Repeat(string(char), 64) }

func timePointer(value time.Time) *time.Time { return &value }

func jsonFields(typeOf reflect.Type) map[string]struct{} {
	fields := make(map[string]struct{}, typeOf.NumField())
	for index := 0; index < typeOf.NumField(); index++ {
		name := strings.Split(typeOf.Field(index).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			fields[name] = struct{}{}
		}
	}
	return fields
}
