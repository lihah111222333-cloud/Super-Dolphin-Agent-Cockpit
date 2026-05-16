package skill

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type SkillMirrorTarget struct {
	TargetID        string
	Provider        SkillProvider
	Scope           string
	Root            string
	CanonicalRootID string
}

func PublishSkillMirrors(ctx context.Context, records []canonicalSkillRecord, targets []SkillMirrorTarget) (SkillMirrorReport, error) {
	var report SkillMirrorReport
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		targetReport, err := publishSkillMirrorTarget(recordsForMirrorTarget(records, target), target)
		appendSkillMirrorReport(&report, targetReport)
		if err != nil {
			return report, err
		}
	}
	return report, nil
}

func appendSkillMirrorReport(r *SkillMirrorReport, other SkillMirrorReport) {
	r.Published = append(r.Published, other.Published...)
	r.Skipped = append(r.Skipped, other.Skipped...)
	r.Deleted = append(r.Deleted, other.Deleted...)
	r.Conflicts = append(r.Conflicts, other.Conflicts...)
}

func recordsForMirrorTarget(records []canonicalSkillRecord, target SkillMirrorTarget) []canonicalSkillRecord {
	filtered := make([]canonicalSkillRecord, 0, len(records))
	for _, record := range records {
		if record.Scope == strings.TrimSpace(target.Scope) {
			filtered = append(filtered, record)
		}
	}
	return filtered
}

func publishSkillMirrorTarget(records []canonicalSkillRecord, target SkillMirrorTarget) (SkillMirrorReport, error) {
	if err := validateSkillMirrorTarget(target); err != nil {
		return SkillMirrorReport{}, err
	}
	if err := prepareMirrorRoot(target.Root); err != nil {
		return SkillMirrorReport{}, err
	}
	manifestPath := filepath.Join(target.Root, skillMirrorManifestFile)
	manifest, err := loadSkillMirrorManifest(manifestPath, target)
	if err != nil {
		return SkillMirrorReport{}, err
	}
	report, err := deleteMissingMirrorEntries(&manifest, target, records)
	if err != nil {
		return report, err
	}
	published, err := publishCanonicalRecords(&manifest, target, records)
	appendSkillMirrorReport(&report, published)
	if err != nil {
		return report, err
	}
	if err := writeSkillMirrorManifest(manifestPath, manifest); err != nil {
		return report, err
	}
	return report, nil
}

func validateSkillMirrorTarget(target SkillMirrorTarget) error {
	if strings.TrimSpace(target.TargetID) == "" {
		return fmt.Errorf("skill mirror target_id is required")
	}
	if !supportedSkillProvider(target.Provider) {
		return fmt.Errorf("unsupported skill mirror provider %q", target.Provider)
	}
	if !supportedMirrorScope(target.Scope) {
		return fmt.Errorf("unsupported skill mirror scope %q", target.Scope)
	}
	if !validMirrorRoot(target.Root) {
		return fmt.Errorf("skill mirror target root must be absolute")
	}
	if unsafeMirrorRootString(target.Root) {
		return fmt.Errorf("unsafe skill mirror target root %q", target.Root)
	}
	if target.Scope == skillScopePersonal && !strings.HasPrefix(target.CanonicalRootID, "sd_owner:") {
		return fmt.Errorf("personal skill mirror canonical_root_id must be owner_key")
	}
	return nil
}

func supportedSkillProvider(provider SkillProvider) bool {
	return provider == SkillProviderClaude || provider == SkillProviderCodex
}

func supportedMirrorScope(scope string) bool {
	return scope == skillScopeProject || scope == skillScopePersonal
}

func validMirrorRoot(root string) bool {
	root = strings.TrimSpace(root)
	return root != "" && filepath.IsAbs(root) && filepath.Base(filepath.Clean(root)) == "skills"
}

func unsafeMirrorRootString(root string) bool {
	return strings.Contains(root, "\x00") || strings.Contains(filepath.ToSlash(root), "/../")
}

func prepareMirrorRoot(root string) error {
	if err := rejectSymlinkAncestors(root); err != nil {
		return err
	}
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return os.MkdirAll(root, 0o755)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("skill mirror root is symlink: %s", root)
	}
	if !info.IsDir() {
		return fmt.Errorf("skill mirror root is not a directory: %s", root)
	}
	return nil
}

func rejectSymlinkAncestors(root string) error {
	path, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("normalize skill mirror root: %w", err)
	}
	rootPath := path
	for {
		info, err := os.Lstat(path)
		parent := filepath.Dir(path)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("skill mirror root ancestor is symlink: %s", path)
			}
			if path != rootPath || parent == path {
				return nil
			}
			path = parent
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if parent == path {
			return nil
		}
		path = parent
	}
}

func loadSkillMirrorManifest(path string, target SkillMirrorTarget) (SkillMirrorManifest, error) {
	manifest, err := readSkillMirrorManifest(path)
	if errors.Is(err, os.ErrNotExist) {
		return newSkillMirrorManifest(target), nil
	}
	if err != nil {
		return SkillMirrorManifest{}, err
	}
	if manifest.Provider != string(target.Provider) || manifest.Scope != target.Scope || manifest.CanonicalRootID != target.CanonicalRootID {
		return SkillMirrorManifest{}, fmt.Errorf("skill mirror manifest target mismatch")
	}
	if manifest.Skills == nil {
		manifest.Skills = make(map[string]SkillMirrorEntry)
	}
	return manifest, nil
}

func newSkillMirrorManifest(target SkillMirrorTarget) SkillMirrorManifest {
	return SkillMirrorManifest{
		Version:         1,
		Manager:         "super-dolphin",
		Scope:           target.Scope,
		Provider:        string(target.Provider),
		CanonicalRootID: target.CanonicalRootID,
		GeneratedAt:     time.Now().UTC(),
		Skills:          make(map[string]SkillMirrorEntry),
	}
}

func deleteMissingMirrorEntries(manifest *SkillMirrorManifest, target SkillMirrorTarget, records []canonicalSkillRecord) (SkillMirrorReport, error) {
	var report SkillMirrorReport
	present := canonicalRecordsByName(records)
	for name, entry := range manifest.Skills {
		if _, ok := present[name]; ok {
			continue
		}
		item, deleted, err := deleteMissingMirrorEntry(target, name, entry)
		if err != nil || !deleted {
			if !deleted {
				report.Conflicts = append(report.Conflicts, item)
			}
			return report, err
		}
		delete(manifest.Skills, name)
		report.Deleted = append(report.Deleted, item)
	}
	return report, nil
}

func deleteMissingMirrorEntry(target SkillMirrorTarget, name string, entry SkillMirrorEntry) (SkillMirrorReportItem, bool, error) {
	item := reportItem(target, name, entry.CanonicalID, entry.MirrorHash, "", "")
	finalDir := filepath.Join(target.Root, name)
	hash, exists, err := existingMirrorHash(finalDir)
	item.OldHash = hash
	if err != nil {
		return item, false, err
	}
	if !exists {
		return item, true, nil
	}
	if !entry.Owned || hash != entry.MirrorHash {
		item.ConflictKind = "drift"
		return item, false, nil
	}
	return item, true, os.RemoveAll(finalDir)
}

func publishCanonicalRecords(manifest *SkillMirrorManifest, target SkillMirrorTarget, records []canonicalSkillRecord) (SkillMirrorReport, error) {
	var report SkillMirrorReport
	sort.SliceStable(records, func(i, j int) bool { return records[i].Name < records[j].Name })
	for _, record := range records {
		item, ok, err := publishCanonicalRecord(manifest, target, record)
		if err != nil {
			return report, err
		}
		if ok {
			report.Published = append(report.Published, item)
			continue
		}
		if item.ConflictKind == "" && item.NewHash != "" {
			report.Skipped = append(report.Skipped, item)
			continue
		}
		report.Conflicts = append(report.Conflicts, item)
	}
	return report, nil
}

func publishCanonicalRecord(manifest *SkillMirrorManifest, target SkillMirrorTarget, record canonicalSkillRecord) (SkillMirrorReportItem, bool, error) {
	name := record.Name
	entry, managed := manifest.Skills[name]
	finalDir := filepath.Join(target.Root, name)
	oldHash, exists, err := existingMirrorHash(finalDir)
	if err != nil {
		return reportItem(target, name, canonicalSourceID(record), "", "", ""), false, err
	}
	item := reportItem(target, name, canonicalSourceID(record), oldHash, "", "")
	if conflictKind := existingMirrorConflictKind(managed, exists, entry, oldHash); conflictKind != "" {
		item.ConflictKind = conflictKind
		return item, false, nil
	}
	canonicalHash, err := stableMirrorDirectoryHash(record.Dir)
	if err != nil {
		return item, false, err
	}
	if unchangedOwnedMirror(managed, exists, entry, oldHash, canonicalHash) {
		item.NewHash = oldHash
		return item, false, nil
	}
	return replaceChangedMirrorRecord(manifest, target, record, canonicalHash, item)
}

func existingMirrorConflictKind(managed, exists bool, entry SkillMirrorEntry, oldHash string) string {
	switch {
	case exists && !managed:
		return "unmanaged"
	case managed && exists && (!entry.Owned || oldHash != entry.MirrorHash):
		return "drift"
	default:
		return ""
	}
}

func unchangedOwnedMirror(managed, exists bool, entry SkillMirrorEntry, oldHash, canonicalHash string) bool {
	return managed && exists && entry.Owned && oldHash == entry.MirrorHash && entry.CanonicalHash == canonicalHash
}

func replaceChangedMirrorRecord(manifest *SkillMirrorManifest, target SkillMirrorTarget, record canonicalSkillRecord, canonicalHash string, item SkillMirrorReportItem) (SkillMirrorReportItem, bool, error) {
	newHash, err := replaceMirrorSkillDir(target.Root, record.Name, record.Dir)
	if err != nil {
		return item, false, err
	}
	item.NewHash = newHash
	manifest.Skills[record.Name] = mirrorManifestEntry(record, canonicalHash, newHash)
	manifest.GeneratedAt = time.Now().UTC()
	return item, true, nil
}

func existingMirrorHash(dir string) (string, bool, error) {
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", true, fmt.Errorf("skill mirror path is symlink: %s", dir)
	}
	if !info.IsDir() {
		return "", true, fmt.Errorf("skill mirror path is not a directory: %s", dir)
	}
	hash, err := stableMirrorDirectoryHash(dir)
	return hash, true, err
}

func replaceMirrorSkillDir(root, name, canonicalDir string) (string, error) {
	tempDir, err := os.MkdirTemp(root, ".skill-mirror-"+name+"-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tempDir)
	if err := copyCanonicalSkillDir(canonicalDir, tempDir); err != nil {
		return "", err
	}
	hash, err := stableMirrorDirectoryHash(tempDir)
	if err != nil {
		return "", err
	}
	finalDir := filepath.Join(root, name)
	if err := os.RemoveAll(finalDir); err != nil {
		return "", err
	}
	return hash, os.Rename(tempDir, finalDir)
}

func copyCanonicalSkillDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := safeCanonicalCopyRelativePath(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		return copyCanonicalSkillEntry(path, filepath.Join(dst, filepath.FromSlash(rel)), rel, entry)
	})
}

func safeCanonicalCopyRelativePath(root, path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("normalize canonical path %s: %w", path, err)
	}
	rel, err := filepath.Rel(root, filepath.Clean(absPath))
	if err != nil {
		return "", fmt.Errorf("rel canonical path %s: %w", path, err)
	}
	rel = filepath.ToSlash(rel)
	if rel == "." {
		return rel, nil
	}
	if unsafeMirrorRelativePath(rel) {
		return "", fmt.Errorf("unsafe canonical path %s escapes root", path)
	}
	return rel, nil
}

func copyCanonicalSkillEntry(src, dst, rel string, entry fs.DirEntry) error {
	info, err := entry.Info()
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("canonical skill path is symlink: %s", src)
	}
	if info.IsDir() {
		return os.MkdirAll(dst, 0o755)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("canonical skill path is not regular: %s", src)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, mirrorFileMode(rel, info.Mode(), data))
}

func mirrorFileMode(rel string, mode os.FileMode, data []byte) os.FileMode {
	if strings.HasPrefix(filepath.ToSlash(rel), "scripts/") && mode.Perm()&0o111 != 0 && strings.HasPrefix(string(data), "#!") {
		return 0o755
	}
	return 0o644
}

func mirrorManifestEntry(record canonicalSkillRecord, canonicalHash, mirrorHash string) SkillMirrorEntry {
	return SkillMirrorEntry{
		CanonicalID:   canonicalSourceID(record),
		CanonicalHash: canonicalHash,
		MirrorHash:    mirrorHash,
		SourceType:    record.Scope,
		PersonalType:  record.PersonalType,
		Owned:         true,
	}
}

func canonicalRecordsByName(records []canonicalSkillRecord) map[string]canonicalSkillRecord {
	byName := make(map[string]canonicalSkillRecord, len(records))
	for _, record := range records {
		byName[record.Name] = record
	}
	return byName
}

func reportItem(target SkillMirrorTarget, rel, canonicalID, oldHash, newHash, kind string) SkillMirrorReportItem {
	return SkillMirrorReportItem{
		TargetID:           target.TargetID,
		Provider:           target.Provider,
		Scope:              target.Scope,
		RelativeMirrorPath: filepath.ToSlash(rel),
		CanonicalID:        canonicalID,
		OldHash:            oldHash,
		NewHash:            newHash,
		ConflictKind:       kind,
	}
}

func (s *service) publishWriteTimeMirrors(ctx context.Context, cwd, scope, personalType, name string) SkillMirrorReport {
	targets := s.writeTimeMirrorTargets(cwd, scope)
	if len(targets) == 0 {
		return unconfiguredMirrorPublishReport(scope, personalType, name)
	}
	records, err := newCanonicalStore(s.resolvedSuperDolphinHome()).scan(cwd)
	if err != nil {
		return mirrorPublishErrorReport(targets, scope, personalType, name, err)
	}
	report, err := PublishSkillMirrors(ctx, records, targets)
	if err != nil {
		appendSkillMirrorReport(&report, mirrorPublishErrorReport(targets, scope, personalType, name, err))
	}
	return report
}

func (s *service) writeTimeMirrorTargets(cwd, scope string) []SkillMirrorTarget {
	scope = strings.TrimSpace(scope)
	if scope != skillScopeProject {
		return uniqueMirrorTargets(s.configuredMirrorTargets(scope))
	}
	fingerprint := RepoFingerprint(cwd)
	targets := []SkillMirrorTarget{
		SkillMirrorTarget{
			TargetID:        "claude:project:" + fingerprint,
			Provider:        SkillProviderClaude,
			Scope:           skillScopeProject,
			Root:            filepath.Join(cwd, ".claude", "skills"),
			CanonicalRootID: fingerprint,
		},
	}
	return uniqueMirrorTargets(targets)
}

func (s *service) configuredMirrorTargets(scope string) []SkillMirrorTarget {
	if s == nil {
		return nil
	}
	targets := make([]SkillMirrorTarget, 0, len(s.mirrorTargets))
	for _, target := range s.mirrorTargets {
		if strings.TrimSpace(target.Scope) == scope {
			targets = append(targets, target)
		}
	}
	return targets
}

func uniqueMirrorTargets(targets []SkillMirrorTarget) []SkillMirrorTarget {
	seen := make(map[string]bool, len(targets))
	out := make([]SkillMirrorTarget, 0, len(targets))
	for _, target := range targets {
		key := target.TargetID + "\x00" + filepath.Clean(target.Root)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, target)
	}
	return out
}

func unconfiguredMirrorPublishReport(scope, personalType, name string) SkillMirrorReport {
	if scope != skillScopePersonal {
		return SkillMirrorReport{}
	}
	return SkillMirrorReport{Conflicts: []SkillMirrorReportItem{{
		TargetID:           "personal:unconfigured",
		Scope:              skillScopePersonal,
		CanonicalID:        canonicalIDForPublishReport(scope, personalType, name),
		ConflictKind:       "publish_targets_unconfigured",
		RelativeMirrorPath: "",
	}}}
}

func mirrorPublishErrorReport(targets []SkillMirrorTarget, scope, personalType, name string, err error) SkillMirrorReport {
	report := SkillMirrorReport{Conflicts: make([]SkillMirrorReportItem, 0, len(targets))}
	for _, target := range targets {
		item := reportItem(target, skillSlug(name), canonicalIDForPublishReport(scope, personalType, name), "", "", "publish_error")
		if err != nil {
			item.Error = err.Error()
		}
		report.Conflicts = append(report.Conflicts, item)
	}
	return report
}

func canonicalIDForPublishReport(scope, personalType, name string) string {
	slug := skillSlug(name)
	if scope == skillScopePersonal {
		return "personal/" + strings.TrimSpace(personalType) + "/" + slug
	}
	return "project/" + slug
}

func attachMirrorPublish(result any, report SkillMirrorReport) any {
	if payload, ok := result.(map[string]any); ok {
		payload["mirror_publish"] = report
		return payload
	}
	return result
}
