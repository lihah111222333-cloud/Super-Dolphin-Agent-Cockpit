// Package contentkeychain 使用显式预配置的macOS钥匙串持久化CONTENT密钥；不负责授权、销毁或GC。
package contentkeychain

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/contentcrypto"
)

var (
	ErrUnsupported = errors.New("content keychain unsupported")
	ErrInvalid     = errors.New("content keychain invalid input")
	ErrUnsafePath  = errors.New("content keychain unsafe path")
	ErrClosed      = errors.New("content keychain closed")
	ErrConflict    = errors.New("content keychain request conflict")
	ErrCorrupt     = errors.New("content keychain corrupt record")
	ErrNative      = errors.New("content keychain native failure")
	errNotFound    = errors.New("content keychain item not found")
	errDuplicate   = errors.New("content keychain duplicate item")
)

// Config 必须显式选择已存在、已解锁的私有钥匙串与隔离命名空间。
type Config struct {
	KeychainPath string
	Namespace    string
}

// Provider 串行管理指定钥匙串；Close只释放引用，不删除持久密钥。
type Provider struct {
	mu                sync.Mutex
	config            Config
	file              *os.File
	identity          os.FileInfo
	directory         *os.File
	directoryIdentity os.FileInfo
	store             nativeStore
	closed            bool
}

type nativeStore interface {
	checkPrivate() error
	read(string, string) ([]byte, error)
	add(string, string, []byte) error
	close() error
}

// New 只打开显式钥匙串；不创建、解锁或改变默认钥匙串。
func New(config Config) (*Provider, error) {
	if !nativeSupported {
		return nil, ErrUnsupported
	}
	if !validText(config.Namespace, 128) {
		return nil, ErrInvalid
	}
	info, err := privatePath(config.KeychainPath)
	if err != nil {
		return nil, err
	}
	p, err := openPinned(config, info)
	if err != nil {
		return nil, err
	}
	if err = p.checkIdentity(); err == nil {
		p.store, err = openNative(config.KeychainPath)
	}
	if err == nil {
		err = p.checkIdentity()
	}
	if err != nil {
		return nil, errors.Join(err, p.Close())
	}
	return p, nil
}

// Close 串行释放原生引用与文件描述符，重复调用不改变持久记录。
func (p *Provider) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	var nativeErr error
	if p.store != nil {
		nativeErr = p.store.close()
	}
	fileErr := closePrivateFile(p.file)
	directoryErr := closePrivateFile(p.directory)
	return errors.Join(nativeErr, fileErr, directoryErr)
}

// CreateAES256KeyLease 校验上下文与文件身份后原子创建或重放已登记密钥。
func (p *Provider) CreateAES256KeyLease(ctx context.Context, request contentcrypto.KeyCreateRequest) (*contentcrypto.EphemeralKeyLease, error) {
	if err := validateCall(ctx, request); err != nil {
		return nil, err
	}
	if p == nil {
		return nil, ErrClosed
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.store == nil {
		return nil, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := p.checkIdentity(); err != nil {
		return nil, err
	}
	lease, acknowledged, err := p.create(request)
	if err != nil {
		return nil, errors.Join(err, p.checkDirectory())
	}
	return p.finishLease(ctx, lease, acknowledged)
}

// finishLease 只允许已验证写确认刷新身份；任何后置失败都先关闭lease再返回零结果。
func (p *Provider) finishLease(ctx context.Context, lease *contentcrypto.EphemeralKeyLease, acknowledged bool) (*contentcrypto.EphemeralKeyLease, error) {
	var err error
	if acknowledged {
		err = p.refreshIdentity()
	} else {
		err = p.checkIdentity()
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return nil, errors.Join(err, lease.Close())
	}
	return lease, nil
}

// validateCall 在进入原生临界区前拒绝无效上下文和请求。
func validateCall(ctx context.Context, request contentcrypto.KeyCreateRequest) error {
	if ctx == nil || isNil(ctx) || !validRequest(request) {
		return ErrInvalid
	}
	return ctx.Err()
}

// isNil 拒绝实现context接口的typed-nil值。
func isNil(value any) bool {
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Chan, reflect.Func, reflect.Map, reflect.Slice:
		return v.IsNil()
	}
	return false
}

// privatePath 拒绝非规范路径、符号链接、非普通文件或宽松父目录权限，不修改权限。
func privatePath(path string) (os.FileInfo, error) {
	if !canonicalPath(path) {
		return nil, ErrUnsafePath
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, ErrUnsafePath
	}
	parent, err := os.Lstat(filepath.Dir(path))
	if err != nil || !parent.IsDir() || parent.Mode().Perm()&0o077 != 0 {
		return nil, ErrUnsafePath
	}
	return info, nil
}

// canonicalPath 拒绝不洁路径和任意祖先符号链接。
func canonicalPath(path string) bool {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false
	}
	real, err := filepath.EvalSymlinks(path)
	return err == nil && real == path
}

// checkIdentity 保持打开文件对象身份，拒绝运行期路径替换或权限放宽。
func (p *Provider) checkIdentity() error {
	if err := p.checkDirectory(); err != nil {
		return err
	}
	return p.matchesIdentity(p.file, p.identity)
}

// matchesIdentity 将已打开对象与当前私有路径逐一比对，不接受路径换绑。
func (p *Provider) matchesIdentity(file *os.File, identity os.FileInfo) error {
	if file == nil || identity == nil {
		return ErrUnsafePath
	}
	current, err := privatePath(p.config.KeychainPath)
	if err != nil {
		return err
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(identity, current) || !os.SameFile(opened, current) {
		return ErrUnsafePath
	}
	return nil
}

// validText 限制非空UTF-8标识，拒绝空白和NUL。
func validText(value string, limit int) bool {
	return value != "" && len(value) <= limit && utf8.ValidString(value) && strings.TrimSpace(value) == value && !strings.ContainsRune(value, 0)
}

var _ contentcrypto.KeyLeaseCreator = (*Provider)(nil)
