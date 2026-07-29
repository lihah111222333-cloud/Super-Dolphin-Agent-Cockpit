package historyjsonl

import (
	"bufio"
	"container/list"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const defaultRecoveryArtifactCacheLimit = 256

// RecoveryArtifactIdentityError 表示 artifact 路径、provider 或内部 UUID 与恢复身份不一致。
type RecoveryArtifactIdentityError struct {
	Provider string
	Expected string
	Cause    error
}

// Error 返回 recovery artifact 身份错误上下文。
func (e *RecoveryArtifactIdentityError) Error() string {
	return fmt.Sprintf("validate %s recovery artifact for %q: %v", e.Provider, e.Expected, e.Cause)
}

// Unwrap 保留 recovery artifact 身份错误根因。
func (e *RecoveryArtifactIdentityError) Unwrap() error {
	return e.Cause
}

// IsRecoveryArtifactIdentityError 判断错误链中是否包含 artifact 身份不一致。
func IsRecoveryArtifactIdentityError(err error) bool {
	var target *RecoveryArtifactIdentityError
	return errors.As(err, &target)
}

// RecoveryArtifactRaceError 表示 discovery 后 artifact 消失或读取期间被替换。
type RecoveryArtifactRaceError struct {
	Path  string
	Cause error
}

// Error 返回 recovery artifact 竞态上下文。
func (e *RecoveryArtifactRaceError) Error() string {
	return fmt.Sprintf("recovery artifact changed concurrently at %q: %v", e.Path, e.Cause)
}

// Unwrap 保留 artifact 竞态根因。
func (e *RecoveryArtifactRaceError) Unwrap() error {
	return e.Cause
}

// IsRecoveryArtifactRaceError 判断错误链中是否包含 artifact 竞态。
func IsRecoveryArtifactRaceError(err error) bool {
	var target *RecoveryArtifactRaceError
	return errors.As(err, &target)
}

type recoveryFS struct {
	lstat        func(string) (os.FileInfo, error)
	stat         func(string) (os.FileInfo, error)
	open         func(string) (*os.File, error)
	evalSymlinks func(string) (string, error)
	walkDir      func(string, fs.WalkDirFunc) error
}

var defaultRecoveryFS = recoveryFS{
	lstat:        os.Lstat,
	stat:         os.Stat,
	open:         os.Open,
	evalSymlinks: filepath.EvalSymlinks,
	walkDir:      filepath.WalkDir,
}

type recoveryCacheKey struct {
	provider    string
	identity    string
	rolloutPath string
	codexHome   string
	claudeHome  string
}

type recoveryCacheEntry struct {
	key      recoveryCacheKey
	path     string
	info     os.FileInfo
	revision [sha256.Size]byte
}

type recoveryArtifactCache struct {
	mu      sync.Mutex
	limit   int
	entries map[recoveryCacheKey]*list.Element
	order   *list.List
}

// newRecoveryArtifactCache 创建有界 LRU cache，零或负上限直接失败。
func newRecoveryArtifactCache(limit int) *recoveryArtifactCache {
	if limit <= 0 {
		panic("recovery artifact cache limit must be positive")
	}
	return &recoveryArtifactCache{
		limit:   limit,
		entries: make(map[recoveryCacheKey]*list.Element, limit),
		order:   list.New(),
	}
}

// get 返回 cache 快照并刷新 LRU 顺序。
func (c *recoveryArtifactCache) get(key recoveryCacheKey) (recoveryCacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.entries[key]
	if !ok {
		return recoveryCacheEntry{}, false
	}
	c.order.MoveToFront(element)
	return element.Value.(recoveryCacheEntry), true
}

// put 写入 cache，并严格维持容量上限。
func (c *recoveryArtifactCache) put(entry recoveryCacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.entries[entry.key]; ok {
		element.Value = entry
		c.order.MoveToFront(element)
		return
	}
	element := c.order.PushFront(entry)
	c.entries[entry.key] = element
	if c.order.Len() <= c.limit {
		return
	}
	oldest := c.order.Back()
	delete(c.entries, oldest.Value.(recoveryCacheEntry).key)
	c.order.Remove(oldest)
}

// remove 删除失效 cache 项。
func (c *recoveryArtifactCache) remove(key recoveryCacheKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.entries[key]
	if !ok {
		return
	}
	delete(c.entries, key)
	c.order.Remove(element)
}

// len 返回 cache 当前条目数，仅供同包门禁测试。
func (c *recoveryArtifactCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}

type recoveryValidator struct {
	fs    recoveryFS
	cache *recoveryArtifactCache
}

// defaultRecoveryValidator 是 historyjsonl owner 内部的受控有界运行时 cache。
var defaultRecoveryValidator = newRecoveryValidator(defaultRecoveryFS, defaultRecoveryArtifactCacheLimit)

// newRecoveryValidator 创建可注入文件系统的 recovery artifact validator。
func newRecoveryValidator(ops recoveryFS, cacheLimit int) *recoveryValidator {
	if ops.lstat == nil || ops.stat == nil || ops.open == nil || ops.evalSymlinks == nil || ops.walkDir == nil {
		panic("recovery validator filesystem is incomplete")
	}
	return &recoveryValidator{fs: ops, cache: newRecoveryArtifactCache(cacheLimit)}
}

// ValidateRecoveryArtifact 只按已选 provider UUID 验证恢复 artifact。
func ValidateRecoveryArtifact(req ReadRequest) (string, error) {
	return defaultRecoveryValidator.validate(req)
}

// validate 先复用稳定 fingerprint cache，再执行严格 discovery 和内容验证。
func (v *recoveryValidator) validate(req ReadRequest) (string, error) {
	provider := strings.ToLower(strings.TrimSpace(req.Provider))
	identity := strings.ToLower(strings.TrimSpace(req.ProviderThreadID))
	key := recoveryValidationKey(req, provider, identity)
	if cached, ok := v.cache.get(key); ok {
		path, hit, err := v.validateCached(cached)
		if err != nil || hit {
			return path, err
		}
	}
	path, discovered, err := v.resolvePath(req, provider, identity)
	if err != nil {
		return "", err
	}
	info, revision, err := v.validatePath(path, provider, identity, discovered)
	if err != nil {
		return "", err
	}
	v.cache.put(recoveryCacheEntry{key: key, path: path, info: info, revision: revision})
	return path, nil
}

// validateCached 复用已发现路径，但从同一已打开 fd 复验 provider identity。
func (v *recoveryValidator) validateCached(entry recoveryCacheEntry) (string, bool, error) {
	info, err := v.fs.lstat(entry.path)
	if err != nil {
		v.cache.remove(entry.key)
		if errors.Is(err, os.ErrNotExist) {
			return "", false, &RecoveryArtifactRaceError{Path: entry.path, Cause: err}
		}
		return "", false, fmt.Errorf("lstat cached recovery artifact: %w", err)
	}
	if sameRecoveryFile(entry.info, info) {
		file, err := v.fs.open(entry.path)
		if err != nil {
			v.cache.remove(entry.key)
			return "", false, v.pathAccessError(entry.path, err, true)
		}
		defer func() { _ = file.Close() }()
		if err := v.validateCachedIdentity(file, entry, info); err != nil {
			v.cache.remove(entry.key)
			return "", false, err
		}
		return entry.path, true, nil
	}
	v.cache.remove(entry.key)
	validated, revision, err := v.validatePath(entry.path, entry.key.provider, entry.key.identity, true)
	if err != nil {
		return "", false, err
	}
	v.cache.put(recoveryCacheEntry{key: entry.key, path: entry.path, info: validated, revision: revision})
	return entry.path, true, nil
}

// resolvePath 只使用已选 provider UUID，不接受 public thread 或其它候选。
func (v *recoveryValidator) resolvePath(req ReadRequest, provider, identity string) (string, bool, error) {
	explicit := strings.TrimSpace(req.RolloutPath)
	root, err := v.recoveryRoot(req, provider, identity)
	if err != nil {
		return "", false, err
	}
	if explicit != "" {
		path, err := v.secureExplicitPath(root, explicit, provider, identity)
		return path, false, err
	}
	path, err := discoverMatchingArtifact(
		root,
		[]string{identity},
		recoveryArtifactPrefix(provider),
		discoveryOps{stat: v.fs.stat, walkDir: v.fs.walkDir},
	)
	if err != nil {
		return "", true, err
	}
	path, err = v.secureDiscoveredPath(root, path, provider, identity)
	return path, true, err
}

// recoveryRoot 返回已解析 symlink 的 provider canonical artifact root。
func (v *recoveryValidator) recoveryRoot(req ReadRequest, provider, identity string) (string, error) {
	var rawRoot string
	var err error
	switch provider {
	case "codex":
		if strings.TrimSpace(req.CodexHome) == "" {
			return "", recoveryIdentityError(provider, identity, errors.New("authoritative Codex recovery home is required"))
		}
		var home string
		home, err = codexRoot(req.CodexHome)
		rawRoot = filepath.Join(home, "sessions")
	case "claude":
		if strings.TrimSpace(req.ClaudeHome) == "" {
			return "", recoveryIdentityError(provider, identity, errors.New("authoritative Claude recovery home is required"))
		}
		var home string
		home, err = claudeRoot(req.ClaudeHome)
		rawRoot = filepath.Join(home, "projects")
	default:
		return "", fmt.Errorf("unsupported provider %q", provider)
	}
	if err != nil {
		return "", err
	}
	root, err := v.fs.evalSymlinks(rawRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w: provider root %s", errProviderHistoryNotFound, rawRoot)
		}
		return "", fmt.Errorf("canonicalize provider recovery root %s: %w", rawRoot, err)
	}
	return filepath.Clean(root), nil
}

// secureExplicitPath 验证显式 rollout 的 canonical containment、文件类型和命名身份。
func (v *recoveryValidator) secureExplicitPath(root, rawPath, provider, identity string) (string, error) {
	info, err := v.fs.lstat(rawPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w: %w", errProviderHistoryNotFound, err)
		}
		return "", fmt.Errorf("lstat explicit recovery artifact: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", recoveryIdentityError(provider, identity, errors.New("recovery artifact must not be a symlink"))
	}
	path, err := v.fs.evalSymlinks(rawPath)
	if err != nil {
		return "", fmt.Errorf("canonicalize explicit recovery artifact: %w", err)
	}
	if err := validateRecoveryPath(root, path, provider, identity); err != nil {
		return "", err
	}
	return filepath.Clean(path), nil
}

// secureDiscoveredPath 将 discovery 后消失分类为 race，而不是明确 missing。
func (v *recoveryValidator) secureDiscoveredPath(root, rawPath, provider, identity string) (string, error) {
	info, err := v.fs.lstat(rawPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", &RecoveryArtifactRaceError{Path: rawPath, Cause: err}
		}
		return "", fmt.Errorf("lstat discovered recovery artifact: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", recoveryIdentityError(provider, identity, errors.New("discovered recovery artifact must not be a symlink"))
	}
	path, err := v.fs.evalSymlinks(rawPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", &RecoveryArtifactRaceError{Path: rawPath, Cause: err}
		}
		return "", fmt.Errorf("canonicalize discovered recovery artifact: %w", err)
	}
	if err := validateRecoveryPath(root, path, provider, identity); err != nil {
		return "", err
	}
	return filepath.Clean(path), nil
}

// validateRecoveryPath 验证 canonical containment 和 provider-specific 文件名。
func validateRecoveryPath(root, path, provider, identity string) error {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return recoveryIdentityError(provider, identity, fmt.Errorf("resolve recovery artifact containment: %w", err))
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || filepath.IsAbs(relative) {
		return recoveryIdentityError(provider, identity, fmt.Errorf("recovery artifact escapes provider root: %s", path))
	}
	if !matchesArtifactName(filepath.Base(path), recoveryArtifactPrefix(provider), identity) {
		return recoveryIdentityError(provider, identity, fmt.Errorf("artifact filename does not match selected provider UUID: %s", path))
	}
	return nil
}

// recoveryArtifactPrefix 返回 provider-specific artifact 文件名前缀。
func recoveryArtifactPrefix(provider string) string {
	if provider == "codex" {
		return "rollout-"
	}
	return ""
}

// validatePath 打开并完整验证同一普通文件 snapshot。
func (v *recoveryValidator) validatePath(path, provider, identity string, disappearanceIsRace bool) (os.FileInfo, [sha256.Size]byte, error) {
	before, err := v.lstatRegularArtifact(path, provider, identity, disappearanceIsRace)
	if err != nil {
		return nil, [sha256.Size]byte{}, err
	}
	file, err := v.fs.open(path)
	if err != nil {
		return nil, [sha256.Size]byte{}, v.pathAccessError(path, err, disappearanceIsRace)
	}
	defer func() { _ = file.Close() }()
	return v.validateOpenedArtifact(file, path, provider, identity, before)
}

// lstatRegularArtifact 校验 artifact 是当前路径上的普通非 symlink 文件。
func (v *recoveryValidator) lstatRegularArtifact(path, provider, identity string, disappearanceIsRace bool) (os.FileInfo, error) {
	info, err := v.fs.lstat(path)
	if err != nil {
		return nil, v.pathAccessError(path, err, disappearanceIsRace)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, recoveryIdentityError(provider, identity, errors.New("recovery artifact must not be a symlink"))
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("recovery artifact is not a regular file: %s", path)
	}
	return info, nil
}

// validateOpenedArtifact 完整解析已打开 snapshot，并确认读取期间未被替换。
func (v *recoveryValidator) validateOpenedArtifact(file *os.File, path, provider, identity string, before os.FileInfo) (os.FileInfo, [sha256.Size]byte, error) {
	opened, err := file.Stat()
	if err != nil {
		return nil, [sha256.Size]byte{}, fmt.Errorf("stat opened recovery artifact: %w", err)
	}
	if !sameRecoveryFile(before, opened) {
		return nil, [sha256.Size]byte{}, &RecoveryArtifactRaceError{Path: path, Cause: errors.New("artifact changed between lstat and open")}
	}
	digest := sha256.New()
	if err := validateRecoveryContents(io.TeeReader(file, digest), provider, identity); err != nil {
		return nil, [sha256.Size]byte{}, err
	}
	revision := [sha256.Size]byte(digest.Sum(nil))
	after, err := file.Stat()
	if err != nil {
		return nil, [sha256.Size]byte{}, fmt.Errorf("stat validated recovery artifact: %w", err)
	}
	current, err := v.fs.lstat(path)
	if err != nil {
		return nil, [sha256.Size]byte{}, v.pathAccessError(path, err, true)
	}
	if !sameRecoveryFile(before, after) || !sameRecoveryFile(after, current) {
		return nil, [sha256.Size]byte{}, &RecoveryArtifactRaceError{Path: path, Cause: errors.New("artifact changed during validation")}
	}
	return after, revision, nil
}

// validateCachedIdentity 在 metadata 相同的 cache hit 上复验完整 snapshot 内容修订。
func (v *recoveryValidator) validateCachedIdentity(file *os.File, entry recoveryCacheEntry, before os.FileInfo) error {
	opened, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat cached recovery artifact: %w", err)
	}
	if !sameRecoveryFile(before, opened) {
		return &RecoveryArtifactRaceError{Path: entry.path, Cause: errors.New("cached artifact changed between lstat and open")}
	}
	if err := validateCachedRevision(file, entry); err != nil {
		return err
	}
	return v.validateCachedSnapshotStable(file, entry.path, before)
}

func validateCachedRevision(file *os.File, entry recoveryCacheEntry) error {
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return fmt.Errorf("hash cached recovery artifact: %w", err)
	}
	if subtle.ConstantTimeCompare(digest.Sum(nil), entry.revision[:]) != 1 {
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("rewind changed cached recovery artifact: %w", err)
		}
		if err := validateRecoveryContents(file, entry.key.provider, entry.key.identity); err != nil {
			return err
		}
		return &RecoveryArtifactRaceError{
			Path:  entry.path,
			Cause: errors.New("cached artifact content revision changed without metadata revision"),
		}
	}
	return nil
}

func (v *recoveryValidator) validateCachedSnapshotStable(file *os.File, path string, before os.FileInfo) error {
	after, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat cached recovery artifact after identity validation: %w", err)
	}
	current, err := v.fs.lstat(path)
	if err != nil {
		return v.pathAccessError(path, err, true)
	}
	if !sameRecoveryFile(before, after) || !sameRecoveryFile(after, current) {
		return &RecoveryArtifactRaceError{Path: path, Cause: errors.New("cached artifact changed during identity validation")}
	}
	return nil
}

// pathAccessError 区分初始明确缺失和 discovery/cache 后消失。
func (v *recoveryValidator) pathAccessError(path string, err error, disappearanceIsRace bool) error {
	if errors.Is(err, os.ErrNotExist) {
		if disappearanceIsRace {
			return &RecoveryArtifactRaceError{Path: path, Cause: err}
		}
		return fmt.Errorf("%w: %w", errProviderHistoryNotFound, err)
	}
	return fmt.Errorf("access recovery artifact %s: %w", path, err)
}

// validateRecoveryContents 完整解析 JSONL 并要求 provider-native identity 与所选 UUID 一致。
func validateRecoveryContents(reader io.Reader, provider, identity string) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	foundIdentity := false
	for scanner.Scan() {
		raw := scanner.Bytes()
		if len(strings.TrimSpace(string(raw))) == 0 {
			continue
		}
		if _, _, err := parseLineStrict(raw, provider); err != nil {
			return err
		}
		artifactIdentity, found, err := recoveryIdentityFromLine(raw, provider)
		if err != nil {
			return err
		}
		if !found {
			continue
		}
		foundIdentity = true
		if !strings.EqualFold(artifactIdentity, identity) {
			return recoveryIdentityError(provider, identity, fmt.Errorf("artifact UUID is %q", artifactIdentity))
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan recovery artifact: %w", err)
	}
	if !foundIdentity {
		return recoveryIdentityError(provider, identity, errors.New("artifact provider identity is missing"))
	}
	return nil
}

// recoveryIdentityFromLine 提取 provider-native artifact identity。
func recoveryIdentityFromLine(raw []byte, provider string) (string, bool, error) {
	switch provider {
	case "codex":
		var line struct {
			Type    string `json:"type"`
			Payload struct {
				ID string `json:"id"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(raw, &line); err != nil {
			return "", false, &ParseError{Provider: provider, Cause: err}
		}
		if line.Type != "session_meta" {
			return "", false, nil
		}
		if strings.TrimSpace(line.Payload.ID) == "" {
			return "", false, recoveryIdentityError(provider, "", errors.New("session_meta id is empty"))
		}
		return strings.TrimSpace(line.Payload.ID), true, nil
	case "claude":
		var line struct {
			SessionID       string `json:"sessionId"`
			SessionIDLegacy string `json:"session_id"`
		}
		if err := json.Unmarshal(raw, &line); err != nil {
			return "", false, &ParseError{Provider: provider, Cause: err}
		}
		identity := strings.TrimSpace(line.SessionID)
		if identity == "" {
			identity = strings.TrimSpace(line.SessionIDLegacy)
		}
		return identity, identity != "", nil
	default:
		return "", false, fmt.Errorf("unsupported provider %q", provider)
	}
}

// recoveryIdentityError 构造稳定 artifact identity 错误。
func recoveryIdentityError(provider, expected string, cause error) error {
	return &RecoveryArtifactIdentityError{Provider: provider, Expected: expected, Cause: cause}
}

// recoveryValidationKey 规范化 cache key。
func recoveryValidationKey(req ReadRequest, provider, identity string) recoveryCacheKey {
	return recoveryCacheKey{
		provider:    provider,
		identity:    identity,
		rolloutPath: filepath.Clean(strings.TrimSpace(req.RolloutPath)),
		codexHome:   filepath.Clean(strings.TrimSpace(req.CodexHome)),
		claudeHome:  filepath.Clean(strings.TrimSpace(req.ClaudeHome)),
	}
}

// sameRecoveryFile 比较 inode 等价性和 metadata revision fingerprint。
func sameRecoveryFile(left, right os.FileInfo) bool {
	return left != nil &&
		right != nil &&
		os.SameFile(left, right) &&
		left.Size() == right.Size() &&
		left.ModTime().Equal(right.ModTime()) &&
		left.Mode() == right.Mode()
}
