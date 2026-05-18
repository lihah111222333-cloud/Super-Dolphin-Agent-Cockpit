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
	"sync"
	"time"
)

var skillMirrorRootLocks sync.Map

type SkillMirrorTarget struct {
	TargetID, Scope, Root, CanonicalRootID string
	Provider                               SkillProvider
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
		if record.Scope == strings.TrimSpace(target.Scope) && !record.info.DisableModelInvocation {
			filtered = append(filtered, record)
		}
	}
	return filtered
}
func publishSkillMirrorTarget(records []canonicalSkillRecord, target SkillMirrorTarget) (SkillMirrorReport, error) {
	if err := validateSkillMirrorTarget(target); err != nil {
		return SkillMirrorReport{}, err
	}
	unlock := lockSkillMirrorRoot(target.Root)
	defer unlock()
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
	unmanaged, err := unmanagedProviderMirrorReport(target, manifest, records)
	appendSkillMirrorReport(&report, unmanaged)
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
func lockSkillMirrorRoot(root string) func() {
	key := filepath.Clean(strings.TrimSpace(root))
	value, _ := skillMirrorRootLocks.LoadOrStore(key, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}
func unmanagedProviderMirrorReport(target SkillMirrorTarget, manifest SkillMirrorManifest, records []canonicalSkillRecord) (SkillMirrorReport, error) {
	var report SkillMirrorReport
	names, err := skillMirrorNames(target.Root)
	if err != nil {
		return report, err
	}
	present := canonicalRecordsByName(records)
	for _, name := range names {
		if _, managed := manifest.Skills[name]; managed {
			continue
		}
		if _, ok := present[name]; ok {
			continue
		}
		dir := filepath.Join(target.Root, name)
		if !skillMainFileExists(dir) {
			continue
		}
		hash, exists, err := existingMirrorHash(dir)
		if err != nil {
			return report, err
		}
		if !exists {
			continue
		}
		report.Conflicts = append(report.Conflicts, reportItem(target, name, "", hash, "", skillConflictUnmanagedProviderSkill))
	}
	return report, nil
}
func validateSkillMirrorTarget(target SkillMirrorTarget) error {
	if strings.TrimSpace(target.TargetID) == "" {
		return fmt.Errorf("skill mirror target_id is required")
	}
	if target.Provider != SkillProviderClaude && target.Provider != SkillProviderCodex {
		return fmt.Errorf("unsupported skill mirror provider %q", target.Provider)
	}
	if target.Scope != skillScopeProject && target.Scope != skillScopePersonal {
		return fmt.Errorf("unsupported skill mirror scope %q", target.Scope)
	}
	if err := validateMirrorRoot(target.Root); err != nil {
		return err
	}
	if target.Scope == skillScopePersonal && !strings.HasPrefix(target.CanonicalRootID, "sd_owner:") {
		return fmt.Errorf("personal skill mirror canonical_root_id must be owner_key")
	}
	return nil
}
func validateMirrorRoot(root string) error {
	if strings.TrimSpace(root) == "" || !filepath.IsAbs(strings.TrimSpace(root)) || filepath.Base(filepath.Clean(strings.TrimSpace(root))) != "skills" {
		return fmt.Errorf("skill mirror target root must be absolute")
	}
	if strings.Contains(root, "\x00") || strings.Contains(filepath.ToSlash(root), "/../") {
		return fmt.Errorf("unsafe skill mirror target root %q", root)
	}
	return nil
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
	switch {
	case exists && !managed:
		item.ConflictKind = "unmanaged"
		return item, false, nil
	case driftedManagedMirror(managed, exists, entry, oldHash):
		item.ConflictKind = "drift"
		return item, false, nil
	}
	canonicalHash, err := stableMirrorDirectoryHash(record.Dir)
	if err != nil {
		return item, false, err
	}
	unchanged, err := unchangedOwnedMirror(managed, exists, entry, oldHash, canonicalHash, target.Scope, finalDir)
	if err != nil {
		return item, false, err
	}
	if unchanged {
		item.NewHash = oldHash
		return item, false, nil
	}
	return replaceChangedMirrorRecord(manifest, target, record, canonicalHash, item)
}
func driftedManagedMirror(managed, exists bool, entry SkillMirrorEntry, oldHash string) bool {
	return managed && exists && (!entry.Owned || oldHash != entry.MirrorHash)
}
func unchangedOwnedMirror(managed, exists bool, entry SkillMirrorEntry, oldHash, canonicalHash, scope, dir string) (bool, error) {
	if !(managed && exists && entry.Owned && oldHash == entry.MirrorHash && entry.CanonicalHash == canonicalHash) {
		return false, nil
	}
	if scope != skillScopeProject {
		return true, nil
	}
	data, err := os.ReadFile(filepath.Join(dir, skillMainFile))
	if err != nil {
		return false, err
	}
	content := string(data)
	return capProjectMirrorTrustFrontmatter(content) == content, nil
}
func replaceChangedMirrorRecord(manifest *SkillMirrorManifest, target SkillMirrorTarget, record canonicalSkillRecord, canonicalHash string, item SkillMirrorReportItem) (SkillMirrorReportItem, bool, error) {
	newHash, err := replaceMirrorSkillDir(target.Root, record.Name, record.Dir, target.Scope)
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
func replaceMirrorSkillDir(root, name, canonicalDir, scope string) (string, error) {
	tempDir, err := os.MkdirTemp(root, ".skill-mirror-"+name+"-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tempDir)
	if err := copyCanonicalSkillDir(canonicalDir, tempDir, scope); err != nil {
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
func copyCanonicalSkillDir(src, dst, scope string) error {
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
		return copyCanonicalSkillEntry(path, filepath.Join(dst, filepath.FromSlash(rel)), rel, scope, entry)
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
func copyCanonicalSkillEntry(src, dst, rel, scope string, entry fs.DirEntry) error {
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
	if scope == skillScopeProject && strings.EqualFold(filepath.Base(filepath.ToSlash(rel)), skillMainFile) {
		data = []byte(capProjectMirrorTrustFrontmatter(string(data)))
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, mirrorFileMode(rel, info.Mode(), data))
}
func capProjectMirrorTrustFrontmatter(content string) string {
	frontmatter, body, ok := splitFrontmatter(content)
	if !ok {
		return content
	}
	lines := strings.Split(frontmatter, "\n")
	for i, line := range lines {
		if key, value, ok := parseMetaLine(line); ok && metaKeyMatch(key, trustMetaKeys) && parseTrustScope(parseScalar(value)).Trusted() {
			lines[i] = "trust: project"
		}
	}
	return "---\n" + strings.Join(lines, "\n") + "\n---\n" + body
}
func mirrorFileMode(rel string, mode os.FileMode, data []byte) os.FileMode {
	if strings.HasPrefix(filepath.ToSlash(rel), "scripts/") && mode.Perm()&0o111 != 0 && strings.HasPrefix(string(data), "#!") {
		return 0o755
	}
	return 0o644
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
	records, conflicts, err := newCanonicalStoreForOwner(s.resolvedSuperDolphinHome(), defaultOwnerOSUID(), defaultAppProfile()).EffectiveSet(ctx, cwd)
	if err != nil {
		return mirrorPublishErrorReport(targets, scope, personalType, name, err)
	}
	if len(conflicts) > 0 {
		var report SkillMirrorReport
		appendCanonicalConflictReportItems(&report, targets, conflicts)
		return report
	}
	report, err := PublishSkillMirrors(ctx, records, targets)
	if err != nil {
		appendSkillMirrorReport(&report, mirrorPublishErrorReport(targets, scope, personalType, name, err))
	}
	return report
}
func (s *service) publishWriteTimeMirrorsForScope(ctx context.Context, cwd, scope, personalType, name string) SkillMirrorReport {
	targets := s.writeTimeMirrorTargets(cwd, scope)
	if len(targets) == 0 {
		return unconfiguredMirrorPublishReport(scope, personalType, name)
	}
	store := newCanonicalStoreForOwner(s.resolvedSuperDolphinHome(), defaultOwnerOSUID(), defaultAppProfile())
	records, err := store.scan(cwd)
	if err != nil {
		return mirrorPublishErrorReport(targets, scope, personalType, name, err)
	}
	records, err = store.applyEffectivePolicies(cwd, records)
	if err != nil {
		return mirrorPublishErrorReport(targets, scope, personalType, name, err)
	}
	records = filterCanonicalRecordsForScope(records, scope)
	conflicts := canonicalSameNameConflicts(records)
	if len(conflicts) > 0 {
		var report SkillMirrorReport
		appendCanonicalConflictReportItems(&report, targets, conflicts)
		return report
	}
	report, err := PublishSkillMirrors(ctx, records, targets)
	if err != nil {
		appendSkillMirrorReport(&report, mirrorPublishErrorReport(targets, scope, personalType, name, err))
	}
	return report
}
func (s *service) publishWriteTimeMirrorsForEffectiveSet(ctx context.Context, cwd, name string) SkillMirrorReport {
	targets := uniqueMirrorTargets(append(s.writeTimeMirrorTargets(cwd, skillScopeProject), s.writeTimeMirrorTargets(cwd, skillScopePersonal)...))
	if len(targets) == 0 {
		return unconfiguredMirrorPublishReport("", "", name)
	}
	records, conflicts, err := newCanonicalStoreForOwner(s.resolvedSuperDolphinHome(), defaultOwnerOSUID(), defaultAppProfile()).EffectiveSet(ctx, cwd)
	if err != nil {
		return mirrorPublishErrorReport(targets, "", "", name, err)
	}
	if len(conflicts) > 0 {
		var report SkillMirrorReport
		appendCanonicalConflictReportItems(&report, targets, conflicts)
		return report
	}
	report, err := PublishSkillMirrors(ctx, records, targets)
	if err != nil {
		appendSkillMirrorReport(&report, mirrorPublishErrorReport(targets, "", "", name, err))
	}
	return report
}
