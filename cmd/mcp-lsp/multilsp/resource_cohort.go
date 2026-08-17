package multilsp

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

const (
	// ResourceCohortDirEnv 指向同版本语言服务器跨 mcp-lsp/worktree 共享的资源总账目录。
	ResourceCohortDirEnv = "MCP_LSP_RESOURCE_COHORT_DIR"
	// ResourceRepositoryCohortIDEnv 标识稳定 repo/language/server cohort。
	ResourceRepositoryCohortIDEnv = "MCP_LSP_REPOSITORY_COHORT_ID"
	// ResourceCohortRoleEnv 标识当前语言服务是唯一 primary 还是受限 secondary。
	ResourceCohortRoleEnv = "MCP_LSP_REPOSITORY_COHORT_ROLE"
	// ResourceCohortLeaseEnv 指向当前 owner 独占的主次租约文件。
	ResourceCohortLeaseEnv = "MCP_LSP_REPOSITORY_COHORT_LEASE"
	// ResourceProcessRSSLimitMBEnv 是创建时确定、recycler 强制执行的进程树 RSS 上限。
	ResourceProcessRSSLimitMBEnv = "MCP_LSP_PROCESS_RSS_LIMIT_MB"
	// ResourceCohortHardLimitMBEnv 是 repo cohort 唯一可写的 RSS 总预算。
	ResourceCohortHardLimitMBEnv = "AGENT_LSP_COHORT_RSS_LIMIT_MB"
	// DeprecatedResourceCohortHardLimitMBEnv 仅作为旧配置的 fail-fast 迁移墓碑，不参与预算解析。
	DeprecatedResourceCohortHardLimitMBEnv = "AGENT_LSP_RESOURCE_COHORT_HARD_LIMIT_MB"
	ResourceCohortRolePrimary              = "primary"
	ResourceCohortRoleSecondary            = "secondary"

	resourceCohortSchemaVersion      = 2
	resourceCohortMembersDir         = "members"
	defaultCohortHardLimitBytes      = 15 * 1024 * 1024 * 1024
	defaultGoWindowsRSSLimitBytes    = 4 * 1024 * 1024 * 1024
	resourceCohortSoftPercent        = 80
	resourceCohortReportMaxAge       = 2 * time.Minute
	resourceCohortClockSkewTolerance = 5 * time.Second
	resourceCohortQuarantineMaxAge   = 24 * time.Hour
	resourceCohortQuarantineMaxCount = 32
)

var errInvalidResourceCohortReport = errors.New("invalid LSP resource cohort report")

type resourceCohortDecision struct {
	Enabled          bool
	AggregateRSS     uint64
	HardLimit        uint64
	SoftLimit        uint64
	EvictSelf        bool
	StaleMembers     int
	UnhealthyMembers int
}

type resourceCohortMember struct {
	SchemaVersion        int    `json:"schema_version"`
	OwnerPID             int    `json:"owner_pid"`
	OwnerStartIdentity   string `json:"owner_start_identity"`
	ClientPID            int    `json:"client_pid"`
	ClientStartIdentity  string `json:"client_start_identity"`
	WorkspaceHash        string `json:"workspace_hash"`
	LanguageID           string `json:"language_id"`
	RepositoryCohortID   string `json:"repository_cohort_id"`
	Role                 string `json:"role"`
	RSSBytes             uint64 `json:"rss_bytes"`
	ProcessRSSLimitBytes uint64 `json:"process_rss_limit_bytes"`
	CohortHardLimitBytes uint64 `json:"cohort_hard_limit_bytes"`
	ActiveLeases         int    `json:"active_leases"`
	LastActivityUnixNano int64  `json:"last_activity_unix_nano"`
	UpdatedAtUnixNano    int64  `json:"updated_at_unix_nano"`
	Stale                bool   `json:"-"`
}

type resourceCohortVersionedMember struct {
	path   string
	member resourceCohortMember
}

// resourceCohortQuarantine 描述一份待按年龄和数量回收的坏报告证据。
type resourceCohortQuarantine struct {
	path    string
	modTime time.Time
}

type resourceCohortLoadResult struct {
	Members          []resourceCohortMember
	ConservativeRSS  uint64
	StaleMembers     int
	UnhealthyMembers int
}

type resourceProcessPolicy struct {
	repositoryCohortID   string
	role                 string
	rssLimitBytes        uint64
	cohortHardLimitBytes uint64
}

type resourceCohortLease struct {
	SchemaVersion      int    `json:"schema_version"`
	CohortID           string `json:"cohort_id"`
	Role               string `json:"role"`
	OwnerPID           int    `json:"owner_pid"`
	OwnerStartIdentity string `json:"owner_start_identity"`
	CreatedAtUnixNano  int64  `json:"created_at_unix_nano"`
}

// evaluateResourceCohort 使用创建期冻结的 policy 发布实时进程树样本，并按同 cohort 聚合 RSS 选择 owner-only 回收目标。
// 总账只决定当前 owner 是否应关闭自己的 client，从不跨进程发送 kill。
func evaluateResourceCohort(
	current Client,
	workspace workspaceClient,
	policy resourceProcessPolicy,
	rssBytes uint64,
	clientPID int,
	activeLeases int,
	now time.Time,
) (resourceCohortDecision, error) {
	if goplsRootCohortOwnsResources(workspace.languageID) {
		return resourceCohortDecision{}, errors.New("gopls resource cohort evaluation is retired; use the root cohort controller")
	}
	hardLimit := policy.cohortHardLimitBytes
	softLimit := hardLimit * resourceCohortSoftPercent / 100
	cohortDir, enabled, err := resourceCohortDir(current)
	if err != nil {
		return failedResourceCohortDecision(hardLimit, softLimit, rssBytes, activeLeases), err
	}
	if !enabled {
		return resourceCohortDecision{}, nil
	}
	member, err := newResourceCohortMember(workspace, policy, rssBytes, clientPID, activeLeases, now)
	if err != nil {
		return failedResourceCohortDecision(hardLimit, softLimit, rssBytes, activeLeases), err
	}
	membersDir := filepath.Join(cohortDir, resourceCohortMembersDir)
	if err := ensureResourceCohortDirectory(membersDir, true); err != nil {
		return failedResourceCohortDecision(hardLimit, softLimit, rssBytes, activeLeases),
			fmt.Errorf("secure LSP resource cohort members directory: %w", err)
	}
	if err := writeResourceCohortMember(membersDir, member); err != nil {
		return failedResourceCohortDecision(hardLimit, softLimit, rssBytes, activeLeases), err
	}
	loaded, loadErr := loadResourceCohortMembers(membersDir, now, hardLimit)
	if loadErr != nil && loaded.UnhealthyMembers == 0 {
		return resourceCohortDecision{
			Enabled:      true,
			AggregateRSS: addResourceCohortRSS(hardLimit, rssBytes),
			HardLimit:    hardLimit,
			SoftLimit:    softLimit,
			EvictSelf:    activeLeases == 0,
		}, loadErr
	}
	aggregate := loaded.ConservativeRSS
	for _, candidate := range loaded.Members {
		aggregate = addResourceCohortRSS(aggregate, candidate.RSSBytes)
	}
	victims := selectResourceCohortVictims(loaded.Members, aggregate, hardLimit, softLimit)
	return resourceCohortDecision{
		Enabled:          true,
		AggregateRSS:     aggregate,
		HardLimit:        hardLimit,
		SoftLimit:        softLimit,
		EvictSelf:        slices.Contains(victims, resourceCohortMemberKey(member)),
		StaleMembers:     loaded.StaleMembers,
		UnhealthyMembers: loaded.UnhealthyMembers,
	}, loadErr
}

func failedResourceCohortDecision(hardLimit, softLimit, rssBytes uint64, activeLeases int) resourceCohortDecision {
	return resourceCohortDecision{
		Enabled:          true,
		AggregateRSS:     addResourceCohortRSS(hardLimit, rssBytes),
		HardLimit:        hardLimit,
		SoftLimit:        softLimit,
		EvictSelf:        activeLeases == 0,
		UnhealthyMembers: 1,
	}
}

// newResourceCohortMember 为当前 owner/client 生成带 PID 启动身份和匿名 workspace 的严格报告。
func newResourceCohortMember(
	workspace workspaceClient,
	policy resourceProcessPolicy,
	rssBytes uint64,
	clientPID int,
	activeLeases int,
	now time.Time,
) (resourceCohortMember, error) {
	if clientPID <= 1 {
		return resourceCohortMember{}, errors.New("resource cohort client PID must be greater than 1")
	}
	ownerPID := os.Getpid()
	ownerStart, err := hiddenexec.ProcessStartIdentity(ownerPID)
	if err != nil {
		return resourceCohortMember{}, fmt.Errorf("read LSP resource owner start identity: %w", err)
	}
	clientStart, err := hiddenexec.ProcessStartIdentity(clientPID)
	if err != nil {
		return resourceCohortMember{}, fmt.Errorf("read LSP client start identity: %w", err)
	}
	workspaceSum := sha256.Sum256([]byte(workspace.key))
	return resourceCohortMember{
		SchemaVersion:        resourceCohortSchemaVersion,
		OwnerPID:             ownerPID,
		OwnerStartIdentity:   ownerStart,
		ClientPID:            clientPID,
		ClientStartIdentity:  clientStart,
		WorkspaceHash:        hex.EncodeToString(workspaceSum[:8]),
		LanguageID:           normalizeLanguageID(workspace.languageID),
		RepositoryCohortID:   policy.repositoryCohortID,
		Role:                 policy.role,
		RSSBytes:             rssBytes,
		ProcessRSSLimitBytes: policy.rssLimitBytes,
		CohortHardLimitBytes: policy.cohortHardLimitBytes,
		ActiveLeases:         activeLeases,
		LastActivityUnixNano: workspace.lastActivity.UnixNano(),
		UpdatedAtUnixNano:    now.UnixNano(),
	}, nil
}

// resourceCohortDir 从已启动 client 的最终环境读取绝对 cohort 目录。
func resourceCohortDir(current Client) (string, bool, error) {
	typed, ok := concreteClient(current)
	if !ok || typed.transport == nil || typed.transport.cmd == nil {
		return "", false, nil
	}
	value := ""
	for _, entry := range typed.transport.cmd.Env {
		key, candidate, found := strings.Cut(entry, "=")
		if found && key == ResourceCohortDirEnv {
			value = strings.TrimSpace(candidate)
		}
	}
	if value == "" {
		return "", false, nil
	}
	if !filepath.IsAbs(value) {
		return "", true, errors.New(ResourceCohortDirEnv + " must be an absolute path")
	}
	value = filepath.Clean(value)
	if err := ensureResourceCohortDirectory(value, false); err != nil {
		return "", true, fmt.Errorf("secure LSP resource cohort directory: %w", err)
	}
	return value, true, nil
}

// writeResourceCohortMember 通过同目录临时文件和 rename 发布不可见半包的成员报告。
func writeResourceCohortMember(dir string, member resourceCohortMember) error {
	return writeResourceCohortMemberAtPath(dir, "", member)
}

// writeResourceCohortMemberAtPath 原子创建新报告，或原位替换已存在的陈旧报告。
func writeResourceCohortMemberAtPath(dir, destination string, member resourceCohortMember) error {
	payload, err := json.Marshal(member)
	if err != nil {
		return fmt.Errorf("encode LSP resource cohort member: %w", err)
	}
	temp, err := os.CreateTemp(dir, fmt.Sprintf(".member-%d-%d-*.tmp", member.OwnerPID, member.ClientPID))
	if err != nil {
		return fmt.Errorf("create LSP resource cohort member temp file: %w", err)
	}
	tempPath := temp.Name()
	defer func() {
		_ = os.Remove(tempPath)
	}()
	if err := securefs.RestrictPrivateOwnerOnly(tempPath, 0o600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("secure LSP resource cohort member temp file: %w", err)
	}
	if _, err := temp.Write(payload); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write LSP resource cohort member: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync LSP resource cohort member: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close LSP resource cohort member: %w", err)
	}
	if destination == "" {
		base := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(tempPath), "."), ".tmp") + ".json"
		destination = filepath.Join(dir, base)
	}
	if err := os.Rename(tempPath, destination); err != nil {
		return fmt.Errorf("publish LSP resource cohort member: %w", err)
	}
	return nil
}

// removeOwnedResourceCohortMember 只删除当前 mcp-lsp owner 与指定 client 匹配的报告。
func removeOwnedResourceCohortMember(current Client) error {
	typed, ok := concreteClient(current)
	if !ok {
		env, _ := resourceClientEnvironment(current)
		return errors.Join(removeOwnedResourceCohortReports(current), ReleaseResourceCohortLease(env))
	}
	typed.resourceCleanupMu.Lock()
	defer typed.resourceCleanupMu.Unlock()
	var reportsErr error
	if !typed.resourceReportsReleased {
		reportsErr = removeOwnedResourceCohortReports(current)
		if reportsErr == nil {
			typed.resourceReportsReleased = true
		}
	}
	var leaseErr error
	if !typed.resourceCohortLeaseReleased {
		env, _ := resourceClientEnvironment(current)
		leaseErr = ReleaseResourceCohortLease(env)
		if leaseErr == nil {
			typed.resourceCohortLeaseReleased = true
		}
	}
	return errors.Join(reportsErr, leaseErr)
}

// removeOwnedResourceCohortReports 仅删除当前 owner/client 前缀对应的总账报告。
func removeOwnedResourceCohortReports(current Client) error {
	cohortDir, enabled, err := resourceCohortDir(current)
	if err != nil || !enabled {
		return err
	}
	typed, ok := concreteClient(current)
	if !ok {
		return nil
	}
	if typed.transport.cmd.Process == nil {
		return nil
	}
	prefix := fmt.Sprintf("member-%d-%d-", os.Getpid(), typed.transport.cmd.Process.Pid)
	entries, err := os.ReadDir(filepath.Join(cohortDir, resourceCohortMembersDir))
	if err != nil {
		return fmt.Errorf("read owned LSP resource cohort reports: %w", err)
	}
	var removeErr error
	for _, entry := range entries {
		if !ownedResourceCohortReport(entry, prefix) {
			continue
		}
		removeErr = errors.Join(removeErr, removeResourceCohortReport(
			filepath.Join(cohortDir, resourceCohortMembersDir, entry.Name()),
		))
	}
	if removeErr != nil {
		return fmt.Errorf("remove owned LSP resource cohort reports: %w", removeErr)
	}
	return nil
}

func ownedResourceCohortReport(entry os.DirEntry, prefix string) bool {
	return !entry.IsDir() &&
		strings.HasPrefix(entry.Name(), prefix) &&
		strings.HasSuffix(entry.Name(), ".json")
}

func removeResourceCohortReport(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// loadResourceCohortMembers 严格读取并去重仍由原进程身份持有的 cohort 报告。
func loadResourceCohortMembers(dir string, now time.Time, hardLimit uint64) (resourceCohortLoadResult, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return resourceCohortLoadResult{}, fmt.Errorf("read LSP resource cohort members: %w", err)
	}
	result := resourceCohortLoadResult{}
	degradedErr := cleanupResourceCohortQuarantines(dir, now)
	latest := make(map[string]resourceCohortVersionedMember)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		key, candidate, keep, err := loadResourceCohortCandidate(
			path,
			entry.Name(),
			now,
			hardLimit,
			latest,
		)
		if err != nil {
			result.UnhealthyMembers++
			result.ConservativeRSS = addResourceCohortRSS(result.ConservativeRSS, hardLimit)
			if shouldQuarantineResourceCohortReport(err) {
				err = errors.Join(err, quarantineResourceCohortMember(path, now))
			}
			degradedErr = errors.Join(degradedErr, fmt.Errorf("load LSP resource cohort member %s: %w", entry.Name(), err))
			continue
		}
		if !keep {
			continue
		}
		if candidate.member.Stale {
			result.StaleMembers++
		}
		latest[key] = candidate
	}
	result.Members = make([]resourceCohortMember, 0, len(latest))
	for _, candidate := range latest {
		result.Members = append(result.Members, candidate.member)
	}
	degradedErr = errors.Join(degradedErr, cleanupResourceCohortQuarantines(dir, now))
	return result, degradedErr
}

// loadResourceCohortCandidate 读取一份报告并与同一进程身份的已见版本做严格去重。
func loadResourceCohortCandidate(
	path, name string,
	now time.Time,
	hardLimit uint64,
	latest map[string]resourceCohortVersionedMember,
) (string, resourceCohortVersionedMember, bool, error) {
	member, keep, err := loadResourceCohortMember(path, name, now)
	if err != nil {
		return "", resourceCohortVersionedMember{}, false, err
	}
	if !keep {
		return "", resourceCohortVersionedMember{}, false, nil
	}
	if member.CohortHardLimitBytes != hardLimit {
		return "", resourceCohortVersionedMember{}, false, invalidResourceCohortReport(fmt.Errorf(
			"LSP resource cohort hard limit mismatch: member=%d local=%d",
			member.CohortHardLimitBytes,
			hardLimit,
		))
	}
	key := resourceCohortMemberKey(member)
	newest, err := newestResourceCohortMember(
		latest[key],
		resourceCohortVersionedMember{path: path, member: member},
	)
	if err != nil {
		return "", resourceCohortVersionedMember{}, false, err
	}
	return key, newest, true, nil
}

// loadResourceCohortMember 校验单份报告的 schema、进程存活与 PID 启动身份。
func loadResourceCohortMember(path, name string, now time.Time) (resourceCohortMember, bool, error) {
	member, err := readResourceCohortMember(path)
	if errors.Is(err, os.ErrNotExist) {
		return resourceCohortMember{}, false, nil
	}
	if err != nil {
		return resourceCohortMember{}, false, err
	}
	if err := validateResourceCohortMember(member); err != nil {
		return resourceCohortMember{}, false, invalidResourceCohortReport(
			fmt.Errorf("validate LSP resource cohort member %s: %w", name, err),
		)
	}
	updatedAt := time.Unix(0, member.UpdatedAtUnixNano)
	if updatedAt.After(now.Add(resourceCohortClockSkewTolerance)) {
		return resourceCohortMember{}, false, invalidResourceCohortReport(fmt.Errorf(
			"validate LSP resource cohort member %s: updated_at is in the future",
			name,
		))
	}
	if !updatedAt.After(now) && now.Sub(updatedAt) <= resourceCohortReportMaxAge {
		return member, true, nil
	}
	matches, err := refreshStaleResourceCohortMember(&member)
	if err != nil {
		return resourceCohortMember{}, false, err
	}
	if !matches {
		return resourceCohortMember{}, false, removeDiscardedResourceCohortReport(path, "reused-PID")
	}
	return member, true, nil
}

func removeDiscardedResourceCohortReport(path, reason string) error {
	if err := removeResourceCohortReport(path); err != nil {
		return fmt.Errorf("remove %s LSP resource cohort member: %w", reason, err)
	}
	return nil
}

func newestResourceCohortMember(
	current, candidate resourceCohortVersionedMember,
) (resourceCohortVersionedMember, error) {
	if current.path == "" {
		return candidate, nil
	}
	if candidate.member.UpdatedAtUnixNano > current.member.UpdatedAtUnixNano {
		if err := removeResourceCohortReport(current.path); err != nil {
			return resourceCohortVersionedMember{}, fmt.Errorf("remove superseded LSP resource cohort report: %w", err)
		}
		return candidate, nil
	}
	if err := removeResourceCohortReport(candidate.path); err != nil {
		return resourceCohortVersionedMember{}, fmt.Errorf("remove duplicate LSP resource cohort report: %w", err)
	}
	return current, nil
}

// readResourceCohortMember 严格读取字段全集完整且无尾随内容的成员报告。
func readResourceCohortMember(path string) (resourceCohortMember, error) {
	if err := validateResourceCohortMemberFile(path); err != nil {
		return resourceCohortMember{}, err
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return resourceCohortMember{}, fmt.Errorf("read LSP resource cohort member: %w", err)
	}
	if err := validateResourceCohortMemberFields(payload); err != nil {
		return resourceCohortMember{}, invalidResourceCohortReport(
			fmt.Errorf("decode LSP resource cohort member %s: %w", filepath.Base(path), err),
		)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var member resourceCohortMember
	if err := decoder.Decode(&member); err != nil {
		return resourceCohortMember{}, invalidResourceCohortReport(
			fmt.Errorf("decode LSP resource cohort member %s: %w", filepath.Base(path), err),
		)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return resourceCohortMember{}, invalidResourceCohortReport(
			fmt.Errorf("decode LSP resource cohort member %s: trailing payload", filepath.Base(path)),
		)
	}
	return member, nil
}

// validateResourceCohortMemberFile 拒绝符号链接、宽权限、非普通文件及异常大小报告。
func validateResourceCohortMemberFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect LSP resource cohort member: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return invalidResourceCohortReport(fmt.Errorf("LSP resource cohort member is insecure: %s", path))
	}
	if err := securefs.CheckPrivateOwnerOnly(path, info); err != nil {
		return invalidResourceCohortReport(fmt.Errorf("LSP resource cohort member is insecure: %s: %w", path, err))
	}
	if info.Size() <= 0 || info.Size() > 64*1024 {
		return invalidResourceCohortReport(fmt.Errorf("LSP resource cohort member has invalid size: %d", info.Size()))
	}
	return nil
}

func invalidResourceCohortReport(err error) error {
	return fmt.Errorf("%w: %v", errInvalidResourceCohortReport, err)
}

func shouldQuarantineResourceCohortReport(err error) bool {
	return errors.Is(err, errInvalidResourceCohortReport)
}

// validateResourceCohortMemberFields 要求报告显式携带 schema 中的每个字段。
func validateResourceCohortMemberFields(payload []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return err
	}
	memberType := reflect.TypeFor[resourceCohortMember]()
	for field := range memberType.Fields() {
		jsonName, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if jsonName == "" || jsonName == "-" {
			continue
		}
		if _, ok := fields[jsonName]; !ok {
			return fmt.Errorf("required field %q is missing", jsonName)
		}
	}
	return nil
}

// validateResourceCohortMember 对跨进程字段执行完整 fail-fast 校验。
func validateResourceCohortMember(member resourceCohortMember) error {
	checks := []struct {
		invalid bool
		message string
	}{
		{member.SchemaVersion != resourceCohortSchemaVersion, fmt.Sprintf("schema_version = %d, want %d", member.SchemaVersion, resourceCohortSchemaVersion)},
		{member.OwnerPID <= 1, "owner_pid must be greater than 1"},
		{member.OwnerStartIdentity == "", "owner_start_identity is empty"},
		{member.ClientPID <= 1, "client_pid must be greater than 1"},
		{member.ClientStartIdentity == "", "client_start_identity is empty"},
		{member.WorkspaceHash == "", "workspace_hash is empty"},
		{member.LanguageID == "", "language_id is empty"},
		{member.RepositoryCohortID == "", "repository_cohort_id is empty"},
		{member.Role != ResourceCohortRolePrimary && member.Role != ResourceCohortRoleSecondary, "role is invalid"},
		{member.RSSBytes == 0, "rss_bytes must be greater than zero"},
		{member.ProcessRSSLimitBytes == 0, "process_rss_limit_bytes must be greater than zero"},
		{member.CohortHardLimitBytes == 0, "cohort_hard_limit_bytes must be greater than zero"},
		{member.ProcessRSSLimitBytes > member.CohortHardLimitBytes, "process_rss_limit_bytes exceeds cohort_hard_limit_bytes"},
		{!strings.HasPrefix(member.RepositoryCohortID, "repo-"), "repository_cohort_id must start with repo-"},
		{member.ActiveLeases < 0, "active_leases must not be negative"},
		{member.LastActivityUnixNano <= 0, "last_activity_unix_nano must be greater than zero"},
		{member.UpdatedAtUnixNano <= 0, "updated_at_unix_nano must be greater than zero"},
	}
	for _, check := range checks {
		if check.invalid {
			return errors.New(check.message)
		}
	}
	return nil
}

// selectResourceCohortVictims 按 idle LRU 选择无租约成员，直到聚合 RSS 回落到软水位。
func selectResourceCohortVictims(members []resourceCohortMember, aggregate, hardLimit, softLimit uint64) []string {
	if aggregate <= hardLimit || hardLimit == 0 || softLimit >= hardLimit {
		return nil
	}
	candidates := slices.Clone(members)
	candidates = slices.DeleteFunc(candidates, func(member resourceCohortMember) bool {
		return member.ActiveLeases > 0
	})
	slices.SortFunc(candidates, compareResourceCohortVictims)
	remaining := aggregate
	victims := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if remaining <= softLimit {
			break
		}
		victims = append(victims, resourceCohortMemberKey(candidate))
		remaining = subtractResourceCohortRSS(remaining, candidate.RSSBytes)
	}
	return victims
}

func compareResourceCohortVictims(left, right resourceCohortMember) int {
	if left.LastActivityUnixNano != right.LastActivityUnixNano {
		return compareInt64(left.LastActivityUnixNano, right.LastActivityUnixNano)
	}
	if left.RSSBytes != right.RSSBytes {
		if left.RSSBytes > right.RSSBytes {
			return -1
		}
		return 1
	}
	return strings.Compare(resourceCohortMemberKey(left), resourceCohortMemberKey(right))
}

func compareInt64(left, right int64) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func subtractResourceCohortRSS(total, released uint64) uint64 {
	if released >= total {
		return 0
	}
	return total - released
}

func addResourceCohortRSS(total, added uint64) uint64 {
	if ^uint64(0)-total < added {
		return ^uint64(0)
	}
	return total + added
}

func resourceCohortMemberKey(member resourceCohortMember) string {
	return strconv.Itoa(member.OwnerPID) + "/" + member.OwnerStartIdentity + "/" +
		strconv.Itoa(member.ClientPID) + "/" + member.ClientStartIdentity
}

func effectiveLSPLogMessageType(params protocol.LogMessageParams) protocol.LogMessageType {
	if params.Type == protocol.LogMessageError && lspErrorMessageIsWarning(params.Message) {
		return protocol.LogMessageWarning
	}
	return params.Type
}

func lspErrorMessageIsWarning(message string) bool {
	normalized := strings.ToLower(strings.TrimSpace(message))
	return strings.HasPrefix(normalized, "warning:") ||
		strings.Contains(normalized, "warning: while diagnosing orphaned files: session is shut down")
}
