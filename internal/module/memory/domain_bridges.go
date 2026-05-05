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

var ErrInvalidProjectDir = errors.New("invalid project directory")

// ==== team-memory bridge ====

type TeamMemoryManager = teampkg.TeamMemoryManager
type TeamMemoryGuard = teampkg.TeamMemoryGuard
type TeamMemSecretFinding = teampkg.TeamMemSecretFinding
type TeamMemSkippedFile = teampkg.TeamMemSkippedFile
type TeamMemPrePushScanResult = teampkg.TeamMemPrePushScanResult
type TeamMemSecretError = teampkg.TeamMemSecretError

const teamMemoryRootDirName = teampkg.RootDirName

var ErrTeamMemSecretDetected = teampkg.ErrTeamMemSecretDetected

func provideTeamConfig(cfg *Config) teampkg.Config {
	return teamConfigAdapter{cfg: memoryConfig(cfg)}
}

func NewTeamMemoryManager(cfg *Config) *TeamMemoryManager {
	return teampkg.NewTeamMemoryManager(provideTeamConfig(cfg))
}

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

func teamMemPath(m *TeamMemoryManager, buildCtx contract.BuildCtx) string {
	if m == nil {
		return ""
	}
	return m.GetTeamMemPath(buildCtx)
}

func isTeamMemoryEnabled(m *TeamMemoryManager, buildCtx contract.BuildCtx) bool {
	if m == nil {
		return false
	}
	return m.IsTeamMemoryEnabled(buildCtx)
}

func validateTeamMemWriteRequest(m *TeamMemoryManager, raw string) error {
	if m == nil {
		return ErrTeamMemoryDisabled
	}
	return m.ValidateTeamMemWritePath(raw)
}

func validateTeamMemKeyRequest(m *TeamMemoryManager, key string) error {
	if m == nil {
		return ErrTeamMemoryDisabled
	}
	return m.ValidateTeamMemKey(key)
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

func ScanTeamMemContent(content string) []TeamMemSecretFinding {
	return teampkg.ScanTeamMemContent(content)
}

type teamConfigAdapter struct {
	cfg *Config
}

func (a teamConfigAdapter) Gate(buildCtx contract.BuildCtx) teampkg.GateSnapshot {
	gate := ResolveMemoryGate(buildCtx, a.cfg)
	return teampkg.GateSnapshot{
		AutoEnabled:    gate.AutoEnabled,
		TeamMemEnabled: gate.TeamMemEnabled,
		KairosActive:   gate.KairosActive,
	}
}

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

func (h *MemoryLifecycleHooks) MemoryWriteEnabled() bool {
	return h != nil && h.cfg != nil && h.cfg.Enabled
}

func (h *MemoryLifecycleHooks) MemoryWriteToolsEnabled() bool {
	return h != nil && h.cfg != nil && h.cfg.EnableTools
}

func (h *MemoryLifecycleHooks) WriteAgentMemory(ctx context.Context, req contract.AgentMemoryWriteRequest) (contract.AgentMemoryWriteResult, error) {

	if h == nil {
		return contract.AgentMemoryWriteResult{}, agentMemoryError("writer_unavailable", fmt.Errorf("memory writer is not configured"))
	}
	if h.cfg != nil && !h.cfg.Enabled {
		return contract.AgentMemoryWriteResult{}, agentMemoryError("feature_disabled", contract.ErrFeatureDisabled)
	}
	if h.cfg != nil && !h.cfg.EnableTools {
		return contract.AgentMemoryWriteResult{}, agentMemoryError("tools_disabled", contract.ErrFeatureDisabled)
	}
	entry, scope, err := buildAgentMemoryEntry(req)
	if err != nil {
		return contract.AgentMemoryWriteResult{}, err
	}
	options := h.writeOptions(ctx, req.ThreadID)
	outcome, err := h.writeStructuredAgentMemory(ctx, req.ThreadID, entry, options)
	if err != nil {
		return contract.AgentMemoryWriteResult{}, err
	}
	if !outcome.skipped && !outcome.merged {
		h.maybeOverflowMerge(outcome.store, entry.Type, options)
	}
	h.invalidateMemorySections()
	return agentMemoryWriteResult(outcome, entry.Type, scope), nil
}

type agentMemoryWriteOutcome struct {
	store        memoryStructuredStore
	path         string
	actualTarget string
	skipped      bool
	merged       bool
}

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
	if h.checkDedupAndHandle(entry, store, actual, options) {
		return agentMemoryWriteOutcome{store: store, actualTarget: actual, skipped: true}, nil
	}
	written, err := upsertStructuredMemoryReturningEntry(store, entry, options)
	if err != nil {
		return agentMemoryWriteOutcome{}, agentMemoryError("persist_failed", err)
	}
	return agentMemoryWriteOutcome{store: store, path: relativeAgentMemoryPath(store, written.FilePath), actualTarget: actual}, nil
}

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

func mergeAndWriteMemory(store memoryWriteStore, targetPath string, merged MemoryEntry, options WriteOptions) error {
	if err := ValidateMemoryEntryContent(merged); err != nil {
		return err
	}
	root := store.Root()
	raw := formatMemoryEntry(merged)
	return store.withDiskStoreLock(root, func() error {
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

func overflowMergeAndDelete(store memoryWriteStore, keepPath string, merged MemoryEntry, deletePath string, options WriteOptions) error {
	if err := ValidateMemoryEntryContent(merged); err != nil {
		return err
	}
	root := store.Root()
	raw := formatMemoryEntry(merged)
	return store.withDiskStoreLock(root, func() error {
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
		_ = os.Remove(validatedDel)
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
