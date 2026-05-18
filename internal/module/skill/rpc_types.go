package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

const (
	ResolutionViewDiff                   = "view_diff"
	ResolutionViewUnmanaged              = "view_unmanaged"
	ResolutionImportPersonal             = "import_to_personal_imported"
	ResolutionImportProject              = "import_to_project"
	ResolutionTakeoverProvider           = "takeover_provider_skill"
	ResolutionSaveAsNewSkill             = "save_as_new_skill"
	ResolutionSaveAsNewPersonal          = "save_as_new_personal_skill"
	ResolutionSyncBackCanonical          = "sync_back_to_canonical"
	ResolutionSyncBackPersonal           = "sync_back_to_personal"
	ResolutionCanonicalOverwrite         = "canonical_overwrite_mirror"
	ResolutionCanonicalOverwrites        = "canonical_overwrites_mirror"
	ResolutionPersonalOverwrite          = "personal_overwrite_mirror"
	ResolutionRenamePersonal             = "rename_personal"
	ResolutionDisablePersonalForProject  = "disable_personal_for_project"
	ResolutionRenamePersonalType         = "rename_personal_type"
	ResolutionMergeManually              = "merge_manually"
	ResolutionKeepSelected               = "keep_selected"
	ResolutionConfirmDeleteDriftedMirror = "confirm_delete_drifted_mirror"
)

const skillSummarySuggestPromptHeader = `你是技能简介助手。请根据输入为一个 SKILL.md 生成一句中文技能简介。

要求：
1. 只说明"什么时候使用这个技能"，不要解释实现细节。
2. 使用固定句式："当你需要……时使用。"
3. 要写具体场景，不要写成"帮你处理各种问题"这类泛泛能力介绍。
4. 不要总结执行步骤，例如"先读取文件，再分析，然后输出"。
5. 不要输出内部标记、XML 标签、markdown、代码块或多余说明。
6. 严格输出 JSON：{"description":"当你需要……时使用。"}

输入：
`

func suggestSkillSummary(ctx context.Context, dream contract.DreamExecutor, p skillSummarySuggestParams) (string, error) {
	if dream == nil {
		return "", contract.ErrDreamExecutorNotConfigured
	}
	p = normalizeSkillSummarySuggestParams(p)
	if p.Name == "" && p.Content == "" {
		return "", platformrpc.ErrInvalidParams("skill name or content is required")
	}
	prompt, err := buildSkillSummarySuggestPrompt(p)
	if err != nil {
		return "", err
	}
	raw, err := dream.ExecuteDream(ctx, prompt)
	if err != nil {
		return "", err
	}
	return parseSkillSummarySuggestionResult(raw)
}

func normalizeSkillSummarySuggestParams(p skillSummarySuggestParams) skillSummarySuggestParams {
	p.Name = strings.TrimSpace(p.Name)
	p.Description = strings.TrimSpace(p.Description)
	p.Content = strings.TrimSpace(p.Content)
	if scope, err := normalizeSkillScope(p.Scope); err == nil {
		p.Scope = scope
	} else {
		p.Scope = strings.TrimSpace(p.Scope)
	}
	return p
}

func parseSkillSummarySuggestionResult(raw string) (string, error) {
	var out skillSummarySuggestResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &out); err != nil {
		return "", fmt.Errorf("parse skill summary suggestion: %w", err)
	}
	description := strings.TrimSpace(out.Description)
	if description == "" {
		return "", fmt.Errorf("skill summary suggestion is empty")
	}
	if isInternalSkillMarkerSummary(description) {
		return "", fmt.Errorf("skill summary suggestion contains internal marker")
	}
	if err := validateSkillSummarySuggestionQuality(description); err != nil {
		return "", err
	}
	return truncateRunes(description, 120), nil
}

func validateSkillSummarySuggestionQuality(description string) error {
	issue := skillSummarySuggestionQualityIssue(description)
	if issue == "" {
		return nil
	}
	return fmt.Errorf("skill summary suggestion quality: %s", issue)
}

func skillSummarySuggestionQualityIssue(description string) string {
	description = strings.TrimSpace(description)
	if description == "" {
		return "missing"
	}
	switch {
	case skillSummaryLooksLikeWorkflow(description):
		return "workflow"
	case skillSummaryLooksTooGeneric(description):
		return "generic"
	case compactRuneLen(description) < 12:
		return "too_short"
	case compactRuneLen(description) > 120:
		return "too_long"
	case !skillSummaryHasScenarioCue(description):
		return "missing_scenario"
	default:
		return ""
	}
}

func skillSummaryLooksTooGeneric(description string) bool {
	lower := strings.ToLower(description)
	for _, term := range []string{
		"帮你处理各种问题",
		"帮助处理各种问题",
		"处理各种问题",
		"处理很多事情",
		"处理很多事",
		"做很多事情",
		"做很多事",
		"通用助手",
		"提高效率",
		"什么都可以",
		"各种东西",
	} {
		if strings.Contains(lower, term) {
			return true
		}
	}
	return false
}

func skillSummaryLooksLikeWorkflow(description string) bool {
	if strings.Contains(description, "先") && (strings.Contains(description, "然后") || strings.Contains(description, "再") || strings.Contains(description, "最后")) {
		return true
	}
	if strings.Contains(description, "读取") && strings.Contains(description, "分析") && strings.Contains(description, "输出") {
		return true
	}
	for _, term := range []string{"实现步骤", "执行步骤", "工作流程"} {
		if strings.Contains(description, term) {
			return true
		}
	}
	return false
}

func skillSummaryHasScenarioCue(description string) bool {
	for _, term := range []string{"当你需要", "当你遇到", "当你正在", "当你准备", "需要", "遇到", "正在", "准备"} {
		if strings.Contains(description, term) {
			return true
		}
	}
	return false
}

func compactRuneLen(value string) int {
	count := 0
	for _, field := range strings.Fields(value) {
		count += len([]rune(field))
	}
	return count
}

func buildSkillSummarySuggestPrompt(p skillSummarySuggestParams) (string, error) {
	payload := struct {
		Name          string   `json:"name,omitempty"`
		Description   string   `json:"description,omitempty"`
		Scope         string   `json:"scope,omitempty"`
		ScenarioWords []string `json:"scenario_words,omitempty"`
		Content       string   `json:"content,omitempty"`
	}{
		Name:          p.Name,
		Description:   p.Description,
		Scope:         p.Scope,
		ScenarioWords: uniqStrings(p.ScenarioWords),
		Content:       truncateRunes(p.Content, 12000),
	}
	body, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal skill summary input: %w", err)
	}
	return skillSummarySuggestPromptHeader + string(body), nil
}

type skillResolutionListParams struct {
	CWD             string `json:"cwd"`
	IncludeResolved bool   `json:"include_resolved,omitempty"`
}

type skillResolutionListResult struct {
	Items []skillResolutionItem `json:"items"`
}

type skillResolutionItem struct {
	ConflictID       string                         `json:"conflict_id"`
	Kind             string                         `json:"kind"`
	Scope            string                         `json:"scope,omitempty"`
	PersonalType     string                         `json:"personal_type,omitempty"`
	Name             string                         `json:"name"`
	AvailableActions []string                       `json:"available_actions"`
	ProviderEntries  []skillResolutionProviderEntry `json:"provider_entries,omitempty"`
	Sources          []skillResolutionSource        `json:"sources,omitempty"`
}

type skillResolutionProviderEntry struct {
	Provider     string `json:"provider"`
	SourcePath   string `json:"source_path,omitempty"`
	TargetPath   string `json:"target_path,omitempty"`
	SourceHash   string `json:"source_hash,omitempty"`
	TargetHash   string `json:"target_hash,omitempty"`
	TargetID     string `json:"target_id,omitempty"`
	SourcePathID string `json:"source_path_id,omitempty"`
}

type skillResolutionSource struct {
	Scope         string `json:"scope"`
	PersonalType  string `json:"personal_type,omitempty"`
	CanonicalID   string `json:"canonical_id"`
	ContentHash   string `json:"content_hash,omitempty"`
	CanonicalHash string `json:"canonical_hash,omitempty"`
	Path          string `json:"path,omitempty"`
	SkillFile     string `json:"skill_file,omitempty"`
}

type skillResolutionPreviewParams struct {
	CWD                 string   `json:"cwd"`
	Scope               string   `json:"scope,omitempty"`
	PersonalType        string   `json:"personal_type,omitempty"`
	Provider            string   `json:"provider,omitempty"`
	Providers           []string `json:"providers,omitempty"`
	SourceProvider      string   `json:"source_provider,omitempty"`
	SourcePathID        string   `json:"source_path_id,omitempty"`
	Name                string   `json:"name"`
	ConflictID          string   `json:"conflict_id"`
	Action              string   `json:"action"`
	NewName             string   `json:"new_name,omitempty"`
	KeepSourceID        string   `json:"keep_source_id,omitempty"`
	MergeContentHash    string   `json:"merge_content_hash,omitempty"`
	DisablePolicyTarget string   `json:"disable_policy_target,omitempty"`
	IncludeDiff         bool     `json:"include_diff,omitempty"`
}

type skillResolutionApplyParams struct {
	CWD                 string `json:"cwd"`
	Scope               string `json:"scope,omitempty"`
	PersonalType        string `json:"personal_type,omitempty"`
	Provider            string `json:"provider,omitempty"`
	Name                string `json:"name"`
	ConflictID          string `json:"conflict_id"`
	Action              string `json:"action"`
	NewName             string `json:"new_name,omitempty"`
	PreviewID           string `json:"preview_id"`
	PreviewHash         string `json:"preview_hash"`
	SourceProvider      string `json:"source_provider,omitempty"`
	SourcePathID        string `json:"source_path_id,omitempty"`
	KeepSourceID        string `json:"keep_source_id,omitempty"`
	DisablePolicyTarget string `json:"disable_policy_target,omitempty"`
}

type skillResolutionPreviewResult struct {
	ConflictID string                       `json:"conflict_id"`
	Kind       string                       `json:"kind"`
	Items      []skillResolutionPreviewItem `json:"items"`
}

type skillResolutionPreviewItem struct {
	Action                  string `json:"action"`
	Provider                string `json:"provider,omitempty"`
	PreviewID               string `json:"preview_id,omitempty"`
	SourceProvider          string `json:"source_provider,omitempty"`
	SourcePathID            string `json:"source_path_id,omitempty"`
	SourcePath              string `json:"source_path,omitempty"`
	TargetPath              string `json:"target_path,omitempty"`
	SourceHash              string `json:"source_hash,omitempty"`
	TargetHash              string `json:"target_hash,omitempty"`
	PreviewHash             string `json:"preview_hash,omitempty"`
	BackupPath              string `json:"backup_path,omitempty"`
	ConfirmDeleteMirrorHash string `json:"confirm_delete_mirror_hash,omitempty"`
	Diff                    string `json:"diff,omitempty"`
}

type skillResolutionStoredPreview struct {
	Item       skillResolutionPreviewItem
	ConflictID string
	Action     string
	ExpiresAt  time.Time
}

func normalizeResolutionAction(action string) string {
	if action == ResolutionCanonicalOverwrites {
		return ResolutionCanonicalOverwrite
	}
	return action
}

func sameNameApplyAction(action string) bool {
	return action == ResolutionDisablePersonalForProject || action == ResolutionKeepSelected
}

func (s *service) applySkillResolution(ctx context.Context, p skillResolutionApplyParams) (SkillMirrorResolutionReport, error) {
	p.Action = normalizeResolutionAction(p.Action)
	if err := validateResolutionApplyProof(p); err != nil {
		return SkillMirrorResolutionReport{Action: p.Action, Name: p.Name}, err
	}
	item, err := s.resolutionApplyItem(p)
	if err != nil {
		return SkillMirrorResolutionReport{Action: p.Action, Name: p.Name}, err
	}
	preview, err := s.lookupResolutionPreviewForConflict(p.PreviewID, p.ConflictID, p.Action, p.PreviewHash)
	if err != nil {
		return SkillMirrorResolutionReport{Action: p.Action, Name: p.Name}, err
	}
	if item.Kind == skillConflictSameName {
		return s.applySameNameResolution(ctx, item, preview, p)
	}
	target, err := s.resolutionApplyTarget(p.CWD, item, preview, p)
	if err != nil {
		return SkillMirrorResolutionReport{Action: p.Action, Name: p.Name}, err
	}
	req := SkillMirrorResolutionRequest{
		Action:      p.Action,
		Name:        resolutionApplyName(p, item),
		NewName:     p.NewName,
		Target:      target,
		PreviewID:   p.PreviewID,
		PreviewHash: p.PreviewHash,
	}
	if unmanagedProviderApplyAction(p.Action) {
		return applyUnmanagedProviderResolution(ctx, s, req)
	}
	return ResolveSkillMirrorDrift(ctx, s, SkillMirrorResolutionRequest{
		Action:      p.Action,
		Name:        resolutionApplyName(p, item),
		NewName:     p.NewName,
		Target:      target,
		PreviewID:   p.PreviewID,
		PreviewHash: p.PreviewHash,
	})
}

func (s *service) applySameNameResolution(ctx context.Context, item skillResolutionItem, preview skillResolutionPreviewItem, p skillResolutionApplyParams) (SkillMirrorResolutionReport, error) {
	report := SkillMirrorResolutionReport{Action: p.Action, Name: resolutionApplyName(p, item)}
	switch p.Action {
	case ResolutionDisablePersonalForProject:
		return s.applyDisablePersonalForProjectResolution(ctx, report, item, preview, p)
	case ResolutionKeepSelected:
		return s.applyKeepSelectedResolution(ctx, report, item, preview, p)
	default:
		return report, fmt.Errorf("unsupported same-name resolution action %q", p.Action)
	}
}

func (s *service) applyDisablePersonalForProjectResolution(ctx context.Context, report SkillMirrorResolutionReport, item skillResolutionItem, preview skillResolutionPreviewItem, p skillResolutionApplyParams) (SkillMirrorResolutionReport, error) {
	source, err := canonicalSourceByID(item.Sources, p.DisablePolicyTarget)
	if err != nil {
		return report, err
	}
	if source.Scope != skillScopePersonal {
		return report, fmt.Errorf("disable target must be a personal skill")
	}
	targetPath := filepath.Join(defaultProjectSkillsRoot(p.CWD), projectSkillPolicyFile)
	if err := validateSameNamePolicyPreview(preview, source.Path, targetPath); err != nil {
		return report, err
	}
	hash, err := writeProjectDisablePersonalPolicy(p.CWD, item.Name, source.PersonalType)
	if err != nil {
		return report, err
	}
	report.ResultingHash = hash
	markSameNamePolicyMirrorPublish(ctx, s, p.CWD, item.Name, &report)
	s.publishSkillsChanged(ctx, p.Action, item.Name, skillScopeProject)
	return report, nil
}

func (s *service) applyKeepSelectedResolution(ctx context.Context, report SkillMirrorResolutionReport, item skillResolutionItem, preview skillResolutionPreviewItem, p skillResolutionApplyParams) (SkillMirrorResolutionReport, error) {
	source, err := canonicalSourceByID(item.Sources, p.KeepSourceID)
	if err != nil {
		return report, err
	}
	targetPath := keepSelectedPolicyTargetPath(s, p.CWD, source)
	if err := validateSameNamePolicyPreview(preview, source.Path, targetPath); err != nil {
		return report, err
	}
	var hash string
	switch source.Scope {
	case skillScopeProject:
		hash, err = writeProjectKeepSelectedPolicy(p.CWD, item.Name, item.Sources, source)
	case skillScopePersonal:
		hash, err = s.writePersonalKeepSelectedPolicy(item.Name, item.Sources, source)
	default:
		return report, fmt.Errorf("keep selected requires a project or personal skill")
	}
	if err != nil {
		return report, err
	}
	report.ResultingHash = hash
	markSameNamePolicyMirrorPublish(ctx, s, p.CWD, item.Name, &report)
	publishSameNameKeepSelectedChanged(ctx, s, p.Action, item.Name, source)
	return report, nil
}

func keepSelectedPolicyTargetPath(s *service, cwd string, source skillResolutionSource) string {
	if source.Scope == skillScopeProject {
		return filepath.Join(defaultProjectSkillsRoot(cwd), projectSkillPolicyFile)
	}
	return filepath.Join(s.resolvedSuperDolphinHome(), "skills", personalSkillPolicyFile)
}

func sameNameSourcesIncludeProject(sources []skillResolutionSource) bool {
	for _, source := range sources {
		if source.Scope == skillScopeProject {
			return true
		}
	}
	return false
}

func publishSameNameKeepSelectedChanged(ctx context.Context, svc *service, action, name string, source skillResolutionSource) {
	if svc == nil {
		return
	}
	if source.Scope == skillScopePersonal {
		svc.publishSkillsChangedForPersonalType(ctx, action, name, skillScopePersonal, source.PersonalType)
		return
	}
	svc.publishSkillsChanged(ctx, action, name, skillScopeProject)
}

func markSameNamePolicyMirrorPublish(ctx context.Context, svc *service, cwd, name string, report *SkillMirrorResolutionReport) {
	if svc == nil || report == nil {
		return
	}
	publishReport := svc.publishWriteTimeMirrorsForEffectiveSet(ctx, cwd, name)
	if len(publishReport.Conflicts) > 0 {
		report.PartialFailure = true
		if report.FollowUpAction == "" {
			report.FollowUpAction = "retry_mirror_publish"
		}
	}
}

func validateSameNamePolicyPreview(preview skillResolutionPreviewItem, sourcePath, targetPath string) error {
	if !sameResolutionPath(preview.SourcePath, sourcePath) {
		return fmt.Errorf("skill resolution preview source mismatch")
	}
	if !sameResolutionPath(preview.TargetPath, targetPath) {
		return fmt.Errorf("skill resolution preview target mismatch")
	}
	return nil
}

func validateResolutionApplyProof(p skillResolutionApplyParams) error {
	if strings.TrimSpace(p.PreviewID) == "" {
		return fmt.Errorf("preview_id is required")
	}
	if strings.TrimSpace(p.PreviewHash) == "" {
		return fmt.Errorf("preview_hash is required")
	}
	return nil
}

func (s *service) resolutionApplyItem(p skillResolutionApplyParams) (skillResolutionItem, error) {
	list, err := s.listSkillResolutions(p.CWD)
	if err != nil {
		return skillResolutionItem{}, err
	}
	item, ok := findResolutionItemByID(list.Items, p.ConflictID)
	if !ok {
		return skillResolutionItem{}, fmt.Errorf("resolution conflict not found: %s", p.ConflictID)
	}
	if err := validateResolutionApplyAction(item, p.Action); err != nil {
		return skillResolutionItem{}, err
	}
	return item, nil
}

func validateResolutionApplyAction(item skillResolutionItem, action string) error {
	switch {
	case item.Kind == skillConflictSameName:
		return validateSameNameResolutionApplyAction(item, action)
	case item.Kind == skillConflictUnmanagedSameName || item.Kind == skillConflictUnmanagedProviderSkill:
		return validateUnmanagedResolutionApplyAction(action)
	case !mirrorResolutionKind(item.Kind):
		return fmt.Errorf("resolution apply does not support %s", item.Kind)
	}
	return validateMirrorResolutionApplyAction(item, action)
}

func validateSameNameResolutionApplyAction(item skillResolutionItem, action string) error {
	if sameNameApplyAction(action) && resolutionActionAllowed(item.AvailableActions, action) {
		return nil
	}
	return fmt.Errorf("resolution apply does not support same-name action %q", action)
}

func validateUnmanagedResolutionApplyAction(action string) error {
	if unmanagedProviderApplyAction(action) {
		return nil
	}
	return fmt.Errorf("resolution apply does not support unmanaged action %q", action)
}

func validateMirrorResolutionApplyAction(item skillResolutionItem, action string) error {
	if !resolutionActionAllowed(item.AvailableActions, action) {
		return fmt.Errorf("resolution action %q is not available for %s", action, item.Kind)
	}
	if item.Scope == skillScopeProject && !projectMirrorApplyAction(action) {
		return fmt.Errorf("resolution apply does not support project action %q", action)
	}
	return nil
}

func mirrorResolutionKind(kind string) bool {
	switch kind {
	case skillConflictMirrorDrift, skillConflictMultiMirrorDrift, skillConflictCanonicalDeletedWithDrift:
		return true
	default:
		return false
	}
}

func projectMirrorApplyAction(action string) bool {
	return action == ResolutionSyncBackCanonical ||
		action == ResolutionCanonicalOverwrite ||
		action == ResolutionSaveAsNewSkill ||
		action == ResolutionConfirmDeleteDriftedMirror
}

func unmanagedProviderApplyAction(action string) bool {
	return action == ResolutionImportPersonal || action == ResolutionImportProject || action == ResolutionTakeoverProvider
}
