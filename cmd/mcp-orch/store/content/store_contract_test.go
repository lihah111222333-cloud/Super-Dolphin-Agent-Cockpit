package content

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/contentcrypto"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/contentobject"
	sqliteruntime "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
)

func contentTestClock() time.Time { return time.Date(2030, 9, 2, 3, 4, 5, 0, time.UTC) }

type contentTestFixture struct {
	db          *sql.DB
	dbPath      string
	store       *Store
	objectsDir  string
	provider    *contentTestKeyProvider
	objectProbe *contentTestObjectStore
	binding     SourceBinding
}

type contentTestKeyRecord struct {
	request  contentcrypto.KeyCreateRequest
	metadata contentcrypto.KeyMetadata
	key      []byte
}

type contentTestKeyProvider struct {
	mu      sync.Mutex
	records map[string]contentTestKeyRecord
	calls   int
}

type contentTestObjectStore struct {
	delegate contentobject.Store
	mu       sync.Mutex
	puts     []contentobject.PutRequest
}

type contentTamperSealer struct {
	delegate contentcrypto.KeyCreatorSealer
	tamper   func(*contentcrypto.SealedPayload)
}

type contentTamperObjectStore struct {
	delegate contentobject.Store
	tamper   func(*contentobject.PutReceipt)
}

// TestContentStoreContract 聚合CONTENT-1 reservation、prepared、promotion、边界和字段守卫合同。
func TestContentStoreContract(t *testing.T) {
	checks := []struct {
		name string
		run  func(*testing.T)
	}{
		{name: "nil_dependencies", run: testContentStoreRejectsNilAndTypedNilDependencies},
		{name: "reservation", run: testContentReserveReplayConflictAndCurrentBinding},
		{name: "prepared_crash_replay", run: testContentPutPreparedCrashSafeReplay},
		{name: "receipt_mismatch", run: testContentReceiptMismatchNeverWritesMetadata},
		{name: "promote_resolve", run: testContentPreparedPromoteResolveAndReplay},
		{name: "source_binding_rollover", run: testContentSourceBindingRolloverFailsClosed},
		{name: "single_boundary", run: testContentSingle64KiBBoundaryAndInvalidInputs},
		{name: "sqlite_sensitive_bytes", run: testContentPlaintextAndRawKeyNeverReachSQLite},
		{name: "reservation_field_mapping", run: testContentReservationParamsFieldMappingGuard},
		{name: "field_chain_bidirectional_drift", run: testContentFieldChainDifferenceRejectsBothDirections},
		{name: "reservation_put_to_seal_field_chain", run: testContentReservationAndPutFieldsReachSealRequest},
		{name: "sealed_receipt_to_prepared_field_chain", run: testContentSealedAndReceiptFieldsReachPreparedObject},
		{name: "promotion_candidate_params_field_chain", run: testContentPromotionFieldsReachCandidateAndSQLParams},
		{name: "prepared_field_mapping", run: testContentPreparedMappingCopiesEveryReceiptField},
		{name: "plaintext_json", run: testContentPlaintextFieldIsNeverJSONSerializable},
	}
	for _, check := range checks {
		t.Run(check.name, check.run)
	}
}

func testContentStoreRejectsNilAndTypedNilDependencies(t *testing.T) {
	fixture := newContentTestFixture(t, "typed-nil")
	var nilSealer *contentcrypto.AESGCMSealer
	var nilObjects *contentobject.FileStore

	checks := []struct {
		name    string
		db      *sql.DB
		sealer  contentcrypto.KeyCreatorSealer
		objects contentobject.Store
		want    error
	}{
		{name: "nil database", sealer: fixture.store.sealer, objects: fixture.store.objects},
		{name: "nil sealer", db: fixture.db, objects: fixture.store.objects, want: contentcrypto.ErrKeyProviderMissing},
		{name: "typed nil sealer", db: fixture.db, sealer: nilSealer, objects: fixture.store.objects, want: contentcrypto.ErrKeyProviderMissing},
		{name: "nil object store", db: fixture.db, sealer: fixture.store.sealer, want: contentobject.ErrObjectStoreMissing},
		{name: "typed nil object store", db: fixture.db, sealer: fixture.store.sealer, objects: nilObjects, want: contentobject.ErrObjectStoreMissing},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			store, err := NewStore(check.db, check.sealer, check.objects)
			if err == nil || store != nil {
				t.Fatalf("NewStore() = (%#v, %v), want fail-fast rejection", store, err)
			}
			if check.want != nil && !errors.Is(err, check.want) {
				t.Fatalf("NewStore() error = %v, want %v", err, check.want)
			}
		})
	}
}

func testContentReserveReplayConflictAndCurrentBinding(t *testing.T) {
	t.Run("replay and conflict", func(t *testing.T) {
		fixture := newContentTestFixture(t, "reserve-replay")
		request := contentReserveRequest(fixture.binding, "reserve-replay", 37)
		first, err := fixture.store.ReserveIngest(t.Context(), request)
		contentNoError(t, err)
		fixture.store.now = func() time.Time { return request.AssemblyDeadline.Add(time.Nanosecond) }

		retry := request
		retry.ReservationID = "reservation-retry-candidate"
		retry.TicketID = "ticket-retry-candidate"
		retry.ObjectID = "object-retry-candidate"
		replayed, err := fixture.store.ReserveIngest(t.Context(), retry)
		contentNoError(t, err)
		if !reflect.DeepEqual(replayed, first) {
			t.Fatalf("ReserveIngest(replay) = %#v, want original %#v", replayed, first)
		}

		conflict := retry
		conflict.MediaType = "application/octet-stream"
		if _, err := fixture.store.ReserveIngest(t.Context(), conflict); !errors.Is(err, ErrReplayConflict) {
			t.Fatalf("ReserveIngest(conflict) error = %v, want %v", err, ErrReplayConflict)
		}
		expiredNewKey := contentReserveRequest(fixture.binding, "reserve-expired-new-key", request.SizeBytes)
		expiredNewKey.AssemblyDeadline = request.AssemblyDeadline
		_, err = fixture.store.ReserveIngest(t.Context(), expiredNewKey)
		contentWantError(t, "ReserveIngest(new key after deadline)", err, ErrIngestExpired)
		contentAssertRowCount(t, fixture.db, "orchestration_content_ingest_reservations", 1)
	})

	t.Run("stale current binding", func(t *testing.T) {
		fixture := newContentTestFixture(t, "reserve-stale")
		identities, err := identity.NewStore(fixture.db)
		contentNoError(t, err)
		_, err = identities.RevokeBinding(t.Context(), identity.RevokeBindingRequest{
			AgentID: fixture.binding.AgentID, ExpectedBindingVersion: fixture.binding.BindingVersion,
			IncarnationID: fixture.binding.IncarnationID, SourceFenceRef: fixture.binding.FenceRef,
		})
		contentNoError(t, err)
		if _, err := fixture.store.ReserveIngest(t.Context(), contentReserveRequest(fixture.binding, "reserve-stale", 11)); err == nil {
			t.Fatal("ReserveIngest(stale binding) error = nil, want current-binding rejection")
		}
		contentAssertRowCount(t, fixture.db, "orchestration_content_ingest_reservations", 0)
	})
}

func testContentPutPreparedCrashSafeReplay(t *testing.T) {
	fixture := newContentTestFixture(t, "prepared-crash")
	plaintext := []byte("prepared-crash-window-sensitive-plaintext")
	reservation := contentReserve(t, fixture, "prepared-crash", int64(len(plaintext)))
	request := PutSinglePreparedRequest{SchemaVersion: 1, TicketID: reservation.TicketID,
		OperationID: contentPutOperationID(t, reservation.TicketID), Plaintext: append([]byte(nil), plaintext...)}

	_, err := fixture.db.ExecContext(t.Context(), `CREATE TRIGGER content_test_fail_metadata
		BEFORE INSERT ON orchestration_content_objects BEGIN SELECT RAISE(ABORT, 'injected metadata failure'); END`)
	contentNoError(t, err)
	if _, err := fixture.store.PutSinglePrepared(t.Context(), request); err == nil {
		t.Fatal("PutSinglePrepared(injected metadata failure) error = nil")
	}
	contentAssertRowCount(t, fixture.db, "orchestration_content_objects", 0)
	contentAssertRowCount(t, fixture.db, "orchestration_content_private_payload_receipts", 0)
	contentAssertReservationState(t, fixture.db, reservation.ReservationID, "reserved", 1)
	contentAssertPreparationPreflight(t, fixture.db, reservation.ReservationID, request.OperationID, contentTestClock())
	if got := contentSealedObjectFiles(t, fixture.objectsDir); len(got) != 1 {
		t.Fatalf("sealed object files after crash window = %v, want one committed object", got)
	}

	_, err = fixture.db.ExecContext(t.Context(), `DROP TRIGGER content_test_fail_metadata`)
	contentNoError(t, err)
	fixture.store.now = func() time.Time { return reservation.AssemblyDeadline.Add(time.Nanosecond) }
	prepared, err := fixture.store.PutSinglePrepared(t.Context(), request)
	contentNoError(t, err)
	if prepared.State != "prepared" || prepared.ObjectID != reservation.ObjectID || prepared.CiphertextSize != int64(len(plaintext))+16 {
		t.Fatalf("prepared object = %#v, want original object and AES-GCM size", prepared)
	}
	replayed, err := fixture.store.PutSinglePrepared(t.Context(), request)
	contentNoError(t, err)
	if !reflect.DeepEqual(replayed, prepared) {
		t.Fatalf("PutSinglePrepared(replay) = %#v, want %#v", replayed, prepared)
	}
	conflict := request
	conflict.OperationID = "conflicting-" + request.OperationID
	if _, err := fixture.store.PutSinglePrepared(t.Context(), conflict); !errors.Is(err, ErrReplayConflict) {
		t.Fatalf("PutSinglePrepared(conflict) error = %v, want %v", err, ErrReplayConflict)
	}
	contentAssertRowCount(t, fixture.db, "orchestration_content_objects", 1)
	contentAssertRowCount(t, fixture.db, "orchestration_content_private_payload_receipts", 1)
	contentAssertReservationState(t, fixture.db, reservation.ReservationID, "sealed", 2)
	if got := contentSealedObjectFiles(t, fixture.objectsDir); len(got) != 1 {
		t.Fatalf("sealed object files after replay = %v, want exactly one", got)
	}
}

func testContentReceiptMismatchNeverWritesMetadata(t *testing.T) {
	sealCases := []struct {
		name   string
		tamper func(*contentcrypto.SealedPayload)
	}{
		{name: "seal request digest", tamper: func(sealed *contentcrypto.SealedPayload) {
			sealed.RequestDigest = contentDigest("wrong-seal-request-digest")
		}},
		{name: "aad digest", tamper: func(sealed *contentcrypto.SealedPayload) {
			sealed.AADDigest = contentDigest("wrong-aad-digest")
		}},
	}
	for _, testCase := range sealCases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newContentTestFixture(t, "tampered-"+strings.ReplaceAll(testCase.name, " ", "-"))
			plaintext := []byte("tampered-seal-receipt")
			reservation := contentReserve(t, fixture, "tampered-seal-"+strings.ReplaceAll(testCase.name, " ", "-"), int64(len(plaintext)))
			store, err := NewStore(fixture.db, contentTamperSealer{delegate: fixture.store.sealer, tamper: testCase.tamper}, fixture.objectProbe)
			contentNoError(t, err)
			store.now = fixture.store.now
			_, err = store.PutSinglePrepared(t.Context(), PutSinglePreparedRequest{SchemaVersion: 1,
				TicketID: reservation.TicketID, OperationID: contentPutOperationID(t, reservation.TicketID), Plaintext: plaintext})
			if !errors.Is(err, ErrReceiptMismatch) {
				t.Fatalf("PutSinglePrepared(tampered %s) error = %v, want %v", testCase.name, err, ErrReceiptMismatch)
			}
			contentAssertRowCount(t, fixture.db, "orchestration_content_objects", 0)
			contentAssertRowCount(t, fixture.db, "orchestration_content_private_payload_receipts", 0)
			if fixture.objectProbe.putCount() != 0 {
				t.Fatalf("tampered %s reached object backend", testCase.name)
			}
		})
	}

	t.Run("object request digest", func(t *testing.T) {
		fixture := newContentTestFixture(t, "tampered-object-request")
		plaintext := []byte("tampered-object-receipt")
		reservation := contentReserve(t, fixture, "tampered-object-request", int64(len(plaintext)))
		objects := contentTamperObjectStore{delegate: fixture.objectProbe, tamper: func(receipt *contentobject.PutReceipt) {
			receipt.RequestDigest = contentDigest("wrong-object-request-digest")
		}}
		store, err := NewStore(fixture.db, fixture.store.sealer, objects)
		contentNoError(t, err)
		store.now = fixture.store.now
		_, err = store.PutSinglePrepared(t.Context(), PutSinglePreparedRequest{SchemaVersion: 1,
			TicketID: reservation.TicketID, OperationID: contentPutOperationID(t, reservation.TicketID), Plaintext: plaintext})
		if !errors.Is(err, ErrReceiptMismatch) {
			t.Fatalf("PutSinglePrepared(tampered object digest) error = %v, want %v", err, ErrReceiptMismatch)
		}
		contentAssertRowCount(t, fixture.db, "orchestration_content_objects", 0)
		contentAssertRowCount(t, fixture.db, "orchestration_content_private_payload_receipts", 0)
		if fixture.objectProbe.putCount() != 1 {
			t.Fatalf("object backend put count = %d, want one committed external object", fixture.objectProbe.putCount())
		}
	})
}

func testContentPreparedPromoteResolveAndReplay(t *testing.T) {
	fixture := newContentTestFixture(t, "promote-resolve")
	plaintext := []byte("promote-resolve-sensitive-plaintext")
	reservation := contentReserve(t, fixture, "promote-resolve", int64(len(plaintext)))
	_, err := fixture.store.PutSinglePrepared(t.Context(), PutSinglePreparedRequest{
		SchemaVersion: 1, TicketID: reservation.TicketID,
		OperationID: contentPutOperationID(t, reservation.TicketID), Plaintext: plaintext,
	})
	contentNoError(t, err)
	promote := PromoteRequest{SchemaVersion: 1, TicketID: reservation.TicketID,
		ReferenceID: "reference-promote-resolve", CanonicalRef: "content://v1/promote-resolve",
		ProofID: "proof-promote-resolve", ProofRevision: 1, OwnerProofRef: "owner-proof/promote-resolve",
		ProofExpiresAt: contentTestClock().Add(30 * time.Minute)}
	active, err := fixture.store.Promote(t.Context(), promote)
	contentNoError(t, err)
	contentAssertActiveRef(t, active)

	replayed, err := fixture.store.Promote(t.Context(), promote)
	contentNoError(t, err)
	contentAssertDeepEqual(t, "Promote(replay)", replayed, active)
	conflict := promote
	conflict.CanonicalRef = "content://v1/conflicting-ref"
	_, err = fixture.store.Promote(t.Context(), conflict)
	contentWantError(t, "Promote(conflict)", err, ErrReplayConflict)
	resolved, err := fixture.store.ResolveActiveRef(t.Context(), ResolveActiveRefRequest{CanonicalRef: active.CanonicalRef})
	contentNoError(t, err)
	contentAssertDeepEqual(t, "ResolveActiveRef()", resolved, active)
	_, err = fixture.store.ResolveActiveRef(t.Context(), ResolveActiveRefRequest{CanonicalRef: "content://v1/not-found"})
	contentWantError(t, "ResolveActiveRef(not found)", err, ErrContentNotFound)
	fixture.store.now = func() time.Time { return promote.ProofExpiresAt.Add(time.Nanosecond) }
	expiredReplay, err := fixture.store.Promote(t.Context(), promote)
	contentNoError(t, err)
	contentAssertDeepEqual(t, "Promote(exact replay after expiry)", expiredReplay, active)
	_, err = fixture.store.ResolveActiveRef(t.Context(), ResolveActiveRefRequest{CanonicalRef: active.CanonicalRef})
	contentWantError(t, "ResolveActiveRef(expired)", err, ErrContentUnavailable)
	contentAssertReservationState(t, fixture.db, reservation.ReservationID, "promoted", 3)
	contentAssertRowCount(t, fixture.db, "orchestration_content_refs", 1)
}

func testContentSourceBindingRolloverFailsClosed(t *testing.T) {
	t.Run("promote after replacement", func(t *testing.T) {
		fixture := newContentTestFixture(t, "promote-after-replacement")
		plaintext := []byte("promote-after-replacement")
		reservation := contentReserve(t, fixture, "promote-after-replacement", int64(len(plaintext)))
		_, err := fixture.store.PutSinglePrepared(t.Context(), PutSinglePreparedRequest{
			SchemaVersion: 1, TicketID: reservation.TicketID,
			OperationID: contentPutOperationID(t, reservation.TicketID), Plaintext: plaintext,
		})
		contentNoError(t, err)
		contentReplaceCurrentBinding(t, fixture.db, "promote-after-replacement", fixture.binding)
		_, err = fixture.store.Promote(t.Context(), PromoteRequest{SchemaVersion: 1, TicketID: reservation.TicketID,
			ReferenceID: "reference-after-replacement", CanonicalRef: "content://v1/after-replacement",
			ProofID: "proof-after-replacement", ProofRevision: 1, OwnerProofRef: "owner-proof/after-replacement",
			ProofExpiresAt: contentTestClock().Add(30 * time.Minute)})
		if !errors.Is(err, ErrContentUnavailable) {
			t.Fatalf("Promote(stale source binding) error = %v, want %v", err, ErrContentUnavailable)
		}
		contentAssertReservationState(t, fixture.db, reservation.ReservationID, "sealed", 2)
		contentAssertRowCount(t, fixture.db, "orchestration_content_refs", 0)
	})

	t.Run("resolve after replacement", func(t *testing.T) {
		fixture := newContentTestFixture(t, "resolve-after-replacement")
		plaintext := []byte("resolve-after-replacement")
		reservation := contentReserve(t, fixture, "resolve-after-replacement", int64(len(plaintext)))
		_, err := fixture.store.PutSinglePrepared(t.Context(), PutSinglePreparedRequest{
			SchemaVersion: 1, TicketID: reservation.TicketID,
			OperationID: contentPutOperationID(t, reservation.TicketID), Plaintext: plaintext,
		})
		contentNoError(t, err)
		promote := PromoteRequest{SchemaVersion: 1, TicketID: reservation.TicketID,
			ReferenceID: "reference-resolve-replacement", CanonicalRef: "content://v1/resolve-replacement",
			ProofID: "proof-resolve-replacement", ProofRevision: 1, OwnerProofRef: "owner-proof/resolve-replacement",
			ProofExpiresAt: contentTestClock().Add(30 * time.Minute)}
		_, err = fixture.store.Promote(t.Context(), promote)
		contentNoError(t, err)
		contentReplaceCurrentBinding(t, fixture.db, "resolve-after-replacement", fixture.binding)
		if _, err := fixture.store.ResolveActiveRef(t.Context(), ResolveActiveRefRequest{CanonicalRef: promote.CanonicalRef}); !errors.Is(err, ErrContentUnavailable) {
			t.Fatalf("ResolveActiveRef(stale source binding) error = %v, want %v", err, ErrContentUnavailable)
		}
		if _, err := fixture.store.Promote(t.Context(), promote); !errors.Is(err, ErrContentUnavailable) {
			t.Fatalf("Promote(replay with stale source binding) error = %v, want %v", err, ErrContentUnavailable)
		}
	})
}

func testContentSingle64KiBBoundaryAndInvalidInputs(t *testing.T) {
	t.Run("exact 64KiB", func(t *testing.T) {
		fixture := newContentTestFixture(t, "max-boundary")
		plaintext := bytes.Repeat([]byte{0xa7}, contentcrypto.MaxSingleBytes)
		reservation := contentReserve(t, fixture, "max-boundary", int64(len(plaintext)))
		prepared, err := fixture.store.PutSinglePrepared(t.Context(), PutSinglePreparedRequest{
			SchemaVersion: 1, TicketID: reservation.TicketID,
			OperationID: contentPutOperationID(t, reservation.TicketID), Plaintext: plaintext,
		})
		contentNoError(t, err)
		if prepared.CiphertextSize != contentobject.MaxCiphertextBytes {
			t.Fatalf("prepared ciphertext size = %d, want %d", prepared.CiphertextSize, contentobject.MaxCiphertextBytes)
		}
	})

	t.Run("over boundary and malformed", func(t *testing.T) {
		fixture := newContentTestFixture(t, "invalid")
		tooLargeReserve := contentReserveRequest(fixture.binding, "too-large", contentcrypto.MaxSingleBytes+1)
		if _, err := fixture.store.ReserveIngest(t.Context(), tooLargeReserve); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("ReserveIngest(over limit) error = %v, want %v", err, ErrInvalidRequest)
		}
		reservation := contentReserve(t, fixture, "invalid-put", 1)
		providerCalls := fixture.provider.callCount()
		objectPuts := fixture.objectProbe.putCount()
		_, err := fixture.store.PutSinglePrepared(t.Context(), PutSinglePreparedRequest{
			SchemaVersion: 1, TicketID: reservation.TicketID,
			OperationID: "caller-chosen-operation", Plaintext: []byte{0x7f},
		})
		if !errors.Is(err, ErrReplayConflict) {
			t.Fatalf("PutSinglePrepared(non-stable operation) error = %v, want %v", err, ErrReplayConflict)
		}
		if fixture.provider.callCount() != providerCalls || fixture.objectProbe.putCount() != objectPuts {
			t.Fatal("non-stable operation reached key provider or object store before rejection")
		}
		for _, request := range []PutSinglePreparedRequest{
			{},
			{SchemaVersion: 1, TicketID: reservation.TicketID, OperationID: contentPutOperationID(t, reservation.TicketID)},
			{SchemaVersion: 1, TicketID: reservation.TicketID, OperationID: contentPutOperationID(t, reservation.TicketID), Plaintext: make([]byte, contentcrypto.MaxSingleBytes+1)},
		} {
			if _, err := fixture.store.PutSinglePrepared(t.Context(), request); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("PutSinglePrepared(%#v) error = %v, want %v", request, err, ErrInvalidRequest)
			}
		}
		if _, err := fixture.store.Promote(t.Context(), PromoteRequest{}); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("Promote(invalid) error = %v, want %v", err, ErrInvalidRequest)
		}
		if _, err := fixture.store.ResolveActiveRef(t.Context(), ResolveActiveRefRequest{}); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("ResolveActiveRef(invalid) error = %v, want %v", err, ErrInvalidRequest)
		}
	})
}

func testContentPlaintextAndRawKeyNeverReachSQLite(t *testing.T) {
	fixture := newContentTestFixture(t, "sqlite-sensitive-guard")
	plaintext := []byte("CONTENT_SQLITE_PLAINTEXT_SENTINEL_7f1e292cdbe84123")
	reservation := contentReserve(t, fixture, "sqlite-sensitive-guard", int64(len(plaintext)))
	operationID := contentPutOperationID(t, reservation.TicketID)
	rawKey := contentTestKey(operationID)
	_, err := fixture.store.PutSinglePrepared(t.Context(), PutSinglePreparedRequest{
		SchemaVersion: 1, TicketID: reservation.TicketID,
		OperationID: operationID, Plaintext: append([]byte(nil), plaintext...),
	})
	contentNoError(t, err)
	ciphertext := fixture.objectProbe.lastCiphertext(t)

	if _, err := fixture.db.ExecContext(t.Context(), `PRAGMA wal_checkpoint(PASSIVE)`); err != nil {
		t.Fatalf("checkpoint SQLite before sensitive-byte scan: %v", err)
	}
	for _, path := range []string{fixture.dbPath, fixture.dbPath + "-wal", fixture.dbPath + "-shm"} {
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		contentNoError(t, err)
		if bytes.Contains(data, plaintext) {
			t.Fatalf("SQLite artifact %q contains plaintext sentinel", filepath.Base(path))
		}
		if bytes.Contains(data, rawKey) {
			t.Fatalf("SQLite artifact %q contains raw AES key bytes", filepath.Base(path))
		}
		if bytes.Contains(data, ciphertext) || bytes.Contains(data, []byte(base64.StdEncoding.EncodeToString(ciphertext))) {
			t.Fatalf("SQLite artifact %q contains ciphertext payload bytes", filepath.Base(path))
		}
	}
	contentAssertNoSensitiveSQLColumns(t, fixture.db)
}

func newContentTestFixture(t *testing.T, suffix string) *contentTestFixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	contentNoError(t, err)
	dbPath := filepath.Join(root, "content.sqlite")
	db, err := sqliteruntime.Open(t.Context(), sqliteruntime.OpenOptions{Path: dbPath})
	contentNoError(t, err)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close content SQLite: %v", err)
		}
	})
	contentNoError(t, sqliteruntime.RunEmbeddedMigrations(t.Context(), db))

	binding := contentSeedCurrentBinding(t, db, suffix)
	provider := &contentTestKeyProvider{records: make(map[string]contentTestKeyRecord)}
	sealer, err := contentcrypto.NewAESGCMSealer(provider)
	contentNoError(t, err)
	stateDir := filepath.Join(root, "private-state")
	workspaceRoot := filepath.Join(root, "workspace")
	contentMkdir(t, stateDir)
	contentMkdir(t, workspaceRoot)
	objects, err := contentobject.NewFileStore(contentobject.Config{
		StateDir: stateDir, WorkspaceRoot: workspaceRoot, StoreID: "content-test-store-" + suffix,
	})
	contentNoError(t, err)
	objectProbe := &contentTestObjectStore{delegate: objects}
	store, err := NewStore(db, sealer, objectProbe)
	contentNoError(t, err)
	store.now = contentTestClock
	return &contentTestFixture{db: db, dbPath: dbPath, store: store,
		objectsDir: stateDir, provider: provider, objectProbe: objectProbe, binding: binding}
}

func contentSeedCurrentBinding(t *testing.T, db *sql.DB, suffix string) SourceBinding {
	t.Helper()
	store, err := identity.NewStore(db)
	contentNoError(t, err)
	agentID := "content-agent-" + suffix
	reservation, err := store.Reserve(t.Context(), identity.ReservationRequest{
		ReservationID: "identity-reservation-" + suffix, AgentID: agentID,
		IdentityKind: "a2a_remote_agent", SourceKind: "a2a_observation",
		CallerPrincipalRef: "principal/" + suffix, ScopeRef: "scope/" + suffix,
		IdempotencyNamespace: "content-identity/" + suffix, IdempotencyKeyDigest: contentDigest("identity-key/" + suffix),
		RequestDigest: contentDigest("identity-request/" + suffix), SourceFenceRef: "source-fence/" + suffix,
		TargetFenceRef: "target-fence/" + suffix, ReservationGeneration: 1,
	})
	contentNoError(t, err)
	incarnation, err := store.CreateIncarnation(t.Context(), identity.IncarnationRequest{
		IncarnationID: "identity-incarnation-" + suffix, ReservationID: reservation.ReservationID,
		AgentID: reservation.AgentID, IdentityKind: reservation.IdentityKind,
		ExternalIdentityRef: "external/" + suffix, ExternalEpoch: "epoch/" + suffix,
		SourceFenceRef: reservation.SourceFenceRef, IdentityDigest: contentDigest("incarnation/" + suffix),
		IncarnationGeneration: 1,
	})
	contentNoError(t, err)
	preparation, err := store.PrepareBinding(t.Context(), identity.BindingPreparationRequest{
		PreparationID: "identity-preparation-" + suffix, ReservationID: reservation.ReservationID,
		IncarnationID: incarnation.IncarnationID, AgentID: reservation.AgentID, IdentityKind: reservation.IdentityKind,
		SourceFenceRef: reservation.SourceFenceRef, BindingDigest: contentDigest("binding/" + suffix),
		PreparationGeneration: 1, ExpectedBindingVersion: 0, ExpectedIncarnationGeneration: 1,
	})
	contentNoError(t, err)
	active, err := store.FinalizeBinding(t.Context(), identity.FinalizeBindingRequest{
		PreparationID: preparation.PreparationID, AgentID: reservation.AgentID,
		ExpectedPreparationGeneration: preparation.PreparationGeneration, ExpectedBindingVersion: 0,
		IncarnationID: incarnation.IncarnationID, ExternalIdentityRef: incarnation.ExternalIdentityRef,
		IdentityKind: incarnation.IdentityKind, SourceFenceRef: incarnation.SourceFenceRef,
		ActiveProcessReceiptRef: "active-process-receipt/" + suffix, BindingDigest: preparation.BindingDigest,
	})
	contentNoError(t, err)
	return contentSourceBinding(t, active)
}

func contentReplaceCurrentBinding(t *testing.T, db *sql.DB, suffix string, previous SourceBinding) SourceBinding {
	t.Helper()
	store, err := identity.NewStore(db)
	contentNoError(t, err)
	revoked, err := store.RevokeBinding(t.Context(), identity.RevokeBindingRequest{
		AgentID: previous.AgentID, ExpectedBindingVersion: previous.BindingVersion,
		IncarnationID: previous.IncarnationID, SourceFenceRef: previous.FenceRef,
	})
	contentNoError(t, err)
	reservation, err := store.Reserve(t.Context(), identity.ReservationRequest{
		ReservationID: "replacement-reservation-" + suffix, AgentID: previous.AgentID,
		IdentityKind: "a2a_remote_agent", SourceKind: "a2a_observation",
		CallerPrincipalRef: "replacement-principal/" + suffix, ScopeRef: "replacement-scope/" + suffix,
		IdempotencyNamespace:   "replacement-identity/" + suffix,
		IdempotencyKeyDigest:   contentDigest("replacement-key/" + suffix),
		RequestDigest:          contentDigest("replacement-request/" + suffix),
		SourceFenceRef:         "replacement-source-fence/" + suffix,
		TargetFenceRef:         "replacement-target-fence/" + suffix,
		ExpectedBindingVersion: revoked.BindingVersion, ReservationGeneration: 2,
	})
	contentNoError(t, err)
	incarnation, err := store.CreateIncarnation(t.Context(), identity.IncarnationRequest{
		IncarnationID: "replacement-incarnation-" + suffix, ReservationID: reservation.ReservationID,
		AgentID: reservation.AgentID, IdentityKind: reservation.IdentityKind,
		ExternalIdentityRef: "replacement-external/" + suffix, ExternalEpoch: "replacement-epoch/" + suffix,
		SourceFenceRef: reservation.SourceFenceRef, IdentityDigest: contentDigest("replacement-incarnation/" + suffix),
		IncarnationGeneration: 2,
	})
	contentNoError(t, err)
	preparation, err := store.PrepareBinding(t.Context(), identity.BindingPreparationRequest{
		PreparationID: "replacement-preparation-" + suffix, ReservationID: reservation.ReservationID,
		IncarnationID: incarnation.IncarnationID, AgentID: reservation.AgentID, IdentityKind: reservation.IdentityKind,
		SourceFenceRef: reservation.SourceFenceRef, BindingDigest: contentDigest("replacement-binding/" + suffix),
		PreparationGeneration: 2, ExpectedBindingVersion: revoked.BindingVersion, ExpectedIncarnationGeneration: 2,
	})
	contentNoError(t, err)
	active, err := store.FinalizeBinding(t.Context(), identity.FinalizeBindingRequest{
		PreparationID: preparation.PreparationID, AgentID: reservation.AgentID,
		ExpectedPreparationGeneration: preparation.PreparationGeneration,
		ExpectedBindingVersion:        revoked.BindingVersion, IncarnationID: incarnation.IncarnationID,
		ExternalIdentityRef: incarnation.ExternalIdentityRef, IdentityKind: incarnation.IdentityKind,
		SourceFenceRef: incarnation.SourceFenceRef, ActiveProcessReceiptRef: "replacement-process-receipt/" + suffix,
		BindingDigest: preparation.BindingDigest,
	})
	contentNoError(t, err)
	return contentSourceBinding(t, active)
}

func contentSourceBinding(t *testing.T, active identity.CurrentBinding) SourceBinding {
	t.Helper()
	updatedAt, err := time.Parse(time.RFC3339Nano, active.UpdatedAt)
	contentNoError(t, err)
	return SourceBinding{AgentID: active.AgentID, IdentityKind: active.IdentityKind,
		IdentityReservationID: active.ReservationID, ReservationGeneration: active.ReservationGeneration,
		IncarnationID: active.IncarnationID, IncarnationGeneration: active.IncarnationGeneration,
		ExternalIdentityRef: active.ExternalIdentityRef, BindingVersion: active.BindingVersion,
		BindingDigest: active.BindingDigest, FenceRef: active.SourceFenceRef,
		ActiveProcessReceiptRef: active.ActiveProcessReceiptRef, BindingUpdatedAt: updatedAt}
}

func contentReserveRequest(binding SourceBinding, suffix string, size int64) ReserveIngestRequest {
	return ReserveIngestRequest{SchemaVersion: 1, ReservationID: "content-reservation-" + suffix,
		TicketID: "content-ticket-" + suffix, ObjectID: "content-object-" + suffix,
		OwnerID: "content-owner-1", OwnerGeneration: 7, WriterFenceRef: "writer-fence/7",
		SecurityTimeGeneration: 9, WorkspaceRef: "workspace/" + suffix,
		CallerPrincipalRef: "principal/" + suffix, ScopeRef: "scope/" + suffix, Source: binding,
		IdempotencyNamespace: "content-ingest/" + suffix,
		IdempotencyKeyDigest: contentDigest("idempotency/" + suffix),
		ProofRequestDigest:   contentDigest("proof/" + suffix), CandidateRefDigest: contentDigest("candidate/" + suffix),
		ContentIntentDigest: contentDigest("intent/" + suffix), DigestKeyID: "content-digest-key-1",
		DigestKeyVersion: 1, SizeBytes: size, MediaType: "text/plain; charset=utf-8",
		KeyWrapProfile: "test-aes-wrap-v1", QuotaSnapshotRef: "quota/" + suffix,
		QuotaSnapshotDigest: contentDigest("quota/" + suffix), RetentionPolicyID: "retention-default",
		RetentionPolicyVersion: 1, RetentionPolicyDigest: contentDigest("retention/" + suffix),
		AssemblyDeadline: contentTestClock().Add(time.Hour)}
}

func contentReserve(t *testing.T, fixture *contentTestFixture, suffix string, size int64) IngestReservation {
	t.Helper()
	reservation, err := fixture.store.ReserveIngest(t.Context(), contentReserveRequest(fixture.binding, suffix, size))
	contentNoError(t, err)
	return reservation
}

func (p *contentTestKeyProvider) CreateAES256KeyLease(_ context.Context, request contentcrypto.KeyCreateRequest) (*contentcrypto.EphemeralKeyLease, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if record, ok := p.records[request.OperationID]; ok {
		if !reflect.DeepEqual(record.request, request) {
			return nil, fmt.Errorf("key operation conflicts with original request")
		}
		return contentcrypto.NewEphemeralKeyLease(record.metadata, record.key)
	}
	nonceDigest := sha256.Sum256([]byte("nonce/" + request.OperationID))
	metadata := contentcrypto.KeyMetadata{Algorithm: contentcrypto.AlgorithmAES256GCM,
		KeyWrapProfile: "test-aes-wrap-v1", KeyHandleRef: "key-handle/" + request.OperationID,
		KeyReceiptRef:    "key-receipt/" + request.OperationID,
		KeyReceiptDigest: contentDigest("key-receipt/" + request.OperationID),
		Nonce:            append([]byte(nil), nonceDigest[:contentcrypto.GCMNonceBytes]...)}
	key := contentTestKey(request.OperationID)
	p.records[request.OperationID] = contentTestKeyRecord{request: request, metadata: metadata, key: append([]byte(nil), key...)}
	return contentcrypto.NewEphemeralKeyLease(metadata, key)
}

func (p *contentTestKeyProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (s contentTamperSealer) Validate() error {
	if s.delegate == nil || s.tamper == nil {
		return contentcrypto.ErrKeyProviderMissing
	}
	return s.delegate.Validate()
}

func (s contentTamperSealer) CreateAndSeal(ctx context.Context, request contentcrypto.SealRequest) (contentcrypto.SealedPayload, error) {
	sealed, err := s.delegate.CreateAndSeal(ctx, request)
	if err != nil {
		return contentcrypto.SealedPayload{}, err
	}
	s.tamper(&sealed)
	return sealed, nil
}

func (s contentTamperObjectStore) Validate() error {
	if s.delegate == nil || s.tamper == nil {
		return contentobject.ErrObjectStoreMissing
	}
	return s.delegate.Validate()
}

func (s contentTamperObjectStore) Put(ctx context.Context, request contentobject.PutRequest) (contentobject.PutReceipt, error) {
	receipt, err := s.delegate.Put(ctx, request)
	if err != nil {
		return contentobject.PutReceipt{}, err
	}
	s.tamper(&receipt)
	return receipt, nil
}

func (s contentTamperObjectStore) QueryPut(ctx context.Context, request contentobject.QueryRequest) (contentobject.PutReceipt, error) {
	return s.delegate.QueryPut(ctx, request)
}

func (s *contentTestObjectStore) Validate() error {
	if s == nil || s.delegate == nil {
		return contentobject.ErrObjectStoreMissing
	}
	return s.delegate.Validate()
}

func (s *contentTestObjectStore) Put(ctx context.Context, request contentobject.PutRequest) (contentobject.PutReceipt, error) {
	if err := s.Validate(); err != nil {
		return contentobject.PutReceipt{}, err
	}
	s.mu.Lock()
	request.Ciphertext = append([]byte(nil), request.Ciphertext...)
	s.puts = append(s.puts, request)
	s.mu.Unlock()
	return s.delegate.Put(ctx, request)
}

func (s *contentTestObjectStore) QueryPut(ctx context.Context, request contentobject.QueryRequest) (contentobject.PutReceipt, error) {
	if err := s.Validate(); err != nil {
		return contentobject.PutReceipt{}, err
	}
	return s.delegate.QueryPut(ctx, request)
}

func (s *contentTestObjectStore) lastCiphertext(t *testing.T) []byte {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.puts) == 0 {
		t.Fatal("object store received no ciphertext put")
	}
	return append([]byte(nil), s.puts[len(s.puts)-1].Ciphertext...)
}

func (s *contentTestObjectStore) putCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.puts)
}

func contentTestKey(operationID string) []byte {
	digest := sha256.Sum256([]byte("raw-key/" + operationID))
	return append([]byte(nil), digest[:]...)
}

func contentPutOperationID(t *testing.T, ticketID string) string {
	t.Helper()
	operationID, err := StablePrepareOperationID(ticketID)
	contentNoError(t, err)
	return operationID
}

func contentDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", digest[:])
}
