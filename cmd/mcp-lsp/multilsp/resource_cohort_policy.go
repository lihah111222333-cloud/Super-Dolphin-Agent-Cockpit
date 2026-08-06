package multilsp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
)

// ensureResourceCohortDirectory 创建或校验仅当前用户可访问的真实目录。
func ensureResourceCohortDirectory(path string, create bool) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) && create {
		if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("path is not a real directory: %s", path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("directory permissions are too broad: %s mode=%#o", path, info.Mode().Perm())
	}
	return nil
}

// resourceProcessPolicyForClient 读取创建期确定的非 gopls 主次资源策略。
// gopls 已迁移到 root cohort controller，禁止重新接入旧 RSS cohort。
func resourceProcessPolicyForClient(current Client, languageID string) (resourceProcessPolicy, error) {
	if isGoResourceCohortLanguage(languageID) {
		return resourceProcessPolicy{}, errors.New("gopls resource cohort policy is retired; use the root cohort controller")
	}
	return nonGoplsResourceProcessPolicy(current)
}

// nonGoplsResourceProcessPolicy 严格校验创建期注入的 repo cohort、角色、租约与进程上限。
func nonGoplsResourceProcessPolicy(current Client) (resourceProcessPolicy, error) {
	env, ok := resourceClientEnvironment(current)
	if !ok {
		return resourceProcessPolicy{}, errors.New("non-gopls resource policy requires a concrete client environment")
	}
	policy, err := nonGoplsResourcePolicyFromEnvironment(env)
	if err != nil {
		return resourceProcessPolicy{}, err
	}
	leasePath := resourceEnvironmentValue(env, ResourceCohortLeaseEnv)
	if err := validateResourceCohortLeasePath(leasePath, policy.repositoryCohortID); err != nil {
		return resourceProcessPolicy{}, err
	}
	return policy, nil
}

// nonGoplsResourcePolicyFromEnvironment 从冻结环境构造唯一预算快照，不读取父进程配置。
func nonGoplsResourcePolicyFromEnvironment(env []string) (resourceProcessPolicy, error) {
	if _, configured := resourceEnvironmentLookup(env, DeprecatedResourceCohortHardLimitMBEnv); configured {
		return resourceProcessPolicy{}, fmt.Errorf(
			"%s is no longer supported; use %s",
			DeprecatedResourceCohortHardLimitMBEnv,
			ResourceCohortHardLimitMBEnv,
		)
	}
	limit, err := resourceProcessRSSLimitBytes(env)
	if err != nil {
		return resourceProcessPolicy{}, err
	}
	hardLimit, err := resourceCohortHardLimitBytesFromEnvironment(env)
	if err != nil {
		return resourceProcessPolicy{}, err
	}
	policy := resourceProcessPolicy{
		repositoryCohortID:   resourceEnvironmentValue(env, ResourceRepositoryCohortIDEnv),
		role:                 resourceEnvironmentValue(env, ResourceCohortRoleEnv),
		rssLimitBytes:        limit,
		cohortHardLimitBytes: hardLimit,
	}
	if err := policy.validateNonGopls(); err != nil {
		return resourceProcessPolicy{}, err
	}
	return policy, nil
}

// validateNonGopls 校验冻结的 repo 身份、角色与两层 RSS 预算关系。
func (policy resourceProcessPolicy) validateNonGopls() error {
	if !strings.HasPrefix(policy.repositoryCohortID, "repo-") {
		return fmt.Errorf("%s is invalid: %q", ResourceRepositoryCohortIDEnv, policy.repositoryCohortID)
	}
	if policy.role != ResourceCohortRolePrimary && policy.role != ResourceCohortRoleSecondary {
		return fmt.Errorf("%s is invalid: %q", ResourceCohortRoleEnv, policy.role)
	}
	if policy.role == ResourceCohortRoleSecondary && policy.rssLimitBytes >= 2*1024*1024*1024 {
		return errors.New("secondary LSP RSS limit must stay below 2 GiB")
	}
	if policy.rssLimitBytes > policy.cohortHardLimitBytes {
		return errors.New("LSP process RSS limit exceeds global cohort hard limit")
	}
	return nil
}

// resourceClientEnvironment 返回创建真实 transport 时冻结的子进程环境。
func resourceClientEnvironment(current Client) ([]string, bool) {
	typed, ok := concreteClient(current)
	if !ok || typed.transport == nil || typed.transport.cmd == nil {
		return nil, false
	}
	return typed.transport.cmd.Env, true
}

// resourceEnvironmentValue 按 exec.Cmd 的 last-wins 规则读取环境变量。
func resourceEnvironmentValue(env []string, key string) string {
	value, _ := resourceEnvironmentLookup(env, key)
	return value
}

// resourceEnvironmentLookup 按 exec.Cmd 的 last-wins 规则读取值并保留显式空配置的存在性。
func resourceEnvironmentLookup(env []string, key string) (string, bool) {
	value := ""
	configured := false
	for _, entry := range env {
		entryKey, candidate, ok := strings.Cut(entry, "=")
		if ok && entryKey == key {
			value = strings.TrimSpace(candidate)
			configured = true
		}
	}
	return value, configured
}

// resourceProcessRSSLimitBytes 严格解析正整数 MiB 进程树预算。
func resourceProcessRSSLimitBytes(env []string) (uint64, error) {
	return resourceRequiredMiBLimitBytes(env, ResourceProcessRSSLimitMBEnv)
}

// resourceCohortHardLimitBytesFromEnvironment 只读取创建期冻结的 canonical 非 gopls cohort 预算。
func resourceCohortHardLimitBytesFromEnvironment(env []string) (uint64, error) {
	return resourceRequiredMiBLimitBytes(env, ResourceCohortHardLimitMBEnv)
}

func resourceRequiredMiBLimitBytes(env []string, key string) (uint64, error) {
	raw := resourceEnvironmentValue(env, key)
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 || value > ^uint64(0)/(1024*1024) {
		return 0, fmt.Errorf("%s must be a positive integer MiB value: %q", key, raw)
	}
	return value * 1024 * 1024, nil
}

// validateResourceCohortLeasePath 校验租约路径属于指定 cohort 且不是符号链接或宽权限文件。
func validateResourceCohortLeasePath(path, cohortID string) error {
	if cohortID == "" || !filepath.IsAbs(path) || filepath.Base(filepath.Dir(path)) != cohortID {
		return fmt.Errorf("%s does not belong to cohort %q", ResourceCohortLeaseEnv, cohortID)
	}
	if err := ensureResourceCohortDirectory(filepath.Dir(path), false); err != nil {
		return fmt.Errorf("secure repository cohort lease directory: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect repository cohort lease: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("repository cohort lease is insecure: %s", path)
	}
	return nil
}

// ReleaseResourceCohortLease 仅删除由当前 mcp-lsp PID 和启动身份创建的主次租约。
// gopls 不使用主次租约；环境未携带租约时该函数是空操作。
func ReleaseResourceCohortLease(env []string) error {
	leasePath := resourceEnvironmentValue(env, ResourceCohortLeaseEnv)
	if leasePath == "" {
		return nil
	}
	cohortID := resourceEnvironmentValue(env, ResourceRepositoryCohortIDEnv)
	if err := validateResourceCohortLeasePath(leasePath, cohortID); err != nil {
		return err
	}
	lease, err := readResourceCohortLease(leasePath)
	if err != nil {
		return err
	}
	if err := validateOwnedResourceCohortLease(lease, cohortID, leasePath); err != nil {
		return err
	}
	if err := os.Remove(leasePath); err != nil {
		return fmt.Errorf("release repository cohort lease: %w", err)
	}
	return nil
}

// validateOwnedResourceCohortLease 校验租约 schema、角色以及当前 owner 的 PID 启动身份。
func validateOwnedResourceCohortLease(lease resourceCohortLease, cohortID, path string) error {
	if lease.SchemaVersion != 1 || lease.CohortID != cohortID ||
		(lease.Role != ResourceCohortRolePrimary && lease.Role != ResourceCohortRoleSecondary) {
		return fmt.Errorf("repository cohort lease metadata does not match %s", path)
	}
	if lease.OwnerPID != os.Getpid() {
		return fmt.Errorf("repository cohort lease belongs to owner PID %d, current PID is %d", lease.OwnerPID, os.Getpid())
	}
	ownerStart, err := hiddenexec.ProcessStartIdentity(os.Getpid())
	if err != nil {
		return fmt.Errorf("read repository cohort lease owner start identity: %w", err)
	}
	if lease.OwnerStartIdentity != ownerStart {
		return errors.New("repository cohort lease owner start identity does not match current process")
	}
	return nil
}

// readResourceCohortLease 严格读取小型、私有、无额外字段的 repository lease JSON。
func readResourceCohortLease(path string) (resourceCohortLease, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return resourceCohortLease{}, fmt.Errorf("inspect repository cohort lease: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return resourceCohortLease{}, fmt.Errorf("repository cohort lease is insecure: %s", path)
	}
	if info.Size() <= 0 || info.Size() > 64*1024 {
		return resourceCohortLease{}, fmt.Errorf("repository cohort lease has invalid size: %d", info.Size())
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return resourceCohortLease{}, fmt.Errorf("read repository cohort lease: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var lease resourceCohortLease
	if err := decoder.Decode(&lease); err != nil {
		return resourceCohortLease{}, fmt.Errorf("decode repository cohort lease: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return resourceCohortLease{}, errors.New("decode repository cohort lease: trailing payload")
	}
	return lease, nil
}

// quarantineResourceCohortMember 把坏报告改为非 JSON 后缀，保留证据并让下一轮恢复。
func quarantineResourceCohortMember(path string, now time.Time) error {
	quarantinePath := path + ".bad"
	if _, err := os.Lstat(quarantinePath); err == nil {
		quarantinePath = fmt.Sprintf("%s.%d.bad", path, now.UnixNano())
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect LSP resource cohort quarantine: %w", err)
	}
	err := os.Rename(path, quarantinePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("quarantine LSP resource cohort member: %w", err)
	}
	return nil
}

// cleanupResourceCohortQuarantines 删除过期隔离文件并把保留数量限制在固定上限。
func cleanupResourceCohortQuarantines(dir string, now time.Time) error {
	quarantines, err := loadResourceCohortQuarantines(dir)
	return errors.Join(err, removeExcessResourceCohortQuarantines(quarantines, now))
}

// loadResourceCohortQuarantines 读取现存坏报告证据及其修改时间。
func loadResourceCohortQuarantines(dir string) ([]resourceCohortQuarantine, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read LSP resource cohort quarantines: %w", err)
	}
	quarantines := make([]resourceCohortQuarantine, 0)
	var loadErr error
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".bad") {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			loadErr = errors.Join(loadErr, fmt.Errorf("stat LSP resource cohort quarantine %s: %w", entry.Name(), infoErr))
			continue
		}
		quarantines = append(quarantines, resourceCohortQuarantine{
			path:    filepath.Join(dir, entry.Name()),
			modTime: info.ModTime(),
		})
	}
	return quarantines, loadErr
}

// removeExcessResourceCohortQuarantines 删除超龄证据并把新鲜证据限制在固定数量内。
func removeExcessResourceCohortQuarantines(quarantines []resourceCohortQuarantine, now time.Time) error {
	slices.SortFunc(quarantines, func(left, right resourceCohortQuarantine) int {
		if order := left.modTime.Compare(right.modTime); order != 0 {
			return order
		}
		return strings.Compare(left.path, right.path)
	})
	keepFrom := max(0, len(quarantines)-resourceCohortQuarantineMaxCount)
	var cleanupErr error
	for index, quarantine := range quarantines {
		expired := !quarantine.modTime.After(now) && now.Sub(quarantine.modTime) > resourceCohortQuarantineMaxAge
		if index >= keepFrom && !expired {
			continue
		}
		if removeErr := os.Remove(quarantine.path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove LSP resource cohort quarantine %s: %w", filepath.Base(quarantine.path), removeErr))
		}
	}
	return cleanupErr
}

func resourceCohortMemberProcessesAlive(member resourceCohortMember) (bool, error) {
	ownerAlive, err := hiddenexec.ProcessAlive(member.OwnerPID)
	if err != nil {
		return false, fmt.Errorf("check LSP resource owner PID %d: %w", member.OwnerPID, err)
	}
	clientAlive, err := hiddenexec.ProcessAlive(member.ClientPID)
	if err != nil {
		return false, fmt.Errorf("check LSP resource client PID %d: %w", member.ClientPID, err)
	}
	return ownerAlive && clientAlive, nil
}

// refreshStaleResourceCohortMember 对超过新鲜窗口的报告验证 PID 身份并重新估算 RSS。
// 探测结果只在本轮聚合中使用，不刷新 owner 的健康发布时间。
func refreshStaleResourceCohortMember(member *resourceCohortMember) (bool, error) {
	live, err := resourceCohortMemberProcessesAlive(*member)
	if err != nil {
		return false, err
	}
	if !live {
		return false, nil
	}
	matches, err := resourceCohortMemberIdentityMatches(*member)
	if err != nil {
		return false, err
	}
	if !matches {
		return false, nil
	}
	rssBytes, err := refreshStaleResourceCohortRSS(*member)
	if err != nil {
		return false, err
	}
	member.RSSBytes = rssBytes
	member.Stale = true
	if member.ActiveLeases == 0 {
		member.ActiveLeases = 1
	}
	return true, nil
}

// resourceCohortMemberIdentityMatches 校验 owner 和 client 均未发生 PID 复用。
func resourceCohortMemberIdentityMatches(member resourceCohortMember) (bool, error) {
	ownerStart, err := hiddenexec.ProcessStartIdentity(member.OwnerPID)
	if err != nil {
		return false, fmt.Errorf("verify LSP resource owner start identity: %w", err)
	}
	clientStart, err := hiddenexec.ProcessStartIdentity(member.ClientPID)
	if err != nil {
		return false, fmt.Errorf("verify LSP resource client start identity: %w", err)
	}
	return ownerStart == member.OwnerStartIdentity && clientStart == member.ClientStartIdentity, nil
}

// refreshStaleResourceCohortRSS 在 POSIX 上重采样进程树，Windows 上按创建期上限保守计账。
func refreshStaleResourceCohortRSS(member resourceCohortMember) (uint64, error) {
	switch runtime.GOOS {
	case "darwin", "linux":
		rssBytes, err := hiddenexec.ProcessTreeRSSBytes(member.ClientPID)
		if err != nil {
			return 0, fmt.Errorf("refresh stale LSP process-tree RSS: %w", err)
		}
		if rssBytes == 0 {
			return 0, errors.New("refresh stale LSP process-tree RSS: zero-byte sample")
		}
		return rssBytes, nil
	case "windows":
		// 远端 owner 的 Job Object 句柄不可重建；至少按创建期进程上限保守计账。
		return max(member.RSSBytes, member.ProcessRSSLimitBytes), nil
	default:
		return 0, fmt.Errorf("refresh stale LSP process-tree RSS: unsupported platform %s", runtime.GOOS)
	}
}
