package oss

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type portableObjectFixture struct {
	mu                     sync.Mutex
	objects                map[string]portableFixtureObject
	deletes                []string
	confirms               []string
	mutateDataOnCreate     bool
	mutateMetadataOnCreate bool
}

type portableFixtureObject struct {
	data     []byte
	metadata PortableArtifactMetadata
}

func (f *portableObjectFixture) Create(_ context.Context, localPath, key string) error {
	data, err := os.ReadFile(localPath)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, exists := f.objects[key]; exists {
		return errors.New("FileAlreadyExists")
	}
	object := portableFixtureObject{data: append([]byte(nil), data...), metadata: portableBytesMetadata(data)}
	if f.mutateDataOnCreate {
		object.data = []byte("body drift")
	}
	if f.mutateMetadataOnCreate {
		object.metadata = portableBytesMetadata([]byte("metadata lie"))
	}
	f.objects[key] = object
	return nil
}

func (f *portableObjectFixture) Stat(_ context.Context, key string) (PortableArtifactMetadata, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	object, ok := f.objects[key]
	if !ok {
		return PortableArtifactMetadata{}, errors.New("not found")
	}
	return object.metadata, nil
}

func (f *portableObjectFixture) Get(_ context.Context, key string, maxBytes int64) (io.ReadCloser, int64, PortableArtifactMetadata, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	object, ok := f.objects[key]
	if !ok {
		return nil, 0, PortableArtifactMetadata{}, errors.New("not found")
	}
	if int64(len(object.data)) > maxBytes {
		return nil, 0, PortableArtifactMetadata{}, errors.New("object exceeds bound")
	}
	return io.NopCloser(strings.NewReader(string(object.data))), int64(len(object.data)), object.metadata, nil
}

func (f *portableObjectFixture) DeletePrefix(_ context.Context, prefix string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletes = append(f.deletes, prefix)
	for key := range f.objects {
		if strings.HasPrefix(key, prefix) {
			delete(f.objects, key)
		}
	}
	return nil
}

func (f *portableObjectFixture) ConfirmPrefixEmpty(_ context.Context, prefix string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.confirms = append(f.confirms, prefix)
	for key := range f.objects {
		if strings.HasPrefix(key, prefix) {
			return false, nil
		}
	}
	return true, nil
}

func TestPortableArtifactKeysRejectTraversalAndSeparateImmutableKinds(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	for _, bad := range []string{"../portable/", "/tmp/portable/", "portable/../root/", "portable\\root/"} {
		if _, err := CanonicalPortableFinalBundleKey(bad, digest); err == nil {
			t.Fatalf("CanonicalPortableFinalBundleKey(%q) accepted traversal", bad)
		}
	}
	keys, err := NewPortableArtifactKeys("portable/", "job-1", digest, digest)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(keys.InputStagingKey, ".input") || !strings.HasSuffix(keys.FinalBundleKey, ".bundle") || !strings.HasSuffix(keys.ReceiptKey, ".json") {
		t.Fatalf("keys = %#v", keys)
	}
	if strings.HasPrefix(keys.ReceiptKey, "portable/artifacts/") || keys.FinalBundleKey == keys.ReceiptKey {
		t.Fatalf("artifact and receipt keys are not separated: %#v", keys)
	}
	if _, err := CanonicalPortableInputStagingKey("portable/", "../job", digest); err == nil {
		t.Fatal("input staging key accepted traversal")
	}
}

func TestPortableArtifactCreateOnlyIsIdempotentOrConflicting(t *testing.T) {
	fixture := &portableObjectFixture{objects: make(map[string]portableFixtureObject)}
	store, err := NewPortableArtifactStore(fixture, "portable/")
	if err != nil {
		t.Fatal(err)
	}
	path := writePortableFixtureFile(t, []byte("bundle"))
	metadata := portableBytesMetadata([]byte("bundle"))
	key, err := CanonicalPortableFinalBundleKey("portable/", metadata.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateArtifact(context.Background(), path, key, metadata); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if err := store.CreateArtifact(context.Background(), path, key, metadata); err != nil {
		t.Fatalf("same-byte create: %v", err)
	}
	fixture.mu.Lock()
	fixture.objects[key] = portableFixtureObject{data: []byte("bundle"), metadata: portableBytesMetadata([]byte("other-metadata"))}
	fixture.mu.Unlock()
	if err := store.CreateArtifact(context.Background(), path, key, metadata); !errors.Is(err, ErrPortableArtifactConflict) {
		t.Fatalf("metadata-drift create error = %v, want conflict", err)
	}
	fixture.mu.Lock()
	fixture.objects[key] = portableFixtureObject{data: []byte("bundle"), metadata: metadata}
	fixture.mu.Unlock()
	driftPath := writePortableFixtureFile(t, []byte("drifted"))
	if err := store.CreateArtifact(context.Background(), driftPath, key, metadata); !errors.Is(err, ErrPortableArtifactConflict) {
		t.Fatalf("different-byte create error = %v, want conflict", err)
	}
}

func TestPortableArtifactCreateOnlyRequiresReadbackBodyAndMetadata(t *testing.T) {
	data := []byte("bundle")
	metadata := portableBytesMetadata(data)
	localPath := writePortableFixtureFile(t, data)
	key, err := CanonicalPortableFinalBundleKey("portable/", metadata.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name           string
		mutateData     bool
		mutateMetadata bool
	}{
		{name: "body drift", mutateData: true},
		{name: "metadata lie", mutateMetadata: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := &portableObjectFixture{objects: make(map[string]portableFixtureObject), mutateDataOnCreate: test.mutateData, mutateMetadataOnCreate: test.mutateMetadata}
			store, err := NewPortableArtifactStore(fixture, "portable/")
			if err != nil {
				t.Fatal(err)
			}
			if err := store.CreateArtifact(context.Background(), localPath, key, metadata); err == nil {
				t.Fatal("CreateArtifact accepted unverified readback")
			}
		})
	}
}

func TestPortableArtifactReceiptRequiresFinalArtifactAndFinalObjectsCannotBeDeleted(t *testing.T) {
	fixture := &portableObjectFixture{objects: make(map[string]portableFixtureObject)}
	store, err := NewPortableArtifactStore(fixture, "portable/")
	if err != nil {
		t.Fatal(err)
	}
	artifactData := []byte("bundle")
	artifactPath := writePortableFixtureFile(t, artifactData)
	artifactMetadata := portableBytesMetadata(artifactData)
	artifactKey, err := CanonicalPortableFinalBundleKey("portable/", artifactMetadata.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	receiptData := []byte(`{"schema":"portable-builder-receipt/v1"}`)
	receiptPath := writePortableFixtureFile(t, receiptData)
	receiptMetadata := portableBytesMetadata(receiptData)
	receiptKey, err := CanonicalPortableReceiptKey("portable/", artifactMetadata.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateReceipt(context.Background(), receiptPath, receiptKey, artifactKey, receiptMetadata, artifactMetadata); err == nil {
		t.Fatal("receipt was created before final artifact")
	}
	if err := store.CreateArtifact(context.Background(), artifactPath, artifactKey, artifactMetadata); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateReceipt(context.Background(), receiptPath, receiptKey, artifactKey, receiptMetadata, artifactMetadata); err != nil {
		t.Fatalf("receipt after artifact: %v", err)
	}
	if err := store.Delete(context.Background(), artifactKey); !errors.Is(err, ErrPortableArtifactImmutable) {
		t.Fatalf("Delete(final artifact) = %v", err)
	}
	if _, err := store.ConfirmAbsent(context.Background(), receiptKey); !errors.Is(err, ErrPortableArtifactImmutable) {
		t.Fatalf("ConfirmAbsent(receipt) = %v", err)
	}
}

func TestPortableArtifactInputCleanupAndReadOnlyResolverAreBounded(t *testing.T) {
	fixture := &portableObjectFixture{objects: make(map[string]portableFixtureObject)}
	store, err := NewPortableArtifactStore(fixture, "portable/")
	if err != nil {
		t.Fatal(err)
	}
	runPortableInputCleanup(t, store)
	runPortableReadOnlyResolver(t, store)
}

func runPortableInputCleanup(t *testing.T, store *PortableArtifactStore) {
	t.Helper()
	data := []byte("input")
	localPath := writePortableFixtureFile(t, data)
	metadata := portableBytesMetadata(data)
	key, err := CanonicalPortableInputStagingKey("portable/", "job-1", metadata.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateInput(context.Background(), localPath, key, metadata); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	absent, err := store.ConfirmAbsent(context.Background(), key)
	if err != nil || !absent {
		t.Fatalf("input ConfirmAbsent = %v, %v", absent, err)
	}
}

func runPortableReadOnlyResolver(t *testing.T, store *PortableArtifactStore) {
	t.Helper()
	data := []byte("input")
	localPath := writePortableFixtureFile(t, data)
	metadata := portableBytesMetadata(data)
	artifactKey, err := CanonicalPortableFinalBundleKey("portable/", metadata.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateArtifact(context.Background(), localPath, artifactKey, metadata); err != nil {
		t.Fatal(err)
	}
	body, size, got, err := store.Resolve(context.Background(), PortableArtifactKind, artifactKey)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = body.Close() }()
	read, err := io.ReadAll(body)
	if err != nil || string(read) != string(data) || size != int64(len(data)) || got != metadata {
		t.Fatalf("Resolve = %q, %d, %#v, %v", read, size, got, err)
	}
}

func writePortableFixtureFile(t *testing.T, data []byte) string {
	t.Helper()
	filePath := filepath.Join(t.TempDir(), "object")
	if err := os.WriteFile(filePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return filePath
}

func TestPortableArtifactResolveRejectsWrongBodyEarlyEOFAndCloseFailure(t *testing.T) {
	want := portableBytesMetadata([]byte("expected body"))
	key, err := CanonicalPortableFinalBundleKey("portable/", want.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	closeErr := errors.New("underlying close failed")
	client := &portableResolveClient{
		stat:     want,
		body:     &portableFailingReadCloser{reader: bytes.NewReader([]byte("short")), closeErr: closeErr},
		size:     want.SizeBytes,
		metadata: want,
	}
	store, err := NewPortableArtifactStore(client, "portable/")
	if err != nil {
		t.Fatal(err)
	}
	body, _, _, err := store.Resolve(context.Background(), PortableArtifactKind, key)
	if err != nil {
		t.Fatal(err)
	}
	_, readErr := io.ReadAll(body)
	closeReadErr := body.Close()
	if !errors.Is(readErr, io.ErrUnexpectedEOF) {
		t.Fatalf("Resolve read error = %v, want unexpected EOF", readErr)
	}
	if !errors.Is(closeReadErr, closeErr) {
		t.Fatalf("Resolve close error = %v, want %v", closeReadErr, closeErr)
	}
}

func TestPortableArtifactResolveRejectsOverlongAndDriftedBodies(t *testing.T) {
	wantData := []byte("expected body")
	want := portableBytesMetadata(wantData)
	key, err := CanonicalPortableFinalBundleKey("portable/", want.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		body []byte
		want error
	}{
		{name: "overlong", body: append(append([]byte(nil), wantData...), 'x'), want: ErrPortableArtifactBodyTooLong},
		{name: "hash drift", body: []byte("differenttext"), want: ErrPortableArtifactBodyMismatch},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &portableResolveClient{
				stat:     want,
				body:     &portableFailingReadCloser{reader: bytes.NewReader(test.body)},
				size:     want.SizeBytes,
				metadata: want,
			}
			store, err := NewPortableArtifactStore(client, "portable/")
			if err != nil {
				t.Fatal(err)
			}
			body, _, _, err := store.Resolve(context.Background(), PortableArtifactKind, key)
			if err != nil {
				t.Fatal(err)
			}
			_, readErr := io.ReadAll(body)
			if !errors.Is(readErr, test.want) {
				t.Fatalf("Resolve read error = %v, want %v", readErr, test.want)
			}
			if err := body.Close(); !errors.Is(err, test.want) {
				t.Fatalf("Resolve close = %v, want %v", err, test.want)
			}
		})
	}
}

func TestPortableArtifactResolveCloseDrainsUnreadBody(t *testing.T) {
	data := []byte("expected body")
	want := portableBytesMetadata(data)
	key, err := CanonicalPortableFinalBundleKey("portable/", want.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	underlying := &portableFailingReadCloser{reader: bytes.NewReader(data)}
	client := &portableResolveClient{stat: want, body: underlying, size: want.SizeBytes, metadata: want}
	store, err := NewPortableArtifactStore(client, "portable/")
	if err != nil {
		t.Fatal(err)
	}
	body, _, _, err := store.Resolve(context.Background(), PortableArtifactKind, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := body.Read(make([]byte, 1)); err != nil {
		t.Fatalf("initial partial read = %v", err)
	}
	if err := body.Close(); err != nil {
		t.Fatalf("Close after partial read = %v", err)
	}
	if underlying.closeCount != 1 {
		t.Fatalf("underlying Close count = %d, want 1", underlying.closeCount)
	}
}

func TestPortableArtifactResolveClosesBodyWhenGetFails(t *testing.T) {
	want := portableBytesMetadata([]byte("expected body"))
	key, err := CanonicalPortableFinalBundleKey("portable/", want.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	getErr := errors.New("get failed")
	closeErr := errors.New("close failed")
	underlying := &portableFailingReadCloser{reader: bytes.NewReader(nil), closeErr: closeErr}
	client := &portableResolveClient{stat: want, body: underlying, size: want.SizeBytes, metadata: want, getErr: getErr}
	store, err := NewPortableArtifactStore(client, "portable/")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := store.Resolve(context.Background(), PortableArtifactKind, key); !errors.Is(err, getErr) || !errors.Is(err, closeErr) {
		t.Fatalf("Resolve Get error = %v, want get and close errors", err)
	}
	if underlying.closeCount != 1 {
		t.Fatalf("underlying Close count = %d, want 1", underlying.closeCount)
	}
}

type portableResolveClient struct {
	stat      PortableArtifactMetadata
	body      io.ReadCloser
	size      int64
	metadata  PortableArtifactMetadata
	getErr    error
	createErr error
}

func (c *portableResolveClient) Create(context.Context, string, string) error {
	if c.createErr != nil {
		return c.createErr
	}
	return errors.New("unexpected create")
}

func (c *portableResolveClient) Stat(context.Context, string) (PortableArtifactMetadata, error) {
	return c.stat, nil
}

func (c *portableResolveClient) Get(context.Context, string, int64) (io.ReadCloser, int64, PortableArtifactMetadata, error) {
	return c.body, c.size, c.metadata, c.getErr
}

func (c *portableResolveClient) DeletePrefix(context.Context, string) error {
	return errors.New("unexpected delete")
}

func (c *portableResolveClient) ConfirmPrefixEmpty(context.Context, string) (bool, error) {
	return false, errors.New("unexpected confirm")
}

type portableFailingReadCloser struct {
	reader     *bytes.Reader
	closeErr   error
	closeCount int
}

func (r *portableFailingReadCloser) Read(p []byte) (int, error) {
	return r.reader.Read(p)
}

func (r *portableFailingReadCloser) Close() error {
	r.closeCount++
	return r.closeErr
}
