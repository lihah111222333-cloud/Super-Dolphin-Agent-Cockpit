package difftracker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
)

// BeginSnapshot 记录目标仓库当前脏文件的文本快照。
// 脏文件数量或文件大小超过保护阈值时直接返回错误，避免后续工具事件携带不可控 diff。
func BeginSnapshot(ctx context.Context, path string) (*Snapshot, error) {
	root, err := findGitRoot(ctx, path)
	if err != nil {
		return nil, err
	}
	dirtyFiles, err := listDirtyFiles(ctx, root)
	if err != nil {
		return nil, err
	}
	if len(dirtyFiles) > MaxTrackedFiles {
		return nil, fmt.Errorf("difftracker: dirty file count %d exceeds limit %d", len(dirtyFiles), MaxTrackedFiles)
	}
	beforeFiles, err := captureBeforeFiles(ctx, root, dirtyFiles)
	if err != nil {
		return nil, err
	}
	return &Snapshot{RepoRoot: root, DirtyFiles: dirtyFiles, root: root, beforeFiles: beforeFiles}, nil
}

// EmitCurrentGitDiff 在缺少调用前快照时直接输出当前工作区相对 HEAD 的 diff。
// 这是工具调用后兜底路径，只能反映当前状态，无法区分调用前已经存在的脏改动。
func EmitCurrentGitDiff(ctx context.Context, path string) (string, []string, error) {
	root, err := findGitRoot(ctx, path)
	if err != nil {
		return "", nil, err
	}
	snapshot := &Snapshot{RepoRoot: root, root: root, beforeFiles: map[string]beforeFileState{}}
	return EmitGitDiff(ctx, snapshot)
}

// EmitGitDiff 根据调用前 Snapshot 和当前工作区生成统一 diff。
// 返回的 affected 列表包含发生变化的路径；diff 正文仍会按文本和总大小阈值过滤。
func EmitGitDiff(ctx context.Context, snapshot *Snapshot) (string, []string, error) {
	if snapshot == nil || strings.TrimSpace(snapshot.root) == "" {
		return "", nil, nil
	}
	paths, err := snapshotPaths(ctx, snapshot)
	if err != nil {
		return "", nil, err
	}
	blocks := make([]string, 0, len(paths))
	affected := make([]string, 0, len(paths))
	totalBytes := 0
	for _, relPath := range paths {
		block, changed, err := emitDiffBlock(ctx, snapshot, relPath)
		if err != nil {
			return "", nil, err
		}
		if !changed {
			continue
		}
		affected = append(affected, relPath)
		if strings.TrimSpace(block) == "" {
			continue
		}
		block = ensureTrailingNewline(block)
		totalBytes += len(block)
		if totalBytes > MaxTotalDiffBytes {
			return "", nil, fmt.Errorf("difftracker: diff size %d exceeds limit %d", totalBytes, MaxTotalDiffBytes)
		}
		blocks = append(blocks, strings.TrimRight(block, "\n"))
	}
	if len(blocks) == 0 {
		return "", affected, nil
	}
	return strings.Join(blocks, "\n") + "\n", affected, nil
}

func snapshotPaths(ctx context.Context, snapshot *Snapshot) ([]string, error) {
	afterDirty, err := listDirtyFiles(ctx, snapshot.root)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(snapshot.beforeFiles)+len(afterDirty))
	for path := range snapshot.beforeFiles {
		paths = append(paths, path)
	}
	paths = append(paths, afterDirty...)
	paths = uniqueSorted(paths)
	if len(paths) > MaxTrackedFiles {
		return nil, fmt.Errorf("difftracker: tracked file count %d exceeds limit %d", len(paths), MaxTrackedFiles)
	}
	return paths, nil
}

func captureBeforeFiles(ctx context.Context, repoRoot string, dirtyFiles []string) (map[string]beforeFileState, error) {
	beforeFiles := make(map[string]beforeFileState, len(dirtyFiles))
	for _, relPath := range dirtyFiles {
		state, ok, err := captureBeforeFile(ctx, repoRoot, relPath)
		if err != nil {
			return nil, err
		}
		if ok {
			beforeFiles[relPath] = state
		}
	}
	return beforeFiles, nil
}

func captureBeforeFile(ctx context.Context, repoRoot, relPath string) (beforeFileState, bool, error) {
	if shouldSkipGitPath(relPath) {
		return beforeFileState{}, false, nil
	}
	head, tracked, err := readHEADText(ctx, repoRoot, relPath)
	if err != nil {
		return beforeFileState{}, false, err
	}
	before, existedBefore, err := readWorkingTreeText(repoRoot, relPath)
	if err != nil {
		return beforeFileState{}, false, err
	}
	if shouldSkipGitText(relPath, head, before) {
		return beforeFileState{}, false, nil
	}
	return beforeFileState{path: relPath, head: head, before: before, tracked: tracked, existedBefore: existedBefore}, true, nil
}

// emitDiffBlock 生成单个路径的 diff 块，并跳过未变化、二进制或超限内容。
func emitDiffBlock(ctx context.Context, snapshot *Snapshot, relPath string) (string, bool, error) {
	state, hadBefore, err := snapshotState(ctx, snapshot, relPath)
	if err != nil {
		return "", false, err
	}
	after, afterExists, err := readWorkingTreeText(snapshot.root, relPath)
	if err != nil {
		return "", false, err
	}
	if hadBefore && state.existedBefore == afterExists && state.before == after {
		return "", false, nil
	}
	if shouldSkipGitText(relPath, state.head, after) {
		return "", false, nil
	}
	block := buildUnifiedDiffBlockWithState(relPath, state.tracked, state.head, afterExists, after)
	return block, true, nil
}

func snapshotState(ctx context.Context, snapshot *Snapshot, relPath string) (beforeFileState, bool, error) {
	if state, ok := snapshot.beforeFiles[relPath]; ok {
		return state, true, nil
	}
	head, tracked, err := readHEADText(ctx, snapshot.root, relPath)
	if err != nil {
		return beforeFileState{}, false, err
	}
	return beforeFileState{path: relPath, head: head, before: head, tracked: tracked, existedBefore: tracked}, false, nil
}

func readHEADText(ctx context.Context, repoRoot, relPath string) (string, bool, error) {
	content, err := readHEADContent(ctx, repoRoot, relPath)
	if err == nil {
		if shouldSkipGitBytes(relPath, content) {
			return "", false, nil
		}
		return string(content), true, nil
	}
	if os.IsNotExist(err) {
		return "", false, nil
	}
	return "", false, err
}

func readWorkingTreeText(repoRoot, relPath string) (string, bool, error) {
	path := normalizeDiffPath(relPath)
	if path == "" {
		return "", false, nil
	}
	data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(path)))
	if err == nil {
		if shouldSkipGitBytes(relPath, data) {
			return "", false, nil
		}
		return string(data), true, nil
	}
	if os.IsNotExist(err) {
		return "", false, nil
	}
	return "", false, err
}

func shouldSkipGitPath(relPath string) bool {
	return isSkippedBinaryExtension(filepath.Ext(relPath))
}

func shouldSkipGitText(relPath string, texts ...string) bool {
	if shouldSkipGitPath(relPath) {
		return true
	}
	for _, text := range texts {
		if len(text) > MaxFileSizeBytes || strings.IndexByte(text, 0) >= 0 {
			return true
		}
	}
	return false
}

func shouldSkipGitBytes(relPath string, data []byte) bool {
	return shouldSkipGitPath(relPath) || len(data) > MaxFileSizeBytes || looksBinary(data)
}

func looksBinary(data []byte) bool {
	return slices.Contains(data, byte(0))
}

// buildUnifiedDiffBlockWithState 根据 tracked/existed 状态生成 git 风格文件头。
// 新增或删除文件必须落到 /dev/null，调用方已经完成二进制和大小过滤。
func buildUnifiedDiffBlockWithState(path string, tracked bool, before string, afterExists bool, after string) string {
	clean := normalizeDiffPath(path)
	if clean == "" || (tracked == afterExists && before == after) {
		return ""
	}
	fromFile := "/dev/null"
	toFile := "/dev/null"
	if tracked {
		fromFile = "a/" + clean
	}
	if afterExists {
		toFile = "b/" + clean
	}
	text, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(before),
		B:        difflib.SplitLines(after),
		FromFile: fromFile,
		ToFile:   toFile,
		Context:  3,
	})
	if err != nil || text == "" {
		return ""
	}
	return ensureTrailingNewline(text)
}

func normalizeDiffPath(path string) string {
	clean := filepath.ToSlash(filepath.Clean(strings.Trim(strings.TrimSpace(path), `"`)))
	switch clean {
	case "", ".", "/", "/dev/null":
		return ""
	default:
		clean = strings.TrimPrefix(clean, "./")
		return strings.TrimPrefix(strings.TrimPrefix(clean, "a/"), "b/")
	}
}

func normalizeNewlines(text string) string {
	return strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(text)
}

func ensureTrailingNewline(text string) string {
	trimmed := strings.TrimRight(text, "\n")
	if trimmed == "" {
		return ""
	}
	return trimmed + "\n"
}
