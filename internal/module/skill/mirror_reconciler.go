package skill

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skill/mirrorpath"
)

const (
	skillConflictSameName                        = "same_name"
	skillConflictMirrorDrift                     = "mirror_drift"
	skillConflictUnmanagedSameName               = "unmanaged_same_name"
	skillConflictUnmanagedProviderSkill          = "unmanaged_provider_skill"
	skillConflictExternalPersonalProjectSameName = "external_personal_project_same_name"
	skillConflictCanonicalDeletedWithDrift       = "canonical_deleted_with_drift"
	skillConflictMultiMirrorDrift                = "multi_mirror_drift"
	skillConflictMirrorRootSymlink               = "mirror_root_symlink"
	skillMirrorBackupDirName                     = ".super-dolphin-mirror-backup"
)

type SkillMirrorConflict struct {
	Kind          string
	TargetID      string
	Provider      SkillProvider
	Scope         string
	PersonalType  string
	Name          string
	CanonicalID   string
	MirrorPath    string
	CanonicalHash string
	MirrorHash    string
	PreviewHash   string
	Sources       []SkillMirrorConflictSource
	Actions       []SkillMirrorResolutionAction
}

// SkillMirrorConflictSource 指向冲突背后的真实 skill。
// 排查时顺着 canonical_id 找 .agent/skills 或 active personal，不看 mirror 副本。
type SkillMirrorConflictSource struct {
	Scope         string
	PersonalType  string
	CanonicalID   string
	ContentHash   string
	CanonicalHash string
}

// SkillMirrorResolutionAction 是 UI 里可选的处理动作。
// 新动作必须有 preview 校验，不能直接改目录。
type SkillMirrorResolutionAction struct {
	Action      string
	PreviewHash string
}

// SkillMirrorResolutionRequest 只接受用户刚确认过的 preview。
// PreviewHash 不匹配时直接报错，不要重算后继续。
type SkillMirrorResolutionRequest struct {
	Action      string
	Name        string
	NewName     string
	Target      SkillMirrorTarget
	PreviewID   string
	PreviewHash string
}

// SkillMirrorResolutionReport 会告诉前端是否还有后续重试动作。
// 清理或审计失败不能吞掉，要让用户知道下一步。
type SkillMirrorResolutionReport struct {
	Action         string
	Name           string
	ResultingHash  string
	PartialFailure bool
	FollowUpAction string
}

type skillMirrorMutationAuditRecord struct {
	Action       string `json:"action"`
	CanonicalID  string `json:"canonical_id"`
	Scope        string `json:"scope"`
	PersonalType string `json:"personal_type,omitempty"`
	Name         string `json:"name"`
	OldHash      string `json:"old_hash,omitempty"`
	NewHash      string `json:"new_hash,omitempty"`
	Actor        string `json:"actor"`
	Timestamp    string `json:"timestamp"`
}

// DetectSkillMirrorConflicts 只检查 mirror 和真实 skill 是否不一致。
// 这里不修复目录；发现漂移、未知目录或根 symlink 就报告。
func DetectSkillMirrorConflicts(records []canonicalSkillRecord, targets []SkillMirrorTarget) ([]SkillMirrorConflict, error) {
	var conflicts []SkillMirrorConflict
	driftCount := map[string]int{}
	recordsByScopeName := canonicalRecordsByMirrorKey(records)
	for i := range targets {
		targets[i].Root = mirrorpath.ResolveValidRootSymlink(targets[i].Root)
	}
	for _, target := range targets {
		targetConflicts, err := detectSkillMirrorTargetConflicts(records, recordsByScopeName, target)
		if err != nil {
			return conflicts, err
		}
		for _, conflict := range targetConflicts {
			if conflict.Kind == skillConflictMirrorDrift {
				driftCount[conflict.CanonicalID]++
			}
			conflicts = append(conflicts, conflict)
		}
	}
	for i := range conflicts {
		if conflicts[i].Kind == skillConflictMirrorDrift && driftCount[conflicts[i].CanonicalID] > 1 {
			conflicts[i].Kind = skillConflictMultiMirrorDrift
		}
	}
	sortMirrorConflicts(conflicts)
	return conflicts, nil
}

// MirrorConflictsFromCanonical 从 canonical skill 计算 mirror 冲突。
func MirrorConflictsFromCanonical(conflicts []canonicalSkillConflict) []SkillMirrorConflict {
	out := make([]SkillMirrorConflict, 0, len(conflicts))
	for _, conflict := range conflicts {
		item := SkillMirrorConflict{Kind: skillConflictSameName, Name: conflict.Name}
		for _, source := range conflict.Sources {
			item.Sources = append(item.Sources, SkillMirrorConflictSource{
				Scope:        source.Scope,
				PersonalType: source.PersonalType,
				CanonicalID:  canonicalSourceID(canonicalSkillRecord{Name: source.Name, Scope: source.Scope, PersonalType: source.PersonalType}),
				ContentHash:  source.ContentHash,
			})
		}
		out = append(out, item)
	}
	return out
}

// detectSkillMirrorTargetConflicts 检查目标 mirror 中的命名冲突。
func detectSkillMirrorTargetConflicts(allRecords []canonicalSkillRecord, records map[string]canonicalSkillRecord, target SkillMirrorTarget) ([]SkillMirrorConflict, error) {
	conflict, ok, err := validateMirrorRootOrConflict(target)
	if err != nil {
		return nil, err
	}
	if ok {
		return []SkillMirrorConflict{conflict}, nil
	}
	manifest, manifestTargetMismatch, err := targetManifestOrRepair(allRecords, target)
	if err != nil {
		return nil, err
	}
	names, err := skillMirrorNames(target.Root)
	if err != nil {
		return nil, err
	}
	var conflicts []SkillMirrorConflict
	for _, name := range names {
		conflict, ok, err := detectSkillMirrorNameConflict(records, target, manifest, name, manifestTargetMismatch)
		if err != nil {
			return conflicts, err
		}
		if ok {
			conflicts = append(conflicts, conflict)
		}
	}
	return conflicts, nil
}

func validateMirrorRootOrConflict(target SkillMirrorTarget) (SkillMirrorConflict, bool, error) {
	if err := validateExistingMirrorRoot(target); err != nil {
		if conflict, ok, conflictErr := mirrorRootSymlinkConflict(target); ok || conflictErr != nil {
			return conflict, ok, conflictErr
		}
		return SkillMirrorConflict{}, false, err
	}
	return SkillMirrorConflict{}, false, nil
}

func targetManifestOrRepair(records []canonicalSkillRecord, target SkillMirrorTarget) (SkillMirrorManifest, bool, error) {
	manifest, err := readTargetManifest(target)
	if err == nil {
		return manifest, false, nil
	}
	if !errors.Is(err, errSkillMirrorManifestTargetMismatch) {
		return SkillMirrorManifest{}, false, err
	}
	if target.Scope != skillScopePersonal {
		return newSkillMirrorManifest(target), true, nil
	}
	manifest, err = repairMismatchedSkillMirrorManifest(recordsForMirrorTarget(records, target), target)
	return manifest, false, err
}

func mirrorRootSymlinkConflict(target SkillMirrorTarget) (SkillMirrorConflict, bool, error) {
	info, err := os.Lstat(target.Root)
	if errors.Is(err, os.ErrNotExist) {
		return SkillMirrorConflict{}, false, nil
	}
	if err != nil {
		return SkillMirrorConflict{}, false, err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return SkillMirrorConflict{}, false, nil
	}
	hash, err := mirrorRootSymlinkHash(target.Root)
	if err != nil {
		return SkillMirrorConflict{}, false, err
	}
	return SkillMirrorConflict{
		Kind:        skillConflictMirrorRootSymlink,
		TargetID:    target.TargetID,
		Provider:    target.Provider,
		Scope:       target.Scope,
		Name:        mirrorRootSymlinkConflictName(target),
		MirrorPath:  filepath.ToSlash(target.Root),
		MirrorHash:  hash,
		PreviewHash: hash,
		Actions:     mirrorActions(ResolutionViewUnmanaged, ResolutionReplaceProviderRootSymlink),
	}, true, nil
}

func mirrorRootSymlinkHash(root string) (string, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return "", fmt.Errorf("skill mirror root is not a symlink: %s", root)
	}
	target, err := os.Readlink(root)
	if err != nil {
		return "", err
	}
	return hashResolutionEnvelope(map[string]string{
		"kind":   "skill_mirror_root_symlink",
		"root":   filepath.ToSlash(root),
		"target": target,
		"mode":   info.Mode().String(),
	}), nil
}

func mirrorRootSymlinkConflictName(target SkillMirrorTarget) string {
	scope := "技能目录"
	if target.Scope == skillScopeProject {
		scope = "项目技能目录"
	}
	if target.Scope == skillScopePersonal {
		scope = "私人技能目录"
	}
	return mirrorProviderDisplayName(target.Provider) + " " + scope
}

func mirrorProviderDisplayName(provider SkillProvider) string {
	switch provider {
	case SkillProviderClaude:
		return "Claude"
	case SkillProviderCodex:
		return "Codex"
	default:
		return strings.TrimSpace(string(provider))
	}
}

func detectSkillMirrorNameConflict(records map[string]canonicalSkillRecord, target SkillMirrorTarget, manifest SkillMirrorManifest, name string, manifestTargetMismatch bool) (SkillMirrorConflict, bool, error) {
	mirrorDir := filepath.Join(target.Root, name)
	mirrorHash, exists, err := existingMirrorHash(mirrorDir)
	if err != nil || !exists {
		return SkillMirrorConflict{}, false, err
	}
	entry, managed := manifest.Skills[name]
	record, canonicalExists := mirrorCanonicalRecord(records, target, entry, name)
	if !managed {
		return unmanagedMirrorNameConflict(target, record, name, mirrorDir, mirrorHash, canonicalExists, manifestTargetMismatch)
	}
	conflict, err := managedMirrorConflict(target, entry, record, name, mirrorDir, mirrorHash, canonicalExists)
	if err != nil {
		return SkillMirrorConflict{}, false, err
	}
	return conflict, conflict.Kind != "", nil
}

// unmanagedMirrorNameConflict 构造未托管 mirror 同名冲突。
func unmanagedMirrorNameConflict(target SkillMirrorTarget, record canonicalSkillRecord, name, mirrorDir, mirrorHash string, canonicalExists, manifestTargetMismatch bool) (SkillMirrorConflict, bool, error) {
	if target.Scope == skillScopePersonal && !canonicalExists {
		return SkillMirrorConflict{}, false, nil
	}
	if canonicalExists && target.Scope == skillScopeProject {
		return unmanagedProjectSameNameConflict(target, record, name, mirrorDir, mirrorHash, manifestTargetMismatch)
	}
	if canonicalExists && target.Scope == skillScopePersonal && record.Scope == skillScopeProject {
		ignored, err := externalPersonalProjectConflictIgnored(target, record, name, mirrorHash)
		if err != nil || ignored {
			return SkillMirrorConflict{}, false, err
		}
		return externalPersonalProjectSameNameConflict(target, record, name, mirrorDir, mirrorHash)
	}
	return unmanagedProviderSkillConflict(target, record, name, mirrorDir, mirrorHash, canonicalExists, manifestTargetMismatch), true, nil
}

// mirrorCanonicalRecord 把 canonical 记录转换成 mirror 记录。
func mirrorCanonicalRecord(records map[string]canonicalSkillRecord, target SkillMirrorTarget, entry SkillMirrorEntry, name string) (canonicalSkillRecord, bool) {
	if record, ok := records[mirrorRecordKey(target.Scope, entry.PersonalType, name)]; ok {
		return record, true
	}
	if target.Scope != skillScopePersonal || strings.TrimSpace(entry.PersonalType) != "" {
		return canonicalSkillRecord{}, false
	}
	for _, personalType := range activePersonalSkillTypes() {
		if record, ok := records[mirrorRecordKey(skillScopePersonal, personalType, name)]; ok {
			return record, true
		}
	}
	if record, ok := records[mirrorRecordKey(skillScopeProject, "", name)]; ok {
		return record, true
	}
	return canonicalSkillRecord{}, false
}

// unmanagedProjectSameNameConflict 构造项目 skill 同名冲突。
func unmanagedProjectSameNameConflict(target SkillMirrorTarget, record canonicalSkillRecord, name, mirrorDir, mirrorHash string, manifestTargetMismatch bool) (SkillMirrorConflict, bool, error) {
	canonicalHash, err := stableMirrorDirectoryHash(record.Dir)
	if err != nil {
		return SkillMirrorConflict{}, false, err
	}
	if !manifestTargetMismatch {
		expectedHash, expectedContentHash, err := expectedCanonicalMirrorHashes(target.Root, record, target.Scope)
		if err != nil {
			return SkillMirrorConflict{}, false, err
		}
		mirrorContentHash, err := skillDirContentHash(mirrorDir)
		if err != nil {
			return SkillMirrorConflict{}, false, err
		}
		if mirrorHash == expectedHash || mirrorContentHash == expectedContentHash {
			return SkillMirrorConflict{}, false, nil
		}
	}
	conflict := unmanagedProviderSkillConflict(target, record, name, mirrorDir, mirrorHash, true, manifestTargetMismatch)
	conflict.CanonicalHash = canonicalHash
	conflict.Actions = mirrorActions(ResolutionViewDiff, ResolutionSyncBackCanonical, ResolutionCanonicalOverwrite, ResolutionSaveAsNewSkill)
	return conflict, true, nil
}

// unmanagedProviderSkillConflict 构造 provider skill 同名冲突。
func unmanagedProviderSkillConflict(target SkillMirrorTarget, record canonicalSkillRecord, name, mirrorDir, mirrorHash string, canonicalExists, manifestTargetMismatch bool) SkillMirrorConflict {
	canonicalID := ""
	if canonicalExists {
		canonicalID = canonicalSourceID(record)
	}
	kind := skillConflictUnmanagedProviderSkill
	if canonicalExists {
		kind = skillConflictUnmanagedSameName
	}
	actions := mirrorActions("view_unmanaged", "import_to_personal_imported")
	if target.Scope != skillScopePersonal && !manifestTargetMismatch {
		if canonicalExists {
			actions = mirrorActions("view_unmanaged", "import_to_personal_imported", "takeover_provider_skill")
		} else {
			actions = mirrorActions("view_unmanaged", "import_to_personal_imported", "import_to_project", "takeover_provider_skill")
		}
	}
	return SkillMirrorConflict{
		Kind:         kind,
		TargetID:     target.TargetID,
		Provider:     target.Provider,
		Scope:        target.Scope,
		PersonalType: targetPersonalConflictType(target, record),
		Name:         name,
		CanonicalID:  canonicalID,
		MirrorPath:   filepath.ToSlash(mirrorDir),
		MirrorHash:   mirrorHash,
		PreviewHash:  mirrorHash,
		Actions:      actions,
	}
}

func externalPersonalProjectSameNameConflict(target SkillMirrorTarget, record canonicalSkillRecord, name, mirrorDir, mirrorHash string) (SkillMirrorConflict, bool, error) {
	canonicalHash, err := stableMirrorDirectoryHash(record.Dir)
	if err != nil {
		return SkillMirrorConflict{}, false, err
	}
	return SkillMirrorConflict{
		Kind:          skillConflictExternalPersonalProjectSameName,
		TargetID:      target.TargetID,
		Provider:      target.Provider,
		Scope:         skillScopePersonal,
		Name:          name,
		CanonicalID:   canonicalSourceID(record),
		MirrorPath:    filepath.ToSlash(mirrorDir),
		CanonicalHash: canonicalHash,
		MirrorHash:    mirrorHash,
		PreviewHash:   mirrorHash,
		Actions:       mirrorActions(ResolutionViewDiff, ResolutionUseProjectSharedSkill, ResolutionUseExternalProviderSkill, ResolutionSaveAsNewPersonal),
	}, true, nil
}

func externalPersonalProjectConflictIgnored(target SkillMirrorTarget, record canonicalSkillRecord, name, mirrorHash string) (bool, error) {
	if target.Scope != skillScopePersonal || record.Scope != skillScopeProject {
		return false, nil
	}
	projectRoot, ok := projectRootFromProjectSkillDir(record.Dir)
	if !ok {
		return false, nil
	}
	policy, err := readProjectSkillPolicy(projectRoot)
	if err != nil {
		return false, err
	}
	return projectPolicyKeepsExternalProviderSkill(policy, name, target.Provider, mirrorHash), nil
}

func projectRootFromProjectSkillDir(dir string) (string, bool) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "." || dir == "" {
		return "", false
	}
	skillsRoot := filepath.Dir(dir)
	if filepath.Base(skillsRoot) != "skills" {
		return "", false
	}
	agentRoot := filepath.Dir(skillsRoot)
	if filepath.Base(agentRoot) != ".agent" {
		return "", false
	}
	return filepath.Dir(agentRoot), true
}

func targetPersonalConflictType(target SkillMirrorTarget, record canonicalSkillRecord) string {
	if target.Scope != skillScopePersonal {
		return ""
	}
	if strings.TrimSpace(record.PersonalType) != "" {
		return strings.TrimSpace(record.PersonalType)
	}
	return personalSkillTypeUser
}

// managedMirrorConflict 构造已托管 mirror 的冲突信息。
func managedMirrorConflict(target SkillMirrorTarget, entry SkillMirrorEntry, record canonicalSkillRecord, name, mirrorDir, mirrorHash string, canonicalExists bool) (SkillMirrorConflict, error) {
	canonicalHash := entry.CanonicalHash
	if canonicalExists {
		var err error
		canonicalHash, err = stableMirrorDirectoryHash(record.Dir)
		if err != nil {
			return SkillMirrorConflict{}, err
		}
	}
	if canonicalExists && mirrorHash == entry.MirrorHash && canonicalHash == entry.CanonicalHash {
		return SkillMirrorConflict{}, nil
	}
	kind := skillConflictMirrorDrift
	if !canonicalExists {
		kind = skillConflictCanonicalDeletedWithDrift
	}
	personalType := entry.PersonalType
	if personalType == "" {
		personalType = record.PersonalType
	}
	return SkillMirrorConflict{
		Kind:          kind,
		TargetID:      target.TargetID,
		Provider:      target.Provider,
		Scope:         target.Scope,
		PersonalType:  personalType,
		Name:          name,
		CanonicalID:   entry.CanonicalID,
		MirrorPath:    filepath.ToSlash(mirrorDir),
		CanonicalHash: canonicalHash,
		MirrorHash:    mirrorHash,
		PreviewHash:   mirrorHash,
		Actions:       driftActions(target.Scope),
	}, nil
}

func canonicalRecordsByMirrorKey(records []canonicalSkillRecord) map[string]canonicalSkillRecord {
	out := make(map[string]canonicalSkillRecord, len(records))
	for _, record := range records {
		out[mirrorRecordKey(record.Scope, record.PersonalType, record.Name)] = record
	}
	return out
}

func mirrorRecordKey(scope, personalType, name string) string {
	if strings.TrimSpace(scope) != skillScopePersonal {
		personalType = ""
	}
	return strings.TrimSpace(scope) + "/" + strings.TrimSpace(personalType) + "/" + strings.ToLower(strings.TrimSpace(name))
}

func readTargetManifest(target SkillMirrorTarget) (SkillMirrorManifest, error) {
	return loadSkillMirrorManifest(filepath.Join(target.Root, skillMirrorManifestFile), target)
}

// skillMirrorNames 列出 mirror 中已有的 skill 名称。
func skillMirrorNames(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("skill mirror entry is symlink: %s", filepath.Join(root, entry.Name()))
		}
		if entry.IsDir() && entry.Name() != skillMirrorBackupDirName {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func driftActions(scope string) []SkillMirrorResolutionAction {
	if scope == skillScopePersonal {
		return mirrorActions("sync_back_to_personal", "personal_overwrite_mirror", "save_as_new_personal_skill")
	}
	return mirrorActions("sync_back_to_canonical", "canonical_overwrite_mirror", "save_as_new_skill")
}

func mirrorActions(actions ...string) []SkillMirrorResolutionAction {
	out := make([]SkillMirrorResolutionAction, 0, len(actions))
	for _, action := range actions {
		out = append(out, SkillMirrorResolutionAction{Action: action})
	}
	return out
}

func sortMirrorConflicts(conflicts []SkillMirrorConflict) {
	sort.SliceStable(conflicts, func(i, j int) bool {
		if conflicts[i].Kind != conflicts[j].Kind {
			return conflicts[i].Kind < conflicts[j].Kind
		}
		return conflicts[i].Name < conflicts[j].Name
	})
}

func replaceSkillDirFromMirror(sourceDir, targetDir string) error {
	return replaceSkillDirFromMirrorWithCopy(sourceDir, targetDir, copySkillDir)
}

// replaceSkillDirFromMirrorWithCopy 用 mirror 副本替换目标 skill 目录。
func replaceSkillDirFromMirrorWithCopy(sourceDir, targetDir string, copyDir func(string, string) (int, int64, error)) error {
	if err := os.MkdirAll(filepath.Dir(targetDir), 0o755); err != nil {
		return err
	}
	tempDir, err := os.MkdirTemp(filepath.Dir(targetDir), "."+filepath.Base(targetDir)+".sync-*")
	if err != nil {
		return err
	}
	if err := os.Remove(tempDir); err != nil {
		_ = os.RemoveAll(tempDir)
		return err
	}
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.RemoveAll(tempDir)
		}
	}()
	if _, _, err := copyDir(sourceDir, tempDir); err != nil {
		return err
	}
	if !skillMainFileExists(tempDir) {
		return errSkillMainFileNotFound
	}
	if err := os.RemoveAll(targetDir); err != nil {
		return err
	}
	if err := os.Rename(tempDir, targetDir); err != nil {
		return err
	}
	removeTemp = false
	return nil
}

func backupSkillDir(targetDir string) (string, error) {
	if !skillMainFileExists(targetDir) {
		return "", nil
	}
	backupDir := mirrorBackupPathForTargetDir(targetDir, filepath.Base(targetDir)+"-"+time.Now().UTC().Format("20060102T150405.000000000Z"))
	if err := os.MkdirAll(filepath.Dir(backupDir), 0o755); err != nil {
		return "", err
	}
	if _, _, err := copySkillDir(targetDir, backupDir); err != nil {
		_ = os.RemoveAll(backupDir)
		return "", err
	}
	return backupDir, nil
}

func mirrorBackupPathForTargetDir(targetDir, leaf string) string {
	root := filepath.Dir(targetDir)
	return filepath.Join(filepath.Dir(root), skillMirrorBackupDirName, filepath.Base(root), leaf)
}

// validateExistingMirrorRoot 校验已有 mirror 根目录可被当前项目使用。
func validateExistingMirrorRoot(target SkillMirrorTarget) error {
	if err := validateSkillMirrorTarget(target); err != nil {
		return err
	}
	if err := mirrorpath.RejectSymlinkAncestors(target.Root); err != nil {
		return err
	}
	info, err := os.Lstat(target.Root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("skill mirror root is symlink: %s", target.Root)
	}
	if !info.IsDir() {
		return fmt.Errorf("skill mirror root is not a directory: %s", target.Root)
	}
	return nil
}

func ensureProviderSkillDirSafe(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("provider skill dir is symlink: %s", dir)
	}
	if !info.IsDir() {
		return fmt.Errorf("provider skill path is not a directory: %s", dir)
	}
	return nil
}
