package orchestrationcommand_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	orchestrationcommand "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/orchestrationcommand"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	sqliteruntime "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
)

const profileDigestKeyID = "profile-contract-key"

type profileStoreFixture struct {
	db         *sql.DB
	commands   *orchestrationcommand.Store
	profiles   *orchestrationcommand.SendProfileStore
	identities *identity.Store
	source     identity.CurrentBinding
	target     identity.CurrentBinding
	intent     orchestrationcommand.Intent
	profile    orchestrationcommand.SendMessageProfile
}

type profileDigestKeyResolver struct{}

func (profileDigestKeyResolver) ResolveSendProfileDigestKey(_ context.Context, keyID string, version int64) ([]byte, error) {
	if keyID != profileDigestKeyID || version != 1 {
		return nil, fmt.Errorf("unknown profile digest key %q/%d", keyID, version)
	}
	return profileKeyMaterial(), nil
}

type profileCurrentBindingReader struct{ store *identity.Store }

func (r profileCurrentBindingReader) GetSendProfileCurrentBinding(ctx context.Context, dbtx storedb.DBTX, agentID string) (orchestrationcommand.ActiveBindingSelector, error) {
	binding, err := r.store.GetCurrentWithDBTX(ctx, dbtx, agentID)
	if err != nil {
		return orchestrationcommand.ActiveBindingSelector{}, err
	}
	return selectorFromBinding(binding)
}

type profileReserveResult struct {
	record orchestrationcommand.CompleteIntentRecord
	err    error
}

func TestSendMessageProfileStoreContract(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{"fresh_atomic_non_authorizing", testProfileFreshAtomic},
		{"exact_replay_returns_canonical_id", testProfileExactReplayCanonicalID},
		{"profile_conflict", testProfileConflict},
		{"base_only_missing_fails_closed", testProfileBaseOnlyMissing},
		{"relation_failure_rolls_back", testProfileRelationRollback},
		{"stale_binding_rolls_back", testProfileStaleBindingRollback},
		{"deletion_fence_conflict_rolls_back", testProfileDeletionFenceConflictRollback},
		{"legacy_content_quarantine_fails_closed", testProfileLegacyContentQuarantine},
		{"concurrent_reserve_single_canonical_winner", testProfileConcurrentReserve},
		{"close_reopen_preserves_record", testProfileCloseReopen},
		{"digest_mismatch_zero_writes", testProfileDigestMismatch},
		{"nil_context_fail_closed", testProfileStoreNilContextFailsClosed},
	}
	for _, test := range tests {
		t.Run(test.name, test.run)
	}
}

// testProfileStoreNilContextFailsClosed covers all profile-store entry points
// without allowing a nil context to reach the verifier, binding reader or DB.
func testProfileStoreNilContextFailsClosed(t *testing.T) {
	fixture := newProfileStoreFixture(t, filepath.Join(t.TempDir(), "nil-context.sqlite"))
	key := profileKey(fixture.intent)
	var nilCtx context.Context
	if _, err := fixture.profiles.Reserve(nilCtx, fixture.intent, fixture.profile); !errors.Is(err, orchestrationcommand.ErrContextRequired) {
		t.Fatalf("Reserve(nil context) error = %v, want ErrContextRequired", err)
	}
	if _, err := fixture.profiles.Get(nilCtx, fixture.intent.CommandID); !errors.Is(err, orchestrationcommand.ErrContextRequired) {
		t.Fatalf("Get(nil context) error = %v, want ErrContextRequired", err)
	}
	if _, err := fixture.profiles.GetByKey(nilCtx, key); !errors.Is(err, orchestrationcommand.ErrContextRequired) {
		t.Fatalf("GetByKey(nil context) error = %v, want ErrContextRequired", err)
	}
	assertProfileRows(t, fixture.db, 2, 0)
}

func testProfileLegacyContentQuarantine(t *testing.T) {
	fixture := newProfileStoreFixture(t, filepath.Join(t.TempDir(), "legacy-quarantine.sqlite"))
	canonical := reserveProfile(t, fixture.profiles, fixture.intent, fixture.profile)
	_, err := fixture.db.ExecContext(t.Context(), `INSERT INTO orchestration_legacy_command_content_quarantine (
		command_id, complete_intent_digest, reason, source_schema_version, schema_version
	) VALUES (?, ?, 'legacy_missing_content_owner_ref', 131, 1)`,
		canonical.Command.Intent.CommandID, canonical.Profile.CompleteIntentDigest)
	if err != nil {
		t.Fatalf("insert legacy CONTENT quarantine fixture: %v", err)
	}
	assertLegacyContentProfile(t, func() error {
		_, err := fixture.profiles.Get(t.Context(), canonical.Command.Intent.CommandID)
		return err
	})
	assertLegacyContentProfile(t, func() error {
		_, err := fixture.profiles.GetByKey(t.Context(), profileKey(fixture.intent))
		return err
	})
	assertLegacyContentProfile(t, func() error {
		_, err := fixture.profiles.Reserve(t.Context(), fixture.intent, fixture.profile)
		return err
	})
	assertProfileRows(t, fixture.db, 3, 1)
}

func testProfileFreshAtomic(t *testing.T) {
	fixture := newProfileStoreFixture(t, filepath.Join(t.TempDir(), "fresh.sqlite"))
	record := reserveProfile(t, fixture.profiles, fixture.intent, fixture.profile)
	if record.Command.Intent.CommandID != fixture.intent.CommandID || record.Profile != fixture.profile {
		t.Fatalf("fresh record = %#v, want exact command/profile", record)
	}
	if record.SchemaVersion != 1 || record.RecordedAt.IsZero() {
		t.Fatalf("fresh record metadata = %#v, want schema 1 and timestamp", record)
	}
	byID := getProfile(t, fixture.profiles, record.Command.Intent.CommandID)
	byKey := getProfileByKey(t, fixture.profiles, fixture.intent)
	if !reflect.DeepEqual(record, byID) || !reflect.DeepEqual(record, byKey) {
		t.Fatalf("fresh reads drifted: reserve=%#v byID=%#v byKey=%#v", record, byID, byKey)
	}
	assertProfileRows(t, fixture.db, 3, 1)
	assertProfileContentDeletionFence(t, fixture.db, record, fixture.intent.RequestContentRef)
	assertNoExecutionSideEffects(t, fixture.db)
}

func testProfileExactReplayCanonicalID(t *testing.T) {
	fixture := newProfileStoreFixture(t, filepath.Join(t.TempDir(), "replay.sqlite"))
	first := reserveProfile(t, fixture.profiles, fixture.intent, fixture.profile)
	candidate := fixture.intent
	candidate.CommandID = "send-command-replay-candidate"
	replay := reserveProfile(t, fixture.profiles, candidate, fixture.profile)
	if !reflect.DeepEqual(replay, first) || replay.Command.Intent.CommandID != fixture.intent.CommandID {
		t.Fatalf("canonical replay = %#v, want original %#v", replay, first)
	}
	assertProfileRows(t, fixture.db, 3, 1)
	assertNoExecutionSideEffects(t, fixture.db)
}

func testProfileConflict(t *testing.T) {
	fixture := newProfileStoreFixture(t, filepath.Join(t.TempDir(), "conflict.sqlite"))
	reserveProfile(t, fixture.profiles, fixture.intent, fixture.profile)
	changed := fixture.profile
	changed.Caller.AuthContext += "-changed"
	_, err := fixture.profiles.Reserve(t.Context(), fixture.intent, changed)
	if !errors.Is(err, orchestrationcommand.ErrProfileConflict) {
		t.Fatalf("profile conflict error = %v, want ErrProfileConflict", err)
	}
	assertProfileRows(t, fixture.db, 3, 1)
	assertNoExecutionSideEffects(t, fixture.db)
}

func testProfileBaseOnlyMissing(t *testing.T) {
	fixture := newProfileStoreFixture(t, filepath.Join(t.TempDir(), "base-only.sqlite"))
	if _, err := fixture.commands.Reserve(t.Context(), fixture.intent); err != nil {
		t.Fatalf("reserve base-only intent: %v", err)
	}
	assertIncompleteProfile(t, func() error { _, err := fixture.profiles.Get(t.Context(), fixture.intent.CommandID); return err })
	assertIncompleteProfile(t, func() error { _, err := fixture.profiles.GetByKey(t.Context(), profileKey(fixture.intent)); return err })
	assertIncompleteProfile(t, func() error {
		_, err := fixture.profiles.Reserve(t.Context(), fixture.intent, fixture.profile)
		return err
	})
	assertProfileRows(t, fixture.db, 3, 0)
	assertNoExecutionSideEffects(t, fixture.db)
}

func testProfileRelationRollback(t *testing.T) {
	fixture := newProfileStoreFixture(t, filepath.Join(t.TempDir(), "relation.sqlite"))
	profile := fixture.profile
	profile.Authorization.GrantUpdatedAt = profile.Authorization.GrantUpdatedAt.Add(time.Second)
	intent, profile := signProfile(t, fixture.intent, profile)
	_, err := fixture.profiles.Reserve(t.Context(), intent, profile)
	if !errors.Is(err, orchestrationcommand.ErrProfileRelation) {
		t.Fatalf("relation mismatch error = %v, want ErrProfileRelation", err)
	}
	assertProfileRows(t, fixture.db, 2, 0)
	assertNoExecutionSideEffects(t, fixture.db)
}

func testProfileStaleBindingRollback(t *testing.T) {
	fixture := newProfileStoreFixture(t, filepath.Join(t.TempDir(), "stale.sqlite"))
	request := identity.RevokeBindingRequest{AgentID: fixture.target.AgentID, ExpectedBindingVersion: fixture.target.BindingVersion,
		IncarnationID: fixture.target.IncarnationID, SourceFenceRef: fixture.target.SourceFenceRef}
	if _, err := fixture.identities.RevokeBinding(t.Context(), request); err != nil {
		t.Fatalf("revoke target binding: %v", err)
	}
	_, err := fixture.profiles.Reserve(t.Context(), fixture.intent, fixture.profile)
	if !errors.Is(err, orchestrationcommand.ErrBindingNotActive) {
		t.Fatalf("stale binding error = %v, want ErrBindingNotActive", err)
	}
	assertProfileRows(t, fixture.db, 2, 0)
	assertNoExecutionSideEffects(t, fixture.db)
}

func testProfileDeletionFenceConflictRollback(t *testing.T) {
	fixture := newProfileStoreFixture(t, filepath.Join(t.TempDir(), "fence-conflict.sqlite"))
	canonical := reserveProfile(t, fixture.profiles, fixture.intent, fixture.profile)
	candidate := fixture.intent
	candidate.CommandID = "send-command-fence-conflict"
	candidate.IdempotencyNamespace = "profile/send-command/fence-conflict"
	candidate.IdempotencyKeyDigest = profileHash("send-command-fence-conflict-key")
	candidate, profile := signProfile(t, candidate, fixture.profile)

	_, err := fixture.profiles.Reserve(t.Context(), candidate, profile)
	if !errors.Is(err, orchestrationcommand.ErrProfileRelation) {
		t.Fatalf("deletion fence conflict error = %v, want ErrProfileRelation", err)
	}
	assertProfileRows(t, fixture.db, 3, 1)
	assertProfileCommandAbsent(t, fixture.db, candidate.CommandID)
	assertProfileContentDeletionFence(t, fixture.db, canonical, fixture.intent.RequestContentRef)
}

func testProfileConcurrentReserve(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent.sqlite")
	fixture := newProfileStoreFixture(t, path)
	dbTwo := openProfileDB(t, path)
	second := newProfileStore(t, dbTwo, fixture.identities)
	candidate := fixture.intent
	candidate.CommandID = "send-command-concurrent-candidate"
	start := make(chan struct{})
	results := make(chan profileReserveResult, 2)
	var workers sync.WaitGroup
	workers.Go(func() { reserveProfileConcurrent(start, results, fixture.profiles, fixture.intent, fixture.profile) })
	workers.Go(func() { reserveProfileConcurrent(start, results, second, candidate, fixture.profile) })
	close(start)
	workers.Wait()
	close(results)
	assertConcurrentProfileResults(t, results)
	assertProfileRows(t, fixture.db, 3, 1)
	assertNoExecutionSideEffects(t, fixture.db)
}

func testProfileCloseReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reopen.sqlite")
	fixture := newProfileStoreFixture(t, path)
	first := reserveProfile(t, fixture.profiles, fixture.intent, fixture.profile)
	if err := fixture.db.Close(); err != nil {
		t.Fatalf("close profile database: %v", err)
	}
	reopenedDB := openProfileDB(t, path)
	reopenedIdentity, err := identity.NewStore(reopenedDB)
	if err != nil {
		t.Fatalf("reopen identity store: %v", err)
	}
	reopened := newProfileStore(t, reopenedDB, reopenedIdentity)
	got := getProfileByKey(t, reopened, fixture.intent)
	if !reflect.DeepEqual(got, first) {
		t.Fatalf("reopened record = %#v, want %#v", got, first)
	}
	assertProfileRows(t, reopenedDB, 3, 1)
	assertNoExecutionSideEffects(t, reopenedDB)
}

func testProfileDigestMismatch(t *testing.T) {
	fixture := newProfileStoreFixture(t, filepath.Join(t.TempDir(), "digest.sqlite"))
	forged := profileHash("forged-complete-profile-digest")
	intent, profile := fixture.intent, fixture.profile
	intent.RequestDigest, profile.CompleteIntentDigest = forged, forged
	_, err := fixture.profiles.Reserve(t.Context(), intent, profile)
	if !errors.Is(err, orchestrationcommand.ErrProfileConflict) {
		t.Fatalf("digest mismatch error = %v, want ErrProfileConflict", err)
	}
	assertProfileRows(t, fixture.db, 2, 0)
	assertNoExecutionSideEffects(t, fixture.db)
}

func newProfileStoreFixture(t *testing.T, path string) profileStoreFixture {
	t.Helper()
	db := openProfileDB(t, path)
	commands, err := orchestrationcommand.NewStore(db)
	if err != nil {
		t.Fatalf("new command store: %v", err)
	}
	identities, err := identity.NewStore(db)
	if err != nil {
		t.Fatalf("new identity store: %v", err)
	}
	source := createProfileIdentity(t, commands, identities, "source")
	target := createProfileIdentity(t, commands, identities, "target")
	access := newRealAccessStore(t, db, identities)
	grant, request, authorization := createSendGrant(t, access, source, target)
	contentRef := createPromotedProfileContent(t, db, source, grant, authorization)
	intent, profile := profileRequest(t, source, target, grant, request, authorization, contentRef)
	profiles := newProfileStore(t, db, identities)
	return profileStoreFixture{db: db, commands: commands, profiles: profiles, identities: identities,
		source: source, target: target, intent: intent, profile: profile}
}

func openProfileDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sqliteruntime.Open(t.Context(), sqliteruntime.OpenOptions{Path: path})
	if err != nil {
		t.Fatalf("open profile sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqliteruntime.RunEmbeddedMigrations(t.Context(), db); err != nil {
		t.Fatalf("run profile migrations: %v", err)
	}
	return db
}

func newProfileStore(t *testing.T, db *sql.DB, identities *identity.Store) *orchestrationcommand.SendProfileStore {
	t.Helper()
	verifier, err := orchestrationcommand.NewHMACSendProfileDigestVerifier(profileDigestKeyResolver{})
	if err != nil {
		t.Fatalf("new real HMAC verifier: %v", err)
	}
	store, err := orchestrationcommand.NewSendProfileStore(db, verifier, profileCurrentBindingReader{store: identities})
	if err != nil {
		t.Fatalf("new send profile store: %v", err)
	}
	return store
}

func newRealAccessStore(t *testing.T, db *sql.DB, identities *identity.Store) *accesswait.Store {
	t.Helper()
	fenceVerifier, err := eventstore.NewIdentityFenceVerifier(identities)
	if err != nil {
		t.Fatalf("new event fence verifier: %v", err)
	}
	events, err := eventstore.NewStore(db, fenceVerifier)
	if err != nil {
		t.Fatalf("new event store: %v", err)
	}
	store, err := accesswait.NewStore(db, identities, events)
	if err != nil {
		t.Fatalf("new access wait store: %v", err)
	}
	return store
}

func createProfileIdentity(t *testing.T, commands *orchestrationcommand.Store, identities *identity.Store, suffix string) identity.CurrentBinding {
	t.Helper()
	commandID := "profile-launch-" + suffix
	launch := orchestrationcommand.Intent{CommandID: commandID, CommandKind: "launch_agent", CallerPrincipalRef: "launcher/" + suffix,
		ScopeRef: "profile/scope", IdempotencyNamespace: "profile/launch/" + suffix, IdempotencyKeyDigest: profileHash("launch-key-" + suffix),
		RequestDigest: profileHash("launch-request-" + suffix), DigestKeyID: "launch-key", RequestContentRef: "launch-content/" + suffix,
		SourceFenceRef: "launch-source/" + suffix, TargetFenceRef: "launch-target/" + suffix}
	if _, err := commands.Reserve(t.Context(), launch); err != nil {
		t.Fatalf("reserve launch intent %s: %v", suffix, err)
	}
	reservation := reserveProfileIdentity(t, identities, suffix, commandID)
	incarnation := createProfileIncarnation(t, identities, suffix, reservation)
	return finalizeProfileBinding(t, identities, suffix, reservation, incarnation)
}

func reserveProfileIdentity(t *testing.T, identities *identity.Store, suffix, commandID string) identity.Reservation {
	t.Helper()
	request := identity.ReservationRequest{ReservationID: "profile-reservation-" + suffix, CommandID: commandID, AgentID: "profile-agent-" + suffix,
		IdentityKind: "native_subagent", SourceKind: "first_launch", CallerPrincipalRef: "launcher/" + suffix, ScopeRef: "profile/scope",
		ParentAgentID: "profile-root", IdempotencyNamespace: "profile/reservation/" + suffix,
		IdempotencyKeyDigest: profileHash("reservation-key-" + suffix), RequestDigest: profileHash("reservation-request-" + suffix),
		SourceFenceRef: "launch-source/" + suffix, TargetFenceRef: "launch-target/" + suffix, ReservationGeneration: 1}
	reservation, err := identities.Reserve(t.Context(), request)
	if err != nil {
		t.Fatalf("reserve identity %s: %v", suffix, err)
	}
	return reservation
}

func createProfileIncarnation(t *testing.T, identities *identity.Store, suffix string, reservation identity.Reservation) identity.Incarnation {
	t.Helper()
	request := identity.IncarnationRequest{IncarnationID: "profile-incarnation-" + suffix, ReservationID: reservation.ReservationID,
		AgentID: reservation.AgentID, IdentityKind: reservation.IdentityKind, ExternalIdentityRef: "profile-external-" + suffix,
		ExternalEpoch: "profile-epoch-" + suffix, SourceFenceRef: reservation.SourceFenceRef,
		IdentityDigest: profileHash("identity-" + suffix), IncarnationGeneration: 1}
	incarnation, err := identities.CreateIncarnation(t.Context(), request)
	if err != nil {
		t.Fatalf("create incarnation %s: %v", suffix, err)
	}
	return incarnation
}

func finalizeProfileBinding(t *testing.T, identities *identity.Store, suffix string, reservation identity.Reservation, incarnation identity.Incarnation) identity.CurrentBinding {
	t.Helper()
	preparation, err := identities.PrepareBinding(t.Context(), identity.BindingPreparationRequest{PreparationID: "profile-preparation-" + suffix,
		ReservationID: reservation.ReservationID, IncarnationID: incarnation.IncarnationID, AgentID: reservation.AgentID,
		IdentityKind: reservation.IdentityKind, SourceFenceRef: reservation.SourceFenceRef, BindingDigest: profileHash("binding-" + suffix),
		PreparationGeneration: 1, ExpectedBindingVersion: 0, ExpectedIncarnationGeneration: 1})
	if err != nil {
		t.Fatalf("prepare binding %s: %v", suffix, err)
	}
	binding, err := identities.FinalizeBinding(t.Context(), identity.FinalizeBindingRequest{PreparationID: preparation.PreparationID,
		AgentID: reservation.AgentID, ExpectedPreparationGeneration: 1, ExpectedBindingVersion: 0,
		IncarnationID: incarnation.IncarnationID, ExternalIdentityRef: incarnation.ExternalIdentityRef, IdentityKind: reservation.IdentityKind,
		SourceFenceRef: reservation.SourceFenceRef, ActiveProcessReceiptRef: "profile-process-receipt-" + suffix, BindingDigest: preparation.BindingDigest})
	if err != nil {
		t.Fatalf("finalize binding %s: %v", suffix, err)
	}
	return binding
}

func createSendGrant(t *testing.T, store *accesswait.Store, source, target identity.CurrentBinding) (accesswait.AccessGrant, accesswait.AccessRequest, accesswait.Authorization) {
	t.Helper()
	issuedAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	grantRequest := accesswait.AccessGrantRequest{GrantID: "profile-send-grant", CallerPrincipalRef: "profile/principal", ScopeRef: "profile/scope",
		Source: accessFence(source), Target: accessFence(target), CanSend: true,
		IdempotencyNamespace: "profile/grant", IdempotencyKeyDigest: profileHash("grant-key"), RequestDigest: profileHash("grant-request"),
		IssuedAt: issuedAt, ExpiresAt: issuedAt.Add(2 * time.Hour)}
	grant, err := store.CreateGrant(t.Context(), grantRequest)
	if err != nil {
		t.Fatalf("create send grant: %v", err)
	}
	request := accesswait.AccessRequest{RequestID: "profile-send-authorization", GrantID: grant.GrantID,
		CallerPrincipalRef: grant.CallerPrincipalRef, ScopeRef: grant.ScopeRef, Capability: "send",
		Source: grant.Source, Target: grant.Target, RequestDigest: profileHash("authorization-request"), RequestedAt: time.Now().UTC()}
	authorization, err := store.Authorize(t.Context(), request)
	if err != nil {
		t.Fatalf("authorize send grant: %v", err)
	}
	return grant, request, authorization
}

func profileRequest(t *testing.T, source, target identity.CurrentBinding, grant accesswait.AccessGrant, request accesswait.AccessRequest, authorization accesswait.Authorization, contentRef string) (orchestrationcommand.Intent, orchestrationcommand.SendMessageProfile) {
	t.Helper()
	intent := orchestrationcommand.Intent{CommandID: "send-command-canonical", CommandKind: "send_message",
		CallerPrincipalRef: grant.CallerPrincipalRef, ScopeRef: grant.ScopeRef, IdempotencyNamespace: "profile/send-command",
		IdempotencyKeyDigest: profileHash("send-command-key"), RequestDigest: profileHash("placeholder"), DigestKeyID: profileDigestKeyID,
		RequestContentRef: contentRef, SourceFenceRef: source.SourceFenceRef, TargetFenceRef: target.SourceFenceRef}
	profile := orchestrationcommand.SendMessageProfile{Kind: orchestrationcommand.SendMessageProfileKind, Version: 1,
		Caller:        orchestrationcommand.CallerSnapshot{Profile: "standalone_http_local_v1", AuthContext: "profile-auth-context", ExpiresAt: authorization.ExpiresAt},
		Authorization: authorizationDecision(grant, request, authorization), Source: mustSelector(t, source), Target: mustSelector(t, target),
		CompleteIntentDigest: intent.RequestDigest, DigestDomain: orchestrationcommand.SendMessageDigestDomain,
		DigestAlgorithm: orchestrationcommand.HMACSHA256DigestAlgorithm, DigestKeyID: profileDigestKeyID, DigestKeyVersion: 1}
	return signProfile(t, intent, profile)
}

func authorizationDecision(grant accesswait.AccessGrant, request accesswait.AccessRequest, authorization accesswait.Authorization) orchestrationcommand.AuthorizationDecision {
	return orchestrationcommand.AuthorizationDecision{Kind: orchestrationcommand.AccessGrantAuthorizationKind, Decision: "allowed", RequestVersion: 1,
		RequestID: request.RequestID, RequestDigest: request.RequestDigest, GrantID: authorization.GrantID,
		GrantRevision: authorization.GrantRevision, Capability: authorization.Capability, RequestedAt: request.RequestedAt,
		DecidedAt: authorization.AuthorizedAt, ExpiresAt: authorization.ExpiresAt, GrantRequestDigest: grant.RequestDigest,
		GrantIssuedAt: grant.IssuedAt, GrantUpdatedAt: grant.UpdatedAt}
}

func selectorFromBinding(binding identity.CurrentBinding) (orchestrationcommand.ActiveBindingSelector, error) {
	updatedAt, err := time.Parse(time.RFC3339Nano, binding.UpdatedAt)
	if err != nil {
		return orchestrationcommand.ActiveBindingSelector{}, err
	}
	return orchestrationcommand.ActiveBindingSelector{Version: 1, Kind: orchestrationcommand.ActiveBindingSelectorKind,
		AgentID: binding.AgentID, IdentityKind: binding.IdentityKind, ReservationID: binding.ReservationID,
		ReservationGeneration: binding.ReservationGeneration, IncarnationID: binding.IncarnationID,
		IncarnationGeneration: binding.IncarnationGeneration, ExternalIdentityRef: binding.ExternalIdentityRef,
		BindingVersion: binding.BindingVersion, BindingDigest: binding.BindingDigest, FenceRef: binding.SourceFenceRef,
		ActiveProcessReceiptRef: binding.ActiveProcessReceiptRef, State: binding.State,
		BindingSchemaVersion: binding.SchemaVersion, UpdatedAt: updatedAt}, nil
}

func mustSelector(t *testing.T, binding identity.CurrentBinding) orchestrationcommand.ActiveBindingSelector {
	t.Helper()
	selector, err := selectorFromBinding(binding)
	if err != nil {
		t.Fatalf("map active binding selector: %v", err)
	}
	return selector
}

func accessFence(binding identity.CurrentBinding) accesswait.FenceSnapshot {
	return accesswait.FenceSnapshot{AgentID: binding.AgentID, ReservationID: binding.ReservationID,
		ReservationGeneration: binding.ReservationGeneration, IncarnationID: binding.IncarnationID,
		IncarnationGeneration: binding.IncarnationGeneration, BindingVersion: binding.BindingVersion,
		BindingDigest: binding.BindingDigest, SourceFenceRef: binding.SourceFenceRef}
}

func signProfile(t *testing.T, intent orchestrationcommand.Intent, profile orchestrationcommand.SendMessageProfile) (orchestrationcommand.Intent, orchestrationcommand.SendMessageProfile) {
	t.Helper()
	digest, err := orchestrationcommand.ComputeSendProfileDigest(profileKeyMaterial(), intent, profile)
	if err != nil {
		t.Fatalf("compute real profile HMAC: %v", err)
	}
	intent.RequestDigest, profile.CompleteIntentDigest = digest, digest
	return intent, profile
}

func reserveProfile(t *testing.T, store *orchestrationcommand.SendProfileStore, intent orchestrationcommand.Intent, profile orchestrationcommand.SendMessageProfile) orchestrationcommand.CompleteIntentRecord {
	t.Helper()
	record, err := store.Reserve(t.Context(), intent, profile)
	if err != nil {
		t.Fatalf("reserve send profile: %v", err)
	}
	return record
}

func getProfile(t *testing.T, store *orchestrationcommand.SendProfileStore, commandID string) orchestrationcommand.CompleteIntentRecord {
	t.Helper()
	record, err := store.Get(t.Context(), commandID)
	if err != nil {
		t.Fatalf("get send profile: %v", err)
	}
	return record
}

func getProfileByKey(t *testing.T, store *orchestrationcommand.SendProfileStore, intent orchestrationcommand.Intent) orchestrationcommand.CompleteIntentRecord {
	t.Helper()
	record, err := store.GetByKey(t.Context(), profileKey(intent))
	if err != nil {
		t.Fatalf("get send profile by key: %v", err)
	}
	return record
}

func profileKey(intent orchestrationcommand.Intent) orchestrationcommand.Key {
	return orchestrationcommand.Key{Namespace: intent.IdempotencyNamespace, Digest: intent.IdempotencyKeyDigest}
}

func assertIncompleteProfile(t *testing.T, call func() error) {
	t.Helper()
	if err := call(); !errors.Is(err, orchestrationcommand.ErrIncompleteIntent) {
		t.Fatalf("incomplete profile error = %v, want ErrIncompleteIntent", err)
	}
}

func assertLegacyContentProfile(t *testing.T, call func() error) {
	t.Helper()
	err := call()
	if !errors.Is(err, orchestrationcommand.ErrIncompleteIntent) ||
		!errors.Is(err, orchestrationcommand.ErrLegacyContentProofMissing) {
		t.Fatalf("legacy profile error = %v, want incomplete and legacy CONTENT errors", err)
	}
}

func assertProfileRows(t *testing.T, db *sql.DB, commands, profiles int) {
	t.Helper()
	assertTableRows(t, db, "orchestration_command_intents", commands)
	assertTableRows(t, db, "orchestration_command_intent_profiles", profiles)
	assertTableRows(t, db, "orchestration_command_content_deletion_fences", profiles)
}

func assertProfileCommandAbsent(t *testing.T, db *sql.DB, commandID string) {
	t.Helper()
	for _, table := range []string{
		"orchestration_command_intents",
		"orchestration_command_intent_profiles",
		"orchestration_command_content_deletion_fences",
	} {
		var count int
		if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table+" WHERE command_id = ?", commandID).Scan(&count); err != nil {
			t.Fatalf("count rollback rows in %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s rows for rolled-back command %q = %d, want 0", table, commandID, count)
		}
	}
}

func assertNoExecutionSideEffects(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, table := range []string{"orchestration_command_journal", "orchestration_direct_peer_grants",
		"orchestration_direct_peer_messages", "orchestration_direct_peer_events", "orchestration_direct_peer_event_outbox"} {
		assertTableRows(t, db, table, 0)
	}
}

func assertTableRows(t *testing.T, db *sql.DB, table string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("%s rows = %d, want %d", table, got, want)
	}
}

func reserveProfileConcurrent(start <-chan struct{}, results chan<- profileReserveResult, store *orchestrationcommand.SendProfileStore, intent orchestrationcommand.Intent, profile orchestrationcommand.SendMessageProfile) {
	<-start
	record, err := store.Reserve(context.Background(), intent, profile)
	results <- profileReserveResult{record: record, err: err}
}

func assertConcurrentProfileResults(t *testing.T, results <-chan profileReserveResult) {
	t.Helper()
	var records []orchestrationcommand.CompleteIntentRecord
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent reserve error = %v", result.err)
		}
		records = append(records, result.record)
	}
	if len(records) != 2 || !reflect.DeepEqual(records[0], records[1]) {
		t.Fatalf("concurrent records = %#v, want two identical canonical results", records)
	}
}

func profileHash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func profileKeyMaterial() []byte {
	return []byte("profile-contract-real-hmac-key-material-v1")
}
