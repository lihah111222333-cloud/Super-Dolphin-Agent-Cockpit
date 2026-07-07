// Package memory compatibility bridges for the team-memory
// subpackage migration. Owned by the subpackage split; keep here until
// root callers move to direct memory/team imports.
package memory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"

	dedup "github.com/anthropic-ai/super-agent-v3/internal/module/memory/dedup"
	shared "github.com/anthropic-ai/super-agent-v3/internal/module/memory/shared"
	teampkg "github.com/anthropic-ai/super-agent-v3/internal/module/memory/team"
)

var (
	ErrInvalidProjectDir          = errors.New("invalid project directory")
	ErrMemoryOverflowMergeFailed  = errors.New("memory_overflow_merge_failed")
	ErrMemoryOverflowDeleteFailed = errors.New("memory_overflow_delete_failed")
)

// ==== team-memory bridge ====

// TeamMemoryManager 是 team 子包管理器在 root memory 包中的兼容别名。
// 生产装配仍通过它暴露团队记忆根和 entrypoint，避免旧调用方直接依赖子包路径。
type TeamMemoryManager = teampkg.TeamMemoryManager

// TeamMemoryGuard 是团队记忆写入守卫的兼容别名。
// 它保持 root 包对敏感内容扫描的旧出口，实际规则由 team 子包维护。
type TeamMemoryGuard = teampkg.TeamMemoryGuard

// TeamMemSecretFinding 描述团队记忆内容中的敏感信息命中。
// 该别名让 UI 和写入路径继续复用同一份扫描结果结构。
type TeamMemSecretFinding = teampkg.TeamMemSecretFinding

// TeamMemSkippedFile 描述团队记忆预推送扫描跳过的文件。
// 跳过原因来自 team 子包，root 包只负责兼容导出。
type TeamMemSkippedFile = teampkg.TeamMemSkippedFile

// TeamMemPrePushScanResult 汇总团队记忆预推送扫描结果。
// 调用方通过 root 包读取该结构时，仍看到 team 子包的真实扫描语义。
type TeamMemPrePushScanResult = teampkg.TeamMemPrePushScanResult

// TeamMemSecretError 包装团队记忆敏感内容阻断错误。
// provider 或 hook 层可用它区分普通写入失败和密钥泄漏风险。
type TeamMemSecretError = teampkg.TeamMemSecretError

const teamMemoryRootDirName = teampkg.RootDirName

var ErrTeamMemSecretDetected = teampkg.ErrTeamMemSecretDetected

func provideTeamConfig(cfg *Config) teampkg.Config {
	return teamConfigAdapter{cfg: memoryConfig(cfg)}
}

// NewTeamMemoryManager 创建团队记忆管理器适配器。
func NewTeamMemoryManager(cfg *Config) *TeamMemoryManager {
	return teampkg.NewTeamMemoryManager(provideTeamConfig(cfg))
}

// NewTeamMemoryGuard 创建团队记忆写入守卫。
func NewTeamMemoryGuard(manager *TeamMemoryManager) *TeamMemoryGuard {
	return teampkg.NewTeamMemoryGuard(manager)
}

func teamMemoryConfigured(cfg Config) bool {
	return cfg.Enabled && cfg.Features.TeamMemory
}

func configuredTeamMemRoot(cfg *Config, buildCtx ...contract.BuildCtx) (string, error) {
	return provideTeamConfig(cfg).TeamRoot(firstTeamBuildCtx(buildCtx))
}

func configuredTeamMemPath(m *TeamMemoryManager, buildCtx ...contract.BuildCtx) (string, error) {
	return teampkg.ConfiguredTeamMemPath(m, buildCtx...)
}

func firstTeamBuildCtx(buildCtx []contract.BuildCtx) contract.BuildCtx {
	if len(buildCtx) == 0 {
		return contract.BuildCtx{}
	}
	return buildCtx[0]
}

func setTeamMemoryRuntimeReady(ready bool) {
	teampkg.SetRuntimeReady(ready)
}

// ScanTeamMemContent 扫描团队记忆内容并提取结构化条目。
func ScanTeamMemContent(content string) []TeamMemSecretFinding {
	return teampkg.ScanTeamMemContent(content)
}

type teamConfigAdapter struct {
	cfg *Config
}

// Gate 判断团队记忆写入是否允许通过。
func (a teamConfigAdapter) Gate(buildCtx contract.BuildCtx) teampkg.GateSnapshot {
	gate := ResolveMemoryGate(buildCtx, a.cfg)
	return teampkg.GateSnapshot{
		AutoEnabled:    gate.AutoEnabled,
		TeamMemEnabled: gate.TeamMemEnabled,
		KairosActive:   gate.KairosActive,
	}
}

// TeamRoot 返回团队记忆根目录。
func (a teamConfigAdapter) TeamRoot(buildCtx contract.BuildCtx) (string, error) {
	cfg := memoryConfig(a.cfg)
	projectRoot := a.ProjectRoot(buildCtx)
	if projectRoot == "" && strings.TrimSpace(cfg.AutoMemPathOverride) == "" {
		return "", ErrInvalidProjectDir
	}
	root, err := resolvedStoreRoot(cfg.RootDir, projectRoot, cfg.AutoMemPathOverride)
	if err != nil {
		return "", err
	}
	cleaned, err := shared.CleanAbsolutePath(root)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidMemoryRoot, err)
	}
	return filepath.Join(cleaned, teampkg.RootDirName), nil
}

// ProjectRoot 返回项目记忆根目录。
func (a teamConfigAdapter) ProjectRoot(buildCtx contract.BuildCtx) string {
	if buildCtx.GitRoot != "" {
		return strings.TrimSpace(buildCtx.GitRoot)
	}
	if buildCtx.CWD != "" {
		return strings.TrimSpace(buildCtx.CWD)
	}
	if a.cfg == nil {
		return ""
	}
	return strings.TrimSpace(a.cfg.ProjectRoot)
}

// ==== agent memory write helpers ====

const (
	agentMemoryWriteMaxNameRunes        = 96
	agentMemoryWriteMaxDescriptionRunes = 240
	agentMemoryWriteMaxContentBytes     = 8 * 1024
)

var (
	_ contract.AgentMemoryWriter = (*MemoryLifecycleHooks)(nil)

	agentSecretRegexps = []*regexp.Regexp{
		regexp.MustCompile(`(?m)-----BEGIN(?: [A-Z0-9]+)* PRIVATE KEY-----`),
		regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{40,}\b`),
		regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,255}\b`),
		regexp.MustCompile(`\bsk-(?:proj-)?[A-Za-z0-9_-]{20,}\b`),
		regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`),
		regexp.MustCompile(`(?im)\b(?:api[_-]?key|access[_-]?token|refresh[_-]?token|secret[_-]?key|auth[_-]?token|client[_-]?secret|password|secret)\b\s*[:=]\s*['"]?[A-Za-z0-9/_+=.-]{12,}['"]?`),
	}
)

// MemoryWriteEnabled 判断记忆写入是否启用。
func (h *MemoryLifecycleHooks) MemoryWriteEnabled() bool {
	return h != nil && h.cfg != nil && h.cfg.Enabled
}

// MemoryWriteToolsEnabled 判断记忆写入工具是否启用。
func (h *MemoryLifecycleHooks) MemoryWriteToolsEnabled() bool {
	return h != nil && h.cfg != nil && h.cfg.EnableTools
}

// WriteAgentMemory 接住 provider 发来的 memory_write。
// 先检查开关、输入和敏感内容，再写入 memory；写完要让 prompt 下次读到新内容。
func (h *MemoryLifecycleHooks) WriteAgentMemory(ctx context.Context, req contract.AgentMemoryWriteRequest) (contract.AgentMemoryWriteResult, error) {
	if err := h.ensureAgentMemoryWriteReady(); err != nil {
		return contract.AgentMemoryWriteResult{}, err
	}
	entry, scope, err := buildAgentMemoryEntry(req)
	if err != nil {
		return contract.AgentMemoryWriteResult{}, err
	}
	options := h.writeOptions(ctx, req.ThreadID)
	outcome, err := h.writeStructuredAgentMemory(ctx, req.ThreadID, entry, options)
	if err != nil {
		return h.agentMemoryWriteErrorResult(outcome, entry.Type, scope, err)
	}
	return h.finishAgentMemoryWrite(outcome, entry.Type, scope, options)
}

// ensureAgentMemoryWriteReady 在解析请求前检查 memory_write 的模块和工具开关。
// 这里返回带稳定 code 的错误，方便工具面区分功能关闭和 writer 缺失。
func (h *MemoryLifecycleHooks) ensureAgentMemoryWriteReady() error {
	if h == nil {
		return agentMemoryError("writer_unavailable", fmt.Errorf("memory writer is not configured"))
	}
	if h.cfg != nil && !h.cfg.Enabled {
		return agentMemoryError("feature_disabled", contract.ErrFeatureDisabled)
	}
	if h.cfg != nil && !h.cfg.EnableTools {
		return agentMemoryError("tools_disabled", contract.ErrFeatureDisabled)
	}
	return nil
}

// agentMemoryWriteErrorResult 在主写入已落盘但维护失败时保留写入结果。
// 只有显式 partial 才返回非空结果，其它错误继续按主写入失败处理。
func (h *MemoryLifecycleHooks) agentMemoryWriteErrorResult(outcome agentMemoryWriteOutcome, memType MemoryType, scope contract.MemoryScope, err error) (contract.AgentMemoryWriteResult, error) {
	if contract.AgentMemoryErrorCode(err) != "partial" || !outcome.hasPrimaryWrite() {
		return contract.AgentMemoryWriteResult{}, err
	}
	result := agentMemoryWriteResult(outcome, memType, scope)
	h.invalidateMemorySections()
	return result, err
}

// finishAgentMemoryWrite 处理写入后的 overflow 维护和 prompt 区块失效。
// 维护失败会作为 partial 返回，但已经成功的主写入仍会触发失效。
func (h *MemoryLifecycleHooks) finishAgentMemoryWrite(outcome agentMemoryWriteOutcome, memType MemoryType, scope contract.MemoryScope, options WriteOptions) (contract.AgentMemoryWriteResult, error) {
	result := agentMemoryWriteResult(outcome, memType, scope)
	if outcome.skipped || outcome.merged {
		h.invalidateMemorySections()
		return result, nil
	}
	if err := h.maybeOverflowMerge(outcome.store, memType, options); err != nil {
		h.invalidateMemorySections()
		return result, agentMemoryError("partial", err)
	}
	h.invalidateMemorySections()
	return result, nil
}

// agentMemoryWriteOutcome 记录这次写入最终去了哪里。
// 请求里的 scope 只是意图，actualTarget 才是 private/team 路由和合并后的结果。
type agentMemoryWriteOutcome struct {
	store        memoryStructuredStore
	path         string
	actualTarget string
	skipped      bool
	merged       bool
}

func (o agentMemoryWriteOutcome) hasPrimaryWrite() bool {
	return strings.TrimSpace(o.path) != "" || o.skipped || o.merged
}

// writeStructuredAgentMemory 只把已检查的内容写进 private/team memory。
// 它不组 prompt；调用方写完后要通知 prompt 重新读取。
func (h *MemoryLifecycleHooks) writeStructuredAgentMemory(ctx context.Context, threadID string, entry MemoryWriteRequest, options WriteOptions) (agentMemoryWriteOutcome, error) {
	primary, secondary, err := h.intentDiskStores(ctx, threadID, entry.Type)
	if err != nil {
		return agentMemoryWriteOutcome{}, agentMemoryError("persist_failed", err)
	}
	store, err := selectExplicitWriteStore(entry.Name, primary, secondary)
	if err != nil {
		return agentMemoryWriteOutcome{}, agentMemoryError("persist_failed", err)
	}
	primaryScope, secondaryScope := scopeNamesForIntentStores(entry.Type, secondary != nil)
	h.warnCrossScopeSameName(entry.Name, store, primary, secondary, primaryScope, secondaryScope)
	actual := actualAgentMemoryScope(store, secondary, primaryScope, secondaryScope)
	if entry.Type == MemoryTypeFeedback && actual == "team" {
		return agentMemoryWriteOutcome{}, agentMemoryError("team_scope_not_allowed", fmt.Errorf("feedback memory cannot be written to team scope"))
	}
	handled, dedupErr := h.checkDedupAndHandle(entry, store, actual, options)
	if dedupErr != nil {
		return agentMemoryWriteOutcome{}, agentMemoryError("merge_failed", dedupErr)
	}
	if handled {
		return agentMemoryWriteOutcome{store: store, actualTarget: actual, skipped: true}, nil
	}
	written, err := upsertStructuredMemoryReturningEntry(store, entry, options)
	if err != nil {
		outcome := agentMemoryWriteOutcome{store: store, path: relativeAgentMemoryPath(store, written.FilePath), actualTarget: actual}
		if outcome.hasPrimaryWrite() && errors.Is(err, ErrMemoryIndexUpdateFailed) {
			return outcome, agentMemoryError("partial", err)
		}
		return agentMemoryWriteOutcome{}, agentMemoryError("persist_failed", err)
	}
	return agentMemoryWriteOutcome{store: store, path: relativeAgentMemoryPath(store, written.FilePath), actualTarget: actual}, nil
}

// buildAgentMemoryEntry 把 tool 参数变成 memory 条目。
// 缺 name/description/content、未知 type/scope、疑似 secret 都直接报错。
func buildAgentMemoryEntry(req contract.AgentMemoryWriteRequest) (MemoryWriteRequest, contract.MemoryScope, error) {
	name, description, content, err := normalizeAgentMemoryInput(req)
	if err != nil {
		return MemoryWriteRequest{}, "", err
	}
	memType, err := parseAgentMemoryType(req.Type)
	if err != nil {
		return MemoryWriteRequest{}, "", err
	}
	scope, err := resolveAgentMemoryScope(req.Scope, contract.MemoryType(memType))
	if err != nil {
		return MemoryWriteRequest{}, "", err
	}
	if err := validateAgentMemoryGuards(name, description, content); err != nil {
		return MemoryWriteRequest{}, "", err
	}
	return MemoryWriteRequest{Name: name, Description: description, Type: memType, Body: buildExplicitMemoryBody(memType, content), Title: strings.TrimSpace(req.Title), Source: strings.TrimSpace(req.Source)}, scope, nil

}

func normalizeAgentMemoryInput(req contract.AgentMemoryWriteRequest) (string, string, string, error) {
	name := strings.TrimSpace(req.Name)
	description := strings.TrimSpace(req.Description)
	content := strings.TrimSpace(strings.ReplaceAll(req.Content, "\r\n", "\n"))
	if name == "" || description == "" || content == "" {
		return "", "", "", agentMemoryError("invalid_input", fmt.Errorf("name, description and content are required"))
	}
	if inputExceedsAgentMemoryLimits(name, description, content) {
		return "", "", "", agentMemoryError("invalid_input", fmt.Errorf("memory_write input exceeds limits"))
	}
	return name, description, content, nil
}

func inputExceedsAgentMemoryLimits(name, description, content string) bool {
	return utf8.RuneCountInString(name) > agentMemoryWriteMaxNameRunes ||
		utf8.RuneCountInString(description) > agentMemoryWriteMaxDescriptionRunes ||
		len([]byte(content)) > agentMemoryWriteMaxContentBytes
}

func parseAgentMemoryType(raw contract.MemoryType) (MemoryType, error) {
	memType := MemoryType(contract.ParseMemoryType(string(raw)))
	if memType != MemoryTypeFeedback && memType != MemoryTypeProject {
		return "", agentMemoryError("invalid_input", fmt.Errorf("type must be feedback or project"))
	}
	return memType, nil
}

func resolveAgentMemoryScope(raw contract.MemoryScope, memType contract.MemoryType) (contract.MemoryScope, error) {
	scope := raw
	if scope == "" {
		scope = defaultAgentMemoryScope(memType)
	}
	if scope == contract.MemoryScopeLocal {
		return "", agentMemoryError("unsupported_scope", fmt.Errorf("local scope is not supported"))
	}
	if !scope.Valid() || scope != defaultAgentMemoryScope(memType) {
		return "", agentMemoryError("invalid_input", fmt.Errorf("scope does not match type"))
	}
	return scope, nil
}

func validateAgentMemoryGuards(name, description, content string) error {
	if hasProbableSecret(name, description, content) {
		return agentMemoryError("secret_detected", fmt.Errorf("memory content appears to contain a secret"))
	}
	if requiresAgentMemoryConfirmation(content) {
		return agentMemoryError("confirmation_required", fmt.Errorf("memory content requires explicit user confirmation"))
	}
	return nil
}

func defaultAgentMemoryScope(memType contract.MemoryType) contract.MemoryScope {
	if memType == contract.MemoryTypeFeedback {
		return contract.MemoryScopeUser
	}
	return contract.MemoryScopeProject
}

func actualAgentMemoryScope(store, secondary memoryStructuredStore, primaryScope, secondaryScope string) string {
	if store == secondary {
		return secondaryScope
	}
	return primaryScope
}

func agentMemoryWriteResult(outcome agentMemoryWriteOutcome, memType MemoryType, scope contract.MemoryScope) contract.AgentMemoryWriteResult {
	return contract.AgentMemoryWriteResult{
		Path:           outcome.path,
		RequestedScope: scope,
		ActualTarget:   outcome.actualTarget,
		Type:           contract.MemoryType(memType),
		Skipped:        outcome.skipped,
		Merged:         outcome.merged,
	}
}

func upsertStructuredMemoryReturningEntry(store memoryStructuredStore, entry MemoryWriteRequest, options WriteOptions) (MemoryEntry, error) {
	if store == nil {
		return MemoryEntry{}, fmt.Errorf("memory store is nil")
	}
	return store.UpsertStructured(entry, options)
}

func relativeAgentMemoryPath(store memoryStructuredStore, filePath string) string {
	ws, ok := store.(memoryWriteStore)
	if !ok {
		return filepath.ToSlash(filePath)
	}
	rel, err := filepath.Rel(ws.Root(), filePath)
	if err != nil {
		return filepath.ToSlash(filePath)
	}
	return filepath.ToSlash(rel)
}

func agentMemoryError(code string, err error) error {
	return contract.NewAgentMemoryError(code, err)
}

func hasProbableSecret(parts ...string) bool {
	text := strings.Join(parts, "\n")
	for _, re := range agentSecretRegexps {
		if re.MatchString(text) {
			return true
		}
	}
	return containsHighEntropyAssignment(text)
}

var highEntropyAssignmentRe = regexp.MustCompile(`(?m)\b[A-Za-z0-9_.-]*(?:KEY|TOKEN|SECRET|PASSWORD)[A-Za-z0-9_.-]*\b\s*[:=]\s*['"]?([A-Za-z0-9/_+=.-]{24,})['"]?`)

func containsHighEntropyAssignment(text string) bool {
	for _, match := range highEntropyAssignmentRe.FindAllStringSubmatch(text, -1) {
		if len(match) > 1 && looksHighEntropy(match[1]) {
			return true
		}
	}
	return false
}

// looksHighEntropy 判断文本是否像高熵密钥，避免写入敏感内容。
func looksHighEntropy(s string) bool {
	classes := 0
	checks := []func(rune) bool{unicode.IsLower, unicode.IsUpper, unicode.IsDigit, func(r rune) bool { return strings.ContainsRune("/_+=.-", r) }}
	for _, check := range checks {
		for _, r := range s {
			if check(r) {
				classes++
				break
			}
		}
	}
	return classes >= 3
}

func requiresAgentMemoryConfirmation(content string) bool {
	lower := strings.ToLower(content)
	patterns := []string{"tool output", "webpage", "readme", "ignore previous instructions", "confirmed=true", "userapproved=true", "user approved", "用户已确认"}
	for _, pattern := range patterns {
		if strings.Contains(lower, strings.ToLower(pattern)) {
			return true
		}
	}
	return false
}

// ==== explicit memory write helpers ====

func buildExplicitMemoryWrite(intent SaveIntent) MemoryWriteRequest {
	content := normalizeHookContent(intent.Content)
	memoryType := intent.Type
	if !memoryType.IsKnown() {
		memoryType = inferMemoryType(content)
	}
	description := buildExplicitMemoryDescription(content)
	return MemoryWriteRequest{
		Name:        buildExplicitMemoryName(memoryType, description),
		Description: description,
		Type:        memoryType,
		Body:        buildExplicitMemoryBody(memoryType, content),
	}

}

func buildExplicitMemoryDescription(content string) string {
	description := truncateRunes(firstNonEmptyLine(content), memoryHookMaxRunes)
	if description == "" {
		description = truncateRunes(content, memoryHookMaxRunes)
	}
	return description
}

// buildExplicitMemoryBody 把显式记忆条目整理成写入正文。
func buildExplicitMemoryBody(memoryType MemoryType, content string) string {
	content = strings.TrimSpace(content)
	switch memoryType {
	case MemoryTypeFeedback:
		if hasStructuredMemorySection(content, "why") && hasStructuredMemorySection(content, "how to apply") {
			return content
		}
		return strings.Join([]string{
			content,
			"Why: user explicitly asked to remember this working guidance.",
			"How to apply: follow this guidance when future work touches the same area.",
		}, "\n")
	case MemoryTypeProject:
		if hasStructuredMemorySection(content, "why") && hasStructuredMemorySection(content, "how to apply") {
			return content
		}
		return strings.Join([]string{
			content,
			"Why: user explicitly asked to preserve this project context.",
			"How to apply: use this context when making project recommendations or planning follow-up work.",
		}, "\n")
	default:
		return content
	}
}

// buildExplicitMemoryName 为显式记忆条目生成稳定名称。
func buildExplicitMemoryName(memoryType MemoryType, description string) string {
	prefix := "Saved memory"
	switch memoryType {
	case MemoryTypeUser:
		prefix = "User note"
	case MemoryTypeFeedback:
		prefix = "Feedback rule"
	case MemoryTypeProject:
		prefix = "Project note"
	case MemoryTypeReference:
		prefix = "Reference note"
	}
	if description == "" {
		return prefix
	}
	return truncateRunes(prefix+": "+description, 96)
}

func normalizeHookContent(text string) string {
	lines := make([]string, 0, 4)
	for line := range strings.SplitSeq(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(strings.TrimLeft(line, "-*• "))
		line = strings.TrimSpace(strings.TrimLeft(line, ":：-—,，。.!！?？;；"))
		line = strings.TrimSpace(strings.TrimPrefix(line, "that "))
		line = strings.TrimSpace(strings.TrimPrefix(line, "关于 "))
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func hasMeaningfulMemoryContent(text string) bool {
	return strings.IndexFunc(text, func(r rune) bool {
		return unicode.IsLetter(r) || unicode.IsNumber(r)
	}) >= 0
}

// ==== dedup write helpers ====

func mergeAndWriteMemory(store memoryWriteStore, targetPath string, merged MemoryEntry, options WriteOptions, locks *diskLockCoordinator) error {
	if err := ValidateMemoryEntryContent(merged); err != nil {
		return err
	}
	root := store.Root()
	raw := formatMemoryEntry(merged)
	if locks == nil {
		locks = newDiskLockCoordinator()
	}
	return locks.withDiskStoreLock(root, func() error {
		validatedPath, err := ValidateMemoryWritePath(root, targetPath)
		if err != nil {
			return err
		}
		if err := writeAtomicFile(validatedPath, []byte(raw), 0o644); err != nil {
			return err
		}
		return updateIndexAfterMutation(root, options)
	})
}

// overflowMergeAndDelete 合并溢出的记忆条目并删除旧记录。
func overflowMergeAndDelete(store memoryWriteStore, keepPath string, merged MemoryEntry, deletePath string, options WriteOptions, locks *diskLockCoordinator) error {
	if err := ValidateMemoryEntryContent(merged); err != nil {
		return err
	}
	root := store.Root()
	raw := formatMemoryEntry(merged)
	if locks == nil {
		locks = newDiskLockCoordinator()
	}
	return locks.withDiskStoreLock(root, func() error {
		validatedKeep, err := ValidateMemoryWritePath(root, keepPath)
		if err != nil {
			return err
		}
		if err := writeAtomicFile(validatedKeep, []byte(raw), 0o644); err != nil {
			return err
		}
		validatedDel, err := ValidateMemoryWritePath(root, deletePath)
		if err != nil {
			return err
		}
		if err := os.Remove(validatedDel); err != nil {
			return fmt.Errorf("%w: %v", ErrMemoryOverflowDeleteFailed, err)
		}
		return updateIndexAfterMutation(root, options)
	})
}

func snapshotToMemoryEntry(s dedup.EntrySnapshot) MemoryEntry {
	t := ParseMemoryType(s.Type)
	return MemoryEntry{
		Frontmatter: MemoryFrontmatter{
			Name:        s.Name,
			Description: s.Description,
			Type:        &t,
			Lang:        s.Lang,
			Aliases:     s.Aliases,
			SearchKeys:  s.SearchKeys,
			Source:      s.Source,
		},
		Content:  s.Content,
		FilePath: s.Path,
	}
}
