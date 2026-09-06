package contentobject

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNewFileStoreRequiresExplicitDisjointAbsoluteRoots(t *testing.T) {
	base := contentObjectTempRoot(t)
	state := filepath.Join(base, "state")
	workspace := filepath.Join(base, "workspace")
	contentObjectMkdir(t, state)
	contentObjectMkdir(t, workspace)
	realState := filepath.Join(base, "real-state")
	contentObjectMkdir(t, realState)
	symlinkState := filepath.Join(base, "state-link")
	if err := os.Symlink(realState, symlinkState); err != nil {
		t.Fatalf("create state-dir symlink: %v", err)
	}
	symlinkWorkspace := filepath.Join(base, "workspace-link")
	if err := os.Symlink(state, symlinkWorkspace); err != nil {
		t.Fatalf("create workspace symlink: %v", err)
	}
	cases := []struct {
		name   string
		config Config
		want   error
	}{
		{"empty state", Config{WorkspaceRoot: workspace, StoreID: "store-1"}, ErrInvalidObjectRequest},
		{"empty workspace", Config{StateDir: state, StoreID: "store-1"}, ErrInvalidObjectRequest},
		{"relative state", Config{StateDir: "state", WorkspaceRoot: workspace, StoreID: "store-1"}, ErrInvalidObjectRequest},
		{"relative workspace", Config{StateDir: state, WorkspaceRoot: "workspace", StoreID: "store-1"}, ErrInvalidObjectRequest},
		{"same root", Config{StateDir: state, WorkspaceRoot: state, StoreID: "store-1"}, ErrUnsafeStateDir},
		{"workspace below state", Config{StateDir: state, WorkspaceRoot: filepath.Join(state, "workspace"), StoreID: "store-1"}, ErrUnsafeStateDir},
		{"state below workspace", Config{StateDir: filepath.Join(workspace, "state"), WorkspaceRoot: workspace, StoreID: "store-1"}, ErrUnsafeStateDir},
		{"symlink state", Config{StateDir: symlinkState, WorkspaceRoot: workspace, StoreID: "store-1"}, ErrUnsafeStateDir},
		{"symlink workspace", Config{StateDir: state, WorkspaceRoot: symlinkWorkspace, StoreID: "store-1"}, ErrUnsafeStateDir},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, err := NewFileStore(tc.config)
			if err == nil || store != nil {
				t.Fatalf("NewFileStore() = (%v, %v), want rejection", store, err)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("NewFileStore() error = %v, want %v", err, tc.want)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(realState, "content-objects")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected symlink state created content outside canonical root: %v", err)
	}
}

func TestFileStorePrivatePermissionsAndCiphertextBoundaries(t *testing.T) {
	store, config := contentObjectStore(t)
	if err := store.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	for _, size := range []int{1, MaxCiphertextBytes} {
		request := contentObjectRequest(t, "operation-"+contentObjectSizeText(size), "object-"+contentObjectSizeText(size), bytes.Repeat([]byte{0xa7}, size))
		if _, err := store.Put(context.Background(), request); err != nil {
			t.Fatalf("Put(ciphertext size %d) error = %v", size, err)
		}
	}
	overLimit := contentObjectRequest(t, "operation-over", "object-over", bytes.Repeat([]byte{0xb8}, MaxCiphertextBytes+1))
	if _, err := store.Put(context.Background(), overLimit); !errors.Is(err, ErrInvalidObjectRequest) {
		t.Fatalf("Put(over limit) error = %v, want %v", err, ErrInvalidObjectRequest)
	}
	contentObjectAssertTreeModes(t, filepath.Join(config.StateDir, "content-objects"))
}

func TestFileStorePutQueryReopenAndExactReplay(t *testing.T) {
	store, config := contentObjectStore(t)
	store.now = func() time.Time { return time.Date(2026, 9, 2, 3, 4, 5, 0, time.UTC) }
	request := contentObjectRequest(t, "operation-replay", "object-replay", []byte("opaque-ciphertext-replay"))
	receipt, err := store.Put(context.Background(), request)
	contentObjectNoError(t, err)
	storeHash := sha256.Sum256([]byte(config.StoreID))
	operationHash := sha256.Sum256([]byte(request.OperationID))
	wantReceiptRef := "content-object-receipt://v1/" + hex.EncodeToString(storeHash[:]) + "/" + hex.EncodeToString(operationHash[:])
	if receipt.ReceiptRef != wantReceiptRef || strings.Contains(receipt.ReceiptRef, request.OperationID) {
		t.Fatalf("ReceiptRef = %q, want deterministic opaque ref %q", receipt.ReceiptRef, wantReceiptRef)
	}
	replay, err := store.Put(context.Background(), request)
	contentObjectNoError(t, err)
	contentObjectEqualReceipt(t, replay, receipt)
	query := contentObjectQuery(request)
	queried, err := store.QueryPut(context.Background(), query)
	contentObjectNoError(t, err)
	contentObjectEqualReceipt(t, queried, receipt)
	reopened, err := NewFileStore(config)
	contentObjectNoError(t, err)
	reopenedReceipt, err := reopened.QueryPut(context.Background(), query)
	contentObjectNoError(t, err)
	contentObjectEqualReceipt(t, reopenedReceipt, receipt)
	if got := contentObjectSealedFiles(t, config.StateDir); len(got) != 1 {
		t.Fatalf("sealed files = %v, want exactly one", got)
	}
}

func TestFileStoreQueryMissingReturnsRedactedNotFound(t *testing.T) {
	store, config := contentObjectStore(t)
	request := contentObjectRequest(t, "operation-missing", "object-missing", []byte("opaque-missing"))

	_, err := store.QueryPut(context.Background(), contentObjectQuery(request))
	if !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("QueryPut() error = %v, want %v", err, ErrObjectNotFound)
	}
	if strings.Contains(err.Error(), config.StateDir) || strings.Contains(err.Error(), config.WorkspaceRoot) {
		t.Fatalf("QueryPut() error leaked private path: %v", err)
	}
}

func TestFileStoreRejectsConflictingObjectIdentityAndPayload(t *testing.T) {
	store, _ := contentObjectStore(t)
	request := contentObjectRequest(t, "operation-original", "object-conflict", []byte("ciphertext-original"))
	if _, err := store.Put(context.Background(), request); err != nil {
		t.Fatalf("Put(original) error = %v", err)
	}
	otherOperation := request
	otherOperation.OperationID = "operation-other"
	otherOperation = contentObjectFinalize(t, otherOperation)
	_, err := store.Put(context.Background(), otherOperation)
	contentObjectWantError(t, err, ErrObjectConflict)
	otherCiphertext := request
	otherCiphertext.Ciphertext = []byte("ciphertext-other")
	otherCiphertext.CiphertextSize = int64(len(otherCiphertext.Ciphertext))
	otherCiphertext.CiphertextHash = hashCiphertext(otherCiphertext.Ciphertext)
	otherCiphertext = contentObjectFinalize(t, otherCiphertext)
	_, err = store.Put(context.Background(), otherCiphertext)
	contentObjectWantError(t, err, ErrObjectConflict)
	query := contentObjectQuery(request)
	query.RequestDigest = strings.Repeat("f", 64)
	if query.RequestDigest == request.RequestDigest {
		query.RequestDigest = strings.Repeat("e", 64)
	}
	if _, err := store.QueryPut(context.Background(), query); !errors.Is(err, ErrObjectConflict) {
		t.Fatalf("QueryPut(other digest) error = %v, want %v", err, ErrObjectConflict)
	}
}

func TestFileStoreConcurrentReplayPublishesOneEnvelope(t *testing.T) {
	store, config := contentObjectStore(t)
	store.now = func() time.Time { return time.Date(2026, 9, 2, 6, 7, 8, 0, time.UTC) }
	request := contentObjectRequest(t, "operation-concurrent", "object-concurrent", []byte("opaque-concurrent-ciphertext"))
	results := make(chan PutReceipt, 24)
	errorsFound := make(chan error, 24)
	var workers sync.WaitGroup
	for range 24 {
		workers.Go(func() {
			receipt, err := store.Put(context.Background(), request)
			if err != nil {
				errorsFound <- err
				return
			}
			results <- receipt
		})
	}
	workers.Wait()
	close(results)
	close(errorsFound)
	for err := range errorsFound {
		t.Errorf("concurrent Put() error = %v", err)
	}
	var first PutReceipt
	for receipt := range results {
		if first.ReceiptDigest == "" {
			first = receipt
			continue
		}
		contentObjectEqualReceipt(t, receipt, first)
	}
	if got := contentObjectSealedFiles(t, config.StateDir); len(got) != 1 {
		t.Fatalf("sealed files = %v, want one atomic publication", got)
	}
	if temps, err := filepath.Glob(filepath.Join(store.root, "objects", "v1", "*", ".content-sealed-*")); err != nil || len(temps) != 0 {
		t.Fatalf("temporary publications = %v, error = %v", temps, err)
	}
}

func TestFileStorePutRejectsPreLinkFailure(t *testing.T) {
	store, config := contentObjectStore(t)
	store.ops.link = func(source, target string) error {
		return &os.LinkError{Op: "link", Old: source, New: target, Err: os.ErrPermission}
	}
	request := contentObjectRequest(t, "operation-link-failure", "object-link-failure", []byte("opaque-link-failure"))

	receipt, err := store.Put(context.Background(), request)
	if err == nil || errors.Is(err, ErrObjectCommitOutcomeUnknown) {
		t.Fatalf("Put() error = %v, want known pre-link failure", err)
	}
	if !reflect.DeepEqual(receipt, PutReceipt{}) {
		t.Fatalf("Put() receipt = %#v, want zero receipt", receipt)
	}
	if files := contentObjectSealedFiles(t, config.StateDir); len(files) != 0 {
		t.Fatalf("sealed files = %v, want none", files)
	}
	if strings.Contains(err.Error(), config.StateDir) || strings.Contains(err.Error(), config.WorkspaceRoot) {
		t.Fatalf("Put() error leaked private path: %v", err)
	}
}

func TestFileStorePutReturnsOutcomeUnknownAfterDirectorySyncFailure(t *testing.T) {
	store, config := contentObjectStore(t)
	store.ops.syncDirectory = func(string) error { return errors.New("injected directory sync failure") }
	request := contentObjectRequest(t, "operation-sync-failure", "object-sync-failure", []byte("opaque-sync-failure"))

	receipt, err := store.Put(context.Background(), request)
	if !errors.Is(err, ErrObjectCommitOutcomeUnknown) {
		t.Fatalf("Put() error = %v, want %v", err, ErrObjectCommitOutcomeUnknown)
	}
	if !reflect.DeepEqual(receipt, PutReceipt{}) {
		t.Fatalf("Put() receipt = %#v, want zero receipt", receipt)
	}
	if files := contentObjectSealedFiles(t, config.StateDir); len(files) != 1 {
		t.Fatalf("sealed files = %v, want linked envelope with unknown durability", files)
	}
	queried, err := store.QueryPut(context.Background(), contentObjectQuery(request))
	contentObjectNoError(t, err)
	secondReceipt, err := store.Put(context.Background(), request)
	if !errors.Is(err, ErrObjectCommitOutcomeUnknown) {
		t.Fatalf("second Put() error = %v, want %v while directory sync still fails", err, ErrObjectCommitOutcomeUnknown)
	}
	if !reflect.DeepEqual(secondReceipt, PutReceipt{}) {
		t.Fatalf("second Put() receipt = %#v, want zero receipt", secondReceipt)
	}
	store.ops.syncDirectory = defaultFilePublishOps().syncDirectory
	replay, err := store.Put(context.Background(), request)
	contentObjectNoError(t, err)
	contentObjectEqualReceipt(t, replay, queried)
}

func TestFileStorePutReturnsOutcomeUnknownAfterTempCleanupFailure(t *testing.T) {
	store, config := contentObjectStore(t)
	store.ops.remove = func(string) error { return errors.New("injected temp cleanup failure") }
	request := contentObjectRequest(t, "operation-cleanup-failure", "object-cleanup-failure", []byte("opaque-cleanup-failure"))

	receipt, err := store.Put(context.Background(), request)
	if !errors.Is(err, ErrObjectCommitOutcomeUnknown) {
		t.Fatalf("Put() error = %v, want %v", err, ErrObjectCommitOutcomeUnknown)
	}
	if !reflect.DeepEqual(receipt, PutReceipt{}) {
		t.Fatalf("Put() receipt = %#v, want zero receipt", receipt)
	}
	if files := contentObjectSealedFiles(t, config.StateDir); len(files) != 1 {
		t.Fatalf("sealed files = %v, want linked envelope with unknown durability", files)
	}
}

func TestFileStorePutReconcilesExactNoReplaceRace(t *testing.T) {
	store, config := contentObjectStore(t)
	store.ops.link = func(source, target string) error {
		if err := os.Link(source, target); err != nil {
			return err
		}
		return fs.ErrExist
	}
	request := contentObjectRequest(t, "operation-exact-race", "object-exact-race", []byte("opaque-exact-race"))

	receipt, err := store.Put(context.Background(), request)
	contentObjectNoError(t, err)
	if err := receipt.Validate(); err != nil {
		t.Fatalf("Put() receipt validation error = %v", err)
	}
	if files := contentObjectSealedFiles(t, config.StateDir); len(files) != 1 {
		t.Fatalf("sealed files = %v, want one exact competing publication", files)
	}
}

func TestFileStoreHashesMaliciousObjectIDAndNeverWritesWorkspace(t *testing.T) {
	store, config := contentObjectStore(t)
	objectID := "../../workspace/plaintext.txt"
	request := contentObjectRequest(t, "operation-malicious-id", objectID, []byte("opaque-malicious-id-ciphertext"))
	receipt, err := store.Put(context.Background(), request)
	contentObjectNoError(t, err)
	files := contentObjectSealedFiles(t, config.StateDir)
	if len(files) != 1 {
		t.Fatalf("sealed files = %v, want one", files)
	}
	wantHash := sha256.Sum256([]byte(objectID))
	if !strings.Contains(filepath.Base(files[0]), hex.EncodeToString(wantHash[:])) || strings.Contains(receipt.ObjectRef, objectID) {
		t.Fatalf("object identity leaked into path/ref: file=%q ref=%q", files[0], receipt.ObjectRef)
	}
	contentObjectAssertNoFiles(t, config.WorkspaceRoot)
}

func TestFileStoreRejectsTamperedTruncatedSymlinkAndOversizeEnvelope(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{"tampered", contentObjectTamperFile},
		{"truncated", func(t *testing.T, path string) { contentObjectWriteFile(t, path, []byte("{")) }},
		{"symlink", contentObjectReplaceWithSymlink},
		{"oversize", func(t *testing.T, path string) { contentObjectWriteFile(t, path, make([]byte, maxEnvelopeFileBytes+1)) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, config := contentObjectStore(t)
			request := contentObjectRequest(t, "operation-"+tc.name, "object-"+tc.name, []byte("opaque-"+tc.name+"-ciphertext"))
			if _, err := store.Put(context.Background(), request); err != nil {
				t.Fatalf("Put() error = %v", err)
			}
			path := contentObjectSealedFiles(t, config.StateDir)[0]
			tc.mutate(t, path)
			if _, err := store.QueryPut(context.Background(), contentObjectQuery(request)); !errors.Is(err, ErrObjectCorrupt) {
				t.Fatalf("QueryPut() error = %v, want %v", err, ErrObjectCorrupt)
			}
		})
	}
}

func TestFileStoreRejectsUnknownAndTrailingEnvelopeJSON(t *testing.T) {
	cases := []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{"unknown top-level field", func(data []byte) []byte {
			return bytes.Replace(data, []byte(`{"format_version":`), []byte(`{"unknown":true,"format_version":`), 1)
		}},
		{"unknown receipt field", func(data []byte) []byte {
			return bytes.Replace(data, []byte(`"receipt":{`), []byte(`"receipt":{"unknown":true,`), 1)
		}},
		{"trailing JSON value", func(data []byte) []byte { return append(data, []byte(`{}`)...) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, config := contentObjectStore(t)
			request := contentObjectRequest(t, "operation-strict-"+strings.ReplaceAll(tc.name, " ", "-"), "object-strict-"+strings.ReplaceAll(tc.name, " ", "-"), []byte("opaque-strict-json"))
			if _, err := store.Put(context.Background(), request); err != nil {
				t.Fatalf("Put() error = %v", err)
			}
			path := contentObjectSealedFiles(t, config.StateDir)[0]
			data, err := os.ReadFile(path)
			contentObjectNoError(t, err)
			mutated := tc.mutate(data)
			if bytes.Equal(mutated, data) {
				t.Fatal("test mutation did not change envelope JSON")
			}
			contentObjectWriteFile(t, path, mutated)

			if _, err := store.QueryPut(context.Background(), contentObjectQuery(request)); !errors.Is(err, ErrObjectCorrupt) {
				t.Fatalf("QueryPut() error = %v, want %v", err, ErrObjectCorrupt)
			}
		})
	}
}

func TestFileStoreRejectsSymlinkParentWithoutWritingOutsideRoot(t *testing.T) {
	store, _ := contentObjectStore(t)
	escape := filepath.Join(contentObjectTempRoot(t), "escape")
	contentObjectMkdir(t, escape)
	objectsDir := filepath.Join(store.root, "objects")
	if err := os.Symlink(escape, objectsDir); err != nil {
		t.Fatalf("create objects parent symlink: %v", err)
	}
	request := contentObjectRequest(t, "operation-parent-symlink", "object-parent-symlink", []byte("opaque-parent-symlink"))

	if _, err := store.Put(context.Background(), request); !errors.Is(err, ErrUnsafeStateDir) {
		t.Fatalf("Put() error = %v, want %v", err, ErrUnsafeStateDir)
	}
	contentObjectAssertNoFiles(t, escape)
}

func TestFileStoreEnvelopeContainsNoPlaintextAndLeavesWorkspaceEmpty(t *testing.T) {
	store, config := contentObjectStore(t)
	plaintext := []byte("content-object-plaintext-sentinel-never-persist")
	ciphertext := []byte("opaque-aes-gcm-output-without-source-material")
	request := contentObjectRequest(t, "operation-no-plaintext", "object-no-plaintext", ciphertext)
	if _, err := store.Put(context.Background(), request); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	data, err := os.ReadFile(contentObjectSealedFiles(t, config.StateDir)[0])
	contentObjectNoError(t, err)
	if bytes.Contains(data, plaintext) {
		t.Fatal("sealed envelope leaked plaintext")
	}
	contentObjectAssertNoFiles(t, config.WorkspaceRoot)
}

func TestUnavailableStoreAlwaysRejectsPutAndQuery(t *testing.T) {
	request := contentObjectRequest(t, "operation-unavailable", "object-unavailable", []byte("opaque-unavailable"))
	store := UnavailableStore{}
	if err := store.Validate(); !errors.Is(err, ErrObjectStoreMissing) {
		t.Fatalf("Validate() error = %v, want %v", err, ErrObjectStoreMissing)
	}
	if _, err := store.Put(context.Background(), request); !errors.Is(err, ErrObjectStoreMissing) {
		t.Fatalf("Put() error = %v, want %v", err, ErrObjectStoreMissing)
	}
	if _, err := store.QueryPut(context.Background(), contentObjectQuery(request)); !errors.Is(err, ErrObjectStoreMissing) {
		t.Fatalf("QueryPut() error = %v, want %v", err, ErrObjectStoreMissing)
	}
}

func contentObjectStore(t *testing.T) (*FileStore, Config) {
	t.Helper()
	base := contentObjectTempRoot(t)
	config := Config{StateDir: filepath.Join(base, "state"), WorkspaceRoot: filepath.Join(base, "workspace"), StoreID: "private-store-1"}
	contentObjectMkdir(t, config.StateDir)
	contentObjectMkdir(t, config.WorkspaceRoot)
	store, err := NewFileStore(config)
	contentObjectNoError(t, err)
	return store, config
}

func contentObjectTempRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	contentObjectNoError(t, err)
	return root
}

func contentObjectRequest(t *testing.T, operationID, objectID string, ciphertext []byte) PutRequest {
	t.Helper()
	request := PutRequest{SchemaVersion: 1, OperationID: operationID, ObjectID: objectID, ObjectVersion: 1,
		CiphertextHash: hashCiphertext(ciphertext), CiphertextSize: int64(len(ciphertext)), Ciphertext: append([]byte(nil), ciphertext...)}
	return contentObjectFinalize(t, request)
}

func contentObjectFinalize(t *testing.T, request PutRequest) PutRequest {
	t.Helper()
	finalized, err := FinalizePutRequest(request)
	contentObjectNoError(t, err)
	return finalized
}

func contentObjectQuery(request PutRequest) QueryRequest {
	return QueryRequest{SchemaVersion: request.SchemaVersion, OperationID: request.OperationID, RequestDigest: request.RequestDigest,
		ObjectID: request.ObjectID, ObjectVersion: request.ObjectVersion}
}

func contentObjectSealedFiles(t *testing.T, stateDir string) []string {
	t.Helper()
	pattern := filepath.Join(stateDir, "content-objects", "v1", "objects", "v1", "*", "*.sealed")
	files, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("glob sealed files: %v", err)
	}
	return files
}

func contentObjectAssertTreeModes(t *testing.T, root string) {
	t.Helper()
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		want := os.FileMode(0o600)
		if info.IsDir() {
			want = 0o700
		}
		if info.Mode().Perm() != want {
			t.Errorf("mode(%s) = %o, want %o", path, info.Mode().Perm(), want)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk private object tree: %v", err)
	}
}

func contentObjectAssertNoFiles(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read workspace root: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("workspace contains content-object files: %v", entries)
	}
}

func contentObjectTamperFile(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	contentObjectNoError(t, err)
	data[len(data)-2] ^= 1
	contentObjectWriteFile(t, path, data)
}

func contentObjectReplaceWithSymlink(t *testing.T, path string) {
	t.Helper()
	target := filepath.Join(filepath.Dir(path), "attacker-envelope")
	contentObjectWriteFile(t, target, []byte("{}"))
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove sealed envelope: %v", err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatalf("replace sealed envelope with symlink: %v", err)
	}
}

func contentObjectWriteFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write corrupt envelope: %v", err)
	}
}

func contentObjectWantError(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("Put() error = %v, want %v", err, want)
	}
}

func contentObjectEqualReceipt(t *testing.T, got, want PutReceipt) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("receipt mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}

func contentObjectNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func contentObjectMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("create test directory: %v", err)
	}
}

func contentObjectSizeText(size int) string {
	if size == 1 {
		return "one"
	}
	return "max"
}
