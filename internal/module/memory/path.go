package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	shared "github.com/anthropic-ai/super-agent-v3/internal/module/memory/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/util/ctxutil"
	"github.com/anthropic-ai/super-agent-v3/internal/util/pathutil"
	"golang.org/x/text/unicode/norm"
)

const (
	memoryIndexFileName    = "MEMORY.md"
	memoryProjectsDir      = "projects"
	memoryProjectDirName   = "memory"
	gitResolveTimeout      = 4 * time.Second
	consolidationStampFile = ".consolidation.stamp.json"
)

var (
	ErrInvalidMemoryRoot      = shared.ErrInvalidMemoryRoot
	ErrInvalidMemoryReadPath  = errors.New("invalid memory read path")
	ErrInvalidMemoryWritePath = errors.New("invalid memory write path")
)

type consolidationStamp struct {
	LastScanAt    string `json:"last_scan_at,omitempty"`
	LastSuccessAt string `json:"last_success_at,omitempty"`
}

// GetAutoMemPath 根据记忆根和项目根解析 AutoMem 项目目录。
// baseRoot 和 projectRoot 都必须可校验；项目根会先规整到 canonical git root，
// 再经过 SanitizePath 生成稳定目录名。
func GetAutoMemPath(baseRoot, projectRoot string) (string, error) {
	validatedRoot, err := shared.ValidateMemoryRoot(baseRoot)
	if err != nil || validatedRoot == "" {
		return "", err
	}
	canonicalRoot, err := FindCanonicalGitRoot(context.Background(), projectRoot)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(canonicalRoot) == "" {
		return "", fmt.Errorf("%w: empty project root", ErrInvalidMemoryRoot)
	}
	root := strings.TrimSuffix(validatedRoot, string(os.PathSeparator))
	return filepath.Join(root, memoryProjectsDir, SanitizePath(canonicalRoot), memoryProjectDirName), nil
}

// GetAutoMemDailyLogPath 返回指定日期的 AutoMem daily log 路径。
// 路径解析复用 AutoMem 项目目录规则，避免 daily log 写到未授权根目录外。
func GetAutoMemDailyLogPath(baseRoot, projectRoot string, now time.Time) (string, error) {
	return getAutoMemDailyLogPath(baseRoot, projectRoot, now)
}

// FindCanonicalGitRoot 解析项目对应的 canonical git root。
// 它通过 git 命令同时读取 worktree 根和 common git dir，处理 linked worktree 场景；
// git 失败会返回错误而不是退回未经确认的路径。
func FindCanonicalGitRoot(ctx context.Context, projectRoot string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	fallback, err := shared.CleanAbsolutePath(projectRoot)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidMemoryRoot, err)
	}
	gitCtx, cancel := ctxutil.WithTimeout(ctx, gitResolveTimeout)
	defer cancel()

	cmd := exec.CommandContext(gitCtx, "git", "rev-parse", "--path-format=absolute", "--show-toplevel", "--git-common-dir")
	cmd.Dir = fallback
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("resolve git root for %q: %w", fallback, err)
	}
	lines := strings.Split(strings.ReplaceAll(string(output), "\r\n", "\n"), "\n")
	if len(lines) == 0 {
		return fallback, nil
	}
	gitRoot := strings.TrimSpace(lines[0])
	if gitRoot == "" {
		return fallback, nil
	}
	gitRoot = filepath.Clean(norm.NFC.String(gitRoot))
	if len(lines) < 2 {
		return gitRoot, nil
	}
	commonDir := strings.TrimSpace(lines[1])
	if filepath.Base(commonDir) == ".git" {
		parent := filepath.Dir(filepath.Clean(commonDir))
		if parent != "" {
			return parent, nil
		}
	}
	return gitRoot, nil
}

// SanitizePath 将项目路径转换为适合作为记忆目录名的安全 key。
// 具体规则集中在 pathutil 中，保证 memory 与其它模块使用同一套项目 key。
func SanitizePath(raw string) string {
	return pathutil.SanitizeMemoryProjectKey(raw)
}

// ValidateMemoryWritePath 校验写入路径位于记忆根目录内。
// 它会解析已存在路径的真实位置；候选路径逃逸根目录或包含非法输入时立即失败。
func ValidateMemoryWritePath(root, file string) (string, error) {
	validatedRoot, err := shared.ValidateMemoryRoot(root)
	if err != nil {
		return "", err
	}
	if validatedRoot == "" {
		return "", invalidMemoryWritePath("empty root")
	}
	rootDir, candidate, err := prepareMemoryPath(validatedRoot, file, invalidMemoryWritePath)
	if err != nil {
		return "", err
	}
	rootReal, err := resolveMemoryWritePath(rootDir)
	if err != nil {
		return "", err
	}
	candidateReal, err := resolveMemoryWritePath(candidate)
	if err != nil {
		return "", err
	}
	if !pathutil.ContainsPath(rootReal, candidateReal) {
		return "", invalidMemoryWritePath("path escapes root")
	}
	return candidate, nil
}

// ValidateMemoryReadPath 校验读取路径位于记忆根目录内且指向文件。
// 与写入校验不同，读取要求目标已存在并非目录，防止工具读取越界或目录内容。
func ValidateMemoryReadPath(root, file string) (string, error) {
	validatedRoot, err := shared.ValidateMemoryRoot(root)
	if err != nil {
		return "", err
	}
	if validatedRoot == "" {
		return "", invalidMemoryReadPath("empty root")
	}
	rootDir, candidate, err := prepareMemoryPath(validatedRoot, file, invalidMemoryReadPath)
	if err != nil {
		return "", err
	}
	rootReal, err := resolveExistingMemoryPath(rootDir)
	if err != nil {
		return "", invalidMemoryReadPath(err.Error())
	}
	candidateReal, err := resolveExistingMemoryPath(candidate)
	if err != nil {
		return "", invalidMemoryReadPath(err.Error())
	}
	if !pathutil.ContainsPath(rootReal, candidateReal) {
		return "", invalidMemoryReadPath("path escapes root")
	}
	if info, err := os.Stat(candidateReal); err != nil {
		return "", invalidMemoryReadPath(err.Error())
	} else if info.IsDir() {
		return "", invalidMemoryReadPath("path is a directory")
	}
	return candidateReal, nil
}

// prepareMemoryPath 标准化根目录和候选文件路径。
// 它拒绝空路径、NUL 字节和不可解析路径，调用方通过 wrap 选择读/写错误类型。
func prepareMemoryPath(validatedRoot, file string, wrap func(string) error) (string, string, error) {
	file = norm.NFC.String(strings.TrimSpace(file))
	if file == "" {
		return "", "", wrap("empty file path")
	}
	if strings.ContainsRune(file, '\x00') {
		return "", "", wrap("null byte")
	}
	rootDir := strings.TrimSuffix(validatedRoot, string(os.PathSeparator))
	candidate := file
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(rootDir, candidate)
	}
	candidate, err := shared.CleanAbsolutePath(candidate)
	if err != nil {
		return "", "", wrap(err.Error())
	}
	if err := shared.EnsureResolvablePath(rootDir); err != nil {
		return "", "", wrap(err.Error())
	}
	if err := shared.EnsureResolvablePath(candidate); err != nil {
		return "", "", wrap(err.Error())
	}
	return rootDir, candidate, nil
}

// resolveMemoryWritePath 解析写入路径中已存在的最深真实路径。
// 目标文件尚不存在是合法写入场景，因此 os.ErrNotExist 不会阻断。
func resolveMemoryWritePath(path string) (string, error) {
	resolved, err := shared.RealPathDeepestExisting(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", invalidMemoryWritePath(err.Error())
	}
	if resolved == "" {
		return path, nil
	}
	return resolved, nil
}

// resolveExistingMemoryPath 解析必须存在的路径真实位置。
// 读路径校验依赖它发现符号链接逃逸和不存在目标。
func resolveExistingMemoryPath(path string) (string, error) {
	resolved, err := shared.RealPathDeepestExisting(path)
	if err != nil {
		return "", err
	}
	if resolved == "" {
		return "", os.ErrNotExist
	}
	return resolved, nil
}

// invalidMemoryReadPath 包装读取路径校验错误。
// 统一错误类型便于工具桥把失败映射为 invalid_path。
func invalidMemoryReadPath(reason string) error {
	return fmt.Errorf("%w: %s", ErrInvalidMemoryReadPath, reason)
}

// invalidMemoryWritePath 包装写入路径校验错误。
// 调用方可通过 errors.Is 区分路径问题和底层 I/O 问题。
func invalidMemoryWritePath(reason string) error {
	return fmt.Errorf("%w: %s", ErrInvalidMemoryWritePath, reason)
}

// memoryIndexPath 返回根目录下的 MEMORY.md 索引路径。
// 调用前根目录应已由上层解析和校验。
func memoryIndexPath(root string) string {
	return filepath.Join(root, memoryIndexFileName)
}

// memoryTypeDir 返回指定记忆类型的子目录路径。
// 未知类型会被 ParseMemoryType 规整，避免直接拼接未经确认的类型字符串。
func memoryTypeDir(root string, memoryType MemoryType) string {
	return filepath.Join(root, string(ParseMemoryType(string(memoryType))))
}

// writeAtomicFile 通过临时文件和 rename 原子替换目标文件。
// 目标路径先走写路径校验；任何写入、chmod、close 或 rename 失败都会保留错误给调用方。
func writeAtomicFile(path string, data []byte, perm os.FileMode) error {
	if perm == 0 {
		perm = 0o644
	}
	validatedPath, err := ValidateMemoryWritePath(filepath.Dir(path), path)
	if err != nil {
		return err
	}
	dir := filepath.Dir(validatedPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(validatedPath)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, validatedPath); err != nil {
		return err
	}
	cleanup = false
	return nil
}

// loadConsolidationStamp 读取自动整理 stamp。
// stamp 缺失或为空表示尚未整理；JSON 损坏会返回错误，避免使用不可信时间状态。
func loadConsolidationStamp(root string) (consolidationStamp, error) {
	path, err := consolidationStampPath(root)
	if err != nil {
		return consolidationStamp{}, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return consolidationStamp{}, nil
	}
	if err != nil {
		return consolidationStamp{}, err
	}
	if len(raw) == 0 {
		return consolidationStamp{}, nil
	}
	var stamp consolidationStamp
	if err := json.Unmarshal(raw, &stamp); err != nil {
		return consolidationStamp{}, err
	}
	return stamp, nil
}

// saveConsolidationStamp 原子写入自动整理 stamp。
// 路径仍走记忆写入校验，避免整理状态文件逃逸根目录。
func saveConsolidationStamp(root string, stamp consolidationStamp) error {
	path, err := consolidationStampPath(root)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(stamp, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomicFile(path, raw, 0o644)
}

// consolidationStampPath 返回整理状态文件路径。
// 根目录会先规范化，再通过写路径校验生成最终路径。
func consolidationStampPath(root string) (string, error) {
	normalizedRoot, err := normalizeStoreRoot(root)
	if err != nil {
		return "", err
	}
	return ValidateMemoryWritePath(normalizedRoot, filepath.Join(normalizedRoot, consolidationStampFile))
}

// recordConsolidation 记录一次成功整理时间。
// 该时间用于后续 runtime context 和自动整理间隔判断。
func recordConsolidation(root string, when time.Time) error {
	stamp, err := loadConsolidationStamp(root)
	if err != nil {
		return err
	}
	stamp.LastSuccessAt = stampTimeString(when)
	return saveConsolidationStamp(root, stamp)
}

// recordConsolidationScan 记录一次整理扫描时间。
// 扫描时间和成功时间分开保存，便于区分“检查过”与“真正写入整理结果”。
func recordConsolidationScan(root string, when time.Time) error {
	stamp, err := loadConsolidationStamp(root)
	if err != nil {
		return err
	}
	stamp.LastScanAt = stampTimeString(when)
	return saveConsolidationStamp(root, stamp)
}

// stampTimeString 将时间转为 UTC RFC3339Nano 字符串。
// 零值时间会使用当前时间，避免写入不可解析的空成功时间。
func stampTimeString(when time.Time) string {
	if when.IsZero() {
		when = time.Now()
	}
	return when.UTC().Format(time.RFC3339Nano)
}

// lastScanTime 解析 stamp 中的最近扫描时间。
// 空值或格式错误返回零值，由调用方按无历史处理。
func (s consolidationStamp) lastScanTime() time.Time {
	return parseStampTime(s.LastScanAt)
}

// lastSuccessTime 解析 stamp 中的最近成功整理时间。
// 空值或格式错误返回零值，避免坏 stamp 影响启动。
func (s consolidationStamp) lastSuccessTime() time.Time {
	return parseStampTime(s.LastSuccessAt)
}

// parseStampTime 解析整理 stamp 时间字段。
// 解析失败返回零值，不在时间读取路径制造额外错误。
func parseStampTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

// consolidationCandidates 选择可参与整理的记忆条目。
// 先按 canonical name 去重，再过滤掉没有有效正文的条目。
func consolidationCandidates(entries []MemoryEntry) []MemoryEntry {
	unique := uniqueEntriesByCanonicalName(entries)
	selected := make([]MemoryEntry, 0, len(unique))
	for _, entry := range unique {
		if hasMeaningfulMemoryContent(entry.Content) {
			selected = append(selected, entry)
		}
	}
	return selected
}

// staleMemoryPaths 找出整理后应删除的陈旧记忆文件。
// 空正文文件和同名较旧副本都会被纳入删除列表，返回前会去重。
func staleMemoryPaths(entries []MemoryEntry) []string {
	selected := make(map[string]MemoryEntry, len(entries))
	stale := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !hasMeaningfulMemoryContent(entry.Content) {
			if entry.FilePath != "" {
				stale = append(stale, entry.FilePath)
			}
			continue
		}
		key := entry.CanonicalName
		if key == "" {
			key = CanonicalName(entry.Frontmatter.Name)
		}
		current, exists := selected[key]
		if !exists || preferMemoryEntry(entry, current) {
			if exists && current.FilePath != "" {
				stale = append(stale, current.FilePath)
			}
			selected[key] = entry
			continue
		}
		if entry.FilePath != "" {
			stale = append(stale, entry.FilePath)
		}
	}
	return uniqueNonEmptyStrings(stale)
}

// uniqueNonEmptyStrings 清理、去重并保持字符串原始顺序。
// 删除列表和路径列表复用它，避免重复操作同一文件。
func uniqueNonEmptyStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		cleaned = append(cleaned, value)
	}
	return cleaned
}

// removeMemoryFiles 在记忆根目录内删除指定文件。
// 每个路径都重新走写路径校验，NotExist 被视为已经删除。
func removeMemoryFiles(root string, paths []string) error {
	for _, path := range paths {
		validatedPath, err := ValidateMemoryWritePath(root, path)
		if err != nil {
			return err
		}
		if err := os.Remove(validatedPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

type consolidationRollback struct {
	files map[string]consolidationFileBackup
}

type consolidationFileBackup struct {
	exists bool
	data   []byte
	mode   fs.FileMode
}

// newConsolidationRollback 捕获 consolidation 会改动的旧文件快照。
// 后续删除、写入、索引或 stamp 任一步失败时，调用 restore 可以把旧事实和索引恢复回来。
func newConsolidationRollback(root string, paths []string) (*consolidationRollback, error) {
	rollback := &consolidationRollback{files: make(map[string]consolidationFileBackup)}
	if err := rollback.captureValidatedPath(root, memoryIndexPath(root)); err != nil {
		return nil, err
	}
	for _, path := range uniqueNonEmptyStrings(paths) {
		if err := rollback.captureValidatedPath(root, path); err != nil {
			return nil, err
		}
	}
	return rollback, nil
}

func (r *consolidationRollback) captureValidatedPath(root, path string) error {
	validatedPath, err := ValidateMemoryWritePath(root, path)
	if err != nil {
		return err
	}
	return r.captureFile(validatedPath)
}

// captureFile 记录单个文件在提交前的状态。
// 不存在的文件也会记录，回滚时可删除本次失败提交新建出的残留文件。
func (r *consolidationRollback) captureFile(path string) error {
	if r == nil {
		return nil
	}
	if _, exists := r.files[path]; exists {
		return nil
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		r.files[path] = consolidationFileBackup{}
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("consolidation rollback cannot backup directory %q", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	r.files[path] = consolidationFileBackup{exists: true, data: data, mode: info.Mode().Perm()}
	return nil
}

// restore 按快照恢复 consolidation 提交前的文件状态。
// 原本存在的文件会写回原内容，原本不存在的文件会被删除以清掉失败提交残留。
func (r *consolidationRollback) restore() error {
	if r == nil {
		return nil
	}
	for path, backup := range r.files {
		if !backup.exists {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, backup.data, backup.mode); err != nil {
			return err
		}
	}
	return nil
}

// writeConsolidatedMemories 将整理后的记忆条目写回磁盘。
// 任何单条写入失败都会中断，避免索引与部分写入状态继续漂移。
func writeConsolidatedMemories(root string, items []ExtractedMemory) error {
	entries, err := prepareConsolidatedMemoryEntries(root, items)
	if err != nil {
		return err
	}
	return writePreparedConsolidatedMemories(root, entries)
}

// writePreparedConsolidatedMemories 写入已完成批量校验的整理结果。
// 调用方可先用同一批 entries 预计算回滚路径，避免边写边发现路径问题。
func writePreparedConsolidatedMemories(root string, entries []MemoryEntry) error {
	for _, entry := range entries {
		if _, err := writePreparedMemoryFile(root, entry, nil); err != nil {
			return err
		}
	}
	return nil
}

// prepareConsolidatedMemoryEntries 在写盘前构造并校验整理结果。
// 每条结果都会解析目标文件路径，提前发现非法名称或越界写入。
func prepareConsolidatedMemoryEntries(root string, items []ExtractedMemory) ([]MemoryEntry, error) {
	if len(items) == 0 {
		return nil, nil
	}
	normalizedRoot, err := normalizeStoreRoot(root)
	if err != nil {
		return nil, err
	}
	entries := make([]MemoryEntry, 0, len(items))
	for _, item := range items {
		entry := buildConsolidatedMemoryEntry(item)
		prepared, err := prepareWritableEntry(entry, false)
		if err != nil {
			return nil, err
		}
		if _, err := resolveMemoryFilePath(normalizedRoot, prepared); err != nil {
			return nil, err
		}
		entries = append(entries, prepared)
	}
	return entries, nil
}

// consolidatedMemoryEntryPaths 返回整理结果最终可能写入的安全路径。
// 该函数只解析路径，不写文件，用于在删除旧文件前建立完整回滚快照。
func consolidatedMemoryEntryPaths(root string, entries []MemoryEntry) ([]string, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	normalizedRoot, err := normalizeStoreRoot(root)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		path, err := resolveMemoryFilePath(normalizedRoot, entry)
		if err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	return uniqueNonEmptyStrings(paths), nil
}

// buildConsolidatedMemoryEntry 将抽取结果转换为可写 MemoryEntry。
// 描述优先取正文首行并按 hook 预算截断，source 标记为 dream 以区分显式写入。
func buildConsolidatedMemoryEntry(item ExtractedMemory) MemoryEntry {
	item = normalizeExtractedMemory(item)
	description := truncateRunes(firstNonEmptyLine(item.Content), memoryHookMaxRunes)
	if description == "" {
		description = truncateRunes(item.Content, memoryHookMaxRunes)
	}
	return MemoryEntry{
		Frontmatter: MemoryFrontmatter{
			Name:        consolidationName(item, description),
			Description: description,
			Type:        cloneMemoryType(item.Type),
			SearchKeys:  normalizeStringSlice(item.Tags),
			Source:      "dream",
		},
		Content: item.Content,
	}
}

// consolidationName 为整理结果选择稳定名称。
// 有描述时直接使用描述；否则按记忆类型生成兜底名称，避免空 name frontmatter。
func consolidationName(item ExtractedMemory, description string) string {
	if description != "" {
		return description
	}
	if item.Type.IsKnown() {
		return fmt.Sprintf("%s dream note", item.Type)
	}
	return "Dream note"
}

type MemoryType = shared.MemoryType

const (
	MemoryTypeUnknown   = shared.MemoryTypeUnknown
	MemoryTypeUser      = shared.MemoryTypeUser
	MemoryTypeFeedback  = shared.MemoryTypeFeedback
	MemoryTypeProject   = shared.MemoryTypeProject
	MemoryTypeReference = shared.MemoryTypeReference
)

var diskMemoryTypes = []MemoryType{
	MemoryTypeUser,
	MemoryTypeFeedback,
	MemoryTypeProject,
	MemoryTypeReference,
}

// ParseMemoryType 将字符串解析为共享记忆类型枚举。
// 该包装让父包调用方不需要直接依赖 shared 子包。
func ParseMemoryType(raw string) MemoryType { return shared.ParseMemoryType(raw) }

// CanonicalName 生成记忆名称的 canonical key。
// 该 key 用于索引去重、查找和跨 scope 同名检测。
func CanonicalName(raw string) string { return shared.CanonicalName(raw) }

type MemoryScope string

const (
	MemoryScopeUser    MemoryScope = "user"
	MemoryScopeProject MemoryScope = "project"
	MemoryScopeLocal   MemoryScope = "local"
)

type MemoryFrontmatter = shared.MemoryFrontmatter
type MemoryEntry = shared.MemoryEntry
type ParsedMemory = shared.ParsedMemory

type SaveIntent struct {
	Detected bool
	Content  string
	Type     MemoryType
}

func cloneMemoryType(t MemoryType) *MemoryType {
	return shared.CloneMemoryType(t)
}

func normalizeStringSlice(values []string) []string {
	return shared.NormalizeStringSlice(values)
}
