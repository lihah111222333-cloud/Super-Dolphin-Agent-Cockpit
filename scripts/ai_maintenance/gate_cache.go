package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	gateCacheSchemaVersion = "gate-cache-v5"
	defaultGateCacheMaxAge = 10 * time.Minute
	maxGateCacheMarkers    = 32
)

type gateRunner struct {
	run       func() error
	cacheable bool
}

type gateFingerprinter func(gate string, plan gatePlan) (string, error)

type gateResultCache struct {
	root        string
	maxAge      time.Duration
	now         func() time.Time
	fingerprint gateFingerprinter
}

// optionalGateResultCache 在未配置目录时关闭缓存，配置后必须成功建立完整缓存契约。
func optionalGateResultCache(root string, maxAge time.Duration, scope string) (*gateResultCache, error) {
	if root == "" {
		return nil, nil
	}
	return newGateResultCache(root, maxAge, scope)
}

// newGateResultCache 校验缓存目录、有效期与 staged tree 真值源，拒绝不完整配置。
func newGateResultCache(root string, maxAge time.Duration, scope string) (*gateResultCache, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("gate cache directory is empty")
	}
	if maxAge <= 0 {
		return nil, fmt.Errorf("gate cache max age must be positive: %s", maxAge)
	}
	if scope == "" {
		out, err := exec.Command("git", "write-tree").CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("resolve gate cache staged tree: %w\n%s", err, out)
		}
		scope = strings.TrimSpace(string(out))
	}
	if !isGitObjectID(scope) {
		return nil, fmt.Errorf("gate cache scope must be a Git object ID: %q", scope)
	}
	objectType, err := exec.Command("git", "cat-file", "-t", scope).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("resolve gate cache scope object %q: %w\n%s", scope, err, objectType)
	}
	if strings.TrimSpace(string(objectType)) != "tree" {
		return nil, fmt.Errorf("gate cache scope must be a Git tree: %q is %s", scope, strings.TrimSpace(string(objectType)))
	}
	if err := validateImmutableCacheIndex(scope); err != nil {
		return nil, err
	}
	return &gateResultCache{
		root:   root,
		maxAge: maxAge,
		now:    time.Now,
		fingerprint: func(gate string, plan gatePlan) (string, error) {
			return fingerprintGateInputs(scope, gate, plan)
		},
	}, nil
}

// validateImmutableCacheIndex 要求缓存执行绑定到从 scope 派生的独立 index，避免真实 index A→B→A 的 ABA 假绿。
func validateImmutableCacheIndex(scope string) error {
	indexPath := os.Getenv("GIT_INDEX_FILE")
	if !filepath.IsAbs(indexPath) {
		return fmt.Errorf("gate cache requires an absolute GIT_INDEX_FILE, got %q", indexPath)
	}
	info, err := os.Lstat(indexPath)
	if err != nil {
		return fmt.Errorf("stat gate cache index: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("gate cache index must be a regular file: %s", indexPath)
	}
	realGitDir, err := exec.Command("git", "rev-parse", "--absolute-git-dir").CombinedOutput()
	if err != nil {
		return fmt.Errorf("resolve real Git directory: %w\n%s", err, realGitDir)
	}
	realIndex := filepath.Join(strings.TrimSpace(string(realGitDir)), "index")
	if filepath.Clean(indexPath) == filepath.Clean(realIndex) {
		return fmt.Errorf("gate cache index must be isolated from the real Git index: %s", indexPath)
	}
	out, err := exec.Command("git", "write-tree").CombinedOutput()
	if err != nil {
		return fmt.Errorf("read immutable gate cache index: %w\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != scope {
		return fmt.Errorf("gate cache index tree %q does not match cache scope %q", got, scope)
	}
	return nil
}

// run 仅复用完整且未过期的绿色 marker，未命中时真实执行并原子发布结果。
func (c *gateResultCache) run(gate string, plan gatePlan, run func() error) error {
	fingerprint, err := c.fingerprint(gate, plan)
	if err != nil {
		return fmt.Errorf("fingerprint gate %q: %w", gate, err)
	}
	marker, err := c.markerPath(gate, fingerprint)
	if err != nil {
		return err
	}
	hit, err := c.checkMarker(marker, gate, fingerprint)
	if err != nil {
		return err
	}
	if hit {
		if err := c.requireUnchangedFingerprint(gate, plan, fingerprint); err != nil {
			return err
		}
		fmt.Printf("[ai-maintenance] cache hit gate=%s key=%s\n", gate, fingerprint[:12])
		return nil
	}
	if err := runGateWithTiming(gate, run); err != nil {
		return err
	}
	if err := c.requireUnchangedFingerprint(gate, plan, fingerprint); err != nil {
		return err
	}
	if err := c.saveMarker(marker, gate, fingerprint); err != nil {
		return err
	}
	return pruneGateCache(filepath.Dir(marker), maxGateCacheMarkers)
}

// requireUnchangedFingerprint 在复用或发布绿色结果前再次读取输入，避免并发改动制造假绿。
func (c *gateResultCache) requireUnchangedFingerprint(gate string, plan gatePlan, want string) error {
	got, err := c.fingerprint(gate, plan)
	if err != nil {
		return fmt.Errorf("re-fingerprint gate %q: %w", gate, err)
	}
	if got != want {
		return fmt.Errorf("gate %q inputs changed during cache validation; retry the gate", gate)
	}
	return nil
}

func runGateWithTiming(gate string, run func() error) error {
	started := time.Now()
	err := run()
	fmt.Printf("[ai-maintenance] gate=%s duration=%s\n", gate, time.Since(started).Round(time.Millisecond))
	return err
}

func (c *gateResultCache) markerPath(gate, fingerprint string) (string, error) {
	decoded, err := hex.DecodeString(fingerprint)
	if err != nil || len(decoded) != sha256.Size {
		return "", fmt.Errorf("gate %q produced invalid SHA-256 fingerprint %q", gate, fingerprint)
	}
	dirName := strings.NewReplacer(":", "_", "/", "_").Replace(gate)
	return filepath.Join(c.root, dirName, fingerprint+".ok"), nil
}

// checkMarker 区分未命中、过期、有效和损坏四种状态，损坏内容立即阻断。
func (c *gateResultCache) checkMarker(marker, gate, fingerprint string) (bool, error) {
	info, err := os.Stat(marker)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat gate cache marker: %w", err)
	}
	age := c.now().Sub(info.ModTime())
	if age < 0 || age > c.maxAge {
		return false, nil
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		return false, fmt.Errorf("read gate cache marker: %w", err)
	}
	want := gateCacheMarker(gate, fingerprint)
	if !bytes.Equal(data, want) {
		return false, fmt.Errorf("gate cache marker is corrupt: %s", marker)
	}
	return true, nil
}

// saveMarker 通过同目录临时文件和原子 rename 发布绿色结果，避免并发读取半写 marker。
func (c *gateResultCache) saveMarker(marker, gate, fingerprint string) error {
	dir := filepath.Dir(marker)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create gate cache directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".gate-cache-*.tmp")
	if err != nil {
		return fmt.Errorf("create gate cache marker: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod gate cache marker: %w", err)
	}
	if _, err := tmp.Write(gateCacheMarker(gate, fingerprint)); err != nil {
		tmp.Close()
		return fmt.Errorf("write gate cache marker: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync gate cache marker: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close gate cache marker: %w", err)
	}
	if err := os.Rename(tmpName, marker); err != nil {
		return c.refreshMatchingMarker(marker, gate, fingerprint, err)
	}
	return nil
}

func (c *gateResultCache) refreshMatchingMarker(marker, gate, fingerprint string, publishErr error) error {
	data, err := os.ReadFile(marker)
	if err != nil {
		return fmt.Errorf("publish gate cache marker: %w", publishErr)
	}
	if !bytes.Equal(data, gateCacheMarker(gate, fingerprint)) {
		return fmt.Errorf("publish gate cache marker: %w", publishErr)
	}
	now := c.now()
	if err := os.Chtimes(marker, now, now); err != nil {
		return fmt.Errorf("refresh matching gate cache marker after publish conflict: %w", err)
	}
	return nil
}

func gateCacheMarker(gate, fingerprint string) []byte {
	return []byte(gateCacheSchemaVersion + "\n" + gate + "\n" + fingerprint + "\n")
}

func fingerprintGateInputs(scope, gate string, plan gatePlan) (string, error) {
	return fingerprintGateInputsAt(scope, gate, plan, time.Now())
}

// fingerprintGateInputsAt 生成包含 Git 真值源、工具链和环境的稳定输入指纹。
func fingerprintGateInputsAt(scope, gate string, plan gatePlan, _ time.Time) (string, error) {
	h := sha256.New()
	planData, err := json.Marshal(plan)
	if err != nil {
		return "", fmt.Errorf("marshal gate plan: %w", err)
	}
	writeFingerprintField(h, "schema", []byte(gateCacheSchemaVersion))
	writeFingerprintField(h, "staged-tree", []byte(scope))
	writeFingerprintField(h, "gate", []byte(gate))
	writeFingerprintField(h, "plan", planData)
	for _, command := range gateFingerprintCommands(scope, gate) {
		out, err := exec.Command(command[0], command[1:]...).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("run fingerprint command %q: %w\n%s", strings.Join(command, " "), err, out)
		}
		writeFingerprintField(h, "command:"+strings.Join(command, " "), out)
	}
	environ := stableGateEnvironment()
	writeFingerprintField(h, "environment", []byte(strings.Join(environ, "\x00")))
	return hex.EncodeToString(h.Sum(nil)), nil
}

func stableGateEnvironment() []string {
	volatile := map[string]bool{
		"GIT_INDEX_FILE": true,
		"OLDPWD":         true,
		"PWD":            true,
		"SHLVL":          true,
		"_":              true,
	}
	environ := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !volatile[name] {
			environ = append(environ, entry)
		}
	}
	sort.Strings(environ)
	return environ
}

func gateFingerprintCommands(scope, gate string) [][]string {
	commands := [][]string{
		{"git", "diff", "--binary", "--no-ext-diff", scope, "--"},
		{"git", "--version"},
	}
	if gate == "project-map:check" {
		commands = append(commands, []string{"git", "ls-files", "-s", "-z"})
	}
	if gateUsesGo(gate) {
		commands = append(commands, []string{"go", "version"}, []string{"go", "env", "GOOS", "GOARCH", "CGO_ENABLED", "GOROOT"})
	}
	if gateUsesNode(gate) {
		commands = append(commands, []string{"node", "--version"}, []string{"npm", "--version"})
	}
	if gate != "diff:whitespace" {
		commands = append(commands, []string{"make", "--version"})
	}
	return commands
}

func gateUsesGo(gate string) bool {
	return strings.HasPrefix(gate, "backend:") ||
		strings.HasPrefix(gate, "repo:") ||
		strings.HasPrefix(gate, "sqlc:") ||
		gate == "ai-maintenance:self-test" ||
		gate == "codemap:check"
}

func gateUsesNode(gate string) bool {
	return strings.HasPrefix(gate, "frontend:") || gate == "project-map:check"
}

func isGitObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func writeFingerprintField(h hash.Hash, label string, data []byte) {
	fmt.Fprintf(h, "%s\x00%d\x00", label, len(data))
	h.Write(data)
}

// pruneGateCache 仅保留最新有限数量的绿色 marker，防止本地缓存无界增长。
func pruneGateCache(dir string, keep int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read gate cache directory: %w", err)
	}
	type marker struct {
		path    string
		modTime time.Time
	}
	markers := make([]marker, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".ok") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("stat gate cache entry: %w", err)
		}
		markers = append(markers, marker{path: filepath.Join(dir, entry.Name()), modTime: info.ModTime()})
	}
	sort.Slice(markers, func(i, j int) bool { return markers[i].modTime.After(markers[j].modTime) })
	if len(markers) <= keep {
		return nil
	}
	for _, stale := range markers[keep:] {
		if err := os.Remove(stale.path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("prune gate cache marker: %w", err)
		}
	}
	return nil
}
