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

type canonicalSkillConflict struct {
	Kind    string
	Name    string
	Sources []canonicalSkillConflictSource
}

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

type skillSameNameConflictError struct {
	Conflicts []canonicalSkillConflict
}

type projectSkillPolicy struct {
	Version                   int                                  `json:"version"`
	DisablePersonalForProject []projectSkillPolicyDisabledPersonal `json:"disable_personal_for_project,omitempty"`
	KeepSelected              []projectSkillKeepSelected           `json:"keep_selected,omitempty"`
	KeepExternalProviderSkill []projectSkillKeepExternalProvider   `json:"keep_external_provider_skill,omitempty"`
}

type projectSkillPolicyDisabledPersonal struct {
	Name         string `json:"name"`
	PersonalType string `json:"personal_type"`
}

type projectSkillKeepSelected struct {
	Name                 string                     `json:"name"`
	SelectedSourceID     string                     `json:"selected_source_id"`
	SelectedPersonalType string                     `json:"selected_personal_type,omitempty"`
	SelectedContentHash  string                     `json:"selected_content_hash,omitempty"`
	ExcludedSourceIDs    []string                   `json:"excluded_source_ids,omitempty"`
	Sources              []projectSkillPolicySource `json:"sources,omitempty"`
}

type projectSkillKeepExternalProvider struct {
	Name       string `json:"name"`
	Provider   string `json:"provider"`
	SourceHash string `json:"source_hash"`
}

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

func newCanonicalStore(superDolphinHome string) *canonicalStore {
	return &canonicalStore{superDolphinHome: strings.TrimSpace(superDolphinHome)}
}

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
	if _, err := os.Stat(root.path); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var records []canonicalSkillRecord
	err := filepath.WalkDir(root.path, func(path string, entry os.DirEntry, walkErr error) error {
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

func (s *canonicalStore) applyEffectivePolicies(cwd string, records []canonicalSkillRecord) ([]canonicalSkillRecord, error) {
	filtered, err := applyProjectSkillPolicy(cwd, records)
	if err != nil {
		return nil, err
	}
	return s.applyPersonalSkillPolicy(filtered)
}

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
func filterCanonicalRecords(records []canonicalSkillRecord, keep func(canonicalSkillRecord) bool) []canonicalSkillRecord {
	filtered := make([]canonicalSkillRecord, 0, len(records))
	for _, record := range records {
		if keep(record) {
			filtered = append(filtered, record)
		}
	}
	return filtered
}

func stringSliceContains(items []string, want string) bool {
	want = strings.TrimSpace(want)
	for _, item := range items {
		if strings.TrimSpace(item) == want {
			return true
		}
	}
	return false
}
