package admissionproof

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/orchestrationcommand"
)

type proofKeyResolver struct {
	calls  int
	events *[]string
	keyID  string
	key    []byte
}

func (r *proofKeyResolver) ResolveSendProfileDigestKey(_ context.Context, keyID string, version int64) ([]byte, error) {
	r.calls++
	*r.events = append(*r.events, "profile-verify")
	if keyID != r.keyID || version != 1 {
		return nil, fmt.Errorf("unknown test key %q/%d", keyID, version)
	}
	return append([]byte(nil), r.key...), nil
}

type proofContentResolver struct {
	calls    int
	events   *[]string
	keys     []ContentResolveKey
	snapshot ContentSnapshot
	err      error
}

func (r *proofContentResolver) ResolveContentSnapshot(_ context.Context, key ContentResolveKey) (ContentSnapshot, error) {
	r.calls++
	*r.events = append(*r.events, "content-resolve")
	r.keys = append(r.keys, key)
	return r.snapshot, r.err
}

type proofContentVerifier struct {
	calls  int
	events *[]string
	err    error
}

func (v *proofContentVerifier) VerifyContentSnapshot(context.Context, ContentResolveKey, ContentSnapshot) error {
	v.calls++
	*v.events = append(*v.events, "content-verify")
	return v.err
}

type proofResourceResolver struct {
	calls    int
	events   *[]string
	keys     []ResourceResolveKey
	snapshot ResourceSnapshot
	err      error
}

func (r *proofResourceResolver) ResolveResourceSnapshot(_ context.Context, key ResourceResolveKey) (ResourceSnapshot, error) {
	r.calls++
	*r.events = append(*r.events, "resource-resolve")
	r.keys = append(r.keys, key)
	return r.snapshot, r.err
}

type proofResourceVerifier struct {
	calls  int
	events *[]string
	err    error
}

func (v *proofResourceVerifier) VerifyResourceSnapshot(context.Context, ResourceResolveKey, ResourceSnapshot) error {
	v.calls++
	*v.events = append(*v.events, "resource-verify")
	return v.err
}

type gateTestFixture struct {
	config           Config
	record           orchestrationcommand.CompleteIntentRecord
	keyResolver      *proofKeyResolver
	contentResolver  *proofContentResolver
	contentVerifier  *proofContentVerifier
	resourceResolver *proofResourceResolver
	resourceVerifier *proofResourceVerifier
	gate             *Gate
	events           []string
}

// TestAdmissionProofGateContract 聚合proof链映射、fail-closed、摘要和返回面合同。
func TestAdmissionProofGateContract(t *testing.T) {
	checks := []struct {
		name string
		run  func(*testing.T)
	}{
		{"complete_mapping", TestBuildSubmissionMapsCompleteProofChain},
		{"content_fail_closed", TestBuildSubmissionRejectsContentProofFailures},
		{"resource_fail_closed", TestBuildSubmissionRejectsResourceProofFailures},
		{"profile_hmac_first", TestBuildSubmissionRejectsDamagedProfileHMACBeforeResolvers},
		{"snapshot_digest_fields", TestSnapshotDigestsBindEveryBusinessField},
		{"constructor_fail_closed", TestNewGateRejectsInvalidConfigAndNilDependencies},
		{"unavailable_adapters", TestUnavailableResolversReturnZeroSubmission},
		{"json_fields", TestAdmissionProofJSONFieldsRemainEnumerable},
		{"resolver_key_surface", TestAdmissionProofResolveKeysExcludeCallerOwnerSelection},
		{"snapshot_field_guard", TestAdmissionProofSnapshotDigestsCoverEveryBusinessField},
		{"custody_mapping_guard", TestAdmissionProofGateProducerToCustodyMapping},
		{"public_field_guard", TestAdmissionProofPublicFieldsExcludeAuthorityPayload},
		{"dependency_guard", TestAdmissionProofProductionHasNoEffectDependencies},
	}
	for _, check := range checks {
		t.Run(check.name, check.run)
	}
}

func TestBuildSubmissionMapsCompleteProofChain(t *testing.T) {
	fixture := newGateTestFixture(t)
	got, err := fixture.gate.BuildSubmission(t.Context(), fixture.record)
	assertProofNoError(t, err)
	want := expectedSubmission(t, fixture)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildSubmission() = %#v, want %#v", got, want)
	}
	assertProofNoError(t, got.Validate())
	assertResolverKeys(t, fixture)
	assertResolverKeyFields(t)
	assertNoEffectResult(t, got)

	again, err := fixture.gate.BuildSubmission(t.Context(), fixture.record)
	assertProofNoError(t, err)
	if got.OperationID != again.OperationID || got.BatchDigest != again.BatchDigest || got != again {
		t.Fatalf("stable input changed custody request: first=%#v again=%#v", got, again)
	}
	wantEvents := []string{"profile-verify", "content-resolve", "content-verify", "resource-resolve", "resource-verify"}
	if !reflect.DeepEqual(fixture.events, append(wantEvents, wantEvents...)) {
		t.Fatalf("proof call order = %v, want two CONTENT-before-RESOURCE chains %v", fixture.events, append(wantEvents, wantEvents...))
	}
}

func TestBuildSubmissionRejectsContentProofFailures(t *testing.T) {
	tests := []struct {
		name    string
		want    error
		prepare func(*testing.T, *gateTestFixture)
		calls   [4]int
	}{
		{"owner_unavailable", ErrContentOwnerUnavailable, func(_ *testing.T, f *gateTestFixture) { f.contentResolver.err = ErrContentOwnerUnavailable }, [4]int{1, 0, 0, 0}},
		{"invalid", ErrInvalidContentSnapshot, corruptContentSnapshot, [4]int{1, 0, 0, 0}},
		{"mismatch", ErrContentSnapshotMismatch, mismatchContentSnapshot, [4]int{1, 0, 0, 0}},
		{"verifier_failure", ErrContentVerificationFailed, failContentVerifier, [4]int{1, 1, 0, 0}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newGateTestFixture(t)
			test.prepare(t, fixture)
			request, err := fixture.gate.BuildSubmission(t.Context(), fixture.record)
			assertProofFailure(t, request, err, test.want)
			assertProofCalls(t, fixture, test.calls)
		})
	}
}

func TestBuildSubmissionRejectsResourceProofFailures(t *testing.T) {
	tests := []struct {
		name    string
		want    error
		prepare func(*testing.T, *gateTestFixture)
		calls   [4]int
	}{
		{"owner_unavailable", ErrResourceOwnerUnavailable, func(_ *testing.T, f *gateTestFixture) { f.resourceResolver.err = ErrResourceOwnerUnavailable }, [4]int{1, 1, 1, 0}},
		{"invalid", ErrInvalidResourceSnapshot, corruptResourceSnapshot, [4]int{1, 1, 1, 0}},
		{"mismatch", ErrResourceSnapshotMismatch, mismatchResourceSnapshot, [4]int{1, 1, 1, 0}},
		{"verifier_failure", ErrResourceVerificationFailed, failResourceVerifier, [4]int{1, 1, 1, 1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newGateTestFixture(t)
			test.prepare(t, fixture)
			request, err := fixture.gate.BuildSubmission(t.Context(), fixture.record)
			assertProofFailure(t, request, err, test.want)
			assertProofCalls(t, fixture, test.calls)
		})
	}
}

func TestBuildSubmissionRejectsDamagedProfileHMACBeforeResolvers(t *testing.T) {
	fixture := newGateTestFixture(t)
	damaged := proofDigest("damaged-profile")
	fixture.record.Command.Intent.RequestDigest = damaged
	fixture.record.Profile.CompleteIntentDigest = damaged
	request, err := fixture.gate.BuildSubmission(t.Context(), fixture.record)
	assertProofFailure(t, request, err, orchestrationcommand.ErrProfileConflict)
	if fixture.keyResolver.calls != 1 {
		t.Fatalf("profile key resolver calls = %d, want 1", fixture.keyResolver.calls)
	}
	assertProofCalls(t, fixture, [4]int{})
	if !reflect.DeepEqual(fixture.events, []string{"profile-verify"}) {
		t.Fatalf("events after damaged HMAC = %v, want verifier only", fixture.events)
	}
}

func TestSnapshotDigestsBindEveryBusinessField(t *testing.T) {
	fixture := newGateTestFixture(t)
	assertSnapshotFieldsBound(t, "content", fixture.contentResolver.snapshot, func(value reflect.Value) (string, error) {
		return ComputeContentSnapshotDigest(value.Interface().(ContentSnapshot))
	})
	assertSnapshotFieldsBound(t, "resource", fixture.resourceResolver.snapshot, func(value reflect.Value) (string, error) {
		return ComputeResourceSnapshotDigest(value.Interface().(ResourceSnapshot))
	})
}

func TestNewGateRejectsInvalidConfigAndNilDependencies(t *testing.T) {
	fixture := newGateTestFixture(t)
	for _, config := range []Config{{AuthorityProfile: fixture.config.AuthorityProfile}, {SecurityDomainID: fixture.config.SecurityDomainID}} {
		gate, err := NewGate(config, fixture.gate.intentVerifier, fixture.contentResolver, fixture.contentVerifier, fixture.resourceResolver, fixture.resourceVerifier)
		assertGateConstructorRejected(t, gate, err)
	}
	var typedIntent *orchestrationcommand.HMACSendProfileDigestVerifier
	var typedContentResolver *proofContentResolver
	var typedContentVerifier *proofContentVerifier
	var typedResourceResolver *proofResourceResolver
	var typedResourceVerifier *proofResourceVerifier
	tests := []struct {
		name string
		make func() (*Gate, error)
	}{
		{"nil_intent", func() (*Gate, error) {
			return NewGate(fixture.config, nil, fixture.contentResolver, fixture.contentVerifier, fixture.resourceResolver, fixture.resourceVerifier)
		}},
		{"typed_nil_intent", func() (*Gate, error) {
			return NewGate(fixture.config, typedIntent, fixture.contentResolver, fixture.contentVerifier, fixture.resourceResolver, fixture.resourceVerifier)
		}},
		{"nil_content_resolver", func() (*Gate, error) {
			return NewGate(fixture.config, fixture.gate.intentVerifier, nil, fixture.contentVerifier, fixture.resourceResolver, fixture.resourceVerifier)
		}},
		{"typed_nil_content_resolver", func() (*Gate, error) {
			return NewGate(fixture.config, fixture.gate.intentVerifier, typedContentResolver, fixture.contentVerifier, fixture.resourceResolver, fixture.resourceVerifier)
		}},
		{"nil_content_verifier", func() (*Gate, error) {
			return NewGate(fixture.config, fixture.gate.intentVerifier, fixture.contentResolver, nil, fixture.resourceResolver, fixture.resourceVerifier)
		}},
		{"typed_nil_content_verifier", func() (*Gate, error) {
			return NewGate(fixture.config, fixture.gate.intentVerifier, fixture.contentResolver, typedContentVerifier, fixture.resourceResolver, fixture.resourceVerifier)
		}},
		{"nil_resource_resolver", func() (*Gate, error) {
			return NewGate(fixture.config, fixture.gate.intentVerifier, fixture.contentResolver, fixture.contentVerifier, nil, fixture.resourceVerifier)
		}},
		{"typed_nil_resource_resolver", func() (*Gate, error) {
			return NewGate(fixture.config, fixture.gate.intentVerifier, fixture.contentResolver, fixture.contentVerifier, typedResourceResolver, fixture.resourceVerifier)
		}},
		{"nil_resource_verifier", func() (*Gate, error) {
			return NewGate(fixture.config, fixture.gate.intentVerifier, fixture.contentResolver, fixture.contentVerifier, fixture.resourceResolver, nil)
		}},
		{"typed_nil_resource_verifier", func() (*Gate, error) {
			return NewGate(fixture.config, fixture.gate.intentVerifier, fixture.contentResolver, fixture.contentVerifier, fixture.resourceResolver, typedResourceVerifier)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gate, err := test.make()
			assertGateConstructorRejected(t, gate, err)
		})
	}
}

func TestUnavailableResolversReturnZeroSubmission(t *testing.T) {
	t.Run("content", func(t *testing.T) {
		fixture := newGateTestFixture(t)
		gate, err := NewGate(fixture.config, fixture.gate.intentVerifier, UnavailableContentResolver{}, fixture.contentVerifier, fixture.resourceResolver, fixture.resourceVerifier)
		assertProofNoError(t, err)
		request, err := gate.BuildSubmission(t.Context(), fixture.record)
		assertProofFailure(t, request, err, ErrContentOwnerUnavailable)
		if fixture.contentVerifier.calls != 0 || fixture.resourceResolver.calls != 0 || fixture.resourceVerifier.calls != 0 {
			t.Fatalf("content unavailable reached later proof calls: verifier=%d resource=%d/%d", fixture.contentVerifier.calls, fixture.resourceResolver.calls, fixture.resourceVerifier.calls)
		}
	})
	t.Run("resource", func(t *testing.T) {
		fixture := newGateTestFixture(t)
		gate, err := NewGate(fixture.config, fixture.gate.intentVerifier, fixture.contentResolver, fixture.contentVerifier, UnavailableResourceResolver{}, fixture.resourceVerifier)
		assertProofNoError(t, err)
		request, err := gate.BuildSubmission(t.Context(), fixture.record)
		assertProofFailure(t, request, err, ErrResourceOwnerUnavailable)
		if fixture.contentResolver.calls != 1 || fixture.contentVerifier.calls != 1 || fixture.resourceVerifier.calls != 0 {
			t.Fatalf("resource unavailable proof calls = content %d/%d resource verifier %d", fixture.contentResolver.calls, fixture.contentVerifier.calls, fixture.resourceVerifier.calls)
		}
	})
}

func newGateTestFixture(t *testing.T) *gateTestFixture {
	t.Helper()
	fixture := &gateTestFixture{config: Config{SecurityDomainID: "security-domain-test", AuthorityProfile: "external-quorum-v1"}}
	fixture.record = validCompleteIntentRecord(t)
	fixture.keyResolver = &proofKeyResolver{events: &fixture.events, keyID: fixture.record.Profile.DigestKeyID, key: proofProfileKey()}
	verifier, err := orchestrationcommand.NewHMACSendProfileDigestVerifier(fixture.keyResolver)
	assertProofNoError(t, err)
	content := validContentSnapshot(t, fixture.record)
	identityDigest, err := computeIdentityDigest(fixture.record.Profile.Source, fixture.record.Profile.Target)
	assertProofNoError(t, err)
	resource := validResourceSnapshot(t, fixture.record, identityDigest, content.SnapshotDigest)
	fixture.contentResolver = &proofContentResolver{events: &fixture.events, snapshot: content}
	fixture.contentVerifier = &proofContentVerifier{events: &fixture.events}
	fixture.resourceResolver = &proofResourceResolver{events: &fixture.events, snapshot: resource}
	fixture.resourceVerifier = &proofResourceVerifier{events: &fixture.events}
	fixture.gate, err = NewGate(fixture.config, verifier, fixture.contentResolver, fixture.contentVerifier, fixture.resourceResolver, fixture.resourceVerifier)
	assertProofNoError(t, err)
	return fixture
}

func validCompleteIntentRecord(t *testing.T) orchestrationcommand.CompleteIntentRecord {
	t.Helper()
	issuedAt := time.Date(2026, 9, 2, 1, 0, 0, 0, time.UTC)
	intent := orchestrationcommand.Intent{CommandID: "command-proof-1", CommandKind: "send_message", CallerPrincipalRef: "principal/test",
		ScopeRef: "scope/test", IdempotencyNamespace: "admissionproof/send", IdempotencyKeyDigest: proofDigest("idempotency"),
		RequestDigest: proofDigest("placeholder"), DigestKeyID: "admissionproof-test-key", RequestContentRef: "content-owner://canonical/message-1",
		SourceFenceRef: "fence/source/7", TargetFenceRef: "fence/target/9"}
	profile := orchestrationcommand.SendMessageProfile{Kind: orchestrationcommand.SendMessageProfileKind, Version: 1,
		Caller:        orchestrationcommand.CallerSnapshot{Profile: "standalone_http_local_v1", AuthContext: "auth/context/1", ExpiresAt: issuedAt.Add(4 * time.Hour)},
		Authorization: validAuthorization(issuedAt), Source: validSelector("source-agent", intent.SourceFenceRef, issuedAt),
		Target: validSelector("target-agent", intent.TargetFenceRef, issuedAt), CompleteIntentDigest: intent.RequestDigest,
		DigestDomain: orchestrationcommand.SendMessageDigestDomain, DigestAlgorithm: orchestrationcommand.HMACSHA256DigestAlgorithm,
		DigestKeyID: intent.DigestKeyID, DigestKeyVersion: 1}
	digest, err := orchestrationcommand.ComputeSendProfileDigest(proofProfileKey(), intent, profile)
	assertProofNoError(t, err)
	intent.RequestDigest, profile.CompleteIntentDigest = digest, digest
	recordedAt := issuedAt.Add(3 * time.Minute)
	record := orchestrationcommand.CompleteIntentRecord{Command: orchestrationcommand.Record{Intent: intent, SchemaVersion: 1, CommandVersion: 1, RecordedAt: recordedAt}, Profile: profile, SchemaVersion: 1, RecordedAt: recordedAt}
	assertProofNoError(t, record.Profile.Validate(record.Command.Intent))
	return record
}

func validAuthorization(issuedAt time.Time) orchestrationcommand.AuthorizationDecision {
	return orchestrationcommand.AuthorizationDecision{Kind: orchestrationcommand.AccessGrantAuthorizationKind, Decision: "allowed", RequestVersion: 1,
		RequestID: "access-request-1", RequestDigest: proofDigest("access-request"), GrantID: "access-grant-1", GrantRevision: 3,
		Capability: "send", RequestedAt: issuedAt.Add(time.Minute), DecidedAt: issuedAt.Add(2 * time.Minute), ExpiresAt: issuedAt.Add(3 * time.Hour),
		GrantRequestDigest: proofDigest("grant-request"), GrantIssuedAt: issuedAt, GrantUpdatedAt: issuedAt.Add(30 * time.Second)}
}

func validSelector(agentID, fenceRef string, updatedAt time.Time) orchestrationcommand.ActiveBindingSelector {
	return orchestrationcommand.ActiveBindingSelector{Version: 1, Kind: orchestrationcommand.ActiveBindingSelectorKind, AgentID: agentID,
		IdentityKind: "native_subagent", ReservationID: "reservation-" + agentID, ReservationGeneration: 2,
		IncarnationID: "incarnation-" + agentID, IncarnationGeneration: 4, ExternalIdentityRef: "external/" + agentID,
		BindingVersion: 6, BindingDigest: proofDigest("binding-" + agentID), FenceRef: fenceRef,
		ActiveProcessReceiptRef: "process/" + agentID, State: "active", BindingSchemaVersion: 1, UpdatedAt: updatedAt}
}

func validContentSnapshot(t *testing.T, record orchestrationcommand.CompleteIntentRecord) ContentSnapshot {
	t.Helper()
	snapshot := ContentSnapshot{SchemaVersion: 1, SnapshotID: "content-snapshot-1", CommandID: record.Command.Intent.CommandID,
		CommandVersion: record.Command.CommandVersion, CommandProfileDigest: record.Profile.CompleteIntentDigest,
		CallerPrincipalRef: record.Command.Intent.CallerPrincipalRef, ScopeRef: record.Command.Intent.ScopeRef,
		SourceAgentID: record.Profile.Source.AgentID, SourceFenceRef: record.Profile.Source.FenceRef, ContentRef: record.Command.Intent.RequestContentRef,
		ObjectID: "content-object-1", ObjectVersion: 4, RefVersion: 3, IngestTicketRef: "ingest/ticket/1",
		ContentState: "active", ReferenceState: "live", KeyState: "active", SizeBytes: 128, MediaType: "text/plain",
		RetentionPolicyDigest: proofDigest("retention"), OwnerID: "content-owner-1", OwnerGeneration: 5,
		WriterFenceRef: "writer/content/5", SecurityTimeGeneration: 7, OwnerProofRef: "proof/content/7",
		ValidUntil: record.RecordedAt.Add(2 * time.Hour)}
	snapshot, err := FinalizeContentSnapshot(snapshot)
	assertProofNoError(t, err)
	return snapshot
}

func validResourceSnapshot(t *testing.T, record orchestrationcommand.CompleteIntentRecord, identityDigest, contentDigest string) ResourceSnapshot {
	t.Helper()
	snapshot := ResourceSnapshot{SchemaVersion: 1, SnapshotID: "resource-snapshot-1", CommandID: record.Command.Intent.CommandID,
		CommandVersion: record.Command.CommandVersion, CommandProfileDigest: record.Profile.CompleteIntentDigest,
		CallerPrincipalRef: record.Command.Intent.CallerPrincipalRef, ScopeRef: record.Command.Intent.ScopeRef,
		IdentityFenceDigest: identityDigest, ContentSnapshotDigest: contentDigest, ReservationID: "resource-reservation-1",
		ReservationVersion: 2, ResourceStatus: "reserved", RequestedKind: record.Command.Intent.CommandKind,
		PolicySnapshotID: "resource-policy-1", PolicyRevision: 4, PolicySnapshotDigest: proofDigest("policy"),
		EffectiveLimitsDigest: proofDigest("limits"), UsageWatermark: 0, UsageDigest: proofDigest("usage"),
		ReleaseConditionDigest: proofDigest("release"), OwnerID: "resource-owner-1", OwnerGeneration: 6,
		WriterFenceRef: "writer/resource/6", SecurityTimeGeneration: 8, OwnerProofRef: "proof/resource/8",
		ValidUntil: record.RecordedAt.Add(time.Hour)}
	snapshot, err := FinalizeResourceSnapshot(snapshot)
	assertProofNoError(t, err)
	return snapshot
}

func expectedSubmission(t *testing.T, fixture *gateTestFixture) recoverycustody.SubmissionRequest {
	t.Helper()
	authorizationDigest, err := computeAuthorizationDigest(fixture.record.Profile.Authorization)
	assertProofNoError(t, err)
	identityDigest, err := computeIdentityDigest(fixture.record.Profile.Source, fixture.record.Profile.Target)
	assertProofNoError(t, err)
	operationID, err := computeOperationID(fixture.record.Command.Intent.CommandID, 1, fixture.record.Profile.CompleteIntentDigest)
	assertProofNoError(t, err)
	input := batchDigestInput{CommandID: fixture.record.Command.Intent.CommandID, CommandVersion: 1, CommandProfileDigest: fixture.record.Profile.CompleteIntentDigest,
		AuthorizationDigest: authorizationDigest, IdentityFenceDigest: identityDigest, ContentSnapshotDigest: fixture.contentResolver.snapshot.SnapshotDigest,
		ResourceSnapshotDigest: fixture.resourceResolver.snapshot.SnapshotDigest, IdempotencyKeyDigest: fixture.record.Command.Intent.IdempotencyKeyDigest}
	batchDigest, err := computeBatchDigest(input)
	assertProofNoError(t, err)
	want := recoverycustody.SubmissionRequest{SchemaVersion: 1, OperationID: operationID, SecurityDomainID: fixture.config.SecurityDomainID,
		AuthorityProfile: fixture.config.AuthorityProfile, CommandID: fixture.record.Command.Intent.CommandID, CommandVersion: 1,
		CommandProfileDigest: fixture.record.Profile.CompleteIntentDigest, AuthorizationDigest: authorizationDigest,
		IdentityFenceDigest: identityDigest, ContentSnapshotDigest: fixture.contentResolver.snapshot.SnapshotDigest,
		ResourceSnapshotDigest: fixture.resourceResolver.snapshot.SnapshotDigest, BatchDigest: batchDigest,
		IdempotencyKeyDigest: fixture.record.Command.Intent.IdempotencyKeyDigest}
	want, err = recoverycustody.FinalizeRequestDigest(want)
	assertProofNoError(t, err)
	return want
}

func assertResolverKeys(t *testing.T, fixture *gateTestFixture) {
	t.Helper()
	wantContent := contentResolveKey(fixture.record)
	if len(fixture.contentResolver.keys) != 1 || fixture.contentResolver.keys[0] != wantContent {
		t.Fatalf("content resolver keys = %#v, want %#v", fixture.contentResolver.keys, wantContent)
	}
	identityDigest, err := computeIdentityDigest(fixture.record.Profile.Source, fixture.record.Profile.Target)
	assertProofNoError(t, err)
	wantResource := resourceResolveKey(fixture.record, identityDigest, fixture.contentResolver.snapshot.SnapshotDigest)
	if len(fixture.resourceResolver.keys) != 1 || fixture.resourceResolver.keys[0] != wantResource {
		t.Fatalf("resource resolver keys = %#v, want %#v", fixture.resourceResolver.keys, wantResource)
	}
}

func assertGateConstructorRejected(t *testing.T, gate *Gate, err error) {
	t.Helper()
	if err == nil || gate != nil {
		t.Fatalf("NewGate() = %#v, %v; want nil gate and error", gate, err)
	}
}

func assertResolverKeyFields(t *testing.T) {
	t.Helper()
	assertJSONFieldNames(t, reflect.TypeFor[ContentResolveKey](), []string{"schema_version", "command_id", "command_version", "command_profile_digest", "caller_principal_ref", "scope_ref", "source_agent_id", "source_fence_ref"})
	assertJSONFieldNames(t, reflect.TypeFor[ResourceResolveKey](), []string{"schema_version", "command_id", "command_version", "command_profile_digest", "caller_principal_ref", "scope_ref", "identity_fence_digest", "content_snapshot_digest"})
}

func assertJSONFieldNames(t *testing.T, typ reflect.Type, want []string) {
	t.Helper()
	got := make([]string, 0, typ.NumField())
	for index := 0; index < typ.NumField(); index++ {
		got = append(got, strings.Split(typ.Field(index).Tag.Get("json"), ",")[0])
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s resolver key fields = %v, want trusted-only fields %v", typ.Name(), got, want)
	}
}

func assertNoEffectResult(t *testing.T, request recoverycustody.SubmissionRequest) {
	t.Helper()
	encoded, err := json.Marshal(request)
	assertProofNoError(t, err)
	for _, forbidden := range []string{"accepted", "dispatch", "provider"} {
		if strings.Contains(strings.ToLower(string(encoded)), forbidden) {
			t.Fatalf("custody request exposes forbidden effect field %q: %s", forbidden, encoded)
		}
	}
}

func assertProofFailure(t *testing.T, request recoverycustody.SubmissionRequest, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("BuildSubmission() error = %v, want %v", err, want)
	}
	if request != (recoverycustody.SubmissionRequest{}) {
		t.Fatalf("failed BuildSubmission() request = %#v, want zero", request)
	}
}

func assertProofCalls(t *testing.T, fixture *gateTestFixture, want [4]int) {
	t.Helper()
	got := [4]int{fixture.contentResolver.calls, fixture.contentVerifier.calls, fixture.resourceResolver.calls, fixture.resourceVerifier.calls}
	if got != want {
		t.Fatalf("content resolver/verifier, resource resolver/verifier calls = %v, want %v", got, want)
	}
}

func corruptContentSnapshot(_ *testing.T, fixture *gateTestFixture) {
	fixture.contentResolver.snapshot.SnapshotDigest = proofDigest("corrupt-content")
}

func mismatchContentSnapshot(t *testing.T, fixture *gateTestFixture) {
	t.Helper()
	fixture.contentResolver.snapshot.ContentRef = "content-owner://canonical/other"
	var err error
	fixture.contentResolver.snapshot, err = FinalizeContentSnapshot(fixture.contentResolver.snapshot)
	assertProofNoError(t, err)
}

func failContentVerifier(_ *testing.T, fixture *gateTestFixture) {
	fixture.contentVerifier.err = errors.New("test CONTENT signature rejected")
}

func corruptResourceSnapshot(_ *testing.T, fixture *gateTestFixture) {
	fixture.resourceResolver.snapshot.SnapshotDigest = proofDigest("corrupt-resource")
}

func mismatchResourceSnapshot(t *testing.T, fixture *gateTestFixture) {
	t.Helper()
	fixture.resourceResolver.snapshot.CommandID = "command-proof-other"
	var err error
	fixture.resourceResolver.snapshot, err = FinalizeResourceSnapshot(fixture.resourceResolver.snapshot)
	assertProofNoError(t, err)
}

func failResourceVerifier(_ *testing.T, fixture *gateTestFixture) {
	fixture.resourceVerifier.err = errors.New("test RESOURCE signature rejected")
}

type snapshotDigestComputer func(reflect.Value) (string, error)

func assertSnapshotFieldsBound(t *testing.T, name string, snapshot any, compute snapshotDigestComputer) {
	t.Helper()
	original := reflect.ValueOf(snapshot)
	want, err := compute(original)
	assertProofNoError(t, err)
	for index := 0; index < original.NumField(); index++ {
		fieldName := original.Type().Field(index).Name
		if fieldName == "SnapshotDigest" {
			continue
		}
		t.Run(name+"_"+fieldName, func(t *testing.T) {
			mutated := reflect.New(original.Type()).Elem()
			mutated.Set(original)
			mutateSnapshotField(t, mutated.Field(index))
			got, err := compute(mutated)
			assertProofNoError(t, err)
			if got == want {
				t.Fatalf("changing %s.%s did not change digest", name, fieldName)
			}
		})
	}
	mutated := reflect.New(original.Type()).Elem()
	mutated.Set(original)
	mutated.FieldByName("SnapshotDigest").SetString(proofDigest("ignored-self-digest"))
	got, err := compute(mutated)
	assertProofNoError(t, err)
	if got != want {
		t.Fatalf("%s self digest unexpectedly participates in its own digest", name)
	}
}

func mutateSnapshotField(t *testing.T, field reflect.Value) {
	t.Helper()
	if field.Type() == reflect.TypeFor[time.Time]() {
		field.Set(reflect.ValueOf(field.Interface().(time.Time).Add(time.Second)))
		return
	}
	switch field.Kind() {
	case reflect.String:
		field.SetString(field.String() + "-changed")
	case reflect.Int64:
		field.SetInt(field.Int() + 1)
	default:
		t.Fatalf("unsupported snapshot field type %s", field.Type())
	}
}

func proofDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func proofProfileKey() []byte {
	return []byte("admissionproof-test-real-hmac-key-material-v1")
}

func assertProofNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
