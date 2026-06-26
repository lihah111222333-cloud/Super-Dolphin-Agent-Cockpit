package skill

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

type pathParams struct {
	Path string `json:"path"`
	CWD  string `json:"cwd,omitempty"`
}

type contentParams struct {
	Path         string `json:"path"`
	Content      string `json:"content"`
	Scope        string `json:"scope,omitempty"`
	PersonalType string `json:"personal_type,omitempty"`
	CWD          string `json:"cwd,omitempty"`
}

type listSkillFilesParams struct {
	Dir  string `json:"dir"`
	Path string `json:"path,omitempty"`
	CWD  string `json:"cwd,omitempty"`
}

type importSkillDirParams struct {
	Path         string   `json:"path"`
	Paths        []string `json:"paths,omitempty"`
	Name         string   `json:"name,omitempty"`
	Mode         string   `json:"mode,omitempty"`
	Scope        string   `json:"scope,omitempty"`
	PersonalType string   `json:"personal_type,omitempty"`
	CWD          string   `json:"cwd,omitempty"`
}

type deleteLocalSkillParams struct {
	Name         string `json:"name"`
	Scope        string `json:"scope,omitempty"`
	PersonalType string `json:"personal_type,omitempty"`
	CWD          string `json:"cwd,omitempty"`
}

// createSkillParams 是 `skills/create` 的 wire 入参。
// 该 RPC 只创建 project scope skill；调用方必须传 cwd，实际写入仍复用 WriteLocal 的单一路径。
type createSkillParams struct {
	Name    string `json:"name"`
	Content string `json:"content"`
	CWD     string `json:"cwd"`
}

type skillConfigReadParams struct {
	AgentID string `json:"agent_id"`
}

type skillNamedContentParams struct {
	Name         string `json:"name"`
	Content      string `json:"content"`
	Scope        string `json:"scope,omitempty"`
	PersonalType string `json:"personal_type,omitempty"`
	CWD          string `json:"cwd,omitempty"`
}

type skillSummaryWriteParams struct {
	Name         string `json:"name"`
	Summary      string `json:"summary"`
	Scope        string `json:"scope,omitempty"`
	PersonalType string `json:"personal_type,omitempty"`
	CWD          string `json:"cwd,omitempty"`
}
type skillSummarySuggestParams struct {
	CWD           string   `json:"cwd,omitempty"`
	Name          string   `json:"name,omitempty"`
	Description   string   `json:"description,omitempty"`
	Content       string   `json:"content,omitempty"`
	ScenarioWords []string `json:"scenario_words,omitempty"`
	Scope         string   `json:"scope,omitempty"`
	contract.DreamOptions
}

type skillSummarySuggestResult struct {
	Description string `json:"description"`
}

type skillRemoteReadParams struct {
	URL string `json:"url"`
}

type UserInput struct {
	Type    string `json:"type"`
	Text    string `json:"text,omitempty"`
	URL     string `json:"url,omitempty"`
	Path    string `json:"path,omitempty"`
	Name    string `json:"name,omitempty"`
	Content string `json:"content,omitempty"`
}

type skillMatchPreviewParams struct {
	ThreadID string      `json:"threadId,omitempty"`
	AgentID  string      `json:"agent_id,omitempty"` // threadId 为空时用于兼容旧调用方的匹配上下文
	Text     string      `json:"text"`
	Input    []UserInput `json:"input,omitempty"`
	CWD      string      `json:"cwd,omitempty"`
}

type skillListParams struct {
	CWD string `json:"cwd,omitempty"`
}

type skillListItem struct {
	Name                   string     `json:"name"`
	DisplayName            string     `json:"display_name,omitempty"`
	Dir                    string     `json:"dir,omitempty"`
	SkillFile              string     `json:"skill_file,omitempty"`
	Scope                  string     `json:"scope,omitempty"`
	PersonalType           string     `json:"personal_type,omitempty"`
	Summary                string     `json:"summary"`
	Description            string     `json:"description"`
	Trust                  TrustScope `json:"trust"`
	ContentHash            string     `json:"content_hash"`
	DisableModelInvocation bool       `json:"disable_model_invocation"`
}

type skillListResult struct {
	Skills []skillListItem `json:"skills"`
}

func (s *service) listSkillResolutions(cwd string) (skillResolutionListResult, error) {
	superHome := s.resolvedSuperDolphinHome()
	store := newCanonicalStoreForOwner(superHome, defaultOwnerOSUID(), defaultAppProfile())
	rawRecords, canonicalConflicts, effectiveRecords, err := resolutionCanonicalSnapshot(store, cwd)
	if err != nil {
		return skillResolutionListResult{}, err
	}
	targets := s.resolutionMirrorTargets(cwd)
	mirrorConflicts, err := DetectSkillMirrorConflicts(effectiveRecords, targets)
	if err != nil {
		return skillResolutionListResult{}, err
	}
	recordByID := canonicalRecordsByID(rawRecords)
	items := canonicalResolutionItems(canonicalConflicts)
	items = append(items, mirrorResolutionItems(mirrorConflicts, recordByID, superHome)...)
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Kind != items[j].Kind {
			return items[i].Kind < items[j].Kind
		}
		return items[i].Name < items[j].Name
	})
	return skillResolutionListResult{Items: items}, nil
}

func resolutionCanonicalSnapshot(store *canonicalStore, cwd string) ([]canonicalSkillRecord, []canonicalSkillConflict, []canonicalSkillRecord, error) {
	records, err := store.scan(cwd)
	if err != nil {
		return nil, nil, nil, err
	}
	effectiveRecords, err := store.applyEffectivePolicies(cwd, records)
	if err != nil {
		return nil, nil, nil, err
	}
	conflicts := canonicalSameNameConflicts(records)
	return records, conflicts, canonicalRecordsWithoutConflicts(effectiveRecords, conflicts), nil
}
func (s *service) resolutionMirrorTargets(cwd string) []SkillMirrorTarget {
	projectRoot := s.projectRootForCWD(cwd)
	fingerprint := RepoFingerprint(projectRoot)
	superHome := s.resolvedSuperDolphinHome()
	ownerKey := "sd_owner:current"
	if owner, err := resolveOwnerIdentity(superHome, defaultOwnerOSUID(), defaultAppProfile()); err == nil {
		ownerKey = owner.OwnerKey
	}
	return []SkillMirrorTarget{
		{TargetID: "claude:project:" + fingerprint, Provider: SkillProviderClaude, Scope: skillScopeProject, Root: providerProjectMirrorRoot(SkillProviderClaude, projectRoot), CanonicalRootID: fingerprint},
		{TargetID: "codex:project:" + fingerprint, Provider: SkillProviderCodex, Scope: skillScopeProject, Root: providerProjectMirrorRoot(SkillProviderCodex, projectRoot), CanonicalRootID: fingerprint},
		{TargetID: "claude:user-global:" + ownerKey, Provider: SkillProviderClaude, Scope: skillScopePersonal, Root: providerPersonalMirrorRoot(SkillProviderClaude), CanonicalRootID: ownerKey},
		{TargetID: "codex:user-global:" + ownerKey, Provider: SkillProviderCodex, Scope: skillScopePersonal, Root: providerPersonalMirrorRoot(SkillProviderCodex), CanonicalRootID: ownerKey},
	}
}
func canonicalRecordsByID(records []canonicalSkillRecord) map[string]canonicalSkillRecord {
	out := make(map[string]canonicalSkillRecord, len(records))
	for _, record := range records {
		out[canonicalSourceID(record)] = record
	}
	return out
}

func canonicalResolutionItems(conflicts []canonicalSkillConflict) []skillResolutionItem {
	items := make([]skillResolutionItem, 0, len(conflicts))
	for _, conflict := range conflicts {
		item := skillResolutionItem{Kind: skillConflictSameName, Name: conflict.Name}
		for _, source := range conflict.Sources {
			item.Sources = append(item.Sources, skillResolutionSource{
				Scope:         source.Scope,
				PersonalType:  source.PersonalType,
				CanonicalID:   canonicalSourceID(canonicalSkillRecord{Name: source.Name, Scope: source.Scope, PersonalType: source.PersonalType, Dir: source.Dir}),
				ContentHash:   source.ContentHash,
				CanonicalHash: source.DirHash,
				Path:          filepath.ToSlash(source.Dir),
				SkillFile:     filepath.ToSlash(source.SkillFile),
			})
		}
		item.AvailableActions = sameNameResolutionActions(item.Sources)
		item.ConflictID = resolutionConflictID(item)
		items = append(items, item)
	}
	return items
}

func mirrorResolutionItem(conflict SkillMirrorConflict, records map[string]canonicalSkillRecord, superHome string) skillResolutionItem {
	record := records[conflict.CanonicalID]
	targetPath := filepath.ToSlash(record.Dir)
	if targetPath == "" {
		targetPath = deletedCanonicalTargetPath(conflict, superHome)
	}
	if conflict.Kind == skillConflictMirrorRootSymlink {
		targetPath = filepath.ToSlash(conflict.MirrorPath)
	}
	item := skillResolutionItem{
		Kind:             conflict.Kind,
		Scope:            conflict.Scope,
		PersonalType:     conflict.PersonalType,
		Name:             conflict.Name,
		AvailableActions: mirrorResolutionActions(conflict),
	}
	item.ProviderEntries = append(item.ProviderEntries, skillResolutionProviderEntry{
		Provider:     string(conflict.Provider),
		SourcePath:   filepath.ToSlash(conflict.MirrorPath),
		TargetPath:   targetPath,
		SourceHash:   conflict.MirrorHash,
		TargetHash:   resolutionTargetHash(conflict, record),
		TargetID:     conflict.TargetID,
		SourcePathID: "provider:" + string(conflict.Provider),
	})
	item.ConflictID = resolutionConflictID(item)
	return item
}

func deletedCanonicalTargetPath(conflict SkillMirrorConflict, superHome string) string {
	name := strings.TrimSpace(conflict.Name)
	mirrorRoot := filepath.Dir(filepath.FromSlash(conflict.MirrorPath))
	if conflict.Scope == skillScopePersonal {
		personalType := strings.TrimSpace(conflict.PersonalType)
		if personalType == "" {
			personalType = personalTypeFromCanonicalID(conflict.CanonicalID)
		}
		return filepath.ToSlash(filepath.Join(superHome, "skills", "personal", personalType, name))
	}
	repoRoot := filepath.Dir(filepath.Dir(mirrorRoot))
	return filepath.ToSlash(filepath.Join(defaultProjectSkillsRoot(repoRoot), name))
}

func personalTypeFromCanonicalID(canonicalID string) string {
	parts := strings.Split(filepath.ToSlash(canonicalID), "/")
	if len(parts) >= 2 && parts[0] == skillScopePersonal {
		return parts[1]
	}
	return ""
}

func mirrorResolutionItems(conflicts []SkillMirrorConflict, records map[string]canonicalSkillRecord, superHome string) []skillResolutionItem {
	items := make([]skillResolutionItem, 0, len(conflicts))
	seen := map[string]int{}
	for _, conflict := range conflicts {
		if _, ok := records[conflict.CanonicalID]; ok && conflict.Kind == skillConflictCanonicalDeletedWithDrift {
			continue
		}
		item := mirrorResolutionItem(conflict, records, superHome)
		key := mirrorResolutionItemKey(conflict)
		if idx, ok := seen[key]; ok {
			items[idx].ProviderEntries = append(items[idx].ProviderEntries, item.ProviderEntries...)
			items[idx].ConflictID = resolutionConflictID(items[idx])
			continue
		}
		seen[key] = len(items)
		items = append(items, item)
	}
	return items
}

func mirrorResolutionItemKey(conflict SkillMirrorConflict) string {
	return strings.Join([]string{
		conflict.Kind,
		conflict.Scope,
		conflict.PersonalType,
		conflict.Name,
		conflict.CanonicalID,
	}, "\x00")
}

func resolutionTargetHash(conflict SkillMirrorConflict, record canonicalSkillRecord) string {
	if record.DirHash != "" {
		return record.DirHash
	}
	return conflict.CanonicalHash
}

func sameNameResolutionActions(sources []skillResolutionSource) []string {
	actions := []string{ResolutionViewDiff}
	if len(sources) > 1 {
		return append(actions, ResolutionKeepSelected, ResolutionRenamePersonal)
	}
	return actions
}

// mirrorResolutionActions 根据 mirror 冲突类型返回前端可展示的动作集合。
// 如果冲突自带 Actions，以后端检测结果为准；否则使用当前 scope 的默认 drift 处理动作。
func mirrorResolutionActions(conflict SkillMirrorConflict) []string {
	switch conflict.Kind {
	case skillConflictExternalPersonalProjectSameName:
		return resolutionActionNames(conflict.Actions)
	case skillConflictUnmanagedSameName, skillConflictUnmanagedProviderSkill:
		if len(conflict.Actions) > 0 {
			return resolutionActionNames(conflict.Actions)
		}
		return []string{ResolutionViewUnmanaged, ResolutionImportPersonal, ResolutionTakeoverProvider}
	case skillConflictCanonicalDeletedWithDrift:
		if conflict.Scope == skillScopePersonal {
			return []string{ResolutionViewDiff, ResolutionSaveAsNewPersonal, ResolutionSyncBackPersonal, ResolutionConfirmDeleteDriftedMirror}
		}
		return []string{ResolutionViewDiff, ResolutionSaveAsNewSkill, ResolutionSyncBackCanonical, ResolutionConfirmDeleteDriftedMirror}
	case skillConflictMirrorRootSymlink:
		return []string{ResolutionViewUnmanaged, ResolutionReplaceProviderRootSymlink}
	default:
		return driftResolutionActions(conflict.Scope)
	}
}

func resolutionActionNames(actions []SkillMirrorResolutionAction) []string {
	out := make([]string, 0, len(actions))
	for _, action := range actions {
		if name := strings.TrimSpace(action.Action); name != "" {
			out = append(out, name)
		}
	}
	return out
}

func driftResolutionActions(scope string) []string {
	if scope == skillScopePersonal {
		return []string{ResolutionViewDiff, ResolutionSaveAsNewPersonal, ResolutionSyncBackPersonal, ResolutionPersonalOverwrite}
	}
	return []string{ResolutionViewDiff, ResolutionSaveAsNewSkill, ResolutionSyncBackCanonical, ResolutionCanonicalOverwrite}
}

func resolutionConflictID(item skillResolutionItem) string {
	type conflictIDEnvelope struct {
		Kind      string                         `json:"kind"`
		Scope     string                         `json:"scope,omitempty"`
		Type      string                         `json:"personal_type,omitempty"`
		Name      string                         `json:"name"`
		Providers []skillResolutionProviderEntry `json:"providers,omitempty"`
		Sources   []skillResolutionSource        `json:"sources,omitempty"`
	}
	return "sha256:" + hashResolutionEnvelope(conflictIDEnvelope{
		Kind:      item.Kind,
		Scope:     item.Scope,
		Type:      item.PersonalType,
		Name:      item.Name,
		Providers: item.ProviderEntries,
		Sources:   item.Sources,
	})
}

// previewSkillResolution 为用户选择的冲突处理动作生成可确认预览。
// 只有 action 属于当前 conflict 的 AvailableActions 时才会生成 preview 并缓存 preview_id。
func (s *service) previewSkillResolution(p skillResolutionPreviewParams) (skillResolutionPreviewResult, error) {
	p.Action = normalizeResolutionAction(p.Action)
	list, err := s.listSkillResolutions(p.CWD)
	if err != nil {
		return skillResolutionPreviewResult{}, err
	}
	item, ok := findResolutionItemByID(list.Items, p.ConflictID)
	if !ok {
		return skillResolutionPreviewResult{}, fmt.Errorf("resolution conflict not found: %s", p.ConflictID)
	}
	if !resolutionActionAllowed(item.AvailableActions, p.Action) {
		return skillResolutionPreviewResult{}, fmt.Errorf("resolution action %q is not available for %s", p.Action, item.Kind)
	}
	previews, err := buildResolutionPreviewItems(item, p, s.resolvedSuperDolphinHome())
	if err != nil {
		return skillResolutionPreviewResult{}, err
	}
	for i := range previews {
		previews[i] = s.storeResolutionPreview(item.ConflictID, previews[i])
	}
	return skillResolutionPreviewResult{ConflictID: item.ConflictID, Kind: item.Kind, Items: previews}, nil
}

// storeResolutionPreview 缓存一次 mirror/canonical 修复预览。
// preview_id 带随机 nonce 且定时清理过期项，apply 阶段必须带回匹配的 preview_hash。
func (s *service) storeResolutionPreview(conflictID string, preview skillResolutionPreviewItem) skillResolutionPreviewItem {
	if s == nil || preview.PreviewHash == "" {
		return preview
	}
	now := time.Now().UTC()
	preview.PreviewID = "resolution-preview:" + hashResolutionEnvelope(map[string]string{
		"conflict_id":  conflictID,
		"action":       preview.Action,
		"preview_hash": preview.PreviewHash,
		"nonce":        fmt.Sprintf("%d", now.UnixNano()),
	})[:16]
	s.resolutionPreviewMu.Lock()
	defer s.resolutionPreviewMu.Unlock()
	if s.resolutionPreviews == nil {
		s.resolutionPreviews = map[string]skillResolutionStoredPreview{}
	}
	for id, stored := range s.resolutionPreviews {
		if now.After(stored.ExpiresAt) {
			delete(s.resolutionPreviews, id)
		}
	}
	s.resolutionPreviews[preview.PreviewID] = skillResolutionStoredPreview{Item: preview, ConflictID: conflictID, Action: preview.Action, ExpiresAt: now.Add(15 * time.Minute)}
	return preview
}

func canonicalResolutionPreviewItem(item skillResolutionItem, p skillResolutionPreviewParams) (skillResolutionPreviewItem, error) {
	source, target, err := canonicalResolutionPreviewSources(item, p)
	if err != nil {
		return skillResolutionPreviewItem{}, err
	}
	preview := skillResolutionPreviewItem{Action: p.Action, SourcePath: source.Path, TargetPath: target.Path, SourceHash: source.CanonicalHash, TargetHash: target.CanonicalHash}
	if p.IncludeDiff || p.Action == ResolutionViewDiff {
		preview.Diff = resolutionPreviewDiff(preview)
	}
	if p.Action == ResolutionViewDiff {
		return preview, nil
	}
	preview.BackupPath = resolutionPreviewBackupPath(preview.TargetPath, item, p)
	preview.PreviewHash = resolutionPreviewHash(item, preview, p)
	return preview, nil
}

// canonicalResolutionPreviewSources 为 canonical 同名冲突选择预览源和目标。
// 重命名、保留指定来源和手工合并分别有不同必填字段，缺失时直接返回错误。
func canonicalResolutionPreviewSources(item skillResolutionItem, p skillResolutionPreviewParams) (skillResolutionSource, skillResolutionSource, error) {
	if len(item.Sources) < 2 {
		return skillResolutionSource{}, skillResolutionSource{}, fmt.Errorf("canonical resolution preview requires at least two sources")
	}
	switch p.Action {
	case ResolutionRenamePersonal, ResolutionRenamePersonalType:
		return renamePreviewSources(item.Sources, p.KeepSourceID, p.NewName)
	case ResolutionKeepSelected:
		source, err := canonicalSourceByID(item.Sources, p.KeepSourceID)
		if err != nil {
			return source, skillResolutionSource{}, err
		}
		return source, source, nil
	case ResolutionMergeManually:
		if strings.TrimSpace(p.MergeContentHash) == "" {
			return skillResolutionSource{}, skillResolutionSource{}, fmt.Errorf("merge_content_hash is required for merge_manually")
		}
	}
	return item.Sources[0], item.Sources[1], nil
}

func renamePreviewSources(sources []skillResolutionSource, keepSourceID, newName string) (skillResolutionSource, skillResolutionSource, error) {
	if strings.TrimSpace(keepSourceID) == "" {
		return renamePersonalPreviewSources(sources, newName)
	}
	source, err := canonicalSourceByID(sources, keepSourceID)
	if err != nil {
		return source, skillResolutionSource{}, err
	}
	name, err := validateSkillName(newName)
	if err != nil {
		return skillResolutionSource{}, skillResolutionSource{}, fmt.Errorf("new_name is required for rename: %w", err)
	}
	target := source
	target.Path = filepath.ToSlash(filepath.Join(filepath.Dir(filepath.FromSlash(source.Path)), name))
	target.CanonicalHash = ""
	return source, target, nil
}

func renamePersonalPreviewSources(sources []skillResolutionSource, newName string) (skillResolutionSource, skillResolutionSource, error) {
	name, err := validateSkillName(newName)
	if err != nil {
		return skillResolutionSource{}, skillResolutionSource{}, fmt.Errorf("new_name is required for rename: %w", err)
	}
	for _, source := range sources {
		if source.Scope == skillScopePersonal {
			target := source
			target.Path = filepath.ToSlash(filepath.Join(filepath.Dir(source.Path), name))
			target.CanonicalHash = ""
			return source, target, nil
		}
	}
	return skillResolutionSource{}, skillResolutionSource{}, fmt.Errorf("personal source is required for rename")
}

func canonicalSourceByID(sources []skillResolutionSource, id string) (skillResolutionSource, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return skillResolutionSource{}, fmt.Errorf("source id is required")
	}
	for _, source := range sources {
		if source.CanonicalID == id {
			return source, nil
		}
	}
	return skillResolutionSource{}, fmt.Errorf("source id %q is not part of conflict", id)
}

func syncBackResolutionAction(action string) bool {
	return action == ResolutionSyncBackCanonical || action == ResolutionSyncBackPersonal
}

func overwriteResolutionAction(action string) bool {
	return action == ResolutionCanonicalOverwrite || action == ResolutionPersonalOverwrite
}

func sameResolutionSourceHashes(entries []skillResolutionProviderEntry) bool {
	if len(entries) < 2 {
		return true
	}
	first := entries[0].SourceHash
	for _, entry := range entries[1:] {
		if entry.SourceHash != first {
			return false
		}
	}
	return true
}

func resolutionPreviewBackupPath(targetPath string, item skillResolutionItem, p skillResolutionPreviewParams) string {
	seed := hashResolutionEnvelope(map[string]string{"conflict_id": item.ConflictID, "action": p.Action, "provider": p.Provider, "target_path": targetPath})
	return filepath.ToSlash(mirrorBackupPathForTargetDir(targetPath, filepath.Base(targetPath)+"-preview-"+seed[:12]))
}

func resolutionPreviewDiff(preview skillResolutionPreviewItem) string {
	return fmt.Sprintf("source %s %s\ntarget %s %s", preview.SourceHash, preview.SourcePath, preview.TargetHash, preview.TargetPath)
}

func findResolutionItemByID(items []skillResolutionItem, id string) (skillResolutionItem, bool) {
	for _, item := range items {
		if item.ConflictID == id {
			return item, true
		}
	}
	return skillResolutionItem{}, false
}

func resolutionActionAllowed(actions []string, action string) bool {
	for _, allowed := range actions {
		if allowed == action {
			return true
		}
	}
	return false
}
