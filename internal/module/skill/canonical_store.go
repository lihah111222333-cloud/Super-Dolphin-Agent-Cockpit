package skill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	projectSkillPolicyFile  = ".super-dolphin-skill-policy.json"
	personalSkillPolicyFile = ".super-dolphin-personal-skill-policy.json"
)

// canonicalStore 只读真正的 skill 来源：项目 .agent/skills 和 active personal。
// 不要把 .claude/.agents 加进来，它们只是生成结果。
type canonicalStore struct {
	superDolphinHome string
	osUID            string
	appProfile       string
}

// canonicalSkillRecord 是一个真实 skill 目录扫描出的规范记录，provider mirror 只从这些记录生成。
type canonicalSkillRecord struct {
	Name         string
	Scope        string
	PersonalType string
	Dir          string
	SkillFile    string
	ContentHash  string
	DirHash      string
	info         SkillInfo
}

// canonicalSkillConflict 描述 canonical skill 集合中必须人工处理的冲突。
type canonicalSkillConflict struct {
	Kind    string
	Name    string
	Sources []canonicalSkillConflictSource
}

// canonicalSkillConflictSource 记录冲突来源的路径、scope 和 hash，供 UI 展示和策略选择。
type canonicalSkillConflictSource struct {
	Name         string
	Scope        string
	PersonalType string
	Dir          string
	SkillFile    string
	ContentHash  string
	DirHash      string
}

// canonicalScanRoot 是一次扫描会进入的真实目录。
// personal/hub 只放 catalog 数据，不能参与运行时匹配或 provider mirror。
type canonicalScanRoot struct {
	path         string
	scope        string
	personalType string
	defaultTrust TrustScope
}

// skillSameNameConflictError 包装同名冲突列表，保留 errors.Is 判断能力。
type skillSameNameConflictError struct {
	Conflicts []canonicalSkillConflict
}

// projectSkillPolicy 是项目根下保存的 skill 选择/屏蔽策略。
type projectSkillPolicy struct {
	Version                   int                                  `json:"version"`
	DisablePersonalForProject []projectSkillPolicyDisabledPersonal `json:"disable_personal_for_project,omitempty"`
	KeepSelected              []projectSkillKeepSelected           `json:"keep_selected,omitempty"`
	KeepExternalProviderSkill []projectSkillKeepExternalProvider   `json:"keep_external_provider_skill,omitempty"`
}

// projectSkillPolicyDisabledPersonal 表示项目禁用某个 personal skill 来源。
type projectSkillPolicyDisabledPersonal struct {
	Name         string `json:"name"`
	PersonalType string `json:"personal_type"`
}

// projectSkillKeepSelected 表示项目在同名 skill 来源中固定选择某一项。
type projectSkillKeepSelected struct {
	Name                 string                     `json:"name"`
	SelectedSourceID     string                     `json:"selected_source_id"`
	SelectedPersonalType string                     `json:"selected_personal_type,omitempty"`
	SelectedContentHash  string                     `json:"selected_content_hash,omitempty"`
	ExcludedSourceIDs    []string                   `json:"excluded_source_ids,omitempty"`
	Sources              []projectSkillPolicySource `json:"sources,omitempty"`
}

// projectSkillKeepExternalProvider 记录项目允许保留的外部 provider mirror skill。
type projectSkillKeepExternalProvider struct {
	Name       string `json:"name"`
	Provider   string `json:"provider"`
	SourceHash string `json:"source_hash"`
}

// projectSkillPolicySource 保存策略写入时看到的候选来源快照。
type projectSkillPolicySource struct {
	CanonicalID  string `json:"canonical_id"`
	Scope        string `json:"scope"`
	PersonalType string `json:"personal_type,omitempty"`
	ContentHash  string `json:"content_hash,omitempty"`
}

// Error 返回错误的可读文本。
func (e skillSameNameConflictError) Error() string {
	names := make([]string, 0, len(e.Conflicts))
	for _, conflict := range e.Conflicts {
		names = append(names, conflict.Name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ErrSkillSameNameConflict.Error()
	}
	return ErrSkillSameNameConflict.Error() + ": " + strings.Join(names, ", ")
}

// Unwrap 暴露底层错误，方便 errors.Is 或 errors.As 判断。
func (e skillSameNameConflictError) Unwrap() error { return ErrSkillSameNameConflict }

// newCanonicalStore 创建 canonical store，默认使用当前进程 owner 身份。
func newCanonicalStore(superDolphinHome string) *canonicalStore {
	return &canonicalStore{superDolphinHome: strings.TrimSpace(superDolphinHome)}
}

// newCanonicalStoreForOwner 创建指定 owner 的 canonical store，用于 provider mirror reconciliation。
func newCanonicalStoreForOwner(superDolphinHome, osUID, appProfile string) *canonicalStore {
	return &canonicalStore{
		superDolphinHome: strings.TrimSpace(superDolphinHome),
		osUID:            strings.TrimSpace(osUID),
		appProfile:       strings.TrimSpace(appProfile),
	}
}

// EffectiveSet 给运行时和 provider mirror 提供可用 skill。
// 同名冲突会直接返回 conflicts；不要在这里偷偷按优先级选一个。
func (s *canonicalStore) EffectiveSet(_ context.Context, cwd string) ([]canonicalSkillRecord, []canonicalSkillConflict, error) {
	records, err := s.scan(cwd)
	if err != nil {
		return nil, nil, err
	}
	records, err = s.applyEffectivePolicies(cwd, records)
	if err != nil {
		return nil, nil, err
	}
	conflicts := canonicalSameNameConflicts(records)
	if len(conflicts) == 0 {
		return records, nil, nil
	}
	return canonicalRecordsWithoutConflicts(records, conflicts), conflicts, nil
}

// scan 扫描所有 canonical roots 并按 name/scope/personalType 稳定排序。
func (s *canonicalStore) scan(cwd string) ([]canonicalSkillRecord, error) {
	roots := s.scanRoots(cwd)
	records := make([]canonicalSkillRecord, 0, len(roots))
	for _, root := range roots {
		found, err := scanCanonicalRoot(root)
		if err != nil {
			return nil, err
		}
		records = append(records, found...)
	}
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].Name != records[j].Name {
			return records[i].Name < records[j].Name
		}
		if records[i].Scope != records[j].Scope {
			return records[i].Scope < records[j].Scope
		}
		return records[i].PersonalType < records[j].PersonalType
	})
	return records, nil
}

// scanRoots 只列运行时真正会读的目录：项目 skill 和 user/agent/imported。
// hub 是 catalog，provider mirror 是生成物，都不要放进扫描列表。
func (s *canonicalStore) scanRoots(cwd string) []canonicalScanRoot {
	projectRoot := projectRootForCWD(cwd, "")
	roots := []canonicalScanRoot{
		{path: defaultProjectSkillsRoot(projectRoot), scope: skillScopeProject, defaultTrust: TrustProject},
	}
	if strings.TrimSpace(s.superDolphinHome) == "" {
		return roots
	}
	personalRoot := filepath.Join(s.superDolphinHome, "skills", "personal")
	for _, personalType := range activePersonalSkillTypes() {
		roots = append(roots, canonicalScanRoot{
			path:         filepath.Join(personalRoot, personalType),
			scope:        skillScopePersonal,
			personalType: personalType,
			defaultTrust: TrustUser,
		})
	}
	return roots
}

// scanCanonicalRoot 扫描 canonical skill 根目录。
func scanCanonicalRoot(root canonicalScanRoot) ([]canonicalSkillRecord, error) {
	if strings.TrimSpace(root.path) == "" {
		return nil, nil
	}
	info, err := os.Lstat(root.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("canonical skill root is symlink: %s", root.path)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("canonical skill root is not a directory: %s", root.path)
	}
	var records []canonicalSkillRecord
	err = filepath.WalkDir(root.path, func(path string, entry os.DirEntry, walkErr error) error {
		record, err := visitCanonicalSkillFile(root, path, entry, walkErr)
		if err != nil || record == nil {
			return err
		}
		records = append(records, *record)
		return filepath.SkipDir
	})
	if err != nil {
		return nil, err
	}
	return records, nil
}

// visitCanonicalSkillFile 读取单个 canonical skill 文件并生成记录。
func visitCanonicalSkillFile(root canonicalScanRoot, path string, entry os.DirEntry, walkErr error) (*canonicalSkillRecord, error) {
	if walkErr != nil || entry == nil {
		return nil, walkErr
	}
	depth, err := scanSkillEntryDepth(root.path, path)
	if err != nil {
		return nil, err
	}
	if entry.IsDir() {
		return nil, visitSkillDir(skillScanRoot{path: root.path}, path, entry.Name(), depth)
	}
	if !strings.EqualFold(entry.Name(), skillMainFile) {
		return nil, nil
	}
	if entry.Type()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("canonical skill file is symlink: %s", path)
	}
	parsed, err := parseSkillRecord(root.path, path, root.defaultTrust)
	if err != nil {
		return nil, fmt.Errorf("parse canonical skill %s: %w", path, err)
	}
	name, displayName, err := normalizeSkillIdentityName(parsed.info.Name, parsed.info.DisplayName)
	if err != nil {
		return nil, fmt.Errorf("validate canonical skill %s: %w", path, err)
	}
	dir := filepath.Dir(path)
	dirHash, err := skillDirContentHash(dir)
	if err != nil {
		return nil, err
	}
	parsed.info.Name = name
	parsed.info.DisplayName = displayName
	parsed.info.Scope = root.scope
	parsed.info.PersonalType = root.personalType
	return &canonicalSkillRecord{
		Name:         name,
		Scope:        root.scope,
		PersonalType: root.personalType,
		Dir:          dir,
		SkillFile:    path,
		ContentHash:  parsed.info.ContentHash,
		DirHash:      dirHash,
		info:         parsed.info,
	}, nil
}

// canonicalSameNameConflicts 找出同名 skill 多来源冲突，调用方必须 fail-closed 或展示给用户处理。
func canonicalSameNameConflicts(records []canonicalSkillRecord) []canonicalSkillConflict {
	byName := make(map[string][]canonicalSkillRecord, len(records))
	displayName := make(map[string]string, len(records))
	for _, record := range records {
		key := strings.ToLower(strings.TrimSpace(record.Name))
		if _, ok := displayName[key]; !ok {
			displayName[key] = record.Name
		}
		byName[key] = append(byName[key], record)
	}
	conflicts := make([]canonicalSkillConflict, 0)
	for key, named := range byName {
		if len(named) < 2 {
			continue
		}
		conflicts = append(conflicts, canonicalSkillConflict{
			Kind:    "same_name",
			Name:    displayName[key],
			Sources: canonicalConflictSources(named),
		})
	}
	sort.SliceStable(conflicts, func(i, j int) bool { return conflicts[i].Name < conflicts[j].Name })
	return conflicts
}

// canonicalConflictSources 将冲突记录转为稳定排序的来源摘要。
func canonicalConflictSources(records []canonicalSkillRecord) []canonicalSkillConflictSource {
	sources := make([]canonicalSkillConflictSource, 0, len(records))
	for _, record := range records {
		sources = append(sources, canonicalSkillConflictSource{
			Name:         record.Name,
			Scope:        record.Scope,
			PersonalType: record.PersonalType,
			Dir:          record.Dir,
			SkillFile:    record.SkillFile,
			ContentHash:  record.ContentHash,
			DirHash:      record.DirHash,
		})
	}
	sort.SliceStable(sources, func(i, j int) bool {
		if sources[i].Scope != sources[j].Scope {
			return sources[i].Scope < sources[j].Scope
		}
		return sources[i].PersonalType < sources[j].PersonalType
	})
	return sources
}

// canonicalRecordsWithoutConflicts 移除所有仍处于同名冲突的记录，避免运行时偷偷选边。
func canonicalRecordsWithoutConflicts(records []canonicalSkillRecord, conflicts []canonicalSkillConflict) []canonicalSkillRecord {
	conflicted := make(map[string]struct{}, len(conflicts))
	for _, conflict := range conflicts {
		conflicted[strings.ToLower(strings.TrimSpace(conflict.Name))] = struct{}{}
	}
	filtered := records[:0]
	for _, record := range records {
		if _, ok := conflicted[strings.ToLower(strings.TrimSpace(record.Name))]; ok {
			continue
		}
		filtered = append(filtered, record)
	}
	return filtered
}

// applyEffectivePolicies 依次应用项目策略和 personal 策略，得到运行时 effective records。
func (s *canonicalStore) applyEffectivePolicies(cwd string, records []canonicalSkillRecord) ([]canonicalSkillRecord, error) {
	filtered, err := applyProjectSkillPolicy(cwd, records)
	if err != nil {
		return nil, err
	}
	return s.applyPersonalSkillPolicy(filtered)
}

// applyProjectSkillPolicy 读取项目策略并应用禁用 personal 与 keep-selected 规则。
func applyProjectSkillPolicy(cwd string, records []canonicalSkillRecord) ([]canonicalSkillRecord, error) {
	policy, err := readProjectSkillPolicy(cwd)
	if err != nil {
		return nil, err
	}
	filtered, err := applyProjectDisabledPersonalPolicy(records, policy.DisablePersonalForProject)
	if err != nil {
		return nil, err
	}
	return applyProjectKeepSelectedPolicy(filtered, policy.KeepSelected)
}

// applyProjectDisabledPersonalPolicy 移除项目显式禁用的 personal skill 来源。
func applyProjectDisabledPersonalPolicy(records []canonicalSkillRecord, disabledItems []projectSkillPolicyDisabledPersonal) ([]canonicalSkillRecord, error) {
	if len(disabledItems) == 0 {
		return records, nil
	}
	disabled := make(map[string]struct{}, len(disabledItems))
	for _, item := range disabledItems {
		name, _, err := normalizeSkillIdentityName(item.Name, "")
		if err != nil {
			return nil, fmt.Errorf("invalid project skill policy name: %w", err)
		}
		personalType := strings.ToLower(strings.TrimSpace(item.PersonalType))
		if _, _, err := normalizeSkillTarget(skillScopePersonal, personalType); err != nil {
			return nil, fmt.Errorf("invalid project skill policy personal_type: %w", err)
		}
		disabled[canonicalSourceID(canonicalSkillRecord{Name: name, Scope: skillScopePersonal, PersonalType: personalType})] = struct{}{}
	}
	return filterCanonicalRecords(records, func(record canonicalSkillRecord) bool {
		_, ok := disabled[canonicalSourceID(record)]
		return !ok
	}), nil
}

// applyProjectKeepSelectedPolicy 按项目选择策略保留或排除同名 skill 来源。
func applyProjectKeepSelectedPolicy(records []canonicalSkillRecord, selections []projectSkillKeepSelected) ([]canonicalSkillRecord, error) {
	if len(selections) == 0 {
		return records, nil
	}
	selectionByName, err := projectSelectionByName(selections)
	if err != nil {
		return nil, err
	}
	sourceIDsByName := canonicalSourceIDsByName(records)
	return filterCanonicalRecords(records, func(record canonicalSkillRecord) bool {
		return keepCanonicalRecordForProjectSelection(record, selectionByName, sourceIDsByName)
	}), nil
}

// projectSelectionByName 校验并按规范化名称索引 keep-selected 策略。
func projectSelectionByName(selections []projectSkillKeepSelected) (map[string]projectSkillKeepSelected, error) {
	selectionByName := make(map[string]projectSkillKeepSelected, len(selections))
	for _, selection := range selections {
		name, _, err := normalizeSkillIdentityName(selection.Name, "")
		if err != nil {
			return nil, fmt.Errorf("invalid project skill policy name: %w", err)
		}
		selection.Name = name
		selectionByName[strings.ToLower(name)] = selection
	}
	return selectionByName, nil
}

// keepCanonicalRecordForProjectSelection 判断单条记录是否被项目 keep-selected 策略保留。
func keepCanonicalRecordForProjectSelection(record canonicalSkillRecord, selectionByName map[string]projectSkillKeepSelected, sourceIDsByName map[string]map[string]struct{}) bool {
	selection, ok := selectionByName[strings.ToLower(record.Name)]
	if !ok {
		return true
	}
	sourceID := canonicalSourceID(record)
	selectedSourceID := strings.TrimSpace(selection.SelectedSourceID)
	if selectedSourceID != "" && !canonicalSourceIDExistsForName(sourceIDsByName, selection.Name, selectedSourceID) {
		return true
	}
	if selectedSourceID == "" {
		return !stringSliceContains(selection.ExcludedSourceIDs, sourceID)
	}
	return sourceID == selectedSourceID
}

// canonicalSourceIDsByName 建立 skill 名称到来源 ID 集合的索引。
func canonicalSourceIDsByName(records []canonicalSkillRecord) map[string]map[string]struct{} {
	sourceIDsByName := make(map[string]map[string]struct{}, len(records))
	for _, record := range records {
		nameKey := strings.ToLower(strings.TrimSpace(record.Name))
		if nameKey == "" {
			continue
		}
		if sourceIDsByName[nameKey] == nil {
			sourceIDsByName[nameKey] = make(map[string]struct{})
		}
		sourceIDsByName[nameKey][canonicalSourceID(record)] = struct{}{}
	}
	return sourceIDsByName
}

// canonicalSourceIDExistsForName 判断策略中的来源 ID 是否仍存在于当前扫描结果。
func canonicalSourceIDExistsForName(sourceIDsByName map[string]map[string]struct{}, name, sourceID string) bool {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return false
	}
	sources := sourceIDsByName[strings.ToLower(strings.TrimSpace(name))]
	if len(sources) == 0 {
		return false
	}
	_, ok := sources[sourceID]
	return ok
}

// readProjectSkillPolicy 从项目 skill root 读取策略文件，读取前拒绝可写 symlink 路径。
func readProjectSkillPolicy(cwd string) (projectSkillPolicy, error) {
	var policy projectSkillPolicy
	path := filepath.Join(defaultProjectSkillsRoot(projectRootForCWD(cwd, "")), projectSkillPolicyFile)
	if err := rejectWritableSymlinkComponentIfExists(path); err != nil {
		return projectSkillPolicy{}, err
	}
	if err := readJSONFileIfExists(path, &policy); err != nil {
		return projectSkillPolicy{}, err
	}
	return policy, nil
}

// writeProjectDisablePersonalPolicy 追加项目禁用 personal 来源策略并写回策略文件。
func writeProjectDisablePersonalPolicy(cwd, name, personalType string) (string, error) {
	name, _, err := normalizeSkillIdentityName(name, "")
	if err != nil {
		return "", err
	}
	_, normalizedType, err := normalizeSkillTarget(skillScopePersonal, personalType)
	if err != nil {
		return "", err
	}
	policy, err := readProjectSkillPolicy(cwd)
	if err != nil {
		return "", err
	}
	if policy.Version == 0 {
		policy.Version = 1
	}
	next := projectSkillPolicyDisabledPersonal{Name: name, PersonalType: normalizedType}
	policy.DisablePersonalForProject = appendProjectDisabledPersonal(policy.DisablePersonalForProject, next)
	return writeSkillPolicyJSON(filepath.Join(defaultProjectSkillsRoot(projectRootForCWD(cwd, "")), projectSkillPolicyFile), policy, 0o644)
}

// projectPolicyKeepsExternalProviderSkill 判断项目策略是否保留外部 provider skill。
func projectPolicyKeepsExternalProviderSkill(policy projectSkillPolicy, name string, provider SkillProvider, sourceHash string) bool {
	name = strings.TrimSpace(name)
	providerValue := strings.TrimSpace(string(provider))
	sourceHash = strings.TrimSpace(sourceHash)
	if name == "" || providerValue == "" || sourceHash == "" {
		return false
	}
	for _, item := range policy.KeepExternalProviderSkill {
		if strings.EqualFold(item.Name, name) &&
			strings.EqualFold(item.Provider, providerValue) &&
			strings.TrimSpace(item.SourceHash) == sourceHash {
			return true
		}
	}
	return false
}

// appendProjectDisabledPersonal 去重追加 disabled personal 策略项。
func appendProjectDisabledPersonal(items []projectSkillPolicyDisabledPersonal, next projectSkillPolicyDisabledPersonal) []projectSkillPolicyDisabledPersonal {
	for _, item := range items {
		if strings.EqualFold(item.Name, next.Name) && strings.EqualFold(item.PersonalType, next.PersonalType) {
			return items
		}
	}
	return append(items, next)
}

// canonicalSourceID 生成策略中使用的稳定来源 ID。
func canonicalSourceID(record canonicalSkillRecord) string {
	name := strings.TrimSpace(record.Name)
	switch strings.TrimSpace(record.Scope) {
	case skillScopeProject:
		if dirName := strings.TrimSpace(filepath.Base(filepath.Clean(record.Dir))); dirName != "" && dirName != "." {
			return skillScopeProject + "/" + dirName
		}
		return skillScopeProject + "/" + name
	case skillScopePersonal:
		return skillScopePersonal + "/" + strings.TrimSpace(record.PersonalType) + "/" + name
	default:
		return strings.TrimSpace(record.Scope) + "/" + name
	}
}

// readJSONFileIfExists 读取可选 JSON 文件；不存在或空文件都视为无策略。
func readJSONFileIfExists(path string, dst any) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, dst)
}

// writeSkillPolicyJSON 写入 skill 策略文件。
func writeSkillPolicyJSON(path string, value any, mode os.FileMode) (string, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	if err := rejectWritableSymlinkComponentIfExists(path); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		return "", err
	}
	if err := os.Chmod(path, mode); err != nil {
		return "", err
	}
	return skillContentHash(string(data)), nil
}

// filterCanonicalRecords 按谓词复制保留 canonical records。
func filterCanonicalRecords(records []canonicalSkillRecord, keep func(canonicalSkillRecord) bool) []canonicalSkillRecord {
	filtered := make([]canonicalSkillRecord, 0, len(records))
	for _, record := range records {
		if keep(record) {
			filtered = append(filtered, record)
		}
	}
	return filtered
}

// stringSliceContains 判断字符串切片是否包含 trim 后的目标值。
func stringSliceContains(items []string, want string) bool {
	want = strings.TrimSpace(want)
	for _, item := range items {
		if strings.TrimSpace(item) == want {
			return true
		}
	}
	return false
}
