package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

const (
	agentLSPSharedCacheDirEnv         = "AGENT_LSP_SHARED_CACHE_DIR"
	runtimePrimaryRSSLimitEnv         = "AGENT_LSP_PRIMARY_RSS_LIMIT_MB"
	runtimeSecondaryRSSLimitEnv       = "AGENT_LSP_SECONDARY_RSS_LIMIT_MB"
	runtimeNodePrimaryHeapEnv         = "AGENT_LSP_NODE_PRIMARY_HEAP_LIMIT_MB"
	runtimeNodeSecondaryHeapEnv       = "AGENT_LSP_NODE_SECONDARY_HEAP_LIMIT_MB"
	runtimeDefaultPrimaryRSSLimitMB   = 2560
	runtimeDefaultSecondaryRSSLimitMB = 2560
	runtimeDefaultNodePrimaryHeapMB   = 2048
	runtimeDefaultNodeSecondaryHeapMB = 2048
	runtimeDefaultCohortRSSLimitMB    = 15 * 1024
	runtimeResourceLeaseSchemaVersion = 1
	runtimeResourceLeaseLockTimeout   = 2 * time.Second
	runtimeResourceLeaseLockRetry     = 10 * time.Millisecond
	runtimeSecondaryLeaseMaxCount     = 256
	runtimeLeaseQuarantineMaxCount    = 32
	runtimeLeaseQuarantineMaxAge      = 24 * time.Hour
	// runtimeNodeMaxOldSpaceMB 保留 adapter 初始化选项的主实例预算；secondary 在 client factory 按租约收窄。
	runtimeNodeMaxOldSpaceMB = runtimeDefaultNodePrimaryHeapMB
)

type runtimeServerResourceLease struct {
	SchemaVersion      int    `json:"schema_version"`
	CohortID           string `json:"cohort_id"`
	Role               string `json:"role"`
	OwnerPID           int    `json:"owner_pid"`
	OwnerStartIdentity string `json:"owner_start_identity"`
	CreatedAtUnixNano  int64  `json:"created_at_unix_nano"`
}

type runtimeServerResourceLimits struct {
	primaryRSSMB        int
	secondaryRSSMB      int
	primaryNodeHeapMB   int
	secondaryNodeHeapMB int
	cohortRSSMB         int
}

// runtimeServerLeaseQuarantine 描述一份待按年龄和数量回收的坏 secondary 租约证据。
type runtimeServerLeaseQuarantine struct {
	path    string
	modTime time.Time
}

// runtimeServerEnvironment 为语言服务器注入全局 RSS 总账及稳定 repo/language 内存 cohort。
// Node compile cache 只是磁盘编译启动优化；内存治理只由主次 heap、RSS 准入与 owner-only 回收承担。
func runtimeServerEnvironment(
	command multilsp.ServerCommand,
	binary, workspaceRoot string,
	languageIDs, env []string,
	nodeBacked bool,
) ([]string, error) {
	limits, err := runtimeServerResolveResourceLimits(env)
	if err != nil {
		return nil, err
	}
	root, resourceDir, err := runtimeServerResourceDirectories(command, binary)
	if err != nil {
		return nil, err
	}
	overrides := []string{
		multilsp.ResourceCohortDirEnv + "=" + resourceDir,
	}
	if runtimeServerIsGopls(command, binary) {
		return appendRuntimeServerEnvironment(env, overrides), nil
	}
	overrides = append(
		overrides,
		multilsp.ResourceCohortHardLimitMBEnv+"="+strconv.Itoa(limits.cohortRSSMB),
	)
	cohortID, err := runtimeServerRepositoryCohortID(command, binary, workspaceRoot, languageIDs)
	if err != nil {
		return nil, err
	}
	role, leasePath, err := runtimeServerAcquireResourceLease(root, cohortID)
	if err != nil {
		return nil, err
	}
	processLimitMB := limits.primaryRSSMB
	if role == multilsp.ResourceCohortRoleSecondary {
		processLimitMB = limits.secondaryRSSMB
	}
	overrides = append(overrides,
		multilsp.ResourceRepositoryCohortIDEnv+"="+cohortID,
		multilsp.ResourceCohortRoleEnv+"="+role,
		multilsp.ResourceCohortLeaseEnv+"="+leasePath,
		multilsp.ResourceProcessRSSLimitMBEnv+"="+strconv.Itoa(processLimitMB),
	)
	if nodeBacked {
		overrides, err = runtimeServerNodeEnvironment(root, command, binary, env, overrides, limits, role)
		if err != nil {
			return nil, errors.Join(err, multilsp.ReleaseResourceCohortLease(overrides))
		}
	}
	return appendRuntimeServerEnvironment(env, overrides), nil
}

// runtimeServerResourceDirectories 建立安全的共享总账根和 members 子目录。
func runtimeServerResourceDirectories(command multilsp.ServerCommand, binary string) (string, string, error) {
	resourceDir, err := runtimeServerResourceCohortDir(command, binary)
	if err != nil {
		return "", "", err
	}
	root, err := runtimeServerCacheRoot()
	if err != nil {
		return "", "", err
	}
	if err := runtimeServerEnsurePrivateDescendant(root, filepath.Join(resourceDir, "members")); err != nil {
		return "", "", fmt.Errorf("secure shared LSP resource cohort directory %s: %w", resourceDir, err)
	}
	return root, resourceDir, nil
}

// runtimeServerNodeEnvironment 应用角色对应的 Node heap，并仅为支持版本启用磁盘 compile cache。
func runtimeServerNodeEnvironment(
	root string,
	command multilsp.ServerCommand,
	binary string,
	env, overrides []string,
	limits runtimeServerResourceLimits,
	role string,
) ([]string, error) {
	nodeVersion, portable, err := runtimeServerNodeVersion(env)
	if err != nil {
		return overrides, err
	}
	heapLimitMB := limits.primaryNodeHeapMB
	if role == multilsp.ResourceCohortRoleSecondary {
		heapLimitMB = limits.secondaryNodeHeapMB
	}
	overrides = append(overrides, "NODE_OPTIONS="+runtimeServerNodeOptions(env, heapLimitMB))
	if !portable {
		return overrides, nil
	}
	cacheDir, err := runtimeServerCacheDir(command, binary)
	if err != nil {
		return overrides, err
	}
	if err := runtimeServerEnsurePrivateDescendant(root, cacheDir); err != nil {
		return overrides, fmt.Errorf("secure Node compile cache directory %s: %w", cacheDir, err)
	}
	nodeCompileCacheDir := filepath.Join(cacheDir, "node-compile", runtimeServerCacheName(nodeVersion))
	if err := runtimeServerEnsurePrivateDescendant(root, nodeCompileCacheDir); err != nil {
		return overrides, fmt.Errorf("create portable Node compile cache directory %s: %w", nodeCompileCacheDir, err)
	}
	return append(overrides,
		"NODE_COMPILE_CACHE="+nodeCompileCacheDir,
		"NODE_COMPILE_CACHE_PORTABLE=1",
	), nil
}

// runtimeServerRepositoryCohortID 绑定 repo、语言、服务内容和参数，跨同仓库 worktree 保持稳定。
func runtimeServerRepositoryCohortID(
	command multilsp.ServerCommand,
	binary, workspaceRoot string,
	languageIDs []string,
) (string, error) {
	_, binaryDigest, err := runtimeServerBinaryIdentity(binary, nil)
	if err != nil {
		return "", err
	}
	repository, err := runtimeServerRepositoryIdentity(workspaceRoot)
	if err != nil {
		return "", err
	}
	languages, err := runtimeServerLanguageIdentity(languageIDs)
	if err != nil {
		return "", err
	}
	identity := strings.Join([]string{
		repository,
		languages,
		binaryDigest,
		strings.Join(command.Args, "\x00"),
	}, "\x00")
	sum := sha256.Sum256([]byte(identity))
	return "repo-" + hex.EncodeToString(sum[:12]), nil
}

func runtimeServerLanguageIdentity(languageIDs []string) (string, error) {
	values := make([]string, 0, len(languageIDs))
	for _, languageID := range languageIDs {
		languageID = strings.ToLower(strings.TrimSpace(languageID))
		if languageID != "" && !slices.Contains(values, languageID) {
			values = append(values, languageID)
		}
	}
	if len(values) == 0 {
		return "", errors.New("language-server repository cohort requires at least one language ID")
	}
	slices.Sort(values)
	return strings.Join(values, ","), nil
}

// runtimeServerRepositoryIdentity 使用 Git common-dir 合并 linked worktree；非 Git 根按规范绝对路径显式隔离。
func runtimeServerRepositoryIdentity(workspaceRoot string) (string, error) {
	root := strings.TrimSpace(workspaceRoot)
	if root == "" {
		return "", errors.New("language-server repository cohort requires a workspace root")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root for resource cohort: %w", err)
	}
	absRoot, err = filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", fmt.Errorf("resolve real workspace root for resource cohort: %w", err)
	}
	rootInfo, err := os.Stat(absRoot)
	if err != nil {
		return "", fmt.Errorf("stat workspace root for resource cohort: %w", err)
	}
	if !rootInfo.IsDir() {
		return "", fmt.Errorf("workspace root for resource cohort is not a directory: %s", absRoot)
	}
	commonDir, found, err := runtimeServerFindGitCommonDir(absRoot)
	if err != nil {
		return "", err
	}
	if found {
		return "git:" + commonDir, nil
	}
	return "root:" + filepath.Clean(absRoot), nil
}

// runtimeServerFindGitCommonDir 向上查找 Git marker，并解析主仓库或 linked worktree 的 common-dir。
func runtimeServerFindGitCommonDir(start string) (string, bool, error) {
	for dir := filepath.Clean(start); ; dir = filepath.Dir(dir) {
		marker := filepath.Join(dir, ".git")
		info, err := os.Lstat(marker)
		if err == nil {
			gitDir, linked, resolveErr := runtimeServerResolveGitDir(marker, info)
			if resolveErr != nil {
				return "", false, resolveErr
			}
			commonDir, commonErr := runtimeServerResolveGitCommonDir(gitDir, linked)
			return commonDir, commonErr == nil, commonErr
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", false, fmt.Errorf("inspect Git marker for resource cohort: %w", err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false, nil
		}
	}
}

// runtimeServerResolveGitDir 区分主仓库目录 marker 与 linked worktree 的 gitdir 文件。
func runtimeServerResolveGitDir(marker string, info os.FileInfo) (string, bool, error) {
	if info.Mode()&os.ModeSymlink != 0 {
		return "", false, fmt.Errorf("Git marker for resource cohort must not be a symlink: %s", marker)
	}
	if info.IsDir() {
		return marker, false, nil
	}
	if !info.Mode().IsRegular() || info.Size() > 4096 {
		return "", false, fmt.Errorf("Git marker for resource cohort is invalid: %s", marker)
	}
	payload, err := os.ReadFile(marker)
	if err != nil {
		return "", false, fmt.Errorf("read Git worktree marker for resource cohort: %w", err)
	}
	value, ok := strings.CutPrefix(strings.TrimSpace(string(payload)), "gitdir:")
	if !ok || strings.TrimSpace(value) == "" {
		return "", false, fmt.Errorf("Git worktree marker has no gitdir: %s", marker)
	}
	gitDir := strings.TrimSpace(value)
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(filepath.Dir(marker), gitDir)
	}
	return filepath.Clean(gitDir), true, nil
}

// runtimeServerAcquireResourceLease 以 PID 启动身份选举唯一 primary，其余 client 获得受限 secondary 租约。
func runtimeServerAcquireResourceLease(root, cohortID string) (role, path string, retErr error) {
	if !strings.HasPrefix(cohortID, "repo-") {
		return "", "", fmt.Errorf("invalid repository cohort ID %q", cohortID)
	}
	cohortDir := filepath.Join(root, "resource-cohorts", "repositories", cohortID)
	if err := runtimeServerEnsurePrivateDescendant(root, cohortDir); err != nil {
		return "", "", fmt.Errorf("secure repository cohort directory: %w", err)
	}
	lock, err := runtimeServerAcquireResourceLeaseLock(cohortDir)
	if err != nil {
		return "", "", err
	}
	defer func() {
		retErr = errors.Join(retErr, runtimeServerReleaseResourceLeaseLock(lock))
	}()
	activeSecondaries, err := runtimeServerCleanupResourceLeases(cohortDir, cohortID, time.Now())
	if err != nil {
		return "", "", err
	}
	ownerStart, err := hiddenexec.ProcessStartIdentity(os.Getpid())
	if err != nil {
		return "", "", fmt.Errorf("read repository cohort owner start identity: %w", err)
	}
	lease := runtimeServerResourceLease{
		SchemaVersion:      runtimeResourceLeaseSchemaVersion,
		CohortID:           cohortID,
		Role:               multilsp.ResourceCohortRolePrimary,
		OwnerPID:           os.Getpid(),
		OwnerStartIdentity: ownerStart,
		CreatedAtUnixNano:  time.Now().UnixNano(),
	}
	primaryPath := filepath.Join(cohortDir, "primary.json")
	return runtimeServerElectResourceLease(cohortDir, primaryPath, lease, activeSecondaries)
}

// runtimeServerAcquireResourceLeaseLock 在有界等待内取得跨进程选主锁，串行化 stale 校验、删除与发布。
func runtimeServerAcquireResourceLeaseLock(cohortDir string) (*os.File, error) {
	path := filepath.Join(cohortDir, ".primary.lock")
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open repository cohort election lock: %w", err)
	}
	if err := runtimeServerValidateResourceLeaseLock(path, file); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	deadline := time.Now().Add(runtimeResourceLeaseLockTimeout)
	for {
		err = runtimeServerTryLockResourceLease(file)
		if err == nil {
			return file, nil
		}
		if !runtimeServerResourceLeaseLockBusy(err) {
			return nil, errors.Join(fmt.Errorf("lock repository cohort election: %w", err), file.Close())
		}
		if !time.Now().Before(deadline) {
			return nil, errors.Join(errors.New("repository cohort election lock timed out"), file.Close())
		}
		time.Sleep(runtimeResourceLeaseLockRetry)
	}
}

// runtimeServerValidateResourceLeaseLock 拒绝符号链接、宽权限或打开后被替换的选主锁文件。
func runtimeServerValidateResourceLeaseLock(path string, file *os.File) error {
	opened, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect opened repository cohort election lock: %w", err)
	}
	linked, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect repository cohort election lock: %w", err)
	}
	if linked.Mode()&os.ModeSymlink != 0 || !linked.Mode().IsRegular() || !os.SameFile(opened, linked) {
		return fmt.Errorf("repository cohort election lock is insecure: %s", path)
	}
	if err := securefs.CheckPrivateOwnerOnly(path, linked); err != nil {
		return fmt.Errorf("repository cohort election lock is insecure: %s: %w", path, err)
	}
	return nil
}

func runtimeServerReleaseResourceLeaseLock(file *os.File) error {
	if file == nil {
		return nil
	}
	return errors.Join(runtimeServerUnlockResourceLease(file), file.Close())
}

// runtimeServerCleanupResourceLeases 清除 PID 身份已失效的 secondary，并约束 quarantine 数量与年龄。
func runtimeServerCleanupResourceLeases(cohortDir, cohortID string, now time.Time) (int, error) {
	entries, err := os.ReadDir(cohortDir)
	if err != nil {
		return 0, fmt.Errorf("read repository cohort leases: %w", err)
	}
	activeSecondaries := 0
	var cleanupErr error
	for _, entry := range entries {
		if !runtimeServerSecondaryLeaseEntry(entry) {
			continue
		}
		active, entryErr := runtimeServerCleanupSecondaryLease(
			filepath.Join(cohortDir, entry.Name()),
			entry.Name(),
			cohortID,
			now,
		)
		cleanupErr = errors.Join(cleanupErr, entryErr)
		if active {
			activeSecondaries++
		}
	}
	quarantines, quarantineErr := runtimeServerLoadLeaseQuarantines(cohortDir)
	cleanupErr = errors.Join(cleanupErr, quarantineErr, runtimeServerCleanupLeaseQuarantines(quarantines, now))
	if activeSecondaries > runtimeSecondaryLeaseMaxCount {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf(
			"repository cohort has %d active secondary leases; limit is %d",
			activeSecondaries,
			runtimeSecondaryLeaseMaxCount,
		))
	}
	return activeSecondaries, cleanupErr
}

func runtimeServerSecondaryLeaseEntry(entry os.DirEntry) bool {
	return !entry.IsDir() && strings.HasPrefix(entry.Name(), "secondary-") && strings.HasSuffix(entry.Name(), ".json")
}

// runtimeServerCleanupSecondaryLease 校验单份 secondary 的 PID 启动身份，并回收失效或损坏租约。
func runtimeServerCleanupSecondaryLease(path, name, cohortID string, now time.Time) (bool, error) {
	lease, err := runtimeServerReadResourceLease(path)
	if err != nil {
		return false, fmt.Errorf(
			"read repository cohort secondary lease %s: %w",
			name,
			errors.Join(err, runtimeServerQuarantineResourceLease(path, now)),
		)
	}
	if err := runtimeServerValidateSecondaryLease(lease, cohortID, path); err != nil {
		return false, errors.Join(err, runtimeServerQuarantineResourceLease(path, now))
	}
	alive, err := hiddenexec.ProcessAlive(lease.OwnerPID)
	if err != nil {
		return false, fmt.Errorf("check repository cohort secondary process: %w", err)
	}
	if !alive {
		return false, runtimeServerRemoveResourceLease(path, "dead secondary")
	}
	ownerStart, err := hiddenexec.ProcessStartIdentity(lease.OwnerPID)
	if err != nil {
		return false, fmt.Errorf("verify repository cohort secondary start identity: %w", err)
	}
	if ownerStart != lease.OwnerStartIdentity {
		return false, runtimeServerRemoveResourceLease(path, "reused-PID secondary")
	}
	return true, nil
}

// runtimeServerLoadLeaseQuarantines 重新扫描隔离文件，使本轮新隔离的坏租约也立即受上限约束。
func runtimeServerLoadLeaseQuarantines(cohortDir string) ([]runtimeServerLeaseQuarantine, error) {
	entries, err := os.ReadDir(cohortDir)
	if err != nil {
		return nil, fmt.Errorf("read repository cohort lease quarantines: %w", err)
	}
	quarantines := make([]runtimeServerLeaseQuarantine, 0)
	var loadErr error
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".bad") {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			loadErr = errors.Join(loadErr, fmt.Errorf("inspect repository cohort lease quarantine %s: %w", entry.Name(), infoErr))
			continue
		}
		quarantines = append(quarantines, runtimeServerLeaseQuarantine{
			path:    filepath.Join(cohortDir, entry.Name()),
			modTime: info.ModTime(),
		})
	}
	return quarantines, loadErr
}

// runtimeServerValidateSecondaryLease 校验 secondary lease 的 schema、cohort 与 owner 身份字段。
func runtimeServerValidateSecondaryLease(lease runtimeServerResourceLease, cohortID, path string) error {
	if lease.SchemaVersion != runtimeResourceLeaseSchemaVersion || lease.CohortID != cohortID ||
		lease.Role != multilsp.ResourceCohortRoleSecondary || lease.OwnerPID <= 1 ||
		lease.OwnerStartIdentity == "" || lease.CreatedAtUnixNano <= 0 {
		return fmt.Errorf("repository cohort secondary lease is invalid: %s", path)
	}
	return nil
}

// runtimeServerQuarantineResourceLease 隔离损坏 secondary，保留本次 fail-fast 证据供后续有界回收。
func runtimeServerQuarantineResourceLease(path string, now time.Time) error {
	quarantine := path + "." + strconv.FormatInt(now.UnixNano(), 10) + ".bad"
	if err := os.Rename(path, quarantine); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("quarantine repository cohort secondary lease: %w", err)
	}
	return nil
}

// runtimeServerRemoveResourceLease 幂等删除已确认失去 PID 启动身份的 repository lease。
func runtimeServerRemoveResourceLease(path, reason string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s repository cohort lease: %w", reason, err)
	}
	return nil
}

// runtimeServerCleanupLeaseQuarantines 删除超龄 quarantine，并把新鲜证据限制在固定数量内。
func runtimeServerCleanupLeaseQuarantines(quarantines []runtimeServerLeaseQuarantine, now time.Time) error {
	slices.SortFunc(quarantines, func(left, right runtimeServerLeaseQuarantine) int {
		return left.modTime.Compare(right.modTime)
	})
	retained := quarantines[:0]
	var cleanupErr error
	for _, quarantine := range quarantines {
		if !quarantine.modTime.After(now) && now.Sub(quarantine.modTime) > runtimeLeaseQuarantineMaxAge {
			cleanupErr = errors.Join(cleanupErr, runtimeServerRemoveResourceLease(quarantine.path, "expired quarantine"))
			continue
		}
		retained = append(retained, quarantine)
	}
	excess := len(retained) - runtimeLeaseQuarantineMaxCount
	for index := range excess {
		cleanupErr = errors.Join(cleanupErr, runtimeServerRemoveResourceLease(retained[index].path, "excess quarantine"))
	}
	return cleanupErr
}

// runtimeServerElectResourceLease 在跨进程锁内原子发布 primary，并在活跃 primary 存在时创建 secondary。
func runtimeServerElectResourceLease(
	cohortDir, primaryPath string,
	lease runtimeServerResourceLease,
	activeSecondaries int,
) (string, string, error) {
	for range 3 {
		if err := runtimeServerCreateResourceLease(primaryPath, lease); err == nil {
			return lease.Role, primaryPath, nil
		} else if !errors.Is(err, os.ErrExist) {
			return "", "", err
		}
		active, err := runtimeServerResourceLeaseActive(primaryPath, lease.CohortID)
		if err != nil {
			return "", "", err
		}
		if active {
			return runtimeServerCreateSecondaryLease(cohortDir, lease, activeSecondaries)
		}
		if err := os.Remove(primaryPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", "", fmt.Errorf("remove stale repository cohort primary lease: %w", err)
		}
	}
	return "", "", errors.New("repository cohort primary election did not converge")
}

func runtimeServerCreateSecondaryLease(
	cohortDir string,
	lease runtimeServerResourceLease,
	activeSecondaries int,
) (string, string, error) {
	if activeSecondaries >= runtimeSecondaryLeaseMaxCount {
		return "", "", fmt.Errorf("repository cohort has %d active secondary leases; limit is %d", activeSecondaries, runtimeSecondaryLeaseMaxCount)
	}
	lease.Role = multilsp.ResourceCohortRoleSecondary
	token := make([]byte, 12)
	if _, err := rand.Read(token); err != nil {
		return "", "", fmt.Errorf("generate repository cohort secondary lease token: %w", err)
	}
	path := filepath.Join(cohortDir, "secondary-"+hex.EncodeToString(token)+".json")
	if err := runtimeServerCreateResourceLease(path, lease); err != nil {
		return "", "", err
	}
	return lease.Role, path, nil
}

// runtimeServerCreateResourceLease 完整写入同目录临时文件，再以硬链接无覆盖地原子发布 lease。
func runtimeServerCreateResourceLease(path string, lease runtimeServerResourceLease) error {
	payload, err := json.Marshal(lease)
	if err != nil {
		return fmt.Errorf("encode repository cohort lease: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return fmt.Errorf("create repository cohort lease temp file: %w", err)
	}
	tempPath := file.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := securefs.RestrictPrivateOwnerOnly(tempPath, 0o600); err != nil {
		return errors.Join(fmt.Errorf("secure repository cohort lease temp file: %w", err), file.Close())
	}
	if _, err := file.Write(payload); err != nil {
		return errors.Join(
			fmt.Errorf("write repository cohort lease: %w", err),
			file.Close(),
		)
	}
	if err := file.Sync(); err != nil {
		return errors.Join(
			fmt.Errorf("sync repository cohort lease: %w", err),
			file.Close(),
		)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close repository cohort lease: %w", err)
	}
	if err := os.Link(tempPath, path); err != nil {
		return fmt.Errorf("publish repository cohort lease: %w", err)
	}
	return nil
}

// runtimeServerResourceLeaseActive 以 PID 存活和启动身份判断 primary 是否仍属于原 owner。
func runtimeServerResourceLeaseActive(path, cohortID string) (bool, error) {
	lease, err := runtimeServerReadResourceLease(path)
	if err != nil {
		return false, err
	}
	if err := runtimeServerValidatePrimaryLease(lease, cohortID, path); err != nil {
		return false, err
	}
	alive, err := hiddenexec.ProcessAlive(lease.OwnerPID)
	if err != nil {
		return false, fmt.Errorf("check repository cohort primary process: %w", err)
	}
	if !alive {
		return false, nil
	}
	start, err := hiddenexec.ProcessStartIdentity(lease.OwnerPID)
	if err != nil {
		return false, fmt.Errorf("verify repository cohort primary start identity: %w", err)
	}
	return start == lease.OwnerStartIdentity, nil
}

// runtimeServerValidatePrimaryLease 校验 primary lease 的完整 schema 和 cohort 绑定。
func runtimeServerValidatePrimaryLease(lease runtimeServerResourceLease, cohortID, path string) error {
	if lease.SchemaVersion != runtimeResourceLeaseSchemaVersion || lease.CohortID != cohortID ||
		lease.Role != multilsp.ResourceCohortRolePrimary || lease.OwnerPID <= 1 ||
		lease.OwnerStartIdentity == "" || lease.CreatedAtUnixNano <= 0 {
		return fmt.Errorf("repository cohort primary lease is invalid: %s", path)
	}
	return nil
}

// runtimeServerReadResourceLease 严格读取私有、无未知字段和尾随内容的 lease。
func runtimeServerReadResourceLease(path string) (runtimeServerResourceLease, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return runtimeServerResourceLease{}, fmt.Errorf("inspect repository cohort lease: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > 4096 {
		return runtimeServerResourceLease{}, fmt.Errorf("repository cohort lease is insecure: %s", path)
	}
	if err := securefs.CheckPrivateOwnerOnly(path, info); err != nil {
		return runtimeServerResourceLease{}, fmt.Errorf("repository cohort lease is insecure: %s: %w", path, err)
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return runtimeServerResourceLease{}, fmt.Errorf("read repository cohort lease: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var lease runtimeServerResourceLease
	if err := decoder.Decode(&lease); err != nil {
		return runtimeServerResourceLease{}, fmt.Errorf("decode repository cohort lease: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return runtimeServerResourceLease{}, errors.New("repository cohort lease has trailing payload")
	}
	return lease, nil
}

// runtimeServerResourceCohortDir 把所有非 gopls 服务归入一个 15GiB 总 RSS 池。
// gopls 仍按二进制和最终 remote 参数隔离，使每个兼容 daemon cohort 各自使用 4GiB 回收高水位。
func runtimeServerResourceCohortDir(command multilsp.ServerCommand, binary string) (string, error) {
	root, err := runtimeServerCacheRoot()
	if err != nil {
		return "", err
	}
	if runtimeServerIsGopls(command, binary) {
		cohortID := runtimeServerGoplsRemoteID(command.Args)
		if cohortID != "" {
			return filepath.Join(root, "resource-cohorts", "gopls", cohortID), nil
		}
		standaloneDir, err := runtimeServerCacheDir(command, binary)
		if err != nil {
			return "", err
		}
		return filepath.Join(root, "resource-cohorts", "gopls-standalone", filepath.Base(standaloneDir)), nil
	}
	return filepath.Join(root, "resource-cohorts", "non-gopls"), nil
}

func runtimeServerIsGopls(command multilsp.ServerCommand, binary string) bool {
	for _, candidate := range []string{binary, command.Executable} {
		base := filepath.Base(strings.TrimSpace(candidate))
		base = strings.TrimSuffix(base, filepath.Ext(base))
		if strings.EqualFold(base, "gopls") {
			return true
		}
	}
	return false
}

// runtimeServerCacheDir 根据语言服务器真实二进制与启动参数派生兼容 cohort 目录。
func runtimeServerCacheDir(command multilsp.ServerCommand, binary string) (string, error) {
	root, err := runtimeServerCacheRoot()
	if err != nil {
		return "", err
	}
	resolvedBinary, binaryDigest, err := runtimeServerBinaryIdentity(binary, nil)
	if err != nil {
		return "", err
	}
	cacheName := runtimeServerCacheName(filepath.Base(resolvedBinary))
	identity := strings.Join([]string{
		binaryDigest,
		strings.Join(command.Args, "\x00"),
	}, "\x00")
	sum := sha256.Sum256([]byte(identity))
	cohort := hex.EncodeToString(sum[:8])
	return filepath.Join(root, cacheName, cohort), nil
}

// runtimeServerBinaryIdentity 解析真实二进制并计算内容指纹，避免路径或时间戳伪共享。
func runtimeServerBinaryIdentity(binary string, env []string) (string, string, error) {
	pathEnv := runtimeServerEnvValue(env, "PATH")
	resolvedBinary, err := runtimeServerLookPath(
		strings.TrimSpace(binary),
		pathEnv,
		runtimeServerEnvValue(env, "PATHEXT"),
	)
	if err != nil {
		return "", "", fmt.Errorf("resolve language-server binary for shared cache: %w", err)
	}
	resolvedBinary, err = filepath.Abs(resolvedBinary)
	if err != nil {
		return "", "", fmt.Errorf("resolve absolute language-server binary path: %w", err)
	}
	resolvedBinary, err = filepath.EvalSymlinks(resolvedBinary)
	if err != nil {
		return "", "", fmt.Errorf("resolve real language-server binary path: %w", err)
	}
	info, err := os.Stat(resolvedBinary)
	if err != nil {
		return "", "", fmt.Errorf("stat language-server binary for shared cache: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("language-server binary for shared cache is not a regular file: %s", resolvedBinary)
	}
	binaryDigest, err := runtimeServerBinaryDigest(resolvedBinary, info)
	if err != nil {
		return "", "", err
	}
	return resolvedBinary, binaryDigest, nil
}

// runtimeServerBinaryDigest 缓存真实二进制内容指纹；是否同时绑定实路径由上层 cohort 契约决定。
func runtimeServerBinaryDigest(path string, info os.FileInfo) (string, error) {
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("language-server binary is not a regular file: %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open language-server binary for shared cache: %w", err)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		_ = file.Close()
		return "", fmt.Errorf("hash language-server binary for shared cache: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close language-server binary after hashing: %w", err)
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	return digest, nil
}
