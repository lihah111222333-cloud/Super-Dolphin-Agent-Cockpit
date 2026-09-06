package commandintent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/orchestrationcommand"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

type assemblerAuthorizerStub struct {
	calls         int
	request       accesswait.AccessRequest
	authorization accesswait.Authorization
	err           error
}

func (s *assemblerAuthorizerStub) AuthorizeSend(_ context.Context, request accesswait.AccessRequest) (accesswait.Authorization, error) {
	s.calls++
	s.request = request
	return s.authorization, s.err
}

type assemblerIdentityStub struct {
	calls    int
	bindings map[string]identity.CurrentBinding
	err      error
}

type assemblerContentProofStub struct {
	calls    int
	requests []ContentAdmissionProofRequest
	proof    ContentAdmissionProof
	err      error
}

func (s *assemblerContentProofStub) IssueContentAdmissionProof(_ context.Context, request ContentAdmissionProofRequest) (ContentAdmissionProof, error) {
	s.calls++
	s.requests = append(s.requests, request)
	return s.proof, s.err
}

func (s *assemblerIdentityStub) GetCurrent(_ context.Context, agentID string) (identity.CurrentBinding, error) {
	s.calls++
	if s.err != nil {
		return identity.CurrentBinding{}, s.err
	}
	return s.bindings[agentID], nil
}

type assemblerSignerStub struct {
	calls    int
	intents  []orchestrationcommand.Intent
	profiles []orchestrationcommand.SendMessageProfile
	digest   string
	err      error
}

func (s *assemblerSignerStub) SignSendProfileDigest(_ context.Context, intent orchestrationcommand.Intent, profile orchestrationcommand.SendMessageProfile) (string, error) {
	s.calls++
	s.intents = append(s.intents, intent)
	s.profiles = append(s.profiles, profile)
	return s.digest, s.err
}

type assemblerReserverStub struct {
	calls    int
	intents  []orchestrationcommand.Intent
	profiles []orchestrationcommand.SendMessageProfile
	winner   *orchestrationcommand.CompleteIntentRecord
	err      error
}

func (s *assemblerReserverStub) Reserve(_ context.Context, intent orchestrationcommand.Intent, profile orchestrationcommand.SendMessageProfile) (orchestrationcommand.CompleteIntentRecord, error) {
	s.calls++
	s.intents = append(s.intents, intent)
	s.profiles = append(s.profiles, profile)
	if s.err != nil {
		return orchestrationcommand.CompleteIntentRecord{}, s.err
	}
	if s.winner == nil {
		recordedAt := profile.Authorization.DecidedAt
		record := orchestrationcommand.CompleteIntentRecord{
			Command: orchestrationcommand.Record{Intent: intent, SchemaVersion: 1, CommandVersion: 1, RecordedAt: recordedAt},
			Profile: profile, SchemaVersion: 1, RecordedAt: recordedAt,
		}
		s.winner = &record
	}
	return *s.winner, nil
}

type assemblerTestFixture struct {
	principal     common.CallerPrincipal
	request       ReserveAuthorizedSendDraftRequest
	authorization accesswait.Authorization
	source        identity.CurrentBinding
	target        identity.CurrentBinding
	authorizer    *assemblerAuthorizerStub
	identities    *assemblerIdentityStub
	content       *assemblerContentProofStub
	signer        *assemblerSignerStub
	reserver      *assemblerReserverStub
	assembler     *Assembler
	ctx           context.Context
}

// TestCommandIntentAssemblerContract 聚合完整 assembler 与字段守卫合同，供宿主精确 selector 执行。
func TestCommandIntentAssemblerContract(t *testing.T) {
	checks := []struct {
		name string
		run  func(*testing.T)
	}{
		{"complete_mapping", TestAssemblerMapsCompletePrincipalAuthorizationGrantAndBindings},
		{"principal_fail_closed", TestAssemblerRejectsMissingExpiredOrMismatchedPrincipalBeforeOwners},
		{"authorization_binding_fail_closed", TestAssemblerAuthorizationAndBindingFailuresDoNotReserve},
		{"content_proof_fail_closed", TestAssemblerContentProofFailuresDoNotSignOrReserve},
		{"content_dependency", TestAssemblerRequiresContentProofIssuer},
		{"digest_separation", TestAssemblerKeepsAuthorizationGrantAndCompleteDigestsSeparate},
		{"candidate_replay", TestAssemblerCandidateIDReplayReturnsReserverWinner},
		{"result_surface", TestAssemblerResultContractHasNoAcceptedOrProviderFields},
		{"producer_fields", TestAssemblerProducerFieldsRemainReferenced},
		{"wire_fields", TestAssemblerWireDTOsHaveEnumerableJSONFields},
		{"proof_digest_fields", TestContentProofDigestsCoverEveryBusinessField},
		{"proof_request_key", TestContentProofRequestCannotBindCandidateCommandID},
		{"candidate_ref_mapping", TestAssemblerDoesNotMapCandidateContentRefDirectlyToIntent},
		{"binding_one_hot", TestSelectorFromBindingCoversEveryCurrentBindingField},
		{"dependency_surface", TestAssemblerProductionDependencySurfaceHasNoEffectPorts},
		{"nil_context_fail_closed", TestAssemblerNilContextFailsClosed},
	}
	for _, check := range checks {
		t.Run(check.name, check.run)
	}
}

// TestAssemblerNilContextFailsClosed ensures no owner is called when the
// caller violates the context contract.
func TestAssemblerNilContextFailsClosed(t *testing.T) {
	fixture := newAssemblerTestFixture(t)
	var nilCtx context.Context
	if _, err := fixture.assembler.ReserveAuthorizedSendDraft(nilCtx, fixture.request); !errors.Is(err, ErrContextRequired) {
		t.Fatalf("ReserveAuthorizedSendDraft(nil context) error = %v, want ErrContextRequired", err)
	}
	assertAssemblerOwnerCalls(t, fixture, [5]int{})
}

// TestAssemblerMapsCompletePrincipalAuthorizationGrantAndBindings 锁定全部 owner 快照的显式映射。
func TestAssemblerMapsCompletePrincipalAuthorizationGrantAndBindings(t *testing.T) {
	fixture := newAssemblerTestFixture(t)
	got, err := fixture.assembler.ReserveAuthorizedSendDraft(fixture.ctx, fixture.request)
	if err != nil {
		t.Fatalf("ReserveAuthorizedSendDraft() error = %v", err)
	}
	assertAssemblerOwnerCalls(t, fixture, [5]int{1, 2, 1, 1, 1})
	intent, profile := fixture.reserver.intents[0], fixture.reserver.profiles[0]
	assertAssemblerIntentMapping(t, fixture, intent)
	if intent.RequestContentRef == fixture.request.RequestContentCandidateRef {
		t.Fatalf("intent persisted caller candidate ref %q instead of owner canonical ref", intent.RequestContentRef)
	}
	assertContentProofRequestMapping(t, fixture, fixture.content.requests[0])
	assertAssemblerPrincipalMapping(t, fixture.principal, intent, profile)
	assertAssemblerAuthorizationMapping(t, fixture, profile.Authorization)
	assertAssemblerSelectorMapping(t, fixture.source, profile.Source)
	assertAssemblerSelectorMapping(t, fixture.target, profile.Target)
	if fixture.reserver.winner == nil || !reflect.DeepEqual(got, *fixture.reserver.winner) {
		t.Fatalf("assembler result = %#v, want exact immutable reserver record %#v", got, fixture.reserver.winner)
	}
}

// TestAssemblerRejectsMissingExpiredOrMismatchedPrincipalBeforeOwners 覆盖 transport principal 的零调用拒绝。
func TestAssemblerRejectsMissingExpiredOrMismatchedPrincipalBeforeOwners(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		fixture := newAssemblerTestFixture(t)
		assertAssemblerEarlyRejection(t, fixture, context.Background())
	})
	t.Run("expired", func(t *testing.T) {
		fixture := newAssemblerTestFixture(t)
		assertAssemblerEarlyRejection(t, fixture, expiredPrincipalContext(t, fixture.principal))
	})
	t.Run("mismatch", func(t *testing.T) {
		fixture := newAssemblerTestFixture(t)
		principal := fixture.principal
		principal.PrincipalRef = "principal/other"
		assertAssemblerEarlyRejection(t, fixture, bindAssemblerPrincipal(t, principal))
	})
}

// TestAssemblerAuthorizationAndBindingFailuresDoNotReserve 覆盖判决拒绝与 current-fence 漂移。
func TestAssemblerAuthorizationAndBindingFailuresDoNotReserve(t *testing.T) {
	t.Run("authorization denied", func(t *testing.T) {
		fixture := newAssemblerTestFixture(t)
		fixture.authorizer.err = accesswait.ErrAccessDenied
		assertAssemblerNoReserve(t, fixture, accesswait.ErrAccessDenied, 0, 0)
	})
	t.Run("authorization mismatch", func(t *testing.T) {
		fixture := newAssemblerTestFixture(t)
		fixture.authorizer.authorization.RequestID = "request/other"
		assertAssemblerNoReserve(t, fixture, accesswait.ErrAccessDenied, 0, 0)
	})
	t.Run("binding mismatch", func(t *testing.T) {
		fixture := newAssemblerTestFixture(t)
		binding := fixture.identities.bindings[fixture.source.AgentID]
		binding.BindingDigest = strings.Repeat("9", 64)
		fixture.identities.bindings[fixture.source.AgentID] = binding
		assertAssemblerNoReserve(t, fixture, orchestrationcommand.ErrBindingFence, 2, 0)
	})
}

// TestAssemblerContentProofFailuresDoNotSignOrReserve 锁定CONTENT owner拒绝发生在摘要签名与落库之前。
func TestAssemblerContentProofFailuresDoNotSignOrReserve(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*testing.T, *assemblerTestFixture)
		want    error
	}{
		{name: "owner unavailable", want: ErrContentAdmissionUnavailable, prepare: func(_ *testing.T, fixture *assemblerTestFixture) {
			fixture.content.err = ErrContentAdmissionUnavailable
		}},
		{name: "forged snapshot", want: ErrInvalidContentProof, prepare: func(_ *testing.T, fixture *assemblerTestFixture) {
			fixture.content.proof.SnapshotDigest = strings.Repeat("f", 64)
		}},
		{name: "request mismatch", want: ErrContentProofMismatch, prepare: func(t *testing.T, fixture *assemblerTestFixture) {
			fixture.content.proof.RequestDigest = strings.Repeat("0", 64)
			resignAssemblerContentProof(t, fixture)
		}},
		{name: "expired", want: ErrContentProofMismatch, prepare: func(t *testing.T, fixture *assemblerTestFixture) {
			fixture.content.proof.ExpiresAt = fixture.authorization.AuthorizedAt
			resignAssemblerContentProof(t, fixture)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAssemblerTestFixture(t)
			test.prepare(t, fixture)
			assertAssemblerNoReserve(t, fixture, test.want, 2, 1)
		})
	}
}

// TestAssemblerRequiresContentProofIssuer 覆盖nil与typed-nil CONTENT owner依赖。
func TestAssemblerRequiresContentProofIssuer(t *testing.T) {
	fixture := newAssemblerTestFixture(t)
	var typedNil *assemblerContentProofStub
	for _, content := range []ContentAdmissionProofIssuer{nil, typedNil} {
		assembler, err := NewAssembler(fixture.authorizer, fixture.identities, content, fixture.reserver, fixture.signer)
		if err == nil || assembler != nil {
			t.Fatalf("NewAssembler(content=%T) = %#v, %v; want nil assembler and error", content, assembler, err)
		}
	}
}

// TestAssemblerKeepsAuthorizationGrantAndCompleteDigestsSeparate 防止跨 domain 摘要代填。
func TestAssemblerKeepsAuthorizationGrantAndCompleteDigestsSeparate(t *testing.T) {
	fixture := newAssemblerTestFixture(t)
	if _, err := fixture.assembler.ReserveAuthorizedSendDraft(fixture.ctx, fixture.request); err != nil {
		t.Fatal(err)
	}
	intent, decision := fixture.reserver.intents[0], fixture.reserver.profiles[0].Authorization
	if [2]string{decision.RequestDigest, decision.GrantRequestDigest} != [2]string{fixture.request.Access.RequestDigest, fixture.authorization.Grant.RequestDigest} {
		t.Fatalf("decision digests = request %q grant %q", decision.RequestDigest, decision.GrantRequestDigest)
	}
	digests := map[string]bool{intent.RequestDigest: true, decision.RequestDigest: true, decision.GrantRequestDigest: true}
	if len(digests) != 3 {
		t.Fatalf("digest domains collapsed: complete=%q authorization=%q grant=%q", intent.RequestDigest, decision.RequestDigest, decision.GrantRequestDigest)
	}
}

// TestAssemblerCandidateIDReplayReturnsReserverWinner 确保候选 ID 不覆盖原幂等赢家。
func TestAssemblerCandidateIDReplayReturnsReserverWinner(t *testing.T) {
	fixture := newAssemblerTestFixture(t)
	first, err := fixture.assembler.ReserveAuthorizedSendDraft(fixture.ctx, fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	replay := fixture.request
	replay.CandidateCommandID = "command/candidate-two"
	second, err := fixture.assembler.ReserveAuthorizedSendDraft(fixture.ctx, replay)
	if err != nil {
		t.Fatal(err)
	}
	if [2]string{first.Command.Intent.CommandID, second.Command.Intent.CommandID} != [2]string{fixture.request.CandidateCommandID, first.Command.Intent.CommandID} {
		t.Fatalf("replay IDs first=%q second=%q, want original winner", first.Command.Intent.CommandID, second.Command.Intent.CommandID)
	}
	if [2]any{fixture.reserver.calls, fixture.reserver.intents[1].CommandID} != [2]any{2, replay.CandidateCommandID} {
		t.Fatalf("reserver calls=%d second candidate=%q", fixture.reserver.calls, fixture.reserver.intents[1].CommandID)
	}
	if len(fixture.content.requests) != 2 || fixture.content.requests[0] != fixture.content.requests[1] {
		t.Fatalf("proof requests changed with candidate command ID: %#v", fixture.content.requests)
	}
	assertContentProofRequestMapping(t, fixture, fixture.content.requests[1])
}

// TestAssemblerResultContractHasNoAcceptedOrProviderFields 锁定 non-authorizing immutable 返回面。
func TestAssemblerResultContractHasNoAcceptedOrProviderFields(t *testing.T) {
	assertNoEffectFieldNames(t, reflect.TypeFor[orchestrationcommand.CompleteIntentRecord](), "CompleteIntentRecord")
}

func newAssemblerTestFixture(t *testing.T) *assemblerTestFixture {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Millisecond)
	source := assemblerBinding("source", "a", now.Add(-2*time.Minute))
	target := assemblerBinding("target", "b", now.Add(-time.Minute))
	principal := common.CallerPrincipal{PrincipalRef: "principal/operator", ScopeRef: "scope/workspace", Profile: "standalone_http_local_v1", AuthContext: "auth/boot-1", ExpiresAt: now.Add(30 * time.Minute)}
	grant := assemblerGrant(source, target, principal, now)
	access := accesswait.AccessRequest{RequestID: "request/send-1", GrantID: grant.GrantID, CallerPrincipalRef: principal.PrincipalRef, ScopeRef: principal.ScopeRef, Capability: "send", Source: assemblerFence(source), Target: assemblerFence(target), RequestDigest: strings.Repeat("c", 64), RequestedAt: now.Add(-30 * time.Second)}
	authorization := accesswait.Authorization{RequestID: access.RequestID, GrantID: grant.GrantID, Capability: "send", GrantRevision: grant.Revision, AuthorizedAt: now, ExpiresAt: grant.ExpiresAt, Grant: grant}
	authorizer := &assemblerAuthorizerStub{authorization: authorization}
	identities := &assemblerIdentityStub{bindings: map[string]identity.CurrentBinding{source.AgentID: source, target.AgentID: target}}
	signer := &assemblerSignerStub{digest: strings.Repeat("d", 64)}
	reserver := &assemblerReserverStub{}
	request := ReserveAuthorizedSendDraftRequest{Access: access, CandidateCommandID: "command/candidate-one", IdempotencyNamespace: "command/send/operator", IdempotencyKeyDigest: strings.Repeat("e", 64), RequestContentCandidateRef: "content/candidate-1", DigestKeyID: "digest/key-1", DigestKeyVersion: 7}
	proofRequest, err := buildContentProofRequest(request, principal, authorization)
	if err != nil {
		t.Fatal(err)
	}
	proof := assemblerContentProof(proofRequest, source, now)
	proof.SnapshotDigest, err = computeContentProofSnapshotDigest(proof)
	if err != nil {
		t.Fatal(err)
	}
	content := &assemblerContentProofStub{proof: proof}
	assembler, err := NewAssembler(authorizer, identities, content, reserver, signer)
	if err != nil {
		t.Fatal(err)
	}
	return &assemblerTestFixture{principal: principal, request: request, authorization: authorization, source: source, target: target, authorizer: authorizer, identities: identities, content: content, signer: signer, reserver: reserver, assembler: assembler, ctx: bindAssemblerPrincipal(t, principal)}
}

func assemblerContentProof(request ContentAdmissionProofRequest, source identity.CurrentBinding, now time.Time) ContentAdmissionProof {
	return ContentAdmissionProof{
		SchemaVersion: 1, OwnerID: "content-owner/main", OwnerGeneration: 3,
		ProofID: "content-proof/send-1", ProofRevision: 2, RequestDigest: request.RequestDigest,
		CanonicalRef: "content/canonical/object-1/ref-4", ObjectID: "content-object/1",
		ObjectVersion: 5, RefVersion: 4, ContentState: "active", ReferenceState: "live",
		KeyReadability: "readable", MediaType: "text/plain", SizeBytes: 128,
		SourceAgentID: source.AgentID, SourceFenceRef: source.SourceFenceRef,
		OwnerProofRef: "content-proof-ref/send-1/revision-2", ExpiresAt: now.Add(10 * time.Minute),
	}
}

func assemblerBinding(agent, suffix string, updatedAt time.Time) identity.CurrentBinding {
	return identity.CurrentBinding{
		AgentID: agent, BindingVersion: 4, ReservationID: "reservation/" + suffix,
		IncarnationID: "incarnation/" + suffix, IdentityKind: "managed_agent",
		ReservationGeneration: 2, IncarnationGeneration: 3,
		ExternalIdentityRef: "external/" + suffix, SourceFenceRef: "fence/" + suffix,
		ActiveProcessReceiptRef: "process/" + suffix, BindingDigest: strings.Repeat(suffix, 64),
		State: "active", SchemaVersion: 1, UpdatedAt: updatedAt.Format(time.RFC3339Nano),
	}
}

func assemblerGrant(source, target identity.CurrentBinding, principal common.CallerPrincipal, now time.Time) accesswait.AccessGrant {
	issuedAt := now.Add(-2 * time.Minute)
	return accesswait.AccessGrant{
		GrantID: "grant/send-1", CallerPrincipalRef: principal.PrincipalRef, ScopeRef: principal.ScopeRef,
		Source: assemblerFence(source), Target: assemblerFence(target), CanSend: true,
		IdempotencyNamespace: "grant/send/operator", IdempotencyKeyDigest: strings.Repeat("a", 64),
		RequestDigest: strings.Repeat("b", 64), IssuedAt: issuedAt, ExpiresAt: now.Add(20 * time.Minute),
		Revision: 5, Status: "active", SchemaVersion: 1, CreatedAt: issuedAt, UpdatedAt: now.Add(-time.Minute),
	}
}

func assemblerFence(binding identity.CurrentBinding) accesswait.FenceSnapshot {
	return accesswait.FenceSnapshot{
		AgentID: binding.AgentID, ReservationID: binding.ReservationID,
		ReservationGeneration: binding.ReservationGeneration, IncarnationID: binding.IncarnationID,
		IncarnationGeneration: binding.IncarnationGeneration, BindingVersion: binding.BindingVersion,
		BindingDigest: binding.BindingDigest, SourceFenceRef: binding.SourceFenceRef,
	}
}

func bindAssemblerPrincipal(t *testing.T, principal common.CallerPrincipal) context.Context {
	t.Helper()
	ctx, err := common.BindCallerPrincipal(context.Background(), principal)
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func expiredPrincipalContext(t *testing.T, principal common.CallerPrincipal) context.Context {
	t.Helper()
	principal.ExpiresAt = time.Now().UTC().Add(100 * time.Millisecond)
	ctx := bindAssemblerPrincipal(t, principal)
	timer := time.NewTimer(time.Until(principal.ExpiresAt) + 10*time.Millisecond)
	defer timer.Stop()
	<-timer.C
	return ctx
}

func assertAssemblerEarlyRejection(t *testing.T, fixture *assemblerTestFixture, ctx context.Context) {
	t.Helper()
	if _, err := fixture.assembler.ReserveAuthorizedSendDraft(ctx, fixture.request); err == nil {
		t.Fatal("ReserveAuthorizedSendDraft() error = nil, want principal rejection")
	}
	assertAssemblerOwnerCalls(t, fixture, [5]int{})
}

func assertAssemblerNoReserve(t *testing.T, fixture *assemblerTestFixture, want error, wantIdentityCalls, wantContentCalls int) {
	t.Helper()
	_, err := fixture.assembler.ReserveAuthorizedSendDraft(fixture.ctx, fixture.request)
	if !errors.Is(err, want) {
		t.Fatalf("ReserveAuthorizedSendDraft() error = %v, want %v", err, want)
	}
	assertAssemblerOwnerCalls(t, fixture, [5]int{1, wantIdentityCalls, wantContentCalls, 0, 0})
}

func assertAssemblerOwnerCalls(t *testing.T, fixture *assemblerTestFixture, want [5]int) {
	t.Helper()
	got := [5]int{fixture.authorizer.calls, fixture.identities.calls, fixture.content.calls, fixture.signer.calls, fixture.reserver.calls}
	if got != want {
		t.Fatalf("owner calls authorizer/identities/content/signer/reserver = %v, want %v", got, want)
	}
}

func assertContentProofRequestMapping(t *testing.T, fixture *assemblerTestFixture, got ContentAdmissionProofRequest) {
	t.Helper()
	wantText := [8]string{
		fixture.request.IdempotencyNamespace, fixture.request.IdempotencyKeyDigest,
		fixture.principal.PrincipalRef, fixture.principal.ScopeRef,
		fixture.authorization.RequestID, fixture.request.Access.RequestDigest,
		fixture.authorization.GrantID, fixture.request.RequestContentCandidateRef,
	}
	gotText := [8]string{
		got.IdempotencyNamespace, got.IdempotencyKeyDigest, got.CallerPrincipalRef, got.ScopeRef,
		got.AuthorizationRequestID, got.AuthorizationRequestDigest, got.GrantID, got.CandidateRef,
	}
	if gotText != wantText {
		t.Fatalf("content proof stable bindings = %q, want %q", gotText, wantText)
	}
	if got.GrantRevision != fixture.authorization.GrantRevision {
		t.Fatalf("content proof grant revision = %d, want %d", got.GrantRevision, fixture.authorization.GrantRevision)
	}
	wantFences := [2]accesswait.FenceSnapshot{fixture.authorization.Grant.Source, fixture.authorization.Grant.Target}
	if [2]accesswait.FenceSnapshot{got.Source, got.Target} != wantFences {
		t.Fatalf("content proof fences = %#v, want %#v", [2]accesswait.FenceSnapshot{got.Source, got.Target}, wantFences)
	}
	if got.SchemaVersion != 1 || got.RequestDigest != fixture.content.proof.RequestDigest {
		t.Fatalf("content proof envelope = schema %d digest %q", got.SchemaVersion, got.RequestDigest)
	}
}

func resignAssemblerContentProof(t *testing.T, fixture *assemblerTestFixture) {
	t.Helper()
	digest, err := computeContentProofSnapshotDigest(fixture.content.proof)
	if err != nil {
		t.Fatal(err)
	}
	fixture.content.proof.SnapshotDigest = digest
}

func assertAssemblerIntentMapping(t *testing.T, fixture *assemblerTestFixture, intent orchestrationcommand.Intent) {
	t.Helper()
	request := fixture.request
	want := orchestrationcommand.Intent{
		CommandID: request.CandidateCommandID, CommandKind: "send_message",
		CallerPrincipalRef: fixture.principal.PrincipalRef, ScopeRef: fixture.principal.ScopeRef,
		IdempotencyNamespace: request.IdempotencyNamespace, IdempotencyKeyDigest: request.IdempotencyKeyDigest,
		RequestDigest: fixture.signer.digest, DigestKeyID: request.DigestKeyID,
		RequestContentRef: fixture.content.proof.CanonicalRef,
		SourceFenceRef:    fixture.authorization.Grant.Source.SourceFenceRef,
		TargetFenceRef:    fixture.authorization.Grant.Target.SourceFenceRef,
	}
	if intent != want {
		t.Fatalf("intent mapping = %#v, want %#v", intent, want)
	}
}

func assertAssemblerPrincipalMapping(t *testing.T, principal common.CallerPrincipal, intent orchestrationcommand.Intent, profile orchestrationcommand.SendMessageProfile) {
	t.Helper()
	if [2]string{intent.CallerPrincipalRef, intent.ScopeRef} != [2]string{principal.PrincipalRef, principal.ScopeRef} {
		t.Fatalf("intent principal mapping = %#v", intent)
	}
	want := orchestrationcommand.CallerSnapshot{Profile: principal.Profile, AuthContext: principal.AuthContext, ExpiresAt: principal.ExpiresAt}
	if profile.Caller != want {
		t.Fatalf("profile caller mapping = %#v, want %#v", profile.Caller, principal)
	}
}

func assertAssemblerAuthorizationMapping(t *testing.T, fixture *assemblerTestFixture, got orchestrationcommand.AuthorizationDecision) {
	t.Helper()
	wantRequest, wantAuthorization := fixture.request.Access, fixture.authorization
	grant := wantAuthorization.Grant
	want := orchestrationcommand.AuthorizationDecision{
		Kind: orchestrationcommand.AccessGrantAuthorizationKind, Decision: "allowed", RequestVersion: 1,
		RequestID: wantRequest.RequestID, RequestDigest: wantRequest.RequestDigest,
		GrantID: wantAuthorization.GrantID, GrantRevision: wantAuthorization.GrantRevision, Capability: "send",
		RequestedAt: wantRequest.RequestedAt, DecidedAt: wantAuthorization.AuthorizedAt, ExpiresAt: wantAuthorization.ExpiresAt,
		GrantRequestDigest: grant.RequestDigest, GrantIssuedAt: grant.IssuedAt, GrantUpdatedAt: grant.UpdatedAt,
	}
	if got != want {
		t.Fatalf("authorization mapping = %#v, want %#v", got, want)
	}
}

func assertAssemblerSelectorMapping(t *testing.T, binding identity.CurrentBinding, got orchestrationcommand.ActiveBindingSelector) {
	t.Helper()
	updatedAt, err := time.Parse(time.RFC3339Nano, binding.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	want := orchestrationcommand.ActiveBindingSelector{
		Version: 1, Kind: orchestrationcommand.ActiveBindingSelectorKind,
		AgentID: binding.AgentID, IdentityKind: binding.IdentityKind,
		ReservationID: binding.ReservationID, ReservationGeneration: binding.ReservationGeneration,
		IncarnationID: binding.IncarnationID, IncarnationGeneration: binding.IncarnationGeneration,
		ExternalIdentityRef: binding.ExternalIdentityRef, BindingVersion: binding.BindingVersion,
		BindingDigest: binding.BindingDigest, FenceRef: binding.SourceFenceRef,
		ActiveProcessReceiptRef: binding.ActiveProcessReceiptRef, State: binding.State,
		BindingSchemaVersion: binding.SchemaVersion, UpdatedAt: updatedAt,
	}
	if got != want {
		t.Fatalf("selector mapping = %#v, want %#v", got, want)
	}
}

func assertNoEffectFieldNames(t *testing.T, typ reflect.Type, path string) {
	t.Helper()
	if typ == reflect.TypeFor[time.Time]() {
		return
	}
	if typ.Kind() == reflect.Pointer {
		assertNoEffectFieldNames(t, typ.Elem(), path)
		return
	}
	if typ.Kind() != reflect.Struct {
		return
	}
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		if field.PkgPath != "" {
			continue
		}
		jsonName := strings.Split(field.Tag.Get("json"), ",")[0]
		combined := strings.ToLower(field.Name + " " + jsonName)
		for _, forbidden := range []string{"accepted", "provider", "dispatch", "attempt", "pending"} {
			if strings.Contains(combined, forbidden) {
				t.Fatalf("%s.%s exposes forbidden effect field %q", path, field.Name, forbidden)
			}
		}
		assertNoEffectFieldNames(t, field.Type, path+"."+field.Name)
	}
}
