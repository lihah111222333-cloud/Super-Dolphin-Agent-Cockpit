package resourceadmission

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

func TestPublishEffectivePolicyClosedVectorAndDerivedDigests(t *testing.T) {
	fixture := newResourceStoreFixture(t, resourceFixturePath(t, "policy-derived"))
	request := resourcePolicyRequest(t, fixture, 1, "derived")
	assertClosedResourceVector(t, request.EffectiveLimits)
	wantApplicability, wantLimits, wantCanonical := assertResourcePolicyDerivedDigests(t, request)
	snapshot, err := fixture.store.PublishEffectivePolicy(t.Context(), request)
	resourceNoError(t, err)
	assertPublishedResourcePolicy(t, snapshot, request, wantApplicability, wantLimits, wantCanonical)
	if got := resourceTableCount(t, fixture.db, "orchestration_resource_effective_policy_snapshots"); got != 1 {
		t.Fatalf("policy row count = %d, want 1", got)
	}
}

func assertClosedResourceVector(t *testing.T, vector ResourceVector) {
	t.Helper()
	vectorType := reflect.TypeFor[ResourceVector]()
	if vectorType.NumField() != 10 {
		t.Fatalf("RESOURCE vector field count = %d, want closed set of 10", vectorType.NumField())
	}
	for index := range vectorType.NumField() {
		if reflect.ValueOf(vector).Field(index).Int() <= 0 {
			t.Fatalf("effective limit %s is not positive", vectorType.Field(index).Name)
		}
	}
}

func assertResourcePolicyDerivedDigests(t *testing.T, request PublishEffectivePolicyRequest) (string, string, string) {
	t.Helper()
	wantApplicability, err := ComputePolicyApplicabilityDigest(request)
	resourceNoError(t, err)
	wantLimits, err := ComputeResourceVectorDigest(request.EffectiveLimits)
	resourceNoError(t, err)
	wantCanonical, err := ComputeCanonicalPolicyHash(request)
	resourceNoError(t, err)
	if request.PolicyApplicabilityDigest != wantApplicability || request.EffectiveLimitsDigest != wantLimits ||
		request.CanonicalPolicyHash != wantCanonical {
		t.Fatalf("FinalizePublishEffectivePolicyRequest derived digests drifted: request=%#v", request)
	}
	return wantApplicability, wantLimits, wantCanonical
}

func assertPublishedResourcePolicy(
	t *testing.T,
	snapshot PolicySnapshot,
	request PublishEffectivePolicyRequest,
	wantApplicability string,
	wantLimits string,
	wantCanonical string,
) {
	t.Helper()
	if snapshot.EffectiveLimits != request.EffectiveLimits || snapshot.EffectiveLimitsDigest != wantLimits ||
		snapshot.PolicyApplicabilityDigest != wantApplicability || snapshot.CanonicalPolicyHash != wantCanonical {
		t.Fatalf("published policy did not preserve complete derived vector: %#v", snapshot)
	}
	if err := snapshot.Validate(); err != nil {
		t.Fatalf("published policy snapshot validation: %v", err)
	}
}

func TestPublishEffectivePolicyRejectsEveryZeroDimension(t *testing.T) {
	fixture := newResourceStoreFixture(t, resourceFixturePath(t, "policy-zero"))
	vectorType := reflect.TypeFor[ResourceVector]()
	for index := range vectorType.NumField() {
		field := vectorType.Field(index)
		t.Run(field.Name, func(t *testing.T) {
			request := resourcePolicyRequest(t, fixture, 1, "zero-"+field.Name)
			vector := reflect.ValueOf(&request.EffectiveLimits).Elem()
			vector.Field(index).SetInt(0)
			_, err := fixture.store.PublishEffectivePolicy(t.Context(), request)
			if !errors.Is(err, ErrPolicyIncomplete) {
				t.Fatalf("PublishEffectivePolicy() zero %s error = %v, want ErrPolicyIncomplete", field.Name, err)
			}
		})
	}
	if got := resourceTableCount(t, fixture.db, "orchestration_resource_effective_policy_snapshots"); got != 0 {
		t.Fatalf("zero-dimension policy writes = %d, want 0", got)
	}
}

func TestPublishEffectivePolicyReplayConflictAndCanonicalCandidates(t *testing.T) {
	fixture := newResourceStoreFixture(t, resourceFixturePath(t, "policy-replay"))
	request := resourcePolicyRequest(t, fixture, 1, "replay")
	winner, err := fixture.store.PublishEffectivePolicy(t.Context(), request)
	resourceNoError(t, err)

	replay := request
	replay.SnapshotID = "resource-policy-retry-candidate"
	replay.PublishReceiptRef = "resource-policy-retry-receipt"
	replay, err = FinalizePublishEffectivePolicyRequest(replay)
	resourceNoError(t, err)
	got, err := fixture.store.PublishEffectivePolicy(t.Context(), replay)
	resourceNoError(t, err)
	if !reflect.DeepEqual(got, winner) {
		t.Fatalf("exact policy replay = %#v, want canonical winner %#v", got, winner)
	}

	conflict := replay
	conflict.SourcePolicySetRef = "resource-policy-source/conflict"
	conflict.SnapshotID = "resource-policy-conflicting-candidate"
	conflict.PublishReceiptRef = "resource-policy-conflicting-receipt"
	conflict, err = FinalizePublishEffectivePolicyRequest(conflict)
	resourceNoError(t, err)
	if _, err := fixture.store.PublishEffectivePolicy(t.Context(), conflict); !errors.Is(err, ErrReplayConflict) {
		t.Fatalf("conflicting policy replay error = %v, want ErrReplayConflict", err)
	}
	if got := resourceTableCount(t, fixture.db, "orchestration_resource_effective_policy_snapshots"); got != 1 {
		t.Fatalf("policy rows after replay/conflict = %d, want 1", got)
	}
}

func TestPublishEffectivePolicyRevisionOwnerAndTimeFence(t *testing.T) {
	fixture := newResourceStoreFixture(t, resourceFixturePath(t, "policy-revision"))
	firstRequest := resourcePolicyRequest(t, fixture, 1, "revision-1")
	first, err := fixture.store.PublishEffectivePolicy(t.Context(), firstRequest)
	resourceNoError(t, err)

	gap := resourcePolicyRequest(t, fixture, 3, "revision-gap")
	if _, err := fixture.store.PublishEffectivePolicy(t.Context(), gap); !errors.Is(err, ErrPolicyStale) {
		t.Fatalf("revision gap error = %v, want ErrPolicyStale", err)
	}

	rollback := resourcePolicyRequest(t, fixture, 2, "revision-time-rollback")
	rollback.EffectiveAt = first.EffectiveAt.Add(-time.Nanosecond)
	rollback, err = FinalizePublishEffectivePolicyRequest(rollback)
	resourceNoError(t, err)
	if _, err := fixture.store.PublishEffectivePolicy(t.Context(), rollback); !errors.Is(err, ErrPolicyStale) {
		t.Fatalf("effective_at rollback error = %v, want ErrPolicyStale", err)
	}

	ownerDrift := resourcePolicyRequest(t, fixture, 2, "revision-owner-drift")
	ownerDrift.PolicyOwner.OwnerGeneration++
	ownerDrift, err = FinalizePublishEffectivePolicyRequest(ownerDrift)
	resourceNoError(t, err)
	if _, err := fixture.store.PublishEffectivePolicy(t.Context(), ownerDrift); !errors.Is(err, ErrWriterFence) {
		t.Fatalf("policy owner drift error = %v, want ErrWriterFence", err)
	}

	secondRequest := resourcePolicyRequest(t, fixture, 2, "revision-2")
	secondRequest.EffectiveAt = first.EffectiveAt
	secondRequest, err = FinalizePublishEffectivePolicyRequest(secondRequest)
	resourceNoError(t, err)
	second, err := fixture.store.PublishEffectivePolicy(t.Context(), secondRequest)
	resourceNoError(t, err)
	if second.Revision != 2 || second.PolicyOwner != first.PolicyOwner || second.EffectiveAt != first.EffectiveAt {
		t.Fatalf("same-owner contiguous revision = %#v, want revision 2 with preserved owner/time fence", second)
	}
	if got := resourceTableCount(t, fixture.db, "orchestration_resource_effective_policy_snapshots"); got != 2 {
		t.Fatalf("contiguous policy row count = %d, want 2", got)
	}
}

func TestReserveMissingPolicyLeavesNoResourceRows(t *testing.T) {
	fixture := newResourceStoreFixture(t, resourceFixturePath(t, "reserve-missing-policy"))
	request := resourceReserveRequest(t, fixture, "missing-policy")
	if _, err := fixture.store.Reserve(t.Context(), request); !errors.Is(err, ErrPolicyUnavailable) {
		t.Fatalf("Reserve() missing policy error = %v, want ErrPolicyUnavailable", err)
	}
	assertResourceRows(t, fixture, 0, 0)
}

func TestReserveRejectsEveryOverLimitDimension(t *testing.T) {
	fixture := newResourceStoreFixture(t, resourceFixturePath(t, "reserve-over-limit"))
	_, err := fixture.store.PublishEffectivePolicy(t.Context(), resourcePolicyRequest(t, fixture, 1, "limits"))
	resourceNoError(t, err)
	limits := resourceLimits()
	vectorType := reflect.TypeFor[ResourceVector]()
	for index := range vectorType.NumField() {
		field := vectorType.Field(index)
		t.Run(field.Name, func(t *testing.T) {
			request := resourceReserveRequest(t, fixture, "over-"+field.Name)
			requested := reflect.ValueOf(&request.Requested).Elem()
			requested.Field(index).SetInt(reflect.ValueOf(limits).Field(index).Int() + 1)
			request, err = FinalizeReserveRequest(request)
			resourceNoError(t, err)
			if _, err := fixture.store.Reserve(t.Context(), request); !errors.Is(err, ErrLimitExceeded) {
				t.Fatalf("Reserve() over %s error = %v, want ErrLimitExceeded", field.Name, err)
			}
		})
	}
	assertResourceRows(t, fixture, 0, 0)
}

func TestReserveExactReplayReturnsCanonicalCandidateIDs(t *testing.T) {
	fixture := newResourceStoreFixture(t, resourceFixturePath(t, "reserve-replay"))
	_, err := fixture.store.PublishEffectivePolicy(t.Context(), resourcePolicyRequest(t, fixture, 1, "replay"))
	resourceNoError(t, err)
	request := resourceReserveRequest(t, fixture, "replay")
	winner, err := fixture.store.Reserve(t.Context(), request)
	resourceNoError(t, err)

	replay := request
	replay.ReservationID = "resource-reservation-retry-candidate"
	replay.ResourceSnapshotID = "resource-snapshot-retry-candidate"
	replay.ReservationReceiptRef = "resource-reservation-retry-receipt"
	replay, err = FinalizeReserveRequest(replay)
	resourceNoError(t, err)
	got, err := fixture.store.Reserve(t.Context(), replay)
	resourceNoError(t, err)
	if !reflect.DeepEqual(got, winner) {
		t.Fatalf("exact reservation replay = %#v, want canonical winner %#v", got, winner)
	}
	if got.ReservationID == replay.ReservationID || got.ResourceSnapshotID == replay.ResourceSnapshotID ||
		got.ReservationReceiptRef == replay.ReservationReceiptRef {
		t.Fatalf("replay returned retry candidate identity instead of canonical winner: %#v", got)
	}
	assertResourceRows(t, fixture, 1, 0)
}

func TestReserveRevalidatesProfileBothBindingsAndContent(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *resourceStoreFixture, *ReserveRequest)
		want   error
	}{
		{name: "profile", want: ErrProfileMismatch, mutate: func(_ *testing.T, _ *resourceStoreFixture, request *ReserveRequest) {
			request.CommandProfileDigest = resourceHash("forged-command-profile")
		}},
		{name: "source_binding", want: ErrIdentityFence, mutate: func(t *testing.T, fixture *resourceStoreFixture, _ *ReserveRequest) {
			revokeResourceBinding(t, fixture, fixture.source)
		}},
		{name: "target_binding", want: ErrIdentityFence, mutate: func(t *testing.T, fixture *resourceStoreFixture, _ *ReserveRequest) {
			revokeResourceBinding(t, fixture, fixture.target)
		}},
		{name: "content", want: ErrContentMismatch, mutate: func(t *testing.T, fixture *resourceStoreFixture, _ *ReserveRequest) {
			store, err := NewStore(fixture.db, func() time.Time { return fixture.proofExpires.Add(time.Second) })
			resourceNoError(t, err)
			fixture.store = store
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newResourceStoreFixture(t, resourceFixturePath(t, "reserve-current-"+test.name))
			_, err := fixture.store.PublishEffectivePolicy(t.Context(), resourcePolicyRequest(t, fixture, 1, test.name))
			resourceNoError(t, err)
			request := resourceReserveRequest(t, fixture, test.name)
			test.mutate(t, fixture, &request)
			request, err = FinalizeReserveRequest(request)
			resourceNoError(t, err)
			if _, err := fixture.store.Reserve(t.Context(), request); !errors.Is(err, test.want) {
				t.Fatalf("Reserve() current %s drift error = %v, want %v", test.name, err, test.want)
			}
			assertResourceRows(t, fixture, 0, 0)
		})
	}
}

func TestReserveCloseReopenPreservesSnapshot(t *testing.T) {
	path := resourceFixturePath(t, "reserve-reopen")
	fixture := newResourceStoreFixture(t, path)
	_, err := fixture.store.PublishEffectivePolicy(t.Context(), resourcePolicyRequest(t, fixture, 1, "reopen"))
	resourceNoError(t, err)
	winner, err := fixture.store.Reserve(t.Context(), resourceReserveRequest(t, fixture, "reopen"))
	resourceNoError(t, err)
	resourceNoError(t, fixture.db.Close())

	reopenedDB := openResourceTestDB(t, path)
	reopenedStore, err := NewStore(reopenedDB, func() time.Time { return fixture.now })
	resourceNoError(t, err)
	got, err := reopenedStore.Get(t.Context(), winner.ReservationID)
	resourceNoError(t, err)
	resolved, err := reopenedStore.ResolveReservedSnapshot(t.Context(), resourceResolveRequest(winner))
	resourceNoError(t, err)
	if !reflect.DeepEqual(got, winner) || !reflect.DeepEqual(resolved, winner) {
		t.Fatalf("reopened resource snapshot drifted: get=%#v resolve=%#v want=%#v", got, resolved, winner)
	}
}

func TestResolveReservedSnapshotFailsClosedOnAuthorityDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *resourceStoreFixture)
		want   error
	}{
		{name: "policy", want: ErrPolicyStale, mutate: func(t *testing.T, fixture *resourceStoreFixture) {
			_, err := fixture.store.PublishEffectivePolicy(t.Context(), resourcePolicyRequest(t, fixture, 2, "resolve-policy-2"))
			resourceNoError(t, err)
		}},
		{name: "binding", want: ErrIdentityFence, mutate: func(t *testing.T, fixture *resourceStoreFixture) {
			revokeResourceBinding(t, fixture, fixture.source)
		}},
		{name: "content", want: ErrContentMismatch, mutate: func(t *testing.T, fixture *resourceStoreFixture) {
			store, err := NewStore(fixture.db, func() time.Time { return fixture.proofExpires.Add(time.Second) })
			resourceNoError(t, err)
			fixture.store = store
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newResourceStoreFixture(t, resourceFixturePath(t, "resolve-drift-"+test.name))
			_, err := fixture.store.PublishEffectivePolicy(t.Context(), resourcePolicyRequest(t, fixture, 1, "resolve-"+test.name))
			resourceNoError(t, err)
			snapshot, err := fixture.store.Reserve(t.Context(), resourceReserveRequest(t, fixture, "resolve-"+test.name))
			resourceNoError(t, err)
			if _, err := fixture.store.ResolveReservedSnapshot(t.Context(), resourceResolveRequest(snapshot)); err != nil {
				t.Fatalf("ResolveReservedSnapshot() before drift: %v", err)
			}
			test.mutate(t, fixture)
			if _, err := fixture.store.ResolveReservedSnapshot(t.Context(), resourceResolveRequest(snapshot)); !errors.Is(err, test.want) {
				t.Fatalf("ResolveReservedSnapshot() %s drift error = %v, want %v", test.name, err, test.want)
			}
			assertResourceRows(t, fixture, 1, 0)
		})
	}
}

func TestResourceUsageHasNoWriterAndTriggerRejectsDirectInsert(t *testing.T) {
	fixture := newResourceStoreFixture(t, resourceFixturePath(t, "usage-disabled"))
	apiType := reflect.TypeFor[StoreAPI]()
	for index := range apiType.NumMethod() {
		if strings.Contains(apiType.Method(index).Name, "Usage") {
			t.Fatalf("StoreAPI RESOURCE usage writer unexpectedly exists: %s", apiType.Method(index).Name)
		}
	}
	queriesType := reflect.TypeOf(storedb.New(fixture.db))
	for index := range queriesType.NumMethod() {
		name := queriesType.Method(index).Name
		if strings.Contains(name, "ResourceUsage") && name != "CountResourceUsageByReservation" {
			t.Fatalf("generated RESOURCE usage writer unexpectedly exists: %s", name)
		}
	}
	_, err := fixture.db.ExecContext(t.Context(), "INSERT INTO orchestration_resource_usage_ledger DEFAULT VALUES")
	if err == nil || !strings.Contains(err.Error(), "resource usage ledger writer is disabled in schema v133") {
		t.Fatalf("direct usage insert error = %v, want schema v133 disabled-writer trigger", err)
	}
	if got := resourceTableCount(t, fixture.db, "orchestration_resource_usage_ledger"); got != 0 {
		t.Fatalf("usage rows after rejected direct insert = %d, want 0", got)
	}
}

func assertResourceRows(t *testing.T, fixture *resourceStoreFixture, reservations, usage int64) {
	t.Helper()
	if got := resourceTableCount(t, fixture.db, "orchestration_resource_reservations"); got != reservations {
		t.Fatalf("resource reservation rows = %d, want %d", got, reservations)
	}
	if got := resourceTableCount(t, fixture.db, "orchestration_resource_usage_ledger"); got != usage {
		t.Fatalf("resource usage rows = %d, want %d", got, usage)
	}
}
