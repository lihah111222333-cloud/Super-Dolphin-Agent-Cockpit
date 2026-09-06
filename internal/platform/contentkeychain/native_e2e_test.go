//go:build darwin && cgo && e2e

package contentkeychain

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/contentcrypto"
)

const nativeProofPrefix = "ORCH_KEYCHAIN_NATIVE_PROOF:"

// nativeProof 仅允许跨进程传输非秘密结果，不包含request digest、正文或密钥。
type nativeProof struct {
	CiphertextHash string `json:"ciphertext_hash"`
	Nonce          string `json:"nonce"`
	HandleDigest   string `json:"handle_digest"`
	ReceiptDigest  string `json:"receipt_digest"`
	Locked         bool   `json:"locked"`
}

func nativeExplicitConfig(t *testing.T) Config {
	t.Helper()
	path, pathSet := os.LookupEnv("ORCH_KEYCHAIN_TEST_PATH")
	namespace, namespaceSet := os.LookupEnv("ORCH_KEYCHAIN_TEST_NAMESPACE")
	require.True(t, pathSet && namespaceSet, "explicit native test path and namespace are required")
	require.True(t, validText(namespace, 128), "native test namespace is invalid")
	return Config{KeychainPath: path, Namespace: namespace}
}

// nativeTestConfig 父入口始终使用原始ownership manifest，不能被child委派环境绕过。
func nativeTestConfig(t *testing.T) Config {
	t.Helper()
	config := nativeExplicitConfig(t)
	validateNativeOwnership(t, config.KeychainPath)
	return config
}

// nativeChildConfig 子入口仅接受父进程显式委派的当前完整身份，不读取旧inode的manifest。
func nativeChildConfig(t *testing.T) Config {
	t.Helper()
	config := nativeExplicitConfig(t)
	encoded, present := os.LookupEnv("ORCH_KEYCHAIN_NATIVE_EXPECTED_IDENTITY")
	require.True(t, present, "native child requires explicit identity delegation")
	expected := decodeNativeOwnership(t, []byte(encoded))
	matchNativeOwnership(t, config.KeychainPath, expected)
	return config
}

// nativeOwnership 与外置fixture工具的字符串化Darwin文件身份契约保持一致。
type nativeOwnership struct {
	Owner    string `json:"owner"`
	Identity struct {
		Path      string `json:"path"`
		Dev       string `json:"dev"`
		Inode     string `json:"inode"`
		UID       string `json:"uid"`
		BirthSec  string `json:"birth_sec"`
		BirthNsec string `json:"birth_nsec"`
	} `json:"identity"`
}

func nativePrivateIdentity(t *testing.T, path string) (os.FileInfo, *syscall.Stat_t) {
	t.Helper()
	info, err := privatePath(path)
	require.NoError(t, err)
	require.True(t, info.Mode().Perm() == 0600, "fixture file must be mode 0600")
	stat, ok := info.Sys().(*syscall.Stat_t)
	require.True(t, ok, "Darwin file identity is unavailable")
	require.True(t, stat.Uid == uint32(os.Geteuid()), "fixture file has wrong owner")
	parent, err := os.Lstat(filepath.Dir(path))
	require.NoError(t, err)
	parentStat, ok := parent.Sys().(*syscall.Stat_t)
	require.True(t, ok, "Darwin directory identity is unavailable")
	require.True(t, parent.Mode().Perm() == 0700 && parentStat.Uid == uint32(os.Geteuid()), "fixture parent must be current-user mode 0700")
	return info, stat
}

func captureNativeOwnership(t *testing.T, path string) nativeOwnership {
	t.Helper()
	_, stat := nativePrivateIdentity(t, path)
	var identity nativeOwnership
	identity.Owner = "content-keychain-live-v1"
	identity.Identity.Path = path
	identity.Identity.Dev = strconv.FormatInt(int64(stat.Dev), 10)
	identity.Identity.Inode = strconv.FormatUint(stat.Ino, 10)
	identity.Identity.UID = strconv.FormatUint(uint64(stat.Uid), 10)
	identity.Identity.BirthSec = strconv.FormatInt(stat.Birthtimespec.Sec, 10)
	identity.Identity.BirthNsec = strconv.FormatInt(stat.Birthtimespec.Nsec, 10)
	return identity
}

func decodeNativeOwnership(t *testing.T, data []byte) nativeOwnership {
	t.Helper()
	require.True(t, len(data) > 0 && len(data) <= 4096, "ownership identity JSON size is invalid")
	var identity nativeOwnership
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	require.NoError(t, decoder.Decode(&identity), "invalid ownership identity JSON")
	require.ErrorIs(t, decoder.Decode(&struct{}{}), io.EOF, "trailing ownership identity JSON")
	require.True(t, identity.Owner == "content-keychain-live-v1", "fixture ownership marker is invalid")
	return identity
}

func matchNativeOwnership(t *testing.T, path string, expected nativeOwnership) {
	t.Helper()
	current := captureNativeOwnership(t, path)
	require.True(t, current == expected, "complete private fixture identity mismatch")
}

func validateNativeOwnership(t *testing.T, path string) {
	t.Helper()
	before := captureNativeOwnership(t, path)
	data := readNativeOwnership(t, path+".ownership.json")
	defer clear(data)
	manifest := decodeNativeOwnership(t, data)
	require.True(t, before == manifest, "fixture does not match original ownership manifest")
	matchNativeOwnership(t, path, manifest)
}

func readNativeOwnership(t *testing.T, path string) []byte {
	t.Helper()
	info, _ := nativePrivateIdentity(t, path)
	require.True(t, info.Size() > 0 && info.Size() <= 4096, "fixture ownership manifest size is invalid")
	file, err := os.Open(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, file.Close()) }()
	opened, err := file.Stat()
	require.NoError(t, err)
	require.True(t, os.SameFile(info, opened), "fixture manifest changed while opening")
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	require.NoError(t, err)
	require.True(t, len(data) > 0 && len(data) <= 4096, "fixture ownership manifest exceeds its bound")
	current, _ := nativePrivateIdentity(t, path)
	require.True(t, os.SameFile(opened, current), "fixture manifest changed while reading")
	return data
}

func nativeTestRequest() contentcrypto.SealRequest {
	return contentcrypto.SealRequest{
		OperationID: "native-keychain-lifecycle-v1", TicketID: "native-fixture-ticket", ObjectID: "native-fixture-object", ObjectVersion: 1,
		SourceFenceRef: "native-fixture-fence", MediaType: "text/plain", AADProfile: contentcrypto.AADProfileV1,
		ContentIntentDigest: strings.Repeat("d", 64), Plaintext: []byte("ORCH native keychain known fixture plaintext v1."), AAD: []byte("native-keychain-fixture-aad/v1"),
	}
}

func nativeSeal(t *testing.T, provider *Provider, request contentcrypto.SealRequest) contentcrypto.SealedPayload {
	t.Helper()
	sealer, err := contentcrypto.NewAESGCMSealer(provider)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	recorder := &nativeForwardRecorder{t: t, provider: provider, inner: provider.store}
	provider.store = recorder
	defer func() { provider.store = recorder.inner }()
	recorder.snapshot("seal", "before")
	sealed, err := sealer.CreateAndSeal(ctx, request)
	recorder.snapshot("seal", "after")
	recorder.finish(err)
	require.NoError(t, err)
	return sealed
}

func nativeOpen(t *testing.T, config Config) *Provider {
	t.Helper()
	provider, err := New(config)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, provider.Close()) })
	return provider
}

// TestContentKeychainNativeLifecycle 仅在外置provision完成后验证真实创建、重启重放与本地密文。
// 前置：受控工具创建本任务专用钥匙串，核对ownership后显式chmod0600，并用私有密码文件解锁。
// 调用方必须确认default/search-list未改变；本测试与adapter不承担provision、chmod或unlock。
func TestContentKeychainNativeLifecycle(t *testing.T) {
	config := nativeTestConfig(t)
	request := nativeTestRequest()
	defer clear(request.Plaintext)
	provider := nativeOpen(t, config)
	first := nativeSeal(t, provider, request)
	nativeRoundtrip(t, provider, request, first)
	require.NoError(t, provider.Close())

	restarted := nativeChildProof(t, config, "replay")
	require.True(t, restarted == proofFor(first), "independent process did not replay the same key receipt and ciphertext")

	reopened := nativeOpen(t, config)
	replay := nativeSeal(t, reopened, request)
	require.True(t, bytes.Equal(first.Ciphertext, replay.Ciphertext), "reopened ciphertext changed")
	require.True(t, bytes.Equal(first.Nonce, replay.Nonce), "reopened nonce changed")
	require.True(t, first.KeyHandleRef == replay.KeyHandleRef, "reopened key handle changed")
	require.True(t, first.KeyReceiptRef == replay.KeyReceiptRef && first.KeyReceiptDigest == replay.KeyReceiptDigest, "reopened receipt changed")
	nativeConflict(t, reopened, request)
	request.OperationID = "native-keychain-other-operation-v1"
	other := nativeSeal(t, reopened, request)
	require.False(t, bytes.Equal(first.Nonce, other.Nonce), "new operation reused nonce")
	require.True(t, first.KeyHandleRef != other.KeyHandleRef, "new operation reused key handle")
	require.NoError(t, reopened.Close())
	nativeCiphertextFile(t, config, first, request.Plaintext)
}

func nativeConflict(t *testing.T, provider *Provider, request contentcrypto.SealRequest) {
	t.Helper()
	changed := request
	changed.Plaintext = append([]byte(nil), request.Plaintext...)
	defer clear(changed.Plaintext)
	changed.Plaintext[0] ^= 1
	sealer, err := contentcrypto.NewAESGCMSealer(provider)
	require.NoError(t, err)
	_, err = sealer.CreateAndSeal(context.Background(), changed)
	require.ErrorIs(t, err, ErrConflict, "same operation accepted different equal-length plaintext")
}

func nativeRoundtrip(t *testing.T, provider *Provider, request contentcrypto.SealRequest, sealed contentcrypto.SealedPayload) {
	t.Helper()
	provider.mu.Lock()
	data, err := provider.store.read(provider.config.Namespace, accountFor(request.OperationID))
	provider.mu.Unlock()
	defer clear(data)
	require.NoError(t, err)
	record, err := decodeRecord(data)
	require.NoError(t, err)
	defer clear(record.Key)
	defer clear(record.Nonce)
	nativeKeyAbsence(t, provider.config.KeychainPath, record.Key)
	block, err := aes.NewCipher(record.Key)
	require.NoError(t, err)
	gcm, err := cipher.NewGCM(block)
	require.NoError(t, err)
	plaintext, err := gcm.Open(nil, sealed.Nonce, sealed.Ciphertext, request.AAD)
	defer clear(plaintext)
	require.NoError(t, err)
	require.True(t, bytes.Equal(plaintext, request.Plaintext), "native AES-GCM roundtrip mismatch")
}

// nativeKeyAbsence 只检查本测试密钥的有限明文表示，不能证明系统内部缓存或全部介质擦除。
func nativeKeyAbsence(t *testing.T, path string, key []byte) {
	t.Helper()
	encoded := make([]byte, base64.StdEncoding.EncodedLen(len(key)))
	base64.StdEncoding.Encode(encoded, key)
	defer clear(encoded)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	defer clear(data)
	require.False(t, bytes.Contains(data, key), "task keychain file contains the test DEK bytes")
	require.False(t, bytes.Contains(data, encoded), "task keychain file contains the test DEK encoding")
}

func nativeCiphertextFile(t *testing.T, config Config, sealed contentcrypto.SealedPayload, plaintext []byte) {
	t.Helper()
	directory, err := os.MkdirTemp(filepath.Dir(filepath.Dir(config.KeychainPath)), "content-keychain-ciphertext-")
	require.NoError(t, err)
	require.True(t, filepath.Dir(config.KeychainPath) != directory, "ciphertext and keychain directories must differ")
	path := filepath.Join(directory, "fixture.aes-gcm")
	require.NoError(t, os.WriteFile(path, sealed.Ciphertext, 0600))
	ciphertext, err := os.ReadFile(path)
	require.NoError(t, err)
	defer clear(ciphertext)
	require.True(t, bytes.Equal(ciphertext, sealed.Ciphertext), "on-disk ciphertext differs")
	require.False(t, bytes.Contains(ciphertext, plaintext), "ciphertext file contains the known plaintext")
	keychain, err := os.ReadFile(config.KeychainPath)
	require.NoError(t, err)
	defer clear(keychain)
	require.False(t, bytes.Contains(keychain, plaintext), "keychain file contains the known plaintext")
	t.Logf("local ciphertext fixture: %s; not a replication or crypto-erasure proof", path)
}

func proofFor(sealed contentcrypto.SealedPayload) nativeProof {
	handle := sha256.Sum256([]byte(sealed.KeyHandleRef))
	receipt := sha256.Sum256([]byte(sealed.KeyReceiptRef + "\x00" + sealed.KeyReceiptDigest))
	return nativeProof{CiphertextHash: sealed.CiphertextHash, Nonce: hex.EncodeToString(sealed.Nonce), HandleDigest: hex.EncodeToString(handle[:]), ReceiptDigest: hex.EncodeToString(receipt[:])}
}

func nativeChildProof(t *testing.T, config Config, mode string) nativeProof {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "-test.run=^TestContentKeychainNativeChild$", "-test.timeout=10s")
	command.WaitDelay = time.Second
	// replay由public provider成功并Close后调用；locked由严格验证原manifest的新fixture入口调用。
	expected := captureNativeOwnership(t, config.KeychainPath)
	encoded, err := json.Marshal(expected)
	require.NoError(t, err)
	require.True(t, len(encoded) <= 4096, "delegated identity JSON exceeds bound")
	// 只传播四个非秘密字段；不继承父进程可能携带的凭据或密码环境。
	command.Env = []string{"ORCH_KEYCHAIN_TEST_PATH=" + config.KeychainPath, "ORCH_KEYCHAIN_TEST_NAMESPACE=" + config.Namespace, "ORCH_KEYCHAIN_NATIVE_CHILD=" + mode, "ORCH_KEYCHAIN_NATIVE_EXPECTED_IDENTITY=" + string(encoded)}
	matchNativeOwnership(t, config.KeychainPath, expected)
	output, err := command.CombinedOutput()
	defer clear(output)
	matchNativeOwnership(t, config.KeychainPath, expected)
	require.NoError(t, ctx.Err(), "native child exceeded bounded execution time")
	require.NoError(t, err, "native child failed; captured output intentionally not disclosed")
	return parseNativeProof(t, output)
}

func parseNativeProof(t *testing.T, output []byte) nativeProof {
	t.Helper()
	var proof nativeProof
	found := false
	for _, line := range bytes.Split(output, []byte("\n")) {
		if len(line) == 0 || bytes.Equal(line, []byte("PASS")) {
			continue
		}
		require.True(t, bytes.HasPrefix(line, []byte(nativeProofPrefix)), "native child emitted unexpected output")
		require.False(t, found, "duplicate child proof")
		found = true
		decoder := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(line, []byte(nativeProofPrefix))))
		decoder.DisallowUnknownFields()
		require.NoError(t, decoder.Decode(&proof), "invalid child proof")
	}
	require.True(t, found, "missing child proof")
	return proof
}

// TestContentKeychainNativeChild 仅由上面的生命周期或锁定测试在独立进程显式启动。
func TestContentKeychainNativeChild(t *testing.T) {
	config := nativeChildConfig(t)
	mode := os.Getenv("ORCH_KEYCHAIN_NATIVE_CHILD")
	require.True(t, mode == "replay" || mode == "locked", "native child must be explicitly invoked by its parent test")
	var proof nativeProof
	if mode == "locked" {
		proof = nativeLockedProof(t, config)
	} else {
		request := nativeTestRequest()
		defer clear(request.Plaintext)
		provider := nativeOpen(t, config)
		proof = proofFor(nativeSeal(t, provider, request))
		require.NoError(t, provider.Close())
	}
	encoded, err := json.Marshal(proof)
	require.NoError(t, err)
	_, err = fmt.Printf("%s%s\n", nativeProofPrefix, encoded)
	require.NoError(t, err)
}

func nativeLockedProof(t *testing.T, config Config) nativeProof {
	t.Helper()
	provider, err := New(config)
	if err != nil {
		require.True(t, errors.Is(err, ErrNative), "locked keychain did not return a classified native error")
		return nativeProof{Locked: true}
	}
	defer func() { require.NoError(t, provider.Close()) }()
	request := nativeTestRequest()
	defer clear(request.Plaintext)
	sealer, err := contentcrypto.NewAESGCMSealer(provider)
	require.NoError(t, err)
	_, err = sealer.CreateAndSeal(context.Background(), request)
	require.True(t, errors.Is(err, ErrNative), "locked keychain unexpectedly allowed a key lease")
	return nativeProof{Locked: true}
}

// TestContentKeychainNativeRejectsLocked 要求主agent先外置锁定；子进程超时是失败，不是通过。
func TestContentKeychainNativeRejectsLocked(t *testing.T) {
	config := nativeTestConfig(t)
	started := time.Now()
	proof := nativeChildProof(t, config, "locked")
	require.True(t, proof.Locked, "missing classified locked-keychain rejection")
	require.Less(t, time.Since(started), 17*time.Second, "locked-keychain check exceeded bound")
}

// nativeForwardRecorder 仅测试使用；原样转发生产create调用，不另建密钥入口或放宽身份校验。
type nativeForwardRecorder struct {
	t         *testing.T
	provider  *Provider
	inner     nativeStore
	reads     int
	added     bool
	lastStage string
	lastError error
}

func (r *nativeForwardRecorder) snapshot(stage, edge string) {
	r.t.Helper()
	current, err := os.Lstat(r.provider.config.KeychainPath)
	if err != nil {
		r.t.Fatal("native diagnostic path metadata unavailable")
	}
	opened, err := r.provider.file.Stat()
	if err != nil {
		r.t.Fatal("native diagnostic pinned metadata unavailable")
	}
	pathStat, pathOK := current.Sys().(*syscall.Stat_t)
	openStat, openOK := opened.Sys().(*syscall.Stat_t)
	require.True(r.t, pathOK && openOK, "native diagnostic Darwin metadata unavailable")
	r.t.Logf("native-diagnostic stage=%s edge=%s path_dev=%d path_inode=%d path_mode=%#o pinned_dev=%d pinned_inode=%d pinned_mode=%#o", stage, edge, pathStat.Dev, pathStat.Ino, current.Mode().Perm(), openStat.Dev, openStat.Ino, opened.Mode().Perm())
}

func (r *nativeForwardRecorder) read(service, account string) ([]byte, error) {
	stage := "initial-read"
	if r.added {
		stage = "readback"
	}
	r.snapshot(stage, "before")
	data, err := r.inner.read(service, account)
	r.reads++
	r.lastStage, r.lastError = stage, err
	r.snapshot(stage, "after")
	r.t.Logf("native-diagnostic stage=%s result=%s", stage, nativeDiagnosticClass(err))
	return data, err
}

func (r *nativeForwardRecorder) add(service, account string, data []byte) error {
	r.snapshot("add", "before")
	err := r.inner.add(service, account, data)
	r.added = true
	r.lastStage, r.lastError = "add", err
	r.snapshot("add", "after")
	r.t.Logf("native-diagnostic stage=add result=%s", nativeDiagnosticClass(err))
	return err
}

func (r *nativeForwardRecorder) close() error { return r.inner.close() }

func (r *nativeForwardRecorder) finish(err error) {
	stage := r.lastStage
	if r.reads == 0 {
		stage = "pre-identity-or-request"
	}
	if errors.Is(err, ErrUnsafePath) && r.lastError == nil && r.reads > 0 {
		stage = "post-identity"
	}
	r.t.Logf("native-diagnostic seal_result=%s attribution=%s last_native_result=%s", nativeDiagnosticClass(err), stage, nativeDiagnosticClass(r.lastError))
}

func nativeDiagnosticClass(err error) string {
	if err == nil {
		return "ok"
	}
	classes := []struct {
		err  error
		name string
	}{
		{errNotFound, "not-found"}, {errDuplicate, "duplicate"}, {ErrUnsafePath, "unsafe-path"},
		{ErrNative, "native"}, {ErrCorrupt, "corrupt"}, {ErrConflict, "conflict"}, {ErrClosed, "closed"},
		{ErrInvalid, "invalid"}, {context.Canceled, "canceled"}, {context.DeadlineExceeded, "deadline"},
	}
	for _, class := range classes {
		if errors.Is(err, class.err) {
			return class.name
		}
	}
	return "unclassified-error"
}

func (r *nativeForwardRecorder) checkPrivate() error { return r.inner.checkPrivate() }
