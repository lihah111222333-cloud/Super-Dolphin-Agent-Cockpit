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

type projectSkillPolicySource struct {
	CanonicalID  string `json:"canonical_id"`
	Scope        string `json:"scope"`
	PersonalType string `json:"personal_type,omitempty"`
	ContentHash  string `json:"content_hash,omitempty"`
}

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

func (s *canonicalStore) scanRoots(cwd string) []canonicalScanRoot {
	roots := []canonicalScanRoot{
		{path: defaultProjectSkillsRoot(cwd), scope: skillScopeProject, defaultTrust: TrustProject},
	}
	if strings.TrimSpace(s.superDolphinHome) == "" {
		return roots
	}
	personalRoot := filepath.Join(s.superDolphinHome, "skills", "personal")
	return append(roots,
		canonicalScanRoot{path: filepath.Join(personalRoot, personalSkillTypeUser), scope: skillScopePersonal, personalType: personalSkillTypeUser, defaultTrust: TrustUser},
		canonicalScanRoot{path: filepath.Join(personalRoot, personalSkillTypeAgent), scope: skillScopePersonal, personalType: personalSkillTypeAgent, defaultTrust: TrustUser},
		canonicalScanRoot{path: filepath.Join(personalRoot, personalSkillTypeImported), scope: skillScopePersonal, personalType: personalSkillTypeImported, defaultTrust: TrustUser},
		canonicalScanRoot{path: filepath.Join(personalRoot, personalSkillTypeHub), scope: skillScopePersonal, personalType: personalSkillTypeHub, defaultTrust: TrustUser},
	)
}

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
	name, err := validateSkillName(parsed.info.Name)
	if err != nil {
		return nil, fmt.Errorf("validate canonical skill %s: %w", path, err)
	}
	dir := filepath.Dir(path)
	parsed.info.Name = name
	parsed.info.Scope = root.scope
	parsed.info.PersonalType = root.personalType
	return &canonicalSkillRecord{
		Name:         name,
		Scope:        root.scope,
		PersonalType: root.personalType,
		Dir:          dir,
		SkillFile:    path,
		ContentHash:  parsed.info.ContentHash,
		DirHash:      skillDirContentHash(dir),
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
		name, err := validateSkillName(item.Name)
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
	return filterCanonicalRecords(records, func(record canonicalSkillRecord) bool {
		return keepCanonicalRecordForProjectSelection(record, selectionByName)
	}), nil
}

func projectSelectionByName(selections []projectSkillKeepSelected) (map[string]projectSkillKeepSelected, error) {
	selectionByName := make(map[string]projectSkillKeepSelected, len(selections))
	for _, selection := range selections {
		name, err := validateSkillName(selection.Name)
		if err != nil {
			return nil, fmt.Errorf("invalid project skill policy name: %w", err)
		}
		selection.Name = name
		selectionByName[strings.ToLower(name)] = selection
	}
	return selectionByName, nil
}

func keepCanonicalRecordForProjectSelection(record canonicalSkillRecord, selectionByName map[string]projectSkillKeepSelected) bool {
	selection, ok := selectionByName[strings.ToLower(record.Name)]
	if !ok {
		return true
	}
	sourceID := canonicalSourceID(record)
	if selectedProjectCanonicalRecord(record, selection, sourceID) {
		return true
	}
	return !stringSliceContains(selection.ExcludedSourceIDs, sourceID)
}

func selectedProjectCanonicalRecord(record canonicalSkillRecord, selection projectSkillKeepSelected, sourceID string) bool {
	return sourceID == strings.TrimSpace(selection.SelectedSourceID) &&
		record.PersonalType == strings.TrimSpace(selection.SelectedPersonalType) &&
		(strings.TrimSpace(selection.SelectedContentHash) == "" || record.ContentHash == strings.TrimSpace(selection.SelectedContentHash))
}

func readProjectSkillPolicy(cwd string) (projectSkillPolicy, error) {
	var policy projectSkillPolicy
	path := filepath.Join(defaultProjectSkillsRoot(cwd), projectSkillPolicyFile)
	if err := rejectWritableSymlinkComponentIfExists(path); err != nil {
		return projectSkillPolicy{}, err
	}
	if err := readJSONFileIfExists(path, &policy); err != nil {
		return projectSkillPolicy{}, err
	}
	return policy, nil
}

func writeProjectDisablePersonalPolicy(cwd, name, personalType string) (string, error) {
	name, err := validateSkillName(name)
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
	return writeSkillPolicyJSON(filepath.Join(defaultProjectSkillsRoot(cwd), projectSkillPolicyFile), policy, 0o644)
}

func writeProjectKeepSelectedPolicy(cwd, name string, sources []skillResolutionSource, selected skillResolutionSource) (string, error) {
	name, err := validateSkillName(name)
	if err != nil {
		return "", err
	}
	if selected.Scope != skillScopeProject {
		return "", fmt.Errorf("selected source must be project")
	}
	policy, err := readProjectSkillPolicy(cwd)
	if err != nil {
		return "", err
	}
	policy.Version = 1
	policy.KeepSelected = upsertProjectKeepSelected(policy.KeepSelected, buildProjectKeepSelected(name, sources, selected))
	return writeSkillPolicyJSON(filepath.Join(defaultProjectSkillsRoot(cwd), projectSkillPolicyFile), policy, 0o644)
}

func buildProjectKeepSelected(name string, sources []skillResolutionSource, selected skillResolutionSource) projectSkillKeepSelected {
	next := projectSkillKeepSelected{
		Name:                 name,
		SelectedSourceID:     selected.CanonicalID,
		SelectedPersonalType: selected.PersonalType,
		SelectedContentHash:  selected.ContentHash,
	}
	for _, source := range sources {
		next.Sources = append(next.Sources, projectSkillPolicySource{
			CanonicalID:  source.CanonicalID,
			Scope:        source.Scope,
			PersonalType: source.PersonalType,
			ContentHash:  source.ContentHash,
		})
		if source.CanonicalID != selected.CanonicalID {
			next.ExcludedSourceIDs = append(next.ExcludedSourceIDs, source.CanonicalID)
		}
	}
	return next
}

func upsertProjectKeepSelected(items []projectSkillKeepSelected, next projectSkillKeepSelected) []projectSkillKeepSelected {
	filtered := items[:0]
	for _, existing := range items {
		if strings.EqualFold(existing.Name, next.Name) {
			continue
		}
		filtered = append(filtered, existing)
	}
	return append(filtered, next)
}

func appendProjectDisabledPersonal(items []projectSkillPolicyDisabledPersonal, next projectSkillPolicyDisabledPersonal) []projectSkillPolicyDisabledPersonal {
	for _, item := range items {
		if strings.EqualFold(item.Name, next.Name) && strings.EqualFold(item.PersonalType, next.PersonalType) {
			return items
		}
	}
	return append(items, next)
}

func canonicalSourceID(record canonicalSkillRecord) string {
	name := strings.TrimSpace(record.Name)
	switch strings.TrimSpace(record.Scope) {
	case skillScopeProject:
		return skillScopeProject + "/" + name
	case skillScopePersonal:
		return skillScopePersonal + "/" + strings.TrimSpace(record.PersonalType) + "/" + name
	default:
		return strings.TrimSpace(record.Scope) + "/" + name
	}
}

func validateOwnerOnlyFileMode(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("owner policy is not a regular file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("owner policy permissions %v are broader than owner-only", info.Mode().Perm())
	}
	return nil
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
