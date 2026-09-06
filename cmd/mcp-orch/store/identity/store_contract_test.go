package identity

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

const identityTestTimestamp = "2026-01-02T03:04:05.000000000Z"

// TestIdentityStoreContract 聚合 ID-1 owner 的幂等、代际、CAS、重放和字段守卫测试。
func TestIdentityStoreContract(t *testing.T) {
	checks := []struct {
		name string
		run  func(*testing.T)
	}{
		{"reserve", TestIdentityReserveCommandAndObservationContracts},
		{"reserve_replay", TestIdentityReserveReplayAndConflicts},
		{"incarnation", TestIdentityIncarnationRelationsGenerationExternalAndReplay},
		{"independent_generations", TestIdentityAllowsIndependentReservationAndIncarnationGenerations},
		{"prepare", TestIdentityPrepareIsNonAuthorizingAndFenced},
		{"finalize", TestIdentityFinalizeReceiptHistoryReplayAndActiveReject},
		{"abort", TestIdentityAbortPreservesReceiptAndRejectsReservation},
		{"revoke", TestIdentityRevokeAppendsHistoryAndReplacementUsesNewIncarnation},
		{"reopen", TestIdentityStoreCloseReopenPreservesAllRows},
		{"concurrent_reserve", TestIdentityStoreConcurrentReserveAcrossConnectionsHasOneWinner},
		{"concurrent_finalize", TestIdentityStoreConcurrentFinalizeAcrossConnectionsHasOneWinner},
		{"legacy_guard", TestIdentityStoreNeverWritesLegacyAgentTables},
		{"json_guard", TestIdentityJSONFieldGuard},
		{"mapper_guard", TestIdentityRowMappersMapEveryField},
		{"mapper_invalid", TestIdentityRowMappersRejectMalformedRows},
	}
	for _, check := range checks {
		t.Run(check.name, check.run)
	}
}

type identityChain struct {
	reservation Reservation
	incarnation Incarnation
	prep        BindingPreparationRequest
}

type reservationResult struct {
	reservation Reservation
	err         error
}

type bindingResult struct {
	binding CurrentBinding
	err     error
}

func TestIdentityReserveCommandAndObservationContracts(t *testing.T) {
	db := openIdentityTestDB(t)
	commandID := "cmd-reserve-contract"
	seedCommandIntent(t, db, commandID)
	store := mustIdentityStore(t, db)

	commandRequest := commandReservationRequest("reservation-command", commandID, "agent-command", 1)
	commandReservation, err := store.Reserve(t.Context(), commandRequest)
	if err != nil {
		t.Fatalf("Reserve(command) error = %v", err)
	}
	if commandReservation.CommandID == nil || *commandReservation.CommandID != commandID {
		t.Fatalf("command reservation command_id = %v, want %q", commandReservation.CommandID, commandID)
	}

	observationRequest := observationReservationRequest("reservation-observation", "agent-observation", 1)
	observation, err := store.Reserve(t.Context(), observationRequest)
	if err != nil {
		t.Fatalf("Reserve(observation) error = %v", err)
	}
	if observation.CommandID != nil {
		t.Fatalf("observation command_id = %v, want NULL", observation.CommandID)
	}
}

func TestIdentityReserveRejectsCommandPrincipalScopeAndFenceMismatchBeforeInsert(t *testing.T) {
	db := openIdentityTestDB(t)
	commandID := "cmd-reserve-mismatch"
	seedCommandIntent(t, db, commandID)
	store := mustIdentityStore(t, db)
	base := commandReservationRequest("reservation-mismatch", commandID, "agent-mismatch", 1)
	for _, mutate := range []struct {
		name string
		fn   func(*ReservationRequest)
	}{
		{name: "principal", fn: func(req *ReservationRequest) { req.CallerPrincipalRef += "-foreign" }},
		{name: "scope", fn: func(req *ReservationRequest) { req.ScopeRef += "-foreign" }},
		{name: "source fence", fn: func(req *ReservationRequest) { req.SourceFenceRef += "-stale" }},
		{name: "target fence", fn: func(req *ReservationRequest) { req.TargetFenceRef += "-stale" }},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			request := base
			request.ReservationID += "-" + mutate.name
			mutate.fn(&request)
			if _, err := store.Reserve(t.Context(), request); !errors.Is(err, ErrIdentityConflict) {
				t.Fatalf("Reserve(%s) error = %v, want ErrIdentityConflict", mutate.name, err)
			}
			if got := countIdentityRows(t, db, "orchestration_identity_reservations"); got != 0 {
				t.Fatalf("reservation rows after %s mismatch = %d, want 0", mutate.name, got)
			}
		})
	}
}

func TestIdentityReserveReplayAndConflicts(t *testing.T) {
	db := openIdentityTestDB(t)
	commandID := "cmd-reserve-replay"
	seedCommandIntent(t, db, commandID)
	store := mustIdentityStore(t, db)
	request := commandReservationRequest("reservation-original", commandID, "agent-replay", 1)

	first, err := store.Reserve(t.Context(), request)
	if err != nil {
		t.Fatalf("Reserve(first) error = %v", err)
	}
	replayRequest := request
	replayRequest.ReservationID = "reservation-retry-candidate"
	replay, err := store.Reserve(t.Context(), replayRequest)
	if err != nil {
		t.Fatalf("Reserve(replay) error = %v", err)
	}
	if !reflect.DeepEqual(replay, first) {
		t.Fatalf("replay = %#v, want original %#v", replay, first)
	}

	digestConflict := replayRequest
	digestConflict.RequestDigest = testDigest('c')
	_, err = store.Reserve(t.Context(), digestConflict)
	expectIdentityError(t, err, ErrIdentityConflict)

	fieldConflict := replayRequest
	fieldConflict.AgentID = "agent-other"
	_, err = store.Reserve(t.Context(), fieldConflict)
	expectIdentityError(t, err, ErrIdentityConflict)
	if got := countIdentityRows(t, db, "orchestration_identity_reservations"); got != 1 {
		t.Fatalf("reservation rows = %d, want 1 after replay/conflicts", got)
	}
}

func TestIdentityIncarnationRelationsGenerationExternalAndReplay(t *testing.T) {
	db := openIdentityTestDB(t)
	store := mustIdentityStore(t, db)
	chain := createIdentityChain(t, db, store, "incarnation-one", 1)

	exactReplayRequest := incarnationRequestFor(chain.reservation, chain.incarnation.IncarnationID, chain.incarnation.ExternalIdentityRef, chain.incarnation.ExternalEpoch)
	replay, err := store.CreateIncarnation(t.Context(), exactReplayRequest)
	if err != nil {
		t.Fatalf("CreateIncarnation(exact replay) error = %v", err)
	}
	if !reflect.DeepEqual(replay, chain.incarnation) {
		t.Fatalf("incarnation replay = %#v, want %#v", replay, chain.incarnation)
	}

	conflictingReplay := exactReplayRequest
	conflictingReplay.IncarnationID = "incarnation-retry"
	replay, err = store.CreateIncarnation(t.Context(), conflictingReplay)
	if err == nil || !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("CreateIncarnation(replayed id) error = %v, want ErrIdentityConflict", err)
	}
	if replay.IncarnationID != "" {
		t.Fatalf("conflicting replay returned incarnation %#v", replay)
	}

	wrongRelation := incarnationRequestFor(chain.reservation, "incarnation-wrong-agent", "external-wrong", "epoch-wrong")
	wrongRelation.AgentID = "agent-not-reservation"
	_, err = store.CreateIncarnation(t.Context(), wrongRelation)
	expectIdentityError(t, err, ErrIdentityConflict)

	wrongGeneration := incarnationRequestFor(chain.reservation, "incarnation-wrong-generation", "external-generation", "epoch-generation")
	wrongGeneration.IncarnationGeneration = 2
	_, err = store.CreateIncarnation(t.Context(), wrongGeneration)
	expectIdentityError(t, err, ErrIdentityFence)

	second := createIdentityChain(t, db, store, "incarnation-two", 2)
	duplicateExternal := incarnationRequestFor(second.reservation, "incarnation-duplicate-external", "external-one", "epoch-one")
	_, err = store.CreateIncarnation(t.Context(), duplicateExternal)
	expectIdentityError(t, err, ErrIdentityConflict)

	got, err := store.GetIncarnation(t.Context(), chain.incarnation.IncarnationID)
	if err != nil {
		t.Fatalf("GetIncarnation() error = %v", err)
	}
	if !reflect.DeepEqual(got, chain.incarnation) {
		t.Fatalf("incarnation readback = %#v, want %#v", got, chain.incarnation)
	}
}

func TestIdentityPrepareIsNonAuthorizingAndFenced(t *testing.T) {
	db := openIdentityTestDB(t)
	store := mustIdentityStore(t, db)
	chain := createIdentityChain(t, db, store, "prepare-contract", 1)

	prepared, err := store.PrepareBinding(t.Context(), chain.prep)
	if err != nil {
		t.Fatalf("PrepareBinding() error = %v", err)
	}
	if prepared.State != "prepared_non_authorizing" || prepared.ActiveProcessReceiptRef != "" {
		t.Fatalf("preparation = %#v, want non-authorizing empty-receipt row", prepared)
	}
	if _, err := store.GetCurrent(t.Context(), chain.reservation.AgentID); !errors.Is(err, ErrIdentityNotFound) {
		t.Fatalf("GetCurrent(before finalize) error = %v, want ErrIdentityNotFound", err)
	}

	replay, err := store.PrepareBinding(t.Context(), chain.prep)
	if err != nil {
		t.Fatalf("PrepareBinding(replay) error = %v", err)
	}
	if !reflect.DeepEqual(replay, prepared) {
		t.Fatalf("preparation replay = %#v, want %#v", replay, prepared)
	}

	stale := chain.prep
	stale.ExpectedBindingVersion = 1
	_, err = store.PrepareBinding(t.Context(), stale)
	expectIdentityError(t, err, ErrIdentityConflict)
	reservation, err := store.Get(t.Context(), chain.reservation.ReservationID)
	if err != nil {
		t.Fatalf("Get(reservation) error = %v", err)
	}
	if reservation.State != "activation_pending" {
		t.Fatalf("reservation state after preparation = %q, want activation_pending", reservation.State)
	}
}

func TestIdentityFinalizeReceiptHistoryReplayAndActiveReject(t *testing.T) {
	db := openIdentityTestDB(t)
	store := mustIdentityStore(t, db)
	chain := createIdentityChain(t, db, store, "finalize-contract", 1)
	if _, err := store.PrepareBinding(t.Context(), chain.prep); err != nil {
		t.Fatalf("PrepareBinding() error = %v", err)
	}

	finalizeRequest := finalizeRequestFor(chain, "receipt-finalize")
	active, err := store.FinalizeBinding(t.Context(), finalizeRequest)
	if err != nil {
		t.Fatalf("FinalizeBinding() error = %v", err)
	}
	assertActiveBinding(t, active, chain, "receipt-finalize", 1)
	assertIdentityRowState(t, db, "orchestration_binding_preparations", "state", "finalized")
	assertIdentityRowState(t, db, "orchestration_identity_reservations", "state", "activated")

	preparation, err := store.GetPreparation(t.Context(), chain.prep.PreparationID)
	if err != nil {
		t.Fatalf("GetPreparation(finalized) error = %v", err)
	}
	if preparation.ActiveProcessReceiptRef != "receipt-finalize" {
		t.Fatalf("finalized receipt = %q, want receipt-finalize", preparation.ActiveProcessReceiptRef)
	}
	replay, err := store.FinalizeBinding(t.Context(), finalizeRequest)
	if err != nil {
		t.Fatalf("FinalizeBinding(replay) error = %v", err)
	}
	if !reflect.DeepEqual(replay, active) {
		t.Fatalf("finalize replay = %#v, want %#v", replay, active)
	}
	if got := countIdentityRows(t, db, "orchestration_current_bindings"); got != 1 {
		t.Fatalf("binding history rows after replay = %d, want 1", got)
	}

	stale := finalizeRequest
	stale.ExpectedBindingVersion = 1
	_, err = store.FinalizeBinding(t.Context(), stale)
	expectIdentityError(t, err, ErrIdentityFence)

	activeReplacement := createIdentityChainForAgent(t, db, store, "active-replacement", chain.reservation.AgentID, 2)
	_, err = store.PrepareBinding(t.Context(), activeReplacement.prep)
	expectIdentityError(t, err, ErrIdentityFence)
	if got := countIdentityRows(t, db, "orchestration_binding_preparations"); got != 1 {
		t.Fatalf("preparation rows after active replacement rejection = %d, want 1", got)
	}
}

func TestIdentityAbortPreservesReceiptAndRejectsReservation(t *testing.T) {
	db := openIdentityTestDB(t)
	store := mustIdentityStore(t, db)
	chain := createIdentityChain(t, db, store, "abort-contract", 1)
	chain.prep.ActiveProcessReceiptRef = "receipt-cleanup"
	if _, err := store.PrepareBinding(t.Context(), chain.prep); err != nil {
		t.Fatalf("PrepareBinding() error = %v", err)
	}

	abortRequest := AbortBindingRequest{PreparationID: chain.prep.PreparationID, AgentID: chain.reservation.AgentID,
		ExpectedPreparationGeneration: chain.prep.PreparationGeneration, ReasonRef: "cleanup-reason"}
	aborted, err := store.AbortBinding(t.Context(), abortRequest)
	if err != nil {
		t.Fatalf("AbortBinding() error = %v", err)
	}
	if aborted.State != "aborted" || aborted.ActiveProcessReceiptRef != "receipt-cleanup" {
		t.Fatalf("aborted preparation = %#v, want aborted with preserved receipt", aborted)
	}
	reservation, err := store.Get(t.Context(), chain.reservation.ReservationID)
	if err != nil {
		t.Fatalf("Get(rejected reservation) error = %v", err)
	}
	if reservation.State != "rejected" {
		t.Fatalf("reservation state after abort = %q, want rejected", reservation.State)
	}

	replay, err := store.AbortBinding(t.Context(), abortRequest)
	if err != nil {
		t.Fatalf("AbortBinding(replay) error = %v", err)
	}
	if !reflect.DeepEqual(replay, aborted) {
		t.Fatalf("abort replay = %#v, want %#v", replay, aborted)
	}
	if _, err := store.FinalizeBinding(t.Context(), finalizeRequestFor(chain, "receipt-after-abort")); !errors.Is(err, ErrIdentityAlreadyResolved) {
		t.Fatalf("FinalizeBinding(after abort) error = %v, want ErrIdentityAlreadyResolved", err)
	}
}

func TestIdentityRevokeAppendsHistoryAndReplacementUsesNewIncarnation(t *testing.T) {
	db := openIdentityTestDB(t)
	store := mustIdentityStore(t, db)
	first := createIdentityChain(t, db, store, "revoke-first", 1)
	firstActive := prepareAndFinalizeIdentity(t, store, first, "receipt-first")
	revoked := revokeIdentity(t, store, first, firstActive)
	if revoked.State != "revoked" || revoked.BindingVersion != 2 {
		t.Fatalf("revoked binding = %#v, want version 2 revoked", revoked)
	}
	replacement := createIdentityChainForAgent(t, db, store, "revoke-replacement", first.reservation.AgentID, 2)
	replacement.prep.ExpectedBindingVersion = revoked.BindingVersion
	replacementActive := prepareAndFinalizeIdentity(t, store, replacement, "receipt-replacement")
	if replacementActive.State != "active" || replacementActive.BindingVersion != 3 || replacementActive.IncarnationID == first.incarnation.IncarnationID {
		t.Fatalf("replacement binding = %#v, want new incarnation active version 3", replacementActive)
	}
	if got := countIdentityRows(t, db, "orchestration_current_bindings"); got != 3 {
		t.Fatalf("binding history rows = %d, want 3", got)
	}
	current, err := store.GetCurrent(t.Context(), first.reservation.AgentID)
	if err != nil {
		t.Fatalf("GetCurrent(replacement) error = %v", err)
	}
	if !reflect.DeepEqual(current, replacementActive) {
		t.Fatalf("current replacement = %#v, want %#v", current, replacementActive)
	}
	assertHistoricalIdentityReplay(t, store, first, firstActive)
}

func assertHistoricalIdentityReplay(t *testing.T, store *Store, first identityChain, firstActive CurrentBinding) {
	t.Helper()
	replayedFirst, err := store.FinalizeBinding(t.Context(), finalizeRequestFor(first, "receipt-first"))
	if err != nil {
		t.Fatalf("FinalizeBinding(first after replacement) error = %v", err)
	}
	if replayedFirst.BindingVersion != firstActive.BindingVersion || replayedFirst.IncarnationID != firstActive.IncarnationID {
		t.Fatalf("historical finalize replay = %#v, want original binding %#v", replayedFirst, firstActive)
	}
	if _, err := store.CreateIncarnation(t.Context(), incarnationRequestFor(first.reservation, first.incarnation.IncarnationID, first.incarnation.ExternalIdentityRef, first.incarnation.ExternalEpoch)); err != nil {
		t.Fatalf("CreateIncarnation(after reservation activation) error = %v", err)
	}
	if _, err := store.PrepareBinding(t.Context(), first.prep); err != nil {
		t.Fatalf("PrepareBinding(after reservation activation) error = %v", err)
	}
}

func prepareAndFinalizeIdentity(t *testing.T, store *Store, chain identityChain, receipt string) CurrentBinding {
	t.Helper()
	if _, err := store.PrepareBinding(t.Context(), chain.prep); err != nil {
		t.Fatalf("PrepareBinding(%s) error = %v", chain.prep.PreparationID, err)
	}
	binding, err := store.FinalizeBinding(t.Context(), finalizeRequestFor(chain, receipt))
	if err != nil {
		t.Fatalf("FinalizeBinding(%s) error = %v", chain.prep.PreparationID, err)
	}
	return binding
}

func revokeIdentity(t *testing.T, store *Store, chain identityChain, active CurrentBinding) CurrentBinding {
	t.Helper()
	revoked, err := store.RevokeBinding(t.Context(), RevokeBindingRequest{AgentID: chain.reservation.AgentID,
		ExpectedBindingVersion: active.BindingVersion, IncarnationID: chain.incarnation.IncarnationID,
		SourceFenceRef: chain.incarnation.SourceFenceRef})
	if err != nil {
		t.Fatalf("RevokeBinding() error = %v", err)
	}
	return revoked
}

func TestIdentityStoreCloseReopenPreservesAllRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity-reopen.sqlite")
	db := openIdentityTestDBAtPath(t, path)
	store := mustIdentityStore(t, db)
	chain := createIdentityChain(t, db, store, "reopen", 1)
	wantBinding := prepareAndFinalizeIdentity(t, store, chain, "receipt-reopen")
	if err := db.Close(); err != nil {
		t.Fatalf("close identity database: %v", err)
	}
	reopenedDB := openIdentityTestDBAtPath(t, path)
	reopened := mustIdentityStore(t, reopenedDB)
	assertReopenedIdentity(t, reopened, chain, wantBinding)
}

func assertReopenedIdentity(t *testing.T, store *Store, chain identityChain, wantBinding CurrentBinding) {
	t.Helper()
	reservation, err := store.Get(t.Context(), chain.reservation.ReservationID)
	if err != nil {
		t.Fatalf("Get(reopened reservation) error = %v", err)
	}
	incarnation, err := store.GetIncarnation(t.Context(), chain.incarnation.IncarnationID)
	if err != nil {
		t.Fatalf("GetIncarnation(reopened) error = %v", err)
	}
	preparation, err := store.GetPreparation(t.Context(), chain.prep.PreparationID)
	if err != nil {
		t.Fatalf("GetPreparation(reopened) error = %v", err)
	}
	binding, err := store.GetCurrent(t.Context(), chain.reservation.AgentID)
	if err != nil {
		t.Fatalf("GetCurrent(reopened) error = %v", err)
	}
	if reservation.State != "activated" || incarnation.IncarnationID != chain.incarnation.IncarnationID || preparation.State != "finalized" {
		t.Fatalf("reopened rows reservation=%#v incarnation=%#v preparation=%#v", reservation, incarnation, preparation)
	}
	if !reflect.DeepEqual(binding, wantBinding) {
		t.Fatalf("reopened binding = %#v, want %#v", binding, wantBinding)
	}
}

func TestIdentityStoreConcurrentReserveAcrossConnectionsHasOneWinner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity-reserve-concurrent.sqlite")
	dbOne := openIdentityTestDBAtPath(t, path)
	dbTwo := openIdentityTestDBAtPath(t, path)
	seedCommandIntent(t, dbOne, "cmd-concurrent-reserve")
	storeOne := mustIdentityStore(t, dbOne)
	storeTwo := mustIdentityStore(t, dbTwo)
	requestOne := commandReservationRequest("reservation-concurrent-one", "cmd-concurrent-reserve", "agent-concurrent-reserve", 1)
	requestTwo := requestOne
	requestTwo.ReservationID = "reservation-concurrent-two"

	start := make(chan struct{})
	results := make(chan reservationResult, 2)
	var workers sync.WaitGroup
	workers.Go(func() { reserveConcurrently(start, results, storeOne, requestOne) })
	workers.Go(func() { reserveConcurrently(start, results, storeTwo, requestTwo) })
	close(start)
	workers.Wait()
	close(results)

	var observed []Reservation
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent Reserve() error = %v", result.err)
		}
		observed = append(observed, result.reservation)
	}
	if len(observed) != 2 || !reflect.DeepEqual(observed[0], observed[1]) {
		t.Fatalf("concurrent reservations = %#v, want two identical results", observed)
	}
	if got := countIdentityRows(t, dbOne, "orchestration_identity_reservations"); got != 1 {
		t.Fatalf("concurrent reservation rows = %d, want 1", got)
	}
}

func TestIdentityStoreConcurrentFinalizeAcrossConnectionsHasOneWinner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity-finalize-concurrent.sqlite")
	dbOne := openIdentityTestDBAtPath(t, path)
	dbTwo := openIdentityTestDBAtPath(t, path)
	storeOne := mustIdentityStore(t, dbOne)
	storeTwo := mustIdentityStore(t, dbTwo)
	chain := createIdentityChain(t, dbOne, storeOne, "concurrent-finalize", 1)
	if _, err := storeOne.PrepareBinding(t.Context(), chain.prep); err != nil {
		t.Fatalf("PrepareBinding() error = %v", err)
	}
	request := finalizeRequestFor(chain, "receipt-concurrent")
	start := make(chan struct{})
	results := make(chan bindingResult, 2)
	var workers sync.WaitGroup
	workers.Go(func() { finalizeConcurrently(start, results, storeOne, request) })
	workers.Go(func() { finalizeConcurrently(start, results, storeTwo, request) })
	close(start)
	workers.Wait()
	close(results)

	var observed []CurrentBinding
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent FinalizeBinding() error = %v", result.err)
		}
		observed = append(observed, result.binding)
	}
	if len(observed) != 2 || !reflect.DeepEqual(observed[0], observed[1]) {
		t.Fatalf("concurrent bindings = %#v, want two identical results", observed)
	}
	if got := countIdentityRows(t, dbOne, "orchestration_current_bindings"); got != 1 {
		t.Fatalf("concurrent binding rows = %d, want 1", got)
	}
}

func TestIdentityStoreNeverWritesLegacyAgentTables(t *testing.T) {
	db := openIdentityTestDB(t)
	store := mustIdentityStore(t, db)
	before := legacyIdentityRowCounts(t, db)
	chain := createIdentityChain(t, db, store, "legacy-guard", 1)
	if _, err := store.PrepareBinding(t.Context(), chain.prep); err != nil {
		t.Fatalf("PrepareBinding() error = %v", err)
	}
	active, err := store.FinalizeBinding(t.Context(), finalizeRequestFor(chain, "receipt-legacy-guard"))
	if err != nil {
		t.Fatalf("FinalizeBinding() error = %v", err)
	}
	if _, err := store.RevokeBinding(t.Context(), RevokeBindingRequest{AgentID: chain.reservation.AgentID,
		ExpectedBindingVersion: active.BindingVersion, IncarnationID: chain.incarnation.IncarnationID,
		SourceFenceRef: chain.incarnation.SourceFenceRef}); err != nil {
		t.Fatalf("RevokeBinding() error = %v", err)
	}
	after := legacyIdentityRowCounts(t, db)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("legacy agent_* rows changed: before=%v after=%v", before, after)
	}
}

func TestIdentityJSONFieldGuard(t *testing.T) {
	cases := []struct {
		name  string
		value any
	}{
		{name: "reservation request", value: ReservationRequest{}},
		{name: "reservation", value: Reservation{}},
		{name: "incarnation request", value: IncarnationRequest{}},
		{name: "incarnation", value: Incarnation{}},
		{name: "preparation request", value: BindingPreparationRequest{}},
		{name: "preparation", value: BindingPreparation{}},
		{name: "finalize request", value: FinalizeBindingRequest{}},
		{name: "abort request", value: AbortBindingRequest{}},
		{name: "revoke request", value: RevokeBindingRequest{}},
		{name: "current binding", value: CurrentBinding{}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			assertIdentityJSONFields(t, testCase.value)
		})
	}
}

func TestIdentityRowMappersMapEveryField(t *testing.T) {
	commandID := "cmd-mapper"
	reservationRow := validReservationRow(&commandID)
	reservation, err := reservationFromRow(reservationRow)
	if err != nil {
		t.Fatalf("reservationFromRow() error = %v", err)
	}
	assertIdentityMappedFields(t, reservation, reservationRow)
	incarnationRow := validIncarnationRow()
	incarnation, err := incarnationFromRow(incarnationRow)
	if err != nil {
		t.Fatalf("incarnationFromRow() error = %v", err)
	}
	assertIdentityMappedFields(t, incarnation, incarnationRow)
	preparationRow := validPreparationRow()
	preparation, err := preparationFromRow(preparationRow)
	if err != nil {
		t.Fatalf("preparationFromRow() error = %v", err)
	}
	assertIdentityMappedFields(t, preparation, preparationRow)
	bindingRow := validBindingRow()
	binding, err := currentBindingFromRow(bindingRow)
	if err != nil {
		t.Fatalf("currentBindingFromRow() error = %v", err)
	}
	assertIdentityMappedFields(t, binding, bindingRow)
}

func assertIdentityMappedFields(t *testing.T, got, row any) {
	t.Helper()
	gotValue, rowValue := reflect.ValueOf(got), reflect.ValueOf(row)
	gotType, rowType := gotValue.Type(), rowValue.Type()
	for index := 0; index < gotType.NumField(); index++ {
		field := gotType.Field(index)
		rowField, ok := rowType.FieldByName(field.Name)
		if !ok {
			t.Fatalf("mapped field %s.%s has no SQLC counterpart", gotType.Name(), field.Name)
		}
		left, right := gotValue.Field(index), rowValue.FieldByIndex(rowField.Index)
		if left.Type() != right.Type() || !reflect.DeepEqual(left.Interface(), right.Interface()) {
			t.Fatalf("mapped field %s = %v, want %v", field.Name, left.Interface(), right.Interface())
		}
	}
}

func TestIdentityRowMappersRejectMalformedRows(t *testing.T) {
	commandID := "cmd-malformed"
	reservationCases := []struct {
		name string
		edit func(*storedb.OrchestrationIdentityReservation)
	}{
		{name: "unsupported state", edit: func(row *storedb.OrchestrationIdentityReservation) { row.State = "corrupt" }},
		{name: "unsupported schema", edit: func(row *storedb.OrchestrationIdentityReservation) { row.SchemaVersion = 2 }},
		{name: "invalid timestamp", edit: func(row *storedb.OrchestrationIdentityReservation) { row.UpdatedAt = "not-a-timestamp" }},
	}
	for _, testCase := range reservationCases {
		t.Run("reservation/"+testCase.name, func(t *testing.T) {
			row := validReservationRow(&commandID)
			testCase.edit(&row)
			_, err := reservationFromRow(row)
			expectInvalidIdentity(t, err)
		})
	}

	incarnationCases := []struct {
		name string
		edit func(*storedb.OrchestrationExecutionIncarnation)
	}{
		{name: "invalid digest", edit: func(row *storedb.OrchestrationExecutionIncarnation) {
			row.IdentityDigest = strings.Repeat("A", 64)
		}},
		{name: "unsupported schema", edit: func(row *storedb.OrchestrationExecutionIncarnation) { row.SchemaVersion = 2 }},
		{name: "invalid timestamp", edit: func(row *storedb.OrchestrationExecutionIncarnation) { row.CreatedAt = "not-a-timestamp" }},
	}
	for _, testCase := range incarnationCases {
		t.Run("incarnation/"+testCase.name, func(t *testing.T) {
			row := validIncarnationRow()
			testCase.edit(&row)
			_, err := incarnationFromRow(row)
			expectInvalidIdentity(t, err)
		})
	}

	preparationCases := []struct {
		name string
		edit func(*storedb.OrchestrationBindingPreparation)
	}{
		{name: "unsupported state", edit: func(row *storedb.OrchestrationBindingPreparation) { row.State = "corrupt" }},
		{name: "invalid resolved timestamp", edit: func(row *storedb.OrchestrationBindingPreparation) { row.ResolvedAt = "not-a-timestamp" }},
		{name: "unsupported schema", edit: func(row *storedb.OrchestrationBindingPreparation) { row.SchemaVersion = 2 }},
	}
	for _, testCase := range preparationCases {
		t.Run("preparation/"+testCase.name, func(t *testing.T) {
			row := validPreparationRow()
			testCase.edit(&row)
			_, err := preparationFromRow(row)
			expectInvalidIdentity(t, err)
		})
	}

	bindingCases := []struct {
		name string
		edit func(*storedb.OrchestrationCurrentBinding)
	}{
		{name: "unsupported state", edit: func(row *storedb.OrchestrationCurrentBinding) { row.State = "corrupt" }},
		{name: "invalid digest", edit: func(row *storedb.OrchestrationCurrentBinding) { row.BindingDigest = strings.Repeat("A", 64) }},
		{name: "invalid timestamp", edit: func(row *storedb.OrchestrationCurrentBinding) { row.UpdatedAt = "not-a-timestamp" }},
	}
	for _, testCase := range bindingCases {
		t.Run("binding/"+testCase.name, func(t *testing.T) {
			row := validBindingRow()
			testCase.edit(&row)
			_, err := currentBindingFromRow(row)
			expectInvalidIdentity(t, err)
		})
	}
}
