package skill

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
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

// createSkillParams is the input to skills/create. It is the host-side entry
// point for project-scope self-learning writes (P21 P0a): the caller supplies
// a skill slug and SKILL.md content, scope is always project, and cwd is a
// required field. CreateSkill is a thin wrapper over WriteLocal — the second
// writer path for project scope is explicitly forbidden by the P21 plan.
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
	AgentID  string      `json:"agent_id,omitempty"` // Falls back when threadId is empty.
	Text     string      `json:"text"`
	Input    []UserInput `json:"input,omitempty"`
	CWD      string      `json:"cwd,omitempty"`
}

type skillListParams struct {
	CWD string `json:"cwd,omitempty"`
}

type skillListItem struct {
	Name                   string     `json:"name"`
	Scope                  string     `json:"scope,omitempty"`
	PersonalType           string     `json:"personal_type,omitempty"`
	Summary                string     `json:"summary"`
	Description            string     `json:"description"`
	Trust                  TrustScope `json:"trust"`
	ContentHash            string     `json:"content_hash"`
	DisableModelInvocation bool       `json:"disable_model_invocation"`
	DisclosureTier         string     `json:"disclosure_tier,omitempty"`
}

type skillListResult struct {
	Skills []skillListItem `json:"skills"`
}

type skillExpandParams struct {
	Name          string `json:"name"`
	Section       string `json:"section,omitempty"`
	MaxBytes      int64  `json:"max_bytes,omitempty"`
	CWD           string `json:"cwd,omitempty"`
	ApprovalScope string `json:"approval_scope,omitempty"`
	Scope         string `json:"scope,omitempty"`
	AgentID       string `json:"agentId,omitempty"`
	ThreadID      string `json:"threadId,omitempty"`
	SessionID     string `json:"sessionId,omitempty"`
	TurnID        string `json:"turnId,omitempty"`
}

type skillExpandResult struct {
	Name        string     `json:"name"`
	Section     string     `json:"section"`
	Path        string     `json:"path"`
	Summary     string     `json:"summary"`
	Content     string     `json:"content"`
	Truncated   bool       `json:"truncated"`
	TotalBytes  int64      `json:"total_bytes"`
	ContentHash string     `json:"content_hash"`
	Trust       TrustScope `json:"trust"`
}

func (s *service) listSkillResolutions(cwd string) (skillResolutionListResult, error) {
	store := newCanonicalStoreForOwner(s.resolvedSuperDolphinHome(), defaultOwnerOSUID(), defaultAppProfile())
	records, canonicalConflicts, err := resolutionCanonicalSnapshot(store, cwd)
	if err != nil {
		return skillResolutionListResult{}, err
	}
	targets := s.resolutionMirrorTargets(cwd)
	mirrorConflicts, err := DetectSkillMirrorConflicts(records, targets)
	if err != nil {
		return skillResolutionListResult{}, err
	}
	recordByID := canonicalRecordsByID(records)
	items := canonicalResolutionItems(canonicalConflicts)
	items = append(items, mirrorResolutionItems(mirrorConflicts, recordByID)...)
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Kind != items[j].Kind {
			return items[i].Kind < items[j].Kind
		}
		return items[i].Name < items[j].Name
	})
	return skillResolutionListResult{Items: items}, nil
}

func resolutionCanonicalSnapshot(store *canonicalStore, cwd string) ([]canonicalSkillRecord, []canonicalSkillConflict, error) {
	records, err := store.scan(cwd)
	if err != nil {
		return nil, nil, err
	}
	records, err = store.applyEffectivePolicies(cwd, records)
	if err != nil {
		return nil, nil, err
	}
	return records, canonicalSameNameConflicts(records), nil
}

func (s *service) resolutionMirrorTargets(cwd string) []SkillMirrorTarget {
	fingerprint := RepoFingerprint(cwd)
	superHome := s.resolvedSuperDolphinHome()
	ownerKey := "sd_owner:current"
	if owner, err := resolveOwnerIdentity(superHome, defaultOwnerOSUID(), defaultAppProfile()); err == nil {
		ownerKey = owner.OwnerKey
	}
	return []SkillMirrorTarget{
		{TargetID: "claude:project:" + fingerprint, Provider: SkillProviderClaude, Scope: skillScopeProject, Root: filepath.Join(cwd, ".claude", "skills"), CanonicalRootID: fingerprint},
		{TargetID: "codex:project:" + fingerprint, Provider: SkillProviderCodex, Scope: skillScopeProject, Root: filepath.Join(cwd, ".codex", "skills"), CanonicalRootID: fingerprint},
		{TargetID: "claude:app-managed:" + ownerKey, Provider: SkillProviderClaude, Scope: skillScopePersonal, Root: filepath.Join(superHome, "providers", "claude", "skills"), CanonicalRootID: ownerKey},
		{TargetID: "codex:app-managed:" + ownerKey, Provider: SkillProviderCodex, Scope: skillScopePersonal, Root: filepath.Join(superHome, "providers", "codex", "skills"), CanonicalRootID: ownerKey},
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
				CanonicalID:   canonicalSourceID(canonicalSkillRecord{Name: source.Name, Scope: source.Scope, PersonalType: source.PersonalType}),
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

func mirrorResolutionItem(conflict SkillMirrorConflict, records map[string]canonicalSkillRecord) skillResolutionItem {
	record := records[conflict.CanonicalID]
	targetPath := filepath.ToSlash(record.Dir)
	if targetPath == "" {
		targetPath = deletedCanonicalTargetPath(conflict)
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

func deletedCanonicalTargetPath(conflict SkillMirrorConflict) string {
	name := strings.TrimSpace(conflict.Name)
	mirrorRoot := filepath.Dir(filepath.FromSlash(conflict.MirrorPath))
	if conflict.Scope == skillScopePersonal {
		superHome := filepath.Dir(filepath.Dir(filepath.Dir(mirrorRoot)))
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

func mirrorResolutionItems(conflicts []SkillMirrorConflict, records map[string]canonicalSkillRecord) []skillResolutionItem {
	items := make([]skillResolutionItem, 0, len(conflicts))
	seen := map[string]int{}
	for _, conflict := range conflicts {
		item := mirrorResolutionItem(conflict, records)
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
	hasProject, hasPersonal := false, false
	for _, source := range sources {
		hasProject = hasProject || source.Scope == skillScopeProject
		hasPersonal = hasPersonal || source.Scope == skillScopePersonal
	}
	if hasProject && hasPersonal {
		return []string{ResolutionViewDiff, ResolutionRenamePersonal, ResolutionDisablePersonalForProject}
	}
	if hasPersonal {
		return []string{ResolutionViewDiff, ResolutionRenamePersonalType, ResolutionMergeManually, ResolutionKeepSelected}
	}
	return []string{ResolutionViewDiff}
}

func mirrorResolutionActions(conflict SkillMirrorConflict) []string {
	switch conflict.Kind {
	case skillConflictUnmanagedSameName:
		return []string{ResolutionViewUnmanaged, ResolutionImportPersonal, ResolutionTakeoverProvider}
	case skillConflictCanonicalDeletedWithDrift:
		if conflict.Scope == skillScopePersonal {
			return []string{ResolutionViewDiff, ResolutionSaveAsNewPersonal, ResolutionSyncBackPersonal, ResolutionConfirmDeleteDriftedMirror}
		}
		return []string{ResolutionViewDiff, ResolutionSaveAsNewSkill, ResolutionSyncBackCanonical, ResolutionConfirmDeleteDriftedMirror}
	default:
		return driftResolutionActions(conflict.Scope)
	}
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

func (s *service) previewSkillResolution(p skillResolutionPreviewParams) (skillResolutionPreviewResult, error) {
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

func canonicalResolutionPreviewSources(item skillResolutionItem, p skillResolutionPreviewParams) (skillResolutionSource, skillResolutionSource, error) {
	if len(item.Sources) < 2 {
		return skillResolutionSource{}, skillResolutionSource{}, fmt.Errorf("canonical resolution preview requires at least two sources")
	}
	switch p.Action {
	case ResolutionRenamePersonal, ResolutionRenamePersonalType:
		return renamePersonalPreviewSources(item.Sources, p.NewName)
	case ResolutionDisablePersonalForProject:
		source, err := canonicalSourceByID(item.Sources, p.DisablePolicyTarget)
		return source, skillResolutionSource{Path: filepath.ToSlash(filepath.Join(defaultProjectSkillsRoot(p.CWD), projectSkillPolicyFile))}, err
	case ResolutionKeepSelected:
		source, err := canonicalSourceByID(item.Sources, p.KeepSourceID)
		return source, skillResolutionSource{Path: personalPolicyPathForSource(source)}, err
	case ResolutionMergeManually:
		if strings.TrimSpace(p.MergeContentHash) == "" {
			return skillResolutionSource{}, skillResolutionSource{}, fmt.Errorf("merge_content_hash is required for merge_manually")
		}
	}
	return item.Sources[0], item.Sources[1], nil
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

func personalPolicyPathForSource(source skillResolutionSource) string {
	dir := filepath.Dir(filepath.Dir(filepath.Dir(filepath.FromSlash(source.Path))))
	return filepath.ToSlash(filepath.Join(dir, personalSkillPolicyFile))
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
	return filepath.ToSlash(filepath.Join(filepath.Dir(targetPath), ".super-dolphin-mirror-backup", filepath.Base(targetPath)+"-preview-"+seed[:12]))
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
