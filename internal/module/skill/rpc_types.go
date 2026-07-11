package skill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/skill/mirrorpath"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/skill/summarysuggest"
	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
)

const (
	ResolutionViewDiff                   = "view_diff"
	ResolutionViewUnmanaged              = "view_unmanaged"
	ResolutionImportPersonal             = "import_to_personal_imported"
	ResolutionImportProject              = "import_to_project"
	ResolutionTakeoverProvider           = "takeover_provider_skill"
	ResolutionUseProjectSharedSkill      = "use_project_shared_skill"
	ResolutionUseExternalProviderSkill   = "use_external_provider_skill"
	ResolutionSaveAsNewSkill             = "save_as_new_skill"
	ResolutionSaveAsNewPersonal          = "save_as_new_personal_skill"
	ResolutionKeepExternalProviderSkill  = "keep_external_provider_skill"
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
	ResolutionReplaceProviderRootSymlink = "replace_provider_root_symlink"
)

const skillSummarySuggestPromptHeader = `你是 skill 简介生成助手。你的任务不是总结 skill 内容，而是生成一句“LLM 什么时候应该调用这个 skill”的使用场景描述。
严格输出一个中文 JSON：{"description":"当你需要……时使用。"}
规则：
1. description 必须只是一句话，并以“当你需要”开头、以“时使用。”结尾。
2. 描述用户任务触发场景，不写 skill 实现步骤；提炼 1 到 3 个最重要、最具体的调用场景。
3. 优先参考 description、scenario_words（适用场景关键词）、trigger_words、标题、正文前几段；目录名和文件名只作辅助。
4. 不要使用“这个技能”“本技能”“可以帮助”“用于”等表述；不要写“处理各种问题”“提升效率”“辅助开发”等泛泛描述。
5. 不要输出 Markdown、解释、候选项或多余文本；内容不明确时宁可偏窄但准确，不要泛化。

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
	return summarysuggest.ExecuteWithOptions(ctx, dream, prompt, contract.DreamOptions{
		Provider:      strings.TrimSpace(p.Provider),
		Model:         strings.TrimSpace(p.Model),
		ModelProvider: strings.TrimSpace(p.ModelProvider),
	}, parseSkillSummarySuggestionResult)
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
		return "", summarysuggest.MarkRetryable(fmt.Errorf("parse skill summary suggestion: %w", err))
	}
	description := strings.TrimSpace(out.Description)
	if description == "" {
		return "", summarysuggest.MarkRetryable(errors.New("skill summary suggestion is empty"))
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

// skillSummarySuggestionQualityIssue 处理技能摘要suggestionqualityissue。
func skillSummarySuggestionQualityIssue(description string) string {
	description = strings.TrimSpace(description)
	if description == "" {
		return "missing"
	}
	switch {
	case !strings.HasPrefix(description, "当你需要") || !strings.HasSuffix(description, "时使用。"):
		return "invalid_shape"
	case skillSummaryContainsAny(strings.ToLower(description), "这个技能", "本技能", "该技能", "此技能", "this skill"):
		return "self_referential"
	case skillSummaryContainsAny(description, "可以帮助", "可帮助", "帮助你", "帮你", "用于"):
		return "weak_wording"
	case skillSummaryLooksLikeWorkflow(description):
		return "workflow"
	case skillSummaryLooksTooGeneric(description):
		return "generic"
	case compactRuneLen(description) < 12:
		return "too_short"
	case compactRuneLen(description) > 120:
		return "too_long"
	default:
		return ""
	}
}

func skillSummaryContainsAny(description string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(description, term) {
			return true
		}
	}
	return false
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

// skillSummaryLooksLikeWorkflow 处理技能摘要lookslikeworkflow。
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

func compactRuneLen(value string) int {
	count := 0
	for field := range strings.FieldsSeq(value) {
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
	return action == ResolutionKeepSelected || action == ResolutionRenamePersonal
}

// applySkillResolution 应用技能resolution。
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
	if externalPersonalProjectApplyAction(p.Action) {
		return ResolveExternalPersonalProjectSameName(ctx, s, req)
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
	case ResolutionKeepSelected:
		return s.applyKeepSelectedResolution(ctx, report, item, preview, p)
	case ResolutionRenamePersonal:
		return s.applyRenameSameNameResolution(ctx, report, item, preview, p)
	default:
		return report, fmt.Errorf("unsupported same-name resolution action %q", p.Action)
	}
}

func (s *service) applyKeepSelectedResolution(ctx context.Context, report SkillMirrorResolutionReport, item skillResolutionItem, preview skillResolutionPreviewItem, p skillResolutionApplyParams) (SkillMirrorResolutionReport, error) {
	source, err := canonicalSourceByID(item.Sources, p.KeepSourceID)
	if err != nil {
		return report, err
	}
	if err := validateSameNameSourcePreview(preview, source.Path, source.Path); err != nil {
		return report, err
	}
	hash, err := s.removeSameNameDuplicateSources(p.CWD, item, source)
	if err != nil {
		return report, err
	}
	report.ResultingHash = hash
	markSameNamePolicyMirrorPublish(ctx, s, p.CWD, item.Name, &report)
	publishSameNameKeepSelectedChanged(ctx, s, p.Action, item.Name, source)
	return report, nil
}

// removeSameNameDuplicateSources 移除same名称duplicatesources。
func (s *service) removeSameNameDuplicateSources(cwd string, item skillResolutionItem, selected skillResolutionSource) (string, error) {
	if selected.Scope != skillScopeProject && selected.Scope != skillScopePersonal {
		return "", fmt.Errorf("keep selected requires a project or personal skill")
	}
	if err := ensureProviderSkillDirSafe(filepath.FromSlash(selected.Path)); err != nil {
		return "", err
	}
	for _, source := range item.Sources {
		if source.CanonicalID == selected.CanonicalID {
			continue
		}
		record := canonicalSkillRecord{Name: item.Name, Scope: source.Scope, PersonalType: source.PersonalType, Dir: filepath.FromSlash(source.Path)}
		for _, target := range s.writeTimeMirrorTargets(cwd, source.Scope) {
			if _, _, err := cleanupSuppressedPersonalMirrorRecord(target, record); err != nil {
				return "", err
			}
		}
		if err := removeSameNameDuplicateSource(source); err != nil {
			return "", err
		}
	}
	return skillDirContentHash(filepath.FromSlash(selected.Path))
}

func removeSameNameDuplicateSource(source skillResolutionSource) error {
	if source.Scope != skillScopeProject && source.Scope != skillScopePersonal {
		return fmt.Errorf("remove same-name source requires a project or personal skill")
	}
	dir := filepath.FromSlash(source.Path)
	if err := ensureProviderSkillDirSafe(dir); err != nil {
		return err
	}
	if _, err := backupSkillDir(dir); err != nil {
		return err
	}
	return os.RemoveAll(dir)
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

func (s *service) applyRenameSameNameResolution(ctx context.Context, report SkillMirrorResolutionReport, item skillResolutionItem, preview skillResolutionPreviewItem, p skillResolutionApplyParams) (SkillMirrorResolutionReport, error) {
	source, err := canonicalSourceByID(item.Sources, p.KeepSourceID)
	if err != nil {
		return report, err
	}
	name, err := validateSkillName(p.NewName)
	if err != nil {
		return report, err
	}
	targetPath := filepath.ToSlash(filepath.Join(filepath.Dir(filepath.FromSlash(source.Path)), name))
	if err := validateSameNameSourcePreview(preview, source.Path, targetPath); err != nil {
		return report, err
	}
	hash, err := renameSameNameSource(source, filepath.FromSlash(targetPath), name)
	if err != nil {
		return report, err
	}
	report.ResultingHash = hash
	markSameNamePolicyMirrorPublish(ctx, s, p.CWD, item.Name, &report)
	publishSameNameKeepSelectedChanged(ctx, s, p.Action, item.Name, source)
	publishSameNameKeepSelectedChanged(ctx, s, p.Action, name, source)
	return report, nil
}

// renameSameNameSource 处理重命名same名称source。
func renameSameNameSource(source skillResolutionSource, targetDir, newName string) (string, error) {
	if source.Scope != skillScopeProject && source.Scope != skillScopePersonal {
		return "", fmt.Errorf("rename same-name source requires a project or personal skill")
	}
	dir := filepath.FromSlash(source.Path)
	if err := ensureProviderSkillDirSafe(dir); err != nil {
		return "", err
	}
	if err := mirrorpath.RejectSymlinkAncestors(filepath.Dir(targetDir)); err != nil {
		return "", err
	}
	if _, err := os.Stat(targetDir); err == nil {
		return "", fmt.Errorf("new skill target already exists: %s", targetDir)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if _, err := backupSkillDir(dir); err != nil {
		return "", err
	}
	if err := os.Rename(dir, targetDir); err != nil {
		return "", err
	}
	if err := rewriteCopiedSkillIdentity(targetDir, newName, ""); err != nil {
		_ = os.Rename(targetDir, dir)
		return "", err
	}
	return skillDirContentHash(targetDir)
}

func validateSameNameSourcePreview(preview skillResolutionPreviewItem, sourcePath, targetPath string) error {
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

// validateResolutionApplyAction 校验 resolution apply 动作是否适用于当前冲突类型。
func validateResolutionApplyAction(item skillResolutionItem, action string) error {
	switch {
	case item.Kind == skillConflictSameName:
		return validateSameNameResolutionApplyAction(item, action)
	case item.Kind == skillConflictUnmanagedSameName && item.Scope == skillScopeProject && projectMirrorApplyAction(action):
		return validateMirrorResolutionApplyAction(item, action)
	case item.Kind == skillConflictExternalPersonalProjectSameName:
		return validateExternalPersonalProjectResolutionApplyAction(item, action)
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

func validateExternalPersonalProjectResolutionApplyAction(item skillResolutionItem, action string) error {
	if externalPersonalProjectApplyAction(action) && resolutionActionAllowed(item.AvailableActions, action) {
		return nil
	}
	return fmt.Errorf("resolution apply does not support external personal project action %q", action)
}

// validateMirrorResolutionApplyAction 校验镜像resolution应用动作。
func validateMirrorResolutionApplyAction(item skillResolutionItem, action string) error {
	if !resolutionActionAllowed(item.AvailableActions, action) {
		return fmt.Errorf("resolution action %q is not available for %s", action, item.Kind)
	}
	if action == ResolutionConfirmDeleteDriftedMirror && item.Kind != skillConflictCanonicalDeletedWithDrift {
		return fmt.Errorf("resolution action %q requires deleted canonical drift", action)
	}
	if item.Scope == skillScopeProject && !projectMirrorApplyAction(action) {
		return fmt.Errorf("resolution apply does not support project action %q", action)
	}
	return nil
}

func mirrorResolutionKind(kind string) bool {
	switch kind {
	case skillConflictMirrorDrift, skillConflictMultiMirrorDrift, skillConflictCanonicalDeletedWithDrift, skillConflictMirrorRootSymlink:
		return true
	default:
		return false
	}
}

func projectMirrorApplyAction(action string) bool {
	return action == ResolutionSyncBackCanonical ||
		action == ResolutionCanonicalOverwrite ||
		action == ResolutionSaveAsNewSkill ||
		action == ResolutionConfirmDeleteDriftedMirror ||
		action == ResolutionReplaceProviderRootSymlink
}

func unmanagedProviderApplyAction(action string) bool {
	return action == ResolutionImportPersonal || action == ResolutionImportProject || action == ResolutionTakeoverProvider
}

func externalPersonalProjectApplyAction(action string) bool {
	return action == ResolutionUseProjectSharedSkill ||
		action == ResolutionUseExternalProviderSkill ||
		action == ResolutionSaveAsNewPersonal
}
