package contentkeychain

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/contentcrypto"
)

type fakeNative struct {
	transform           func(int, []byte) []byte
	records             map[string][]byte
	reads, adds, closes int
	readFault           func(int) error
	addFault            error
	winner              []byte
	passed              []byte
}

func (f *fakeNative) read(service, account string) ([]byte, error) {
	f.reads++
	if f.readFault != nil {
		if err := f.readFault(f.reads); err != nil {
			return nil, err
		}
	}
	data, ok := f.records[service+account]
	if !ok {
		return nil, errNotFound
	}
	out := append([]byte(nil), data...)
	if f.transform != nil {
		out = f.transform(f.reads, out)
	}
	return out, nil
}
func (f *fakeNative) add(service, account string, data []byte) error {
	f.adds++
	f.passed = data
	if f.addFault != nil {
		return f.addFault
	}
	if f.winner != nil {
		f.records[service+account] = append([]byte(nil), f.winner...)
		return errDuplicate
	}
	if _, ok := f.records[service+account]; ok {
		return errDuplicate
	}
	f.records[service+account] = append([]byte(nil), data...)
	return nil
}
func (f *fakeNative) checkPrivate() error { return nil }

func (f *fakeNative) close() error { f.closes++; return nil }
func fixture(t *testing.T) (*Provider, *fakeNative) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	err = os.Chmod(root, 0700)
	require.NoError(t, err)
	path := filepath.Join(root, "fixture-not-a-keychain")
	err = os.WriteFile(path, []byte("unit fixture only"), 0600)
	require.NoError(t, err)
	info, err := privatePath(path)
	require.NoError(t, err)
	file, err := os.Open(path)
	require.NoError(t, err)
	f := &fakeNative{records: make(map[string][]byte)}
	directory, err := os.Open(filepath.Dir(path))
	require.NoError(t, err)
	directoryInfo, err := directory.Stat()
	require.NoError(t, err)
	p := &Provider{config: Config{path, "unit-scope"}, file: file, identity: info, store: f, directory: directory, directoryIdentity: directoryInfo}
	t.Cleanup(func() {
		if err := p.Close(); err != nil {
			t.Error(err)
		}
	})
	return p, f
}
func requestFixture() contentcrypto.KeyCreateRequest {
	return contentcrypto.KeyCreateRequest{OperationID: "operation-one", RequestDigest: strings.Repeat("a", 64), ObjectID: "object-one", ObjectVersion: 1, Algorithm: contentcrypto.AlgorithmAES256GCM}
}
func createOK(t *testing.T, p *Provider, r contentcrypto.KeyCreateRequest) {
	t.Helper()
	lease, err := p.CreateAES256KeyLease(context.Background(), r)
	require.NoError(t, err)
	err = lease.Close()
	require.NoError(t, err)
}
func TestKeychainAtomicReplayFields(t *testing.T) {
	p, f := fixture(t)
	r := requestFixture()
	createOK(t, p, r)
	createOK(t, p, r)
	if f.adds != 1 {
		t.Fatalf("adds=%d", f.adds)
	}
	if !bytes.Equal(f.passed, make([]byte, len(f.passed))) {
		t.Fatal("add secret buffer retained")
	}
	typ := reflect.TypeOf(r)
	for i := 0; i < typ.NumField(); i++ {
		t.Run(typ.Field(i).Name, func(t *testing.T) {
			changed := r
			v := reflect.ValueOf(&changed).Elem().Field(i)
			switch v.Kind() {
			case reflect.String:
				if typ.Field(i).Name == "RequestDigest" {
					v.SetString(strings.Repeat("b", 64))
				} else {
					v.SetString(v.String() + "-changed")
				}
			case reflect.Int64:
				v.SetInt(v.Int() + 1)
			default:
				t.Fatal("uncovered request field kind")
			}
			data := append([]byte(nil), f.records[p.config.Namespace+accountFor(r.OperationID)]...)
			lease, err := leaseFrom(data, changed)
			if lease != nil || !errors.Is(err, ErrConflict) {
				t.Fatalf("field %s not bound: %v", typ.Field(i).Name, err)
			}
		})
	}
}
func TestKeychainRaceAndFaults(t *testing.T) {
	r := requestFixture()
	t.Run("race winner", testRaceWinner)
	t.Run("ack mismatch", testAckMismatch)

	for _, phase := range []string{"read", "add", "ack"} {
		t.Run(phase, func(t *testing.T) {
			p, f := fixture(t)
			fault := errors.New("fixture native failure")
			if phase == "add" {
				f.addFault = fault
			} else {
				f.readFault = func(n int) error {
					if phase == "read" || n == 2 {
						return fault
					}
					return nil
				}
			}
			lease, err := p.CreateAES256KeyLease(context.Background(), r)
			require.Nil(t, lease)
			require.ErrorIs(t, err, fault)
			if phase == "read" && f.adds != 0 {
				t.Fatal("read failure treated as missing")
			}
			if phase == "ack" {
				f.readFault = nil
				createOK(t, p, r)
				if f.adds != 1 {
					t.Fatal("ack uncertainty created second owner")
				}
			}
		})
	}
}
func TestKeychainContextsCloseAndIdentity(t *testing.T) {
	p, f := fixture(t)
	r := requestFixture()
	var typedNil *nilContext
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, ctx := range []context.Context{nil, typedNil, canceled} {
		if lease, err := p.CreateAES256KeyLease(ctx, r); err == nil || lease != nil {
			t.Fatal("bad context accepted")
		}
	}
	if f.reads != 0 {
		t.Fatal("bad context reached native")
	}
	require.NoError(t, p.Close())
	require.NoError(t, p.Close())
	if f.closes != 1 {
		t.Fatal("non-idempotent close")
	}
	if _, err := p.CreateAES256KeyLease(context.Background(), r); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	p, _ = fixture(t)
	require.NoError(t, os.Rename(p.config.KeychainPath, p.config.KeychainPath+".old"))
	require.NoError(t, os.WriteFile(p.config.KeychainPath, []byte("replacement"), 0600))
	if _, err := p.CreateAES256KeyLease(context.Background(), r); !errors.Is(err, ErrUnsafePath) {
		t.Fatal(err)
	}
}

type nilContext struct{ context.Context }

func TestKeychainSealerRoundtrip(t *testing.T) {
	p, f := fixture(t)
	sealer, err := contentcrypto.NewAESGCMSealer(p)
	require.NoError(t, err)
	request := contentcrypto.SealRequest{OperationID: "roundtrip", TicketID: "ticket", ObjectID: "object", ObjectVersion: 1, SourceFenceRef: "fence", MediaType: "text/plain", AADProfile: contentcrypto.AADProfileV1, ContentIntentDigest: strings.Repeat("c", 64), Plaintext: []byte("known private content"), AAD: []byte("bound aad")}
	sealed, err := sealer.CreateAndSeal(context.Background(), request)
	require.NoError(t, err)
	repeated, err := sealer.CreateAndSeal(context.Background(), request)
	require.NoError(t, err)
	if !reflect.DeepEqual(sealed, repeated) {
		t.Fatal("replay metadata or ciphertext changed")
	}
	raw := f.records[p.config.Namespace+accountFor(request.OperationID)]
	record, err := decodeRecord(raw)
	require.NoError(t, err)
	defer clear(record.Key)
	block, err := aes.NewCipher(record.Key)
	require.NoError(t, err)
	gcm, err := cipher.NewGCM(block)
	require.NoError(t, err)
	plaintext, err := gcm.Open(nil, sealed.Nonce, sealed.Ciphertext, request.AAD)
	require.NoError(t, err)
	defer clear(plaintext)
	if !bytes.Equal(plaintext, request.Plaintext) {
		t.Fatal("plaintext mismatch")
	}
	public, err := json.Marshal(sealed)
	require.NoError(t, err)
	if bytes.Contains(public, []byte(record.RequestDigest)) || bytes.Contains(public, []byte(p.config.KeychainPath)) || bytes.Contains(public, record.Key) {
		t.Fatal("public secret disclosure")
	}
	request.OperationID = "other-operation"
	other, err := sealer.CreateAndSeal(context.Background(), request)
	require.NoError(t, err)
	if bytes.Equal(other.Nonce, sealed.Nonce) {
		t.Fatal("new operation reused nonce")
	}
}

func TestKeychain(t *testing.T) {
	t.Run("atomic_replacement", TestKeychainAtomicFileReplacementContract)
	t.Run("atomic", TestKeychainAtomicReplayFields)
	t.Run("race_faults", TestKeychainRaceAndFaults)
	t.Run("contexts_identity", TestKeychainContextsCloseAndIdentity)
	t.Run("sealer", TestKeychainSealerRoundtrip)
	t.Run("fields", TestKeychainRecordFieldGuard)
	t.Run("corruption", TestKeychainCorruptRecords)
	t.Run("paths", TestKeychainPathValidation)
	t.Run("source", TestKeychainSourceBoundaries)
	t.Run("invalid", TestKeychainInvalidRequests)
}

func testRaceWinner(t *testing.T) {
	r := requestFixture()

	winner, wf := fixture(t)
	createOK(t, winner, r)
	p, f := fixture(t)
	f.winner = wf.records[winner.config.Namespace+accountFor(r.OperationID)]
	createOK(t, p, r)
	if f.adds != 1 || f.reads != 2 || !bytes.Equal(f.records[p.config.Namespace+accountFor(r.OperationID)], f.winner) {
		t.Fatal("race did not replay winner")
	}
	if !bytes.Equal(f.passed, make([]byte, len(f.passed))) {
		t.Fatal("race loser secret retained")
	}
}

func testAckMismatch(t *testing.T) {
	r := requestFixture()

	p, f := fixture(t)
	f.transform = func(n int, data []byte) []byte {
		if n != 2 {
			return data
		}
		record, err := decodeRecord(data)
		require.NoError(t, err)
		clear(data)
		defer clear(record.Key)
		record.Key[0] ^= 1
		changed, err := json.Marshal(record)
		require.NoError(t, err)
		return changed
	}
	if lease, err := p.CreateAES256KeyLease(context.Background(), r); lease != nil || !errors.Is(err, ErrCorrupt) {
		t.Fatal("unverified add accepted")
	}
	f.transform = nil
	createOK(t, p, r)
	if f.adds != 1 {
		t.Fatal("readback failure created new owner")
	}
}

func TestKeychainInvalidRequests(t *testing.T) {
	p, f := fixture(t)
	request := requestFixture()
	typ := reflect.TypeOf(request)
	for i := 0; i < typ.NumField(); i++ {
		t.Run(typ.Field(i).Name, func(t *testing.T) {
			changed := request
			reflect.ValueOf(&changed).Elem().Field(i).SetZero()
			lease, err := p.CreateAES256KeyLease(context.Background(), changed)
			require.Nil(t, lease)
			require.ErrorIs(t, err, ErrInvalid)
		})
	}
	require.Zero(t, f.reads)
	var missing *Provider
	lease, err := missing.CreateAES256KeyLease(context.Background(), request)
	require.Nil(t, lease)
	require.ErrorIs(t, err, ErrClosed)
	require.NoError(t, missing.Close())
}
