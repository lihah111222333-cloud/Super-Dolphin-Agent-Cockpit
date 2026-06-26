package team

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	shared "github.com/anthropic-ai/super-agent-v3/internal/module/memory/shared"
)

// teamSyncLocalFile 是本地团队记忆 Markdown 文件的扫描结果。
type teamSyncLocalFile struct {
	Checksum string
	Content  string
	Path     string
}

// teamSyncTargetPath 将远端相对路径解析为本地安全写入路径。
func teamSyncTargetPath(root, rel string) (string, string, error) {
	normalized, err := validateTeamMemKey(root, rel)
	if err != nil {
		return "", "", err
	}
	target := filepath.Join(root, filepath.FromSlash(normalized))
	validated, err := validateTeamMemWritePath(root, target)
	if err != nil {
		return "", "", err
	}
	return normalized, validated, nil
}

// scanTeamMarkdownFiles 扫描团队记忆目录下可同步的 Markdown 文件。
// 符号链接、内部状态文件和临时文件会被拒绝或跳过，避免远端同步逃逸 root。
func scanTeamMarkdownFiles(root string) (map[string]teamSyncLocalFile, error) {
	root, ok, err := validateTeamSyncScanRoot(root)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	files := map[string]teamSyncLocalFile{}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		normalized, file, ok, readErr := readTeamSyncMarkdownFile(root, path, d, err)
		if readErr != nil {
			return readErr
		}
		if ok {
			files[normalized] = file
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, nil
	}
	return files, nil
}

// validateTeamSyncScanRoot 校验扫描根目录是否存在且真实路径安全。
func validateTeamSyncScanRoot(root string) (string, bool, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", false, nil
	}
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return "", false, err
		}
		return "", false, err
	}
	if _, err := resolveTeamMemRealPath(root, invalidTeamMemWritePath); err != nil {
		return "", false, err
	}
	return root, true, nil
}

// readTeamSyncMarkdownFile 读取单个可同步 Markdown 文件。
// 目录和内部文件会返回 ok=false；符号链接直接报错，防止同步内容越界。
func readTeamSyncMarkdownFile(root, path string, d fs.DirEntry, walkErr error) (string, teamSyncLocalFile, bool, error) {
	if walkErr != nil {
		return "", teamSyncLocalFile{}, false, walkErr
	}
	if d.Type()&os.ModeSymlink != 0 {
		return "", teamSyncLocalFile{}, false, fmt.Errorf("%w: symlink path is not allowed", ErrInvalidTeamMemWritePath)
	}
	if d.IsDir() {
		return "", teamSyncLocalFile{}, false, nil
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", teamSyncLocalFile{}, false, err
	}
	rel = filepath.ToSlash(rel)
	if shouldIgnoreTeamSyncPath(rel) {
		return "", teamSyncLocalFile{}, false, nil
	}
	normalized, validated, err := teamSyncTargetPath(root, rel)
	if err != nil {
		return "", teamSyncLocalFile{}, false, err
	}
	content, err := os.ReadFile(validated)
	if err != nil {
		return "", teamSyncLocalFile{}, false, err
	}
	return normalized, teamSyncLocalFile{
		Checksum: checksumContent(content),
		Content:  string(content),
		Path:     validated,
	}, true, nil
}

// shouldIgnoreTeamSyncPath 判断路径是否应排除在团队记忆同步之外。
// 只同步 .md 文件，内部状态、临时文件和编辑器交换文件一律忽略。
func shouldIgnoreTeamSyncPath(path string) bool {
	path = strings.TrimSpace(filepath.ToSlash(path))
	if path == "" {
		return true
	}
	base := strings.ToLower(filepath.Base(path))
	if base == strings.ToLower(teamSyncStateFileName) || strings.HasPrefix(base, ".team-sync-staging-") {
		return true
	}
	if filepath.Ext(base) != ".md" {
		return true
	}
	for _, suffix := range []string{"~", ".swp", ".swo", ".swx", ".tmp", ".temp", ".lock", ".part", ".staging"} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	return false
}

// writeTeamSyncFile 通过 staging 文件写入团队记忆内容，再原子 rename 到目标路径。
func writeTeamSyncFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	stagePath := filepath.Join(filepath.Dir(path), ".team-sync-staging-"+shared.ShortHash(path)+".tmp")
	if err := os.WriteFile(stagePath, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(stagePath, path)
}

// pruneEmptyTeamDirs 自底向上删除同步后留下的空目录。
func pruneEmptyTeamDirs(root string) error {
	var dirs []string
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() && path != root {
			dirs = append(dirs, path)
		}
		return nil
	})
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err == nil && len(entries) == 0 {
			if err := os.Remove(dir); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	if walkErr != nil {
		return walkErr
	}
	return nil
}

// diffServerChecksums 比较本地文件和远端 checksum，生成上传内容和远端删除列表。
func diffServerChecksums(local map[string]teamSyncLocalFile, server map[string]string) (map[string]string, []string) {
	uploads := map[string]string{}
	for path, file := range local {
		if strings.TrimSpace(server[path]) == file.Checksum {
			continue
		}
		uploads[path] = file.Content
	}
	var deletes []string
	for path := range server {
		if _, ok := local[path]; ok {
			continue
		}
		deletes = append(deletes, path)
	}
	sort.Strings(deletes)
	if len(uploads) == 0 {
		uploads = nil
	}
	return uploads, deletes
}

// localChecksumMap 提取本地扫描结果中的 checksum map。
func localChecksumMap(files map[string]teamSyncLocalFile) map[string]string {
	if len(files) == 0 {
		return nil
	}
	checksums := make(map[string]string, len(files))
	for path, file := range files {
		checksums[path] = strings.TrimSpace(file.Checksum)
	}
	return checksums
}

// checksumMapsEqual 比较两个 checksum map，nil 与空 map 视为相等。
func checksumMapsEqual(left, right map[string]string) bool {
	left = cloneChecksumMap(left)
	right = cloneChecksumMap(right)
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

// checksumTree 将路径和 checksum 排序后计算稳定树 hash。
func checksumTree(values map[string]string) string {
	if len(values) == 0 {
		return ""
	}
	keys := sortedMapKeys(values)
	hasher := sha256.New()
	for _, key := range keys {
		hasher.Write([]byte(key))
		hasher.Write([]byte{'='})
		hasher.Write([]byte(values[key]))
		hasher.Write([]byte{'\n'})
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

// checksumContent 计算远端/本地内容一致性使用的 SHA-256，结果写入同步状态和差异比较。
func checksumContent(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// normalizeRemoteChecksums 合并远端显式 checksum 与文件内容派生 checksum，并过滤内部同步文件。
func normalizeRemoteChecksums(checksums map[string]string, files map[string]TeamSyncFile) map[string]string {
	normalized := cloneChecksumMap(checksums)
	if len(files) == 0 {
		return normalized
	}
	if normalized == nil {
		normalized = map[string]string{}
	}
	for path, file := range files {
		path = strings.TrimSpace(filepath.ToSlash(path))
		if path == "" || shouldIgnoreTeamSyncPath(path) {
			continue
		}
		checksum := strings.TrimSpace(file.Checksum)
		if checksum == "" {
			checksum = checksumContent([]byte(file.Content))
		}
		normalized[path] = checksum
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

// cloneStringMap 复制字符串 map，调用方可安全修改返回值而不污染同步状态缓存。
func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

// sortedMapKeys 返回稳定排序后的 map key，保证 checksum tree 和批量请求顺序可复现。
func sortedMapKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// sortedStringSet 返回稳定排序后的 set key，用于远端删除列表等持久化/日志输出。
func sortedStringSet(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// firstNonEmptyTeamString 返回第一个非空白字符串，用于在远端错误、路径和兜底文案间保留最具体信息。
func firstNonEmptyTeamString(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
