package childhandoff

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone/taskcredential"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

const (
	claudeConfigName = "mcp-config.json"
	codexConfigName  = "config.toml"
	envPrefix        = "SUPER_DOLPHIN_CHILD_BEARER_"
)

// Materialize 至多一次消费 credential，并写入隔离的 0600 provider 配置。
// 返回值只是候选描述，不是 process-start receipt。
func (o *Owner) Materialize(ctx context.Context) (Materialized, error) {
	credential, config, err := o.beginMaterialization(ctx)
	if err != nil {
		if credential != nil {
			return Materialized{}, o.fail(ctx, credential, nil, err)
		}
		return Materialized{}, err
	}
	defer o.finishMaterialization()
	if err := o.recheckTempRoot(config.TempRoot); err != nil {
		return Materialized{}, o.fail(ctx, credential, nil, err)
	}
	return o.materializeCredential(ctx, credential, config)
}

// materializeCredential 消费 bearer、创建私有配置并完成最终身份复核。
func (o *Owner) materializeCredential(ctx context.Context, credential *taskcredential.IssuedCredential, config Config) (Materialized, error) {
	bearer, err := credential.TakeBearer()
	if err != nil {
		return Materialized{}, o.fail(ctx, credential, nil, err)
	}
	secret := &secretValue{value: []byte(bearer)}
	defer clear([]byte(bearer))
	name, err := newEnvironmentName()
	if err != nil {
		return Materialized{}, o.fail(ctx, credential, secret, err)
	}
	secret.key = name
	dir, err := o.createTempDir(config.TempRoot)
	if err != nil {
		return Materialized{}, o.fail(ctx, credential, secret, err)
	}
	path, args, err := o.writeConfig(config, name, dir)
	if err != nil {
		return Materialized{}, o.fail(ctx, credential, secret, err)
	}
	if err := ctx.Err(); err != nil {
		return Materialized{}, o.fail(ctx, credential, secret, err)
	}
	if err := o.recheckMaterialized(config.TempRoot, path, dir); err != nil {
		return Materialized{}, o.fail(ctx, credential, secret, err)
	}
	result := Materialized{Provider: config.Provider, Kind: "isolated_config", secret: secret, path: path, args: args, dir: dir}
	o.mu.Lock()
	if o.closing {
		o.mu.Unlock()
		return Materialized{}, o.fail(ctx, credential, secret, errors.New("handoff closed"))
	}
	o.material = &result
	o.mu.Unlock()
	return result, nil
}

// beginMaterialization 原子占用 owner，并建立 Close 等待所需的活动阶段。
func (o *Owner) beginMaterialization(ctx context.Context) (*taskcredential.IssuedCredential, Config, error) {
	if o == nil || ctx == nil {
		return nil, Config{}, ErrInvalidConfig
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.terminal || o.closing {
		return nil, Config{}, ErrAlreadyMaterialized
	}
	o.terminal = true
	o.active = true
	o.activeDone = make(chan struct{})
	if err := ctx.Err(); err != nil {
		o.active = false
		close(o.activeDone)
		return o.credential, o.config, err
	}
	return o.credential, o.config, nil
}

func (o *Owner) finishMaterialization() {
	o.mu.Lock()
	if o.active {
		o.active = false
		close(o.activeDone)
	}
	o.mu.Unlock()
}

// createTempDir 在 canonical owner-only 根下创建并绑定一次性目录。
func (o *Owner) createTempDir(root string) (string, error) {
	if err := o.recheckTempRoot(root); err != nil {
		return "", ErrMaterialization
	}
	dir, err := os.MkdirTemp(root, ".child-endpoint-")
	if err != nil {
		return "", ErrMaterialization
	}
	identity, err := inspectPrivatePath(dir, true)
	if err != nil {
		return "", ErrMaterialization
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", ErrMaterialization
	}
	identity, err = inspectPrivatePath(dir, true)
	if err != nil || identity.canonical != filepath.Clean(dir) {
		return "", ErrMaterialization
	}
	o.mu.Lock()
	o.tempDir = dir
	o.tempDirID = &identity
	o.mu.Unlock()
	if err := o.recheckTempRoot(root); err != nil {
		return "", ErrMaterialization
	}
	return dir, nil
}

// writeConfig 在已绑定的临时目录中写入严格私有 provider 配置。
func (o *Owner) writeConfig(config Config, name, dir string) (string, []string, error) {
	if err := o.recheckTempRoot(config.TempRoot); err != nil {
		return "", nil, ErrMaterialization
	}
	if !o.matchesTempDir(dir) {
		return "", nil, ErrMaterialization
	}
	path, content, args, err := renderConfig(config.Provider, config.Endpoint, name, dir)
	if err != nil {
		return "", nil, err
	}
	if err := writePrivateFile(path, content); err != nil {
		return "", nil, err
	}
	pathID, err := inspectPrivatePath(path, false)
	if err != nil || pathID.canonical != filepath.Clean(path) || !o.matchesTempDir(filepath.Dir(path)) {
		return "", nil, ErrMaterialization
	}
	o.mu.Lock()
	o.tempPath = path
	o.tempPathID = &pathID
	o.mu.Unlock()
	return path, args, nil
}

// recheckTempRoot 在敏感阶段确认临时根的 canonical 路径和文件对象未被替换。
func (o *Owner) recheckTempRoot(path string) error {
	if o == nil || path == "" {
		return ErrMaterialization
	}
	current, err := inspectTempRoot(path)
	if err != nil || current.canonical != o.rootID.canonical || !os.SameFile(current.info, o.rootID.info) {
		return ErrMaterialization
	}
	return nil
}

func (o *Owner) matchesTempDir(path string) bool {
	o.mu.Lock()
	identity := o.tempDirID
	o.mu.Unlock()
	if identity == nil || filepath.Clean(path) != identity.canonical {
		return false
	}
	current, err := inspectPrivatePath(path, true)
	return err == nil && current.canonical == identity.canonical && os.SameFile(current.info, identity.info)
}

// recheckMaterialized 在返回候选前确认根、目录和配置文件仍是原始私有对象。
func (o *Owner) recheckMaterialized(root, path, dir string) error {
	if err := o.recheckTempRoot(root); err != nil || !o.matchesTempDir(dir) || filepath.Clean(filepath.Dir(path)) != filepath.Clean(dir) {
		return ErrMaterialization
	}
	current, err := inspectPrivatePath(path, false)
	if err != nil {
		return ErrMaterialization
	}
	o.mu.Lock()
	expected := o.tempPathID
	o.mu.Unlock()
	if expected == nil || current.canonical != expected.canonical || !os.SameFile(current.info, expected.info) {
		return ErrMaterialization
	}
	return nil
}

// inspectPrivatePath 校验真实 canonical 路径及 owner-only 权限，并返回对象身份。
func inspectPrivatePath(path string, directory bool) (pathIdentity, error) {
	if !filepath.IsAbs(path) {
		return pathIdentity{}, ErrMaterialization
	}
	info, err := os.Lstat(path)
	if err != nil || !matchesKind(info, directory) {
		return pathIdentity{}, ErrMaterialization
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil || filepath.Clean(canonical) != filepath.Clean(path) {
		return pathIdentity{}, ErrMaterialization
	}
	if err := securefs.CheckPrivateOwnerOnly(path, info); err != nil {
		return pathIdentity{}, ErrMaterialization
	}
	if !privateModeIsUsable(info, directory) {
		return pathIdentity{}, ErrMaterialization
	}
	return pathIdentity{canonical: filepath.Clean(canonical), info: info}, nil
}

// matchesKind 判断路径是否为期望的真实目录或普通文件。
func matchesKind(info os.FileInfo, directory bool) bool {
	if info == nil || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	if directory {
		return info.IsDir()
	}
	return info.Mode().IsRegular()
}

// privateModeIsUsable 保证 Unix mode 不向 group/world 暴露权限；Windows ACL 由 securefs 校验。
func privateModeIsUsable(info os.FileInfo, _ bool) bool {
	return info != nil && info.Mode().Perm()&0o077 == 0
}

// fail 清理一次性 credential、临时文件并撤销 lease，同时折叠底层错误细节。
func (o *Owner) fail(ctx context.Context, credential *taskcredential.IssuedCredential, secret *secretValue, cause error) error {
	if secret != nil {
		secret.discard()
	}
	if credential != nil {
		credential.Discard()
	}
	cleanupErr := o.cleanup(ctx)
	if cause == nil {
		if cleanupErr != nil {
			return cleanupErr
		}
		return ErrMaterialization
	}
	if cleanupErr != nil {
		return errors.Join(fmt.Errorf("%w: %w", ErrMaterialization, sanitizeCause(cause)), cleanupErr)
	}
	return fmt.Errorf("%w: %w", ErrMaterialization, sanitizeCause(cause))
}

func sanitizeCause(cause error) error {
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return cause
	}
	return ErrMaterialization
}

func renderConfig(provider Provider, endpoint, envName, dir string) (string, []byte, []string, error) {
	switch provider {
	case ProviderClaude:
		doc := map[string]any{"mcpServers": map[string]any{"orch": map[string]any{
			"type": "http", "url": endpoint, "headers": map[string]string{"Authorization": "Bearer ${" + envName + "}"},
		}}}
		content, err := json.Marshal(doc)
		if err != nil {
			return "", nil, nil, ErrMaterialization
		}
		return filepath.Join(dir, claudeConfigName), append(content, '\n'), []string{"--mcp-config", filepath.Join(dir, claudeConfigName)}, nil
	case ProviderCodex:
		content := []byte("[mcp_servers.orch]\nurl = \"" + escapeTOML(endpoint) + "\"\nbearer_token_env_var = \"" + envName + "\"\n")
		path := filepath.Join(dir, codexConfigName)
		return path, content, []string{"--config", path}, nil
	default:
		return "", nil, nil, ErrProviderDisabled
	}
}

func escapeTOML(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "\\", "\\\\"), "\"", "\\\"")
}

func writePrivateFile(path string, content []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return ErrMaterialization
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return ErrMaterialization
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return ErrMaterialization
	}
	if err := file.Close(); err != nil {
		return ErrMaterialization
	}
	return nil
}

func newEnvironmentName() (string, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", ErrMaterialization
	}
	return envPrefix + hex.EncodeToString(random[:]), nil
}

// Close 撤销 lease 并删除所有临时 material；重复调用保持幂等。
func (o *Owner) Close() error {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	o.closing = true
	o.terminal = true
	done := o.activeDone
	active := o.active
	credential := o.credential
	if o.material != nil && o.material.secret != nil {
		o.material.secret.discard()
	}
	o.mu.Unlock()
	if active && done != nil {
		<-done
	}
	if credential != nil {
		credential.Discard()
	}
	if err := o.cleanup(context.Background()); err != nil {
		return err
	}
	return nil
}

// cleanup 在一次线性化点撤销 lease 并移除临时文件。
func (o *Owner) cleanup(ctx context.Context) error {
	o.cleanupOnce.Do(func() {
		config, material, path, dir, dirID, pathID := o.takeCleanupState()
		o.cleanupMu.Lock()
		o.cleanupCfg = config
		o.cleanupMu.Unlock()
		if material != nil && material.secret != nil {
			material.secret.discard()
		}
		if err := o.recheckTempRoot(config.TempRoot); err != nil {
			o.setCleanupError()
		} else if err := removeCleanupFiles(path, dir, dirID, pathID); err != nil {
			o.setCleanupError()
		}
		if err := o.revokeOnce(ctx, config); err != nil {
			o.setCleanupError()
		}
	})
	o.cleanupMu.Lock()
	defer o.cleanupMu.Unlock()
	if o.cleanupErr != nil && !o.revokeDone {
		if err := revokeCleanup(ctx, o.cleanupCfg); err == nil {
			o.revokeDone = true
			if o.cleanupErr == ErrCleanup {
				o.cleanupErr = nil
			}
		}
	}
	return o.cleanupErr
}

func (o *Owner) setCleanupError() {
	o.cleanupMu.Lock()
	o.cleanupErr = ErrCleanup
	o.cleanupMu.Unlock()
}

func (o *Owner) revokeOnce(ctx context.Context, config Config) error {
	o.cleanupMu.Lock()
	defer o.cleanupMu.Unlock()
	if o.revokeDone {
		return nil
	}
	if err := revokeCleanup(ctx, config); err != nil {
		return err
	}
	o.revokeDone = true
	return nil
}

func (o *Owner) takeCleanupState() (Config, *Materialized, string, string, *pathIdentity, *pathIdentity) {
	o.mu.Lock()
	defer o.mu.Unlock()
	config, material, path, dir := o.config, o.material, o.tempPath, o.tempDir
	dirID, pathID := o.tempDirID, o.tempPathID
	o.tempPath, o.tempDir = "", ""
	o.tempDirID, o.tempPathID = nil, nil
	if material != nil {
		path, dir = material.path, material.dir
		material.path, material.dir = "", ""
		material.args = nil
	}
	return config, material, path, dir, dirID, pathID
}

// removeCleanupFiles 尽力删除文件和目录，即使文件删除失败也继续清理目录。
func removeCleanupFiles(path, dir string, identities ...*pathIdentity) error {
	if path != "" && dir == "" {
		return ErrCleanup
	}
	if err := validateCleanupIdentities(path, dir, identities); err != nil {
		return err
	}
	failed := removeCleanupPath(path)
	if dir != "" {
		if err := removeCleanupDir(dir, identities); err != nil && failed == nil {
			failed = err
		}
		if err := syncDirectory(filepath.Dir(dir)); err != nil && failed == nil {
			failed = err
		}
	}
	return failed
}

// validateCleanupIdentities 在删除前确认目录和配置文件没有被替换。
func validateCleanupIdentities(path, dir string, identities []*pathIdentity) error {
	if dir == "" {
		return nil
	}
	if len(identities) > 0 && identities[0] != nil && !matchesPathIdentity(dir, *identities[0], true) {
		return ErrCleanup
	}
	if len(identities) > 1 && identities[1] != nil && path != "" && !matchesPathIdentity(path, *identities[1], false) {
		return ErrCleanup
	}
	return nil
}

// removeCleanupPath 删除已绑定的配置文件，不跟随符号链接。
func removeCleanupPath(path string) error {
	if path == "" {
		return nil
	}
	return removeIfPresent(path)
}

// removeCleanupDir 非递归移除空目录，避免目录替换后的 RemoveAll 误删。
func removeCleanupDir(dir string, identities []*pathIdentity) error {
	if len(identities) > 0 && identities[0] != nil && !matchesPathIdentity(dir, *identities[0], true) {
		return ErrCleanup
	}
	return os.Remove(dir)
}

func matchesPathIdentity(path string, expected pathIdentity, directory bool) bool {
	current, err := inspectPrivatePath(path, directory)
	return err == nil && current.canonical == expected.canonical && os.SameFile(current.info, expected.info)
}

func revokeCleanup(ctx context.Context, config Config) error {
	if isNil(config.Revoker) || !validLeaseID(config.LeaseID) {
		return ErrCleanup
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return config.Revoker.Revoke(context.WithoutCancel(ctx), config.LeaseID)
}

func removeIfPresent(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func syncDirectory(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}
