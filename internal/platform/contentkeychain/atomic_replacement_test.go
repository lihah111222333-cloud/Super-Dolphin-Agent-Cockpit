package contentkeychain

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/contentcrypto"
	"github.com/stretchr/testify/require"
)

// replacementNative 仅模拟OS原子替换；真实Provider和lease创建逻辑保持不变。
type replacementNative struct {
	privateResult error
	*fakeNative
	beforeRead func(int)
	afterAdd   func()
	addResult  error
}

func (s *replacementNative) checkPrivate() error { return s.privateResult }

func (s *replacementNative) read(service, account string) ([]byte, error) {
	if s.beforeRead != nil {
		s.beforeRead(s.reads + 1)
	}
	return s.fakeNative.read(service, account)
}

func (s *replacementNative) add(service, account string, data []byte) error {
	err := s.fakeNative.add(service, account, data)
	if s.afterAdd != nil {
		s.afterAdd()
	}
	if s.addResult != nil {
		return s.addResult
	}
	return err
}

func replacementFixture(t *testing.T) (*Provider, *replacementNative) {
	t.Helper()
	p, f := fixture(t)
	s := &replacementNative{fakeNative: f}
	p.store = s
	return p, s
}

func replacePrivateFile(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	temp, err := os.CreateTemp(filepath.Dir(path), "atomic-fixture-")
	require.NoError(t, err)
	require.NoError(t, temp.Chmod(mode))
	_, err = temp.Write([]byte("replacement unit fixture; never a keychain"))
	require.NoError(t, err)
	require.NoError(t, temp.Close())
	require.NoError(t, os.Rename(temp.Name(), path))
}

func TestKeychainAtomicFileReplacementContract(t *testing.T) {
	t.Run("acknowledged_add", testAcknowledgedReplacement)
	t.Run("duplicate_winner", testDuplicateReplacement)
	t.Run("before_call", testReplacementBeforeCall)
	t.Run("readonly_replay", testReadonlyReplacement)
	t.Run("parent_replaced", testParentReplacement)
	t.Run("unsafe_or_bad_ack", testUnsafeReplacement)
	t.Run("close_all_descriptors", testReplacementClose)
	t.Run("native_private_rejected", testReplacementPrivateRejection)
	t.Run("old_close_error", testReplacementOldCloseError)
}

func testAcknowledgedReplacement(t *testing.T) {
	p, s := replacementFixture(t)
	old := p.file
	s.afterAdd = func() { replacePrivateFile(t, p.config.KeychainPath, 0600) }
	createOK(t, p, requestFixture())
	require.NotSame(t, old, p.file, "successful acknowledged replacement must repin")
	_, err := old.Stat()
	require.ErrorIs(t, err, os.ErrClosed, "superseded descriptor must be closed")
	createOK(t, p, requestFixture())
	require.Equal(t, 1, s.adds, "replay must not add a second owner")
}

func testDuplicateReplacement(t *testing.T) {
	p, s := replacementFixture(t)
	s.addResult = errDuplicate
	s.afterAdd = func() { replacePrivateFile(t, p.config.KeychainPath, 0600) }
	createOK(t, p, requestFixture())
	createOK(t, p, requestFixture())
	require.Equal(t, 1, s.adds, "validated duplicate winner must replay without another add")
}

func testReplacementBeforeCall(t *testing.T) {
	p, s := replacementFixture(t)
	old := p.file
	replacePrivateFile(t, p.config.KeychainPath, 0600)
	lease, err := p.CreateAES256KeyLease(context.Background(), requestFixture())
	assertNoReplacementLease(t, lease)
	require.ErrorIs(t, err, ErrUnsafePath)
	require.Zero(t, s.reads)
	require.Zero(t, s.adds)
	require.Same(t, old, p.file, "pre-call replacement must not repin")
}

func testReadonlyReplacement(t *testing.T) {
	p, s := replacementFixture(t)
	createOK(t, p, requestFixture())
	old := p.file
	s.beforeRead = func(int) { replacePrivateFile(t, p.config.KeychainPath, 0600) }
	lease, err := p.CreateAES256KeyLease(context.Background(), requestFixture())
	assertNoReplacementLease(t, lease)
	require.ErrorIs(t, err, ErrUnsafePath)
	require.Same(t, old, p.file, "readonly replay cannot acknowledge replacement")
	require.Equal(t, 1, s.adds)
}

func testParentReplacement(t *testing.T) {
	p, s := replacementFixture(t)
	old := p.file
	parent := filepath.Dir(p.config.KeychainPath)
	s.afterAdd = func() {
		require.NoError(t, os.Rename(parent, parent+"-moved"))
		t.Cleanup(func() { require.NoError(t, os.RemoveAll(parent+"-moved")) })
		require.NoError(t, os.Mkdir(parent, 0700))
		// 保留原文件inode也不能掩盖父目录已被替换。
		require.NoError(t, os.Rename(filepath.Join(parent+"-moved", filepath.Base(p.config.KeychainPath)), p.config.KeychainPath))
	}
	lease, err := p.CreateAES256KeyLease(context.Background(), requestFixture())
	assertNoReplacementLease(t, lease)
	require.ErrorIs(t, err, ErrUnsafePath)
	require.Same(t, old, p.file)
}

func testUnsafeReplacement(t *testing.T) {
	for _, scenario := range []string{"wide", "symlink", "bad_ack", "read_error", "add_error", "conflict"} {
		t.Run(scenario, func(t *testing.T) { runUnsafeReplacement(t, scenario) })
	}
}

func runUnsafeReplacement(t *testing.T, scenario string) {
	p, s := replacementFixture(t)
	old := p.file
	s.afterAdd = func() { replacePrivateFile(t, p.config.KeychainPath, 0600) }
	expected := configureUnsafeReplacement(t, p, s, scenario)
	lease, err := p.CreateAES256KeyLease(context.Background(), requestFixture())
	assertNoReplacementLease(t, lease)
	require.ErrorIs(t, err, expected)
	require.Same(t, old, p.file, "failed acknowledgement must not repin")
	_, statErr := old.Stat()
	require.NoError(t, statErr, "failed refresh must preserve original descriptor until Close")
}

func configureUnsafeReplacement(t *testing.T, p *Provider, s *replacementNative, scenario string) error {
	t.Helper()
	switch scenario {
	case "wide":
		s.afterAdd = func() { replacePrivateFile(t, p.config.KeychainPath, 0644) }
		return ErrUnsafePath
	case "symlink":
		s.afterAdd = func() {
			target := p.config.KeychainPath + "-target"
			require.NoError(t, os.WriteFile(target, []byte("unit fixture"), 0600))
			require.NoError(t, os.Remove(p.config.KeychainPath))
			require.NoError(t, os.Symlink(target, p.config.KeychainPath))
		}
		return ErrUnsafePath
	case "bad_ack":
		s.transform = func(int, []byte) []byte { return []byte("corrupt fixture record") }
		return ErrCorrupt
	case "read_error":
		failure := errors.New("fixture readback failed")
		s.readFault = func(n int) error {
			if n == 2 {
				return failure
			}
			return nil
		}
		return failure
	case "add_error":
		s.addResult = ErrNative
		return ErrNative
	case "conflict":
		winner, f := fixture(t)
		changed := requestFixture()
		changed.ObjectVersion++
		createOK(t, winner, changed)
		s.winner = f.records[winner.config.Namespace+accountFor(changed.OperationID)]
		return ErrConflict
	default:
		t.Fatal("unknown replacement test scenario")
		return ErrInvalid
	}
}

// providerDescriptors 动态收集真实Provider拥有的文件指针，使新增父目录fd不能遗漏Close断言。
func providerDescriptors(p *Provider) []*os.File {
	value := reflect.ValueOf(p).Elem()
	var files []*os.File
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		if field.Type() == reflect.TypeOf((*os.File)(nil)) && !field.IsNil() {
			files = append(files, (*os.File)(field.UnsafePointer()))
		}
	}
	return files
}

func testReplacementClose(t *testing.T) {
	p, s := replacementFixture(t)
	files := providerDescriptors(p)
	require.GreaterOrEqual(t, len(files), 2, "provider must own both file and parent-directory descriptors")
	require.NoError(t, p.Close())
	require.NoError(t, p.Close())
	require.Equal(t, 1, s.closes)
	for _, file := range files {
		_, err := file.Stat()
		require.ErrorIs(t, err, os.ErrClosed, "owned descriptor remains open")
	}
}

func assertNoReplacementLease(t *testing.T, lease *contentcrypto.EphemeralKeyLease) {
	t.Helper()
	absent := lease == nil
	if lease != nil {
		require.NoError(t, lease.Close())
	}
	require.True(t, absent, "rejected operation returned a lease")
}

func testReplacementPrivateRejection(t *testing.T) {
	p, s := replacementFixture(t)
	old := p.file
	s.afterAdd = func() { replacePrivateFile(t, p.config.KeychainPath, 0600) }
	s.privateResult = ErrUnsafePath
	lease, err := p.CreateAES256KeyLease(context.Background(), requestFixture())
	assertNoReplacementLease(t, lease)
	require.ErrorIs(t, err, ErrUnsafePath)
	require.Same(t, old, p.file, "native owner/ACL rejection must not publish a candidate")
}

func testReplacementOldCloseError(t *testing.T) {
	p, s := replacementFixture(t)
	old := p.file
	s.afterAdd = func() {
		replacePrivateFile(t, p.config.KeychainPath, 0600)
		require.NoError(t, old.Close())
	}
	lease, err := p.CreateAES256KeyLease(context.Background(), requestFixture())
	assertNoReplacementLease(t, lease)
	require.ErrorIs(t, err, ErrUnsafePath, "failed old descriptor close must be reported")
	require.Nil(t, p.file, "close failure must not publish candidate or allow implicit recovery")
	before := s.reads
	lease, err = p.CreateAES256KeyLease(context.Background(), requestFixture())
	assertNoReplacementLease(t, lease)
	require.ErrorIs(t, err, ErrUnsafePath)
	require.Equal(t, before, s.reads, "failed provider must not retry native operations")
}
