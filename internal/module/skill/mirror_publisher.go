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

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/skill/mirrorpath"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/skill/skillhash"
)

// mirrorLockRegistry 按 provider mirror 根目录保存写入锁。
type mirrorLockRegistry struct{ m sync.Map }

// skillMirrorRootLocks 避免同一 skills 目录被并发发布流程同时删除或重写。
var skillMirrorRootLocks = &mirrorLockRegistry{}

// SkillMirrorTarget 指向 provider 会读取的 skills 目录。
// 这里的内容是生成物，不是真实 skill 来源。
type SkillMirrorTarget struct {
	TargetID, Scope, Root, CanonicalRootID string
	Provider                               SkillProvider
}

// PublishSkillMirrors 把真实 skill 目录复制到 provider mirror。
// 只做“真实来源 -> mirror”；遇到人工改动或未知目录要报告，不要自动覆盖。
func PublishSkillMirrors(ctx context.Context, records []canonicalSkillRecord, targets []SkillMirrorTarget) (SkillMirrorReport, error) {
	var report SkillMirrorReport
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		targetRecords := recordsForMirrorTarget(records, target)
		if targetUsesCanonicalSelfMirror(records, target) {
			targetRecords = canonicalRecordsForMirrorTargetScope(records, target)
		}
		targetReport, err := publishSkillMirrorTarget(targetRecords, target)
		appendSkillMirrorReport(&report, targetReport)
		if err != nil {
			return report, err
		}
	}
	return report, nil
}

// appendSkillMirrorReport 合并单个 target 的发布结果。
func appendSkillMirrorReport(r *SkillMirrorReport, other SkillMirrorReport) {
	r.Published = append(r.Published, other.Published...)
	r.Skipped = append(r.Skipped, other.Skipped...)
	r.Deleted = append(r.Deleted, other.Deleted...)
	r.Conflicts = append(r.Conflicts, other.Conflicts...)
}

// recordsForMirrorTarget 过滤当前 mirror target 可见的真实 skill。
// 禁用模型调用的 skill 不写入 provider mirror，避免运行时误触发。
func recordsForMirrorTarget(records []canonicalSkillRecord, target SkillMirrorTarget) []canonicalSkillRecord {
	filtered := make([]canonicalSkillRecord, 0, len(records))
	for _, record := range records {
		if record.Scope == strings.TrimSpace(target.Scope) && !record.info.DisableModelInvocation {
			filtered = append(filtered, record)
		}
	}
	return filtered
}

func canonicalRecordsForMirrorTargetScope(records []canonicalSkillRecord, target SkillMirrorTarget) []canonicalSkillRecord {
	filtered := make([]canonicalSkillRecord, 0, len(records))
	for _, record := range records {
		if record.Scope == strings.TrimSpace(target.Scope) {
			filtered = append(filtered, record)
		}
	}
	return filtered
}

// targetUsesCanonicalSelfMirror 判断 provider 目标是否就是当前项目 canonical skill 根。
// 这种场景不能按普通 mirror 处理，否则目录名和规范化 skill name 不一致时会把真实来源误报为外部 provider 内容。
func targetUsesCanonicalSelfMirror(records []canonicalSkillRecord, target SkillMirrorTarget) bool {
	if target.Scope != skillScopeProject {
		return false
	}
	root := filepath.Clean(strings.TrimSpace(target.Root))
	if filepath.Base(root) == "skills" && filepath.Base(filepath.Dir(root)) == ".agents" {
		return true
	}
	var scoped int
	for _, record := range records {
		if record.Scope != skillScopeProject {
			continue
		}
		scoped++
		if !sameCleanPath(filepath.Dir(record.Dir), target.Root) {
			return false
		}
	}
	return scoped > 0
}

// publishSkillMirrorTarget 刷新一个 provider skills 根。
// 只有本系统创建、且内容没被改过的目录，才会自动替换或删除。
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
	manifest, report, stop, err := loadPublishTargetManifest(records, target, manifestPath)
	if err != nil {
		return report, err
	}
	if stop {
		return report, nil
	}
	if targetUsesCanonicalSelfMirror(records, target) {
		return publishCanonicalSelfMirrorTarget(records, target, manifestPath)
	}
	report, err = deleteMissingMirrorEntries(&manifest, target, records)
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

// publishCanonicalSelfMirrorTarget 记录 canonical 根自身的 mirror manifest。
// 源目录和 provider 读取目录相同时不能自我复制或做 unmanaged 检查；scan 阶段已经对 canonical 目录做了 symlink/格式校验。
func publishCanonicalSelfMirrorTarget(records []canonicalSkillRecord, target SkillMirrorTarget, manifestPath string) (SkillMirrorReport, error) {
	if report := manualOnlySelfMirrorReport(records, target); len(report.Conflicts) > 0 {
		return report, nil
	}
	manifest := newSkillMirrorManifest(target)
	for _, record := range records {
		if record.Scope != target.Scope {
			continue
		}
		hash, err := stableMirrorDirectoryHash(record.Dir)
		if err != nil {
			return SkillMirrorReport{}, err
		}
		manifest.Skills[record.Name] = mirrorManifestEntry(record, hash, hash)
	}
	if err := writeSkillMirrorManifest(manifestPath, manifest); err != nil {
		return SkillMirrorReport{}, err
	}
	return SkillMirrorReport{}, nil
}

func manualOnlySelfMirrorReport(records []canonicalSkillRecord, target SkillMirrorTarget) SkillMirrorReport {
	var report SkillMirrorReport
	for _, record := range records {
		if record.Scope != target.Scope || !record.info.DisableModelInvocation {
			continue
		}
		report.Conflicts = append(report.Conflicts, reportItem(target, record.Name, canonicalSourceID(record), "", "", skillConflictManualOnlySelfMirror))
	}
	return report
}

// loadPublishTargetManifest 加载或修复目标 manifest。
// project manifest 不匹配时只报告冲突；personal mirror 可在来源可确认时重建 manifest。
func loadPublishTargetManifest(records []canonicalSkillRecord, target SkillMirrorTarget, manifestPath string) (SkillMirrorManifest, SkillMirrorReport, bool, error) {
	manifest, err := loadSkillMirrorManifest(manifestPath, target)
	if err == nil {
		return manifest, SkillMirrorReport{}, false, nil
	}
	if !errors.Is(err, errSkillMirrorManifestTargetMismatch) {
		return SkillMirrorManifest{}, SkillMirrorReport{}, false, err
	}
	if target.Scope != skillScopePersonal {
		report, reportErr := projectMismatchedManifestPublishReport(records, target)
		return SkillMirrorManifest{}, report, true, reportErr
	}
	manifest, err = repairMismatchedSkillMirrorManifest(records, target)
	return manifest, SkillMirrorReport{}, false, err
}

// projectMismatchedManifestPublishReport 生成项目 manifest 不匹配的发布报告。
func projectMismatchedManifestPublishReport(records []canonicalSkillRecord, target SkillMirrorTarget) (SkillMirrorReport, error) {
	var report SkillMirrorReport
	names, err := skillMirrorNames(target.Root, DefaultSkillMirrorScanBudget())
	if err != nil {
		return report, err
	}
	present := canonicalRecordsByName(records)
	for _, name := range names {
		record, canonicalExists := present[name]
		mirrorDir := filepath.Join(target.Root, name)
		if !canonicalExists && !skillMainFileExists(mirrorDir) {
			continue
		}
		hash, exists, err := existingMirrorHash(mirrorDir)
		if err != nil {
			return report, err
		}
		if !exists {
			continue
		}
		canonicalID := ""
		kind := skillConflictUnmanagedProviderSkill
		if canonicalExists {
			canonicalID = canonicalSourceID(record)
			kind = "unmanaged"
		}
		report.Conflicts = append(report.Conflicts, reportItem(target, name, canonicalID, hash, "", kind))
	}
	if len(report.Conflicts) == 0 {
		return report, errSkillMirrorManifestTargetMismatch
	}
	return report, nil
}

// lockSkillMirrorRoot 序列化同一 mirror 根目录的发布流程。
// 返回的 unlock 必须由调用方 defer，避免错误路径留下全局互斥锁。
func lockSkillMirrorRoot(root string) func() {
	key := filepath.Clean(strings.TrimSpace(root))
	value, _ := skillMirrorRootLocks.m.LoadOrStore(key, &sync.Mutex{})
	mu, ok := value.(*sync.Mutex)
	if !ok {
		mu = &sync.Mutex{}
	}
	mu.Lock()
	return mu.Unlock
}

// unmanagedProviderMirrorReport 生成未托管 provider mirror 的报告。
func unmanagedProviderMirrorReport(target SkillMirrorTarget, manifest SkillMirrorManifest, records []canonicalSkillRecord) (SkillMirrorReport, error) {
	var report SkillMirrorReport
	names, err := skillMirrorNames(target.Root, DefaultSkillMirrorScanBudget())
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

// validateSkillMirrorTarget 校验 skill mirror 目标路径。
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

// validateMirrorRoot 校验 mirror 根目录是否可写且安全。
func validateMirrorRoot(root string) error {
	if strings.TrimSpace(root) == "" || !filepath.IsAbs(strings.TrimSpace(root)) || filepath.Base(filepath.Clean(strings.TrimSpace(root))) != "skills" {
		return fmt.Errorf("skill mirror target root must be absolute")
	}
	if strings.Contains(root, "\x00") || strings.Contains(filepath.ToSlash(root), "/../") {
		return fmt.Errorf("unsafe skill mirror target root %q", root)
	}
	return nil
}

// prepareMirrorRoot 准备 mirror 根目录并处理旧内容。
func prepareMirrorRoot(root string) error {
	if err := mirrorpath.RejectSymlinkAncestors(root); err != nil {
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

// deleteMissingMirrorEntries 删除已不存在的旧 mirror。
// 如果 mirror 被人改过，就报告出来，不直接删。
func deleteMissingMirrorEntries(manifest *SkillMirrorManifest, target SkillMirrorTarget, records []canonicalSkillRecord) (SkillMirrorReport, error) {
	var report SkillMirrorReport
	present := canonicalRecordsByName(records)
	for name, entry := range manifest.Skills {
		if _, ok := present[name]; ok {
			continue
		}
		item, deleted, err := deleteMissingMirrorEntry(target, name, entry)
		if err != nil {
			return report, err
		}
		if !deleted {
			if target.Scope == skillScopePersonal && item.ConflictKind == "drift" && !isProviderReadableMirrorDrift(target, item) {
				report.Skipped = append(report.Skipped, item)
				continue
			}
			report.Conflicts = append(report.Conflicts, item)
			return report, nil
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

// publishCanonicalRecords 把 canonical skill 记录发布到 provider mirror。
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
		if target.Scope == skillScopePersonal && item.ConflictKind == "drift" && !isProviderReadableMirrorDrift(target, item) {
			report.Skipped = append(report.Skipped, item)
			continue
		}
		report.Conflicts = append(report.Conflicts, item)
	}
	return report, nil
}

// publishCanonicalRecord 更新单个 skill 的 mirror。
// 遇到未知的同名目录要停下来报告，不能把它当作自动导入。
func publishCanonicalRecord(manifest *SkillMirrorManifest, target SkillMirrorTarget, record canonicalSkillRecord) (SkillMirrorReportItem, bool, error) {
	name := record.Name
	entry, managed := manifest.Skills[name]
	finalDir := filepath.Join(target.Root, name)
	oldHash, exists, err := existingMirrorHash(finalDir)
	if err != nil {
		return reportItem(target, name, canonicalSourceID(record), "", "", ""), false, err
	}
	item := reportItem(target, name, canonicalSourceID(record), oldHash, "", "")
	var canonicalHash string
	if exists && !managed {
		return publishUnmanagedMirrorRecord(manifest, target, record, oldHash, item, &canonicalHash)
	}
	if driftedManagedMirror(managed, exists, entry, oldHash, entry.CanonicalHash, true) {
		item.ConflictKind = "drift"
		return item, false, nil
	}
	canonicalHash, err = ensureCanonicalHash(canonicalHash, record.Dir)
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

func isProviderReadableMirrorDrift(target SkillMirrorTarget, item SkillMirrorReportItem) bool {
	if strings.ToLower(strings.TrimSpace(item.ConflictKind)) != "drift" {
		return false
	}
	scope := strings.ToLower(strings.TrimSpace(target.Scope))
	targetID := strings.ToLower(strings.TrimSpace(target.TargetID))
	return scope == skillScopeProject ||
		strings.Contains(targetID, ":project:") ||
		strings.Contains(targetID, ":app-managed:")
}

// ensureCanonicalHash 保证后续发布流程已有 canonical hash，避免调用方重复分支判断。
func ensureCanonicalHash(hash, dir string) (string, error) {
	if hash != "" {
		return hash, nil
	}
	return stableMirrorDirectoryHash(dir)
}

func publishUnmanagedMirrorRecord(manifest *SkillMirrorManifest, target SkillMirrorTarget, record canonicalSkillRecord, oldHash string, item SkillMirrorReportItem, canonicalHash *string) (SkillMirrorReportItem, bool, error) {
	adopted, err := adoptIdenticalUnmanagedMirror(manifest, target, record, oldHash, &item, canonicalHash)
	if err != nil || adopted {
		return item, false, err
	}
	item.ConflictKind = "unmanaged"
	return item, false, nil
}

// adoptIdenticalUnmanagedMirror 接管内容一致的未托管 mirror。
func adoptIdenticalUnmanagedMirror(manifest *SkillMirrorManifest, target SkillMirrorTarget, record canonicalSkillRecord, oldHash string, item *SkillMirrorReportItem, canonicalHash *string) (bool, error) {
	hash, err := stableMirrorDirectoryHash(record.Dir)
	if err != nil {
		return false, err
	}
	*canonicalHash = hash
	expectedHash, expectedContentHash, err := expectedCanonicalMirrorHashes(target.Root, record, target.Scope)
	if err != nil {
		return false, err
	}
	mirrorContentHash, err := skillDirContentHash(filepath.Join(target.Root, record.Name))
	if err != nil {
		return false, err
	}
	if oldHash != expectedHash && mirrorContentHash != expectedContentHash {
		return false, nil
	}
	item.NewHash = oldHash
	manifest.Skills[record.Name] = mirrorManifestEntry(record, hash, oldHash)
	manifest.GeneratedAt = time.Now().UTC()
	return true, nil
}

// unchangedOwnedMirror 判断已托管 mirror 是否无需更新。
func unchangedOwnedMirror(managed, exists bool, entry SkillMirrorEntry, oldHash, canonicalHash, scope, dir string) (bool, error) {
	if !(managed && exists && entry.Owned && oldHash == entry.MirrorHash && entry.CanonicalHash == canonicalHash) {
		return false, nil
	}
	if scope != skillScopeProject {
		return true, nil
	}
	tracker, err := skillhash.NewContentLimitTracker(dir)
	if err != nil {
		return false, err
	}
	data, err := skillhash.ReadFileWithLimits(filepath.Join(dir, skillMainFile), tracker.Limits())
	if err != nil {
		return false, err
	}
	content := string(data)
	return capProjectMirrorTrustFrontmatter(content) == content, nil
}
func replaceChangedMirrorRecord(manifest *SkillMirrorManifest, target SkillMirrorTarget, record canonicalSkillRecord, canonicalHash string, item SkillMirrorReportItem) (SkillMirrorReportItem, bool, error) {
	newHash, err := replaceMirrorSkillDir(target.Root, record.Name, record.Dir, target.Scope, record.info.DisplayName)
	if err != nil {
		return item, false, err
	}
	item.NewHash = newHash
	manifest.Skills[record.Name] = mirrorManifestEntry(record, canonicalHash, newHash)
	manifest.GeneratedAt = time.Now().UTC()
	return item, true, nil
}

type skillMirrorIdentity struct {
	Name        string
	DisplayName string
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

// replaceMirrorSkillDir 用临时目录重建单个 mirror。
// 这里只复制真实来源目录，不从旧 mirror 里补内容。
func replaceMirrorSkillDir(root, name, canonicalDir, scope string, displayName ...string) (string, error) {
	tempDir, err := os.MkdirTemp(root, ".skill-mirror-"+name+"-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tempDir)
	identity := skillMirrorIdentity{Name: name}
	if len(displayName) > 0 {
		identity.DisplayName = displayName[0]
	}
	if err := copyCanonicalSkillDir(canonicalDir, tempDir, scope, identity); err != nil {
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

// copyCanonicalSkillDir 把真实 skill 复制到 mirror。
// symlink、越界路径和非常规文件都要拒绝。
func copyCanonicalSkillDir(src, dst, scope string, identity ...skillMirrorIdentity) error {
	tracker, err := skillhash.NewContentLimitTracker(src)
	if err != nil {
		return err
	}
	err = filepath.WalkDir(src, func(path string, entry fs.DirEntry, walkErr error) error {
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
		return copyCanonicalSkillEntry(path, filepath.Join(dst, filepath.FromSlash(rel)), rel, scope, entry, &tracker)
	})
	if err != nil {
		return err
	}
	if len(identity) > 0 && strings.TrimSpace(identity[0].Name) != "" {
		return rewriteCopiedSkillIdentity(dst, identity[0].Name, identity[0].DisplayName)
	}
	return nil
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
	if mirrorpath.UnsafeRelative(rel) {
		return "", fmt.Errorf("unsafe canonical path %s escapes root", path)
	}
	return rel, nil
}

// copyCanonicalSkillEntry 复制一个 canonical skill 到 mirror 目录。
func copyCanonicalSkillEntry(src, dst, rel, scope string, entry fs.DirEntry, tracker *skillhash.ContentLimitTracker) error {
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
	if err := tracker.AddFile(src, info.Size()); err != nil {
		return err
	}
	if scope == skillScopeProject && strings.EqualFold(filepath.Base(filepath.ToSlash(rel)), skillMainFile) {
		return copyCanonicalSkillMainFile(src, dst, rel, info.Mode(), tracker)
	}
	return copyCanonicalResourceFile(src, dst, rel, info.Mode(), tracker)
}

// capProjectMirrorTrustFrontmatter 限制项目 mirror frontmatter 的信任范围。
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
