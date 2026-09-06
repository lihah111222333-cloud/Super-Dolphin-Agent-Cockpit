package contentcrypto

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

type testKeyLeaseCreator struct {
	key      []byte
	metadata KeyMetadata
	requests []KeyCreateRequest
	leases   []*EphemeralKeyLease
	records  map[string]testKeyLeaseRecord
}

type testKeyLeaseRecord struct {
	request  KeyCreateRequest
	metadata KeyMetadata
}

type testKeyLeaseConflictError struct{}

func (*testKeyLeaseConflictError) Error() string {
	return "test key operation conflicts with prior request digest"
}

func (p *testKeyLeaseCreator) CreateAES256KeyLease(_ context.Context, request KeyCreateRequest) (*EphemeralKeyLease, error) {
	p.requests = append(p.requests, request)
	record, exists := p.records[request.OperationID]
	if exists && record.request.RequestDigest != request.RequestDigest {
		return nil, &testKeyLeaseConflictError{}
	}
	if !exists {
		record = testKeyLeaseRecord{request: request, metadata: p.metadata}
		record.metadata.Nonce = make([]byte, GCMNonceBytes)
		if _, err := cryptorand.Read(record.metadata.Nonce); err != nil {
			return nil, err
		}
		p.records[request.OperationID] = record
	}
	lease, err := NewEphemeralKeyLease(record.metadata, p.key)
	if err != nil {
		return nil, err
	}
	p.leases = append(p.leases, lease)
	return lease, nil
}

func TestContentCryptoSecurityContract(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{"roundtrip-boundaries-receipts", TestAESGCMSealerRoundTripBoundariesAndReceipts},
		{"idempotency-nonce-aad", TestAESGCMSealerIdempotencyFreshOperationsAndAuthenticatedAAD},
		{"operation-digest-conflict", TestAESGCMSealerCreatorRejectsSameOperationWithDifferentDigest},
		{"invalid-requests", TestAESGCMSealerRejectsInvalidRequestsBeforeCreatingKey},
		{"request-digest-boundary", TestSealRequestDigestBindsBusinessFieldsAndContentIntentBoundary},
		{"sealed-request-binding", TestValidateSealedForRequestRejectsMismatchedReceipt},
		{"lease-close", TestEphemeralKeyLeaseCloseOverwritesAndInvalidates},
		{"receipt-digest", TestKeyReceiptDigestIsRequired},
		{"missing-provider", TestMissingKeyProvidersFailFast},
		{"json-field-registry", TestContentCryptoJSONFieldRegistriesMatchProductionTypes},
		{"secret-fields", TestContentCryptoSecretFieldsNeverExposeRawMaterial},
		{"production-imports", TestContentCryptoProductionImportsStayInfrastructureNeutral},
	}
	for _, test := range tests {
		t.Run(test.name, test.run)
	}
}

func TestValidateSealedForRequestRejectsMismatchedReceipt(t *testing.T) {
	request := validSealRequestFixture(t, []byte("request-binding"))
	sealed, err := mustTestSealer(t, newTestKeyLeaseCreator()).CreateAndSeal(t.Context(), request)
	if err != nil {
		t.Fatalf("CreateAndSeal() error = %v", err)
	}
	if err := ValidateSealedForRequest(request, sealed); err != nil {
		t.Fatalf("ValidateSealedForRequest(valid) error = %v", err)
	}
	for name, mutate := range map[string]func(*SealedPayload){
		"request digest": func(payload *SealedPayload) { payload.RequestDigest = strings.Repeat("d", 64) },
		"AAD digest":     func(payload *SealedPayload) { payload.AADDigest = strings.Repeat("e", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			changed := sealed
			mutate(&changed)
			if err := ValidateSealedForRequest(request, changed); !errors.Is(err, ErrInvalidSealedResult) {
				t.Fatalf("ValidateSealedForRequest(%s) error = %v", name, err)
			}
		})
	}
	changedRequest := request
	changedRequest.Plaintext = []byte("request-bindinh")
	if err := ValidateSealedForRequest(changedRequest, sealed); !errors.Is(err, ErrInvalidSealedResult) {
		t.Fatalf("ValidateSealedForRequest(changed plaintext) error = %v", err)
	}
}

func TestAESGCMSealerRoundTripBoundariesAndReceipts(t *testing.T) {
	for _, size := range []int{1, MaxSingleBytes} {
		t.Run(strconv.Itoa(size)+"-bytes", func(t *testing.T) {
			plaintext := bytes.Repeat([]byte{0x61}, size)
			request := validSealRequestFixture(t, plaintext)
			provider := newTestKeyLeaseCreator()
			sealer := mustTestSealer(t, provider)

			sealed, err := sealer.CreateAndSeal(context.Background(), request)
			if err != nil {
				t.Fatalf("CreateAndSeal() error = %v", err)
			}
			assertSealedReceipt(t, sealed, request, provider)
			assertAESGCMRoundTrip(t, sealed, request, provider.key)
			withoutReceiptDigest := sealed
			withoutReceiptDigest.KeyReceiptDigest = ""
			if err := withoutReceiptDigest.Validate(); !errors.Is(err, ErrInvalidSealedResult) {
				t.Fatalf("Validate() without receipt digest error = %v, want ErrInvalidSealedResult", err)
			}
			if bytes.Equal(sealed.Ciphertext, plaintext) {
				t.Fatal("ciphertext exposes the plaintext")
			}
			if _, _, err := provider.leases[0].snapshot(); !errors.Is(err, ErrKeyLeaseInvalid) {
				t.Fatalf("provider lease remains usable after sealing: %v", err)
			}
		})
	}
}

func TestAESGCMSealerIdempotencyFreshOperationsAndAuthenticatedAAD(t *testing.T) {
	request := validSealRequestFixture(t, []byte("same plaintext"))
	provider := newTestKeyLeaseCreator()
	sealer := mustTestSealer(t, provider)
	first, err := sealer.CreateAndSeal(context.Background(), request)
	if err != nil {
		t.Fatalf("first CreateAndSeal() error = %v", err)
	}
	second, err := sealer.CreateAndSeal(context.Background(), request)
	if err != nil {
		t.Fatalf("second CreateAndSeal() error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("same operation/request did not reproduce its idempotent sealed payload")
	}
	differentOperation := request
	differentOperation.OperationID = "operation-2"
	third, err := sealer.CreateAndSeal(context.Background(), differentOperation)
	if err != nil {
		t.Fatalf("different-operation CreateAndSeal() error = %v", err)
	}
	if bytes.Equal(first.Nonce, third.Nonce) || bytes.Equal(first.Ciphertext, third.Ciphertext) {
		t.Fatal("different operations reused nonce or ciphertext")
	}
	gcm := testGCM(t, provider.key)
	if _, err := gcm.Open(nil, first.Nonce, first.Ciphertext, []byte("wrong-aad")); err == nil {
		t.Fatal("AES-GCM decryption accepted mismatched AAD")
	}
}

func TestAESGCMSealerCreatorRejectsSameOperationWithDifferentDigest(t *testing.T) {
	request := validSealRequestFixture(t, []byte("payload"))
	provider := newTestKeyLeaseCreator()
	sealer := mustTestSealer(t, provider)
	if _, err := sealer.CreateAndSeal(context.Background(), request); err != nil {
		t.Fatalf("first CreateAndSeal() error = %v", err)
	}
	request.Plaintext = []byte("payloae")
	_, err := sealer.CreateAndSeal(context.Background(), request)
	var conflict *testKeyLeaseConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("conflicting CreateAndSeal() error = %v, want testKeyLeaseConflictError", err)
	}
}

func TestAESGCMSealerRejectsInvalidRequestsBeforeCreatingKey(t *testing.T) {
	valid := validSealRequestFixture(t, []byte("payload"))
	tests := map[string]func(*SealRequest){
		"empty plaintext": func(r *SealRequest) { r.Plaintext = nil },
		"over 64 KiB":     func(r *SealRequest) { r.Plaintext = make([]byte, MaxSingleBytes+1) },
		"empty AAD":       func(r *SealRequest) { r.AAD = nil },
		"wrong profile":   func(r *SealRequest) { r.AADProfile = "orch.content.single.aad/v2" },
		"invalid digest":  func(r *SealRequest) { r.ContentIntentDigest = "not-a-digest" },
		"empty business":  func(r *SealRequest) { r.ObjectID = "" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			provider := newTestKeyLeaseCreator()
			sealer := mustTestSealer(t, provider)
			request := valid
			mutate(&request)
			_, err := sealer.CreateAndSeal(context.Background(), request)
			if !errors.Is(err, ErrInvalidSealRequest) {
				t.Fatalf("CreateAndSeal() error = %v, want ErrInvalidSealRequest", err)
			}
			if len(provider.requests) != 0 {
				t.Fatalf("invalid request created %d key leases", len(provider.requests))
			}
		})
	}
}

func TestSealRequestDigestBindsBusinessFieldsAndContentIntentBoundary(t *testing.T) {
	base := validSealRequestFixture(t, []byte("alpha"))
	mutations := map[string]func(*SealRequest){
		"operation":      func(r *SealRequest) { r.OperationID += "-other" },
		"ticket":         func(r *SealRequest) { r.TicketID += "-other" },
		"object":         func(r *SealRequest) { r.ObjectID += "-other" },
		"object version": func(r *SealRequest) { r.ObjectVersion++ },
		"source fence":   func(r *SealRequest) { r.SourceFenceRef += "-other" },
		"media type":     func(r *SealRequest) { r.MediaType = "application/octet-stream" },
		"AAD profile":    func(r *SealRequest) { r.AADProfile += "/other" },
		"content intent": func(r *SealRequest) { r.ContentIntentDigest = strings.Repeat("b", 64) },
		"AAD":            func(r *SealRequest) { r.AAD = []byte("other-aad") },
	}
	baseDigest := testSealRequestDigest(t, base)
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			changed := base
			mutate(&changed)
			if got := testSealRequestDigest(t, changed); got == baseDigest {
				t.Fatalf("request digest ignored changed %s", name)
			}
		})
	}
	assertPlaintextDigestBoundary(t, base, baseDigest)
}

func TestEphemeralKeyLeaseCloseOverwritesAndInvalidates(t *testing.T) {
	providerKey := bytes.Repeat([]byte{0x5a}, AES256KeyBytes)
	metadata := newTestKeyLeaseCreator().metadata
	wantNonce := append([]byte(nil), metadata.Nonce...)
	lease, err := NewEphemeralKeyLease(metadata, providerKey)
	if err != nil {
		t.Fatalf("NewEphemeralKeyLease() error = %v", err)
	}
	assertLeaseNonceOwnership(t, lease, metadata, wantNonce)
	backing := lease.key
	providerKey[0] = 0
	if backing[0] == 0 {
		t.Fatal("lease retained provider key backing storage")
	}
	if err := lease.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !bytes.Equal(backing, make([]byte, AES256KeyBytes)) || lease.key != nil || !lease.closed {
		t.Fatal("Close() did not overwrite and invalidate the raw key")
	}
	if _, _, err := lease.snapshot(); !errors.Is(err, ErrKeyLeaseInvalid) {
		t.Fatalf("snapshot() after Close error = %v, want ErrKeyLeaseInvalid", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func assertLeaseNonceOwnership(t *testing.T, lease *EphemeralKeyLease, metadata KeyMetadata, wantNonce []byte) {
	t.Helper()
	metadata.Nonce[0] ^= 0xff
	leaseMetadata, _, err := lease.snapshot()
	if err != nil || !bytes.Equal(leaseMetadata.Nonce, wantNonce) {
		t.Fatalf("lease nonce changed through provider slice: metadata=%x error=%v", leaseMetadata.Nonce, err)
	}
	leaseMetadata.Nonce[1] ^= 0xff
	secondMetadata, _, err := lease.snapshot()
	if err != nil || !bytes.Equal(secondMetadata.Nonce, wantNonce) {
		t.Fatalf("lease nonce changed through snapshot slice: metadata=%x error=%v", secondMetadata.Nonce, err)
	}
}

func TestKeyReceiptDigestIsRequired(t *testing.T) {
	metadata := newTestKeyLeaseCreator().metadata
	for name, digest := range map[string]string{
		"empty": "", "non-hex": strings.Repeat("g", 64), "uppercase": strings.Repeat("A", 64),
	} {
		t.Run(name, func(t *testing.T) {
			metadata.KeyReceiptDigest = digest
			if _, err := NewEphemeralKeyLease(metadata, bytes.Repeat([]byte{0x31}, AES256KeyBytes)); !errors.Is(err, ErrKeyLeaseInvalid) {
				t.Fatalf("NewEphemeralKeyLease() error = %v, want ErrKeyLeaseInvalid", err)
			}
		})
	}
}

func TestMissingKeyProvidersFailFast(t *testing.T) {
	var typedNil *testKeyLeaseCreator
	if _, err := NewAESGCMSealer(typedNil); !errors.Is(err, ErrKeyProviderMissing) {
		t.Fatalf("NewAESGCMSealer(typed nil) error = %v, want ErrKeyProviderMissing", err)
	}
	if err := (UnavailableSealer{}).Validate(); !errors.Is(err, ErrKeyProviderMissing) {
		t.Fatalf("UnavailableSealer.Validate() error = %v, want ErrKeyProviderMissing", err)
	}
	_, err := (UnavailableSealer{}).CreateAndSeal(context.Background(), validSealRequestFixture(t, []byte("payload")))
	if !errors.Is(err, ErrKeyProviderMissing) {
		t.Fatalf("UnavailableSealer.CreateAndSeal() error = %v, want ErrKeyProviderMissing", err)
	}
}

func newTestKeyLeaseCreator() *testKeyLeaseCreator {
	return &testKeyLeaseCreator{
		key: bytes.Repeat([]byte{0x42}, AES256KeyBytes),
		metadata: KeyMetadata{
			Algorithm: AlgorithmAES256GCM, KeyWrapProfile: "test-wrap/v1",
			KeyHandleRef: "test-key-handle", KeyReceiptRef: "test-key-receipt",
			KeyReceiptDigest: strings.Repeat("c", 64), Nonce: bytes.Repeat([]byte{0x24}, GCMNonceBytes),
		},
		records: make(map[string]testKeyLeaseRecord),
	}
}

func validSealRequestFixture(t *testing.T, plaintext []byte) SealRequest {
	t.Helper()
	return SealRequest{
		OperationID: "operation-1", TicketID: "ticket-1", ObjectID: "object-1", ObjectVersion: 7,
		SourceFenceRef: "source-fence-1", MediaType: "text/plain", AADProfile: AADProfileV1,
		ContentIntentDigest: keyedContentIntentDigest(t, plaintext), Plaintext: plaintext, AAD: []byte("tenant=1;object=object-1"),
	}
}

func keyedContentIntentDigest(t *testing.T, plaintext []byte) string {
	t.Helper()
	mac := hmac.New(sha256.New, []byte("test-only-owner-content-intent-key"))
	if _, err := mac.Write(plaintext); err != nil {
		t.Fatalf("write keyed content intent fixture: %v", err)
	}
	return stringHex(mac.Sum(nil))
}

func stringHex(value []byte) string {
	const digits = "0123456789abcdef"
	encoded := make([]byte, len(value)*2)
	for index, item := range value {
		encoded[index*2] = digits[item>>4]
		encoded[index*2+1] = digits[item&0x0f]
	}
	return string(encoded)
}

func mustTestSealer(t *testing.T, provider KeyLeaseCreator) *AESGCMSealer {
	t.Helper()
	sealer, err := NewAESGCMSealer(provider)
	if err != nil {
		t.Fatalf("NewAESGCMSealer() error = %v", err)
	}
	if err := sealer.Validate(); err != nil {
		t.Fatalf("AESGCMSealer.Validate() error = %v", err)
	}
	return sealer
}

func assertSealedReceipt(t *testing.T, sealed SealedPayload, request SealRequest, provider *testKeyLeaseCreator) {
	t.Helper()
	if err := sealed.Validate(); err != nil {
		t.Fatalf("SealedPayload.Validate() error = %v", err)
	}
	type mappedReceipt struct {
		operationID, algorithm, wrapProfile, handleRef, receiptRef string
		keyReceiptDigest, aadDigest                                string
		plaintextSize, ciphertextSize                              int64
		nonce                                                      []byte
	}
	gotMapped := mappedReceipt{sealed.OperationID, sealed.Algorithm, sealed.KeyWrapProfile, sealed.KeyHandleRef,
		sealed.KeyReceiptRef, sealed.KeyReceiptDigest, sealed.AADDigest, sealed.PlaintextSize, sealed.CiphertextSize, sealed.Nonce}
	metadata := provider.records[request.OperationID].metadata
	wantMapped := mappedReceipt{request.OperationID, provider.metadata.Algorithm, provider.metadata.KeyWrapProfile,
		provider.metadata.KeyHandleRef, provider.metadata.KeyReceiptRef, provider.metadata.KeyReceiptDigest, hashBytes(request.AAD),
		int64(len(request.Plaintext)), int64(len(sealed.Ciphertext)), metadata.Nonce}
	if !reflect.DeepEqual(gotMapped, wantMapped) {
		t.Fatalf("sealed receipt mapping = %+v, want %+v", gotMapped, wantMapped)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("key create request count = %d, want 1", len(provider.requests))
	}
	created := provider.requests[0]
	wantCreated := KeyCreateRequest{request.OperationID, sealed.RequestDigest, request.ObjectID, request.ObjectVersion, AlgorithmAES256GCM}
	if !reflect.DeepEqual(created, wantCreated) {
		t.Fatalf("key create receipt binding = %+v, want %+v", created, wantCreated)
	}
}

func assertAESGCMRoundTrip(t *testing.T, sealed SealedPayload, request SealRequest, key []byte) {
	t.Helper()
	plaintext, err := testGCM(t, key).Open(nil, sealed.Nonce, sealed.Ciphertext, request.AAD)
	if err != nil {
		t.Fatalf("AES-GCM Open() error = %v", err)
	}
	if !bytes.Equal(plaintext, request.Plaintext) {
		t.Fatalf("decrypted plaintext length = %d, want %d", len(plaintext), len(request.Plaintext))
	}
}

func testGCM(t *testing.T, key []byte) cipher.AEAD {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes.NewCipher() error = %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("cipher.NewGCM() error = %v", err)
	}
	return gcm
}

func testSealRequestDigest(t *testing.T, request SealRequest) string {
	t.Helper()
	digest, err := sealRequestDigest(request, hashBytes(request.AAD))
	if err != nil {
		t.Fatalf("sealRequestDigest() error = %v", err)
	}
	return digest
}

func assertPlaintextDigestBoundary(t *testing.T, base SealRequest, baseDigest string) {
	t.Helper()
	sameSize := base
	sameSize.Plaintext = []byte("omega")
	if got := testSealRequestDigest(t, sameSize); got == baseDigest {
		t.Fatal("request digest ignored changed plaintext bytes when the caller-owned digest was reused")
	}
	changedIntent := base
	changedIntent.ContentIntentDigest = keyedContentIntentDigest(t, sameSize.Plaintext)
	if got := testSealRequestDigest(t, changedIntent); got == baseDigest {
		t.Fatal("request digest ignored caller-owned keyed content intent digest")
	}
	differentSize := base
	differentSize.Plaintext = append(differentSize.Plaintext, '!')
	if got := testSealRequestDigest(t, differentSize); got == baseDigest {
		t.Fatal("request digest ignored plaintext size")
	}
}
