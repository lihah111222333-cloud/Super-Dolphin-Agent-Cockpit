package sourceexport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// TreeEntry 是从指定 Git commit 读取的普通 blob。
type TreeEntry struct {
	Path string
	Mode string
	Data []byte
	Hash string
}

type gitRunner interface {
	Run(ctx context.Context, repo string, args ...string) ([]byte, error)
}

type execGitRunner struct{}

// Run 在指定仓库根执行单个 Git plumbing 命令并保留可操作错误。
func (execGitRunner) Run(ctx context.Context, repo string, args ...string) ([]byte, error) {
	commandArgs := append([]string{"-C", repo}, args...)
	command := exec.CommandContext(ctx, "git", commandArgs...)
	output, err := command.Output()
	if err == nil {
		return output, nil
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(string(exitError.Stderr)))
	}
	return nil, fmt.Errorf("git %s: %w", args[0], err)
}

// ensureSourceClean 拒绝 index 或已跟踪工作区变更；未跟踪文件不属于 Git tree 输入。
func ensureSourceClean(ctx context.Context, runner gitRunner, repo string) error {
	output, err := runner.Run(ctx, repo, "status", "--porcelain=v1", "--untracked-files=no", "-z")
	if err != nil {
		return &Error{Code: CodeSourceDirty, Path: repo, Err: err}
	}
	if len(output) != 0 {
		return &Error{Code: CodeSourceDirty, Path: repo, Err: errors.New("tracked worktree or index changes are present")}
	}
	return nil
}

// loadGitTree 从单一已提交对象读取稳定排序的普通 blob，不读取工作区文件内容。
func loadGitTree(ctx context.Context, runner gitRunner, repo string, revision string) (string, []TreeEntry, error) {
	if err := ensureSourceClean(ctx, runner, repo); err != nil {
		return "", nil, err
	}
	commit, err := resolveCommit(ctx, runner, repo, revision)
	if err != nil {
		return "", nil, err
	}
	output, err := runner.Run(ctx, repo, "ls-tree", "-rz", "--full-tree", commit)
	if err != nil {
		return "", nil, policyError("commit", err)
	}
	entries, err := parseGitTree(output)
	if err != nil {
		return "", nil, err
	}
	if err := validateTreeEntries(entries); err != nil {
		return "", nil, err
	}
	if err := loadTreeEntryData(ctx, runner, repo, entries); err != nil {
		return "", nil, err
	}
	return commit, entries, nil
}

func resolveCommit(ctx context.Context, runner gitRunner, repo string, revision string) (string, error) {
	if strings.TrimSpace(revision) == "" {
		return "", policyError("commit", errors.New("revision must not be empty"))
	}
	output, err := runner.Run(ctx, repo, "rev-parse", "--verify", "--end-of-options", revision+"^{commit}")
	if err != nil {
		return "", policyError("commit", err)
	}
	commit := strings.TrimSpace(string(output))
	if len(commit) != 40 && len(commit) != 64 {
		return "", policyError("commit", fmt.Errorf("unexpected object ID length %d", len(commit)))
	}
	return commit, nil
}

func parseGitTree(output []byte) ([]TreeEntry, error) {
	records := bytes.Split(output, []byte{0})
	entries := make([]TreeEntry, 0, len(records))
	for _, record := range records {
		if len(record) == 0 {
			continue
		}
		entry, err := parseGitTreeRecord(record)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(left int, right int) bool { return entries[left].Path < entries[right].Path })
	return entries, nil
}

func parseGitTreeRecord(record []byte) (TreeEntry, error) {
	metadata, filePath, found := bytes.Cut(record, []byte{'\t'})
	if !found {
		return TreeEntry{}, policyError("git-tree", errors.New("record is missing path separator"))
	}
	fields := bytes.Fields(metadata)
	if len(fields) != 3 {
		return TreeEntry{}, policyError("git-tree", errors.New("record metadata must have mode, type, and hash"))
	}
	if string(fields[1]) != "blob" && string(fields[0]) != "160000" {
		return TreeEntry{}, &Error{Code: CodeForbiddenPath, Path: string(filePath), Err: fmt.Errorf("unsupported Git object type %q", fields[1])}
	}
	return TreeEntry{Path: string(filePath), Mode: string(fields[0]), Hash: string(fields[2])}, nil
}

// validateTreeEntries 拒绝非普通文件模式和大小写折叠后冲突的路径。
func validateTreeEntries(entries []TreeEntry) error {
	seen := make(map[string]string, len(entries))
	for _, entry := range entries {
		if err := validateTreeEntryMode(entry); err != nil {
			return err
		}
		if err := validatePolicyPath("git-tree.path", entry.Path); err != nil {
			return err
		}
		folded := strings.ToLower(entry.Path)
		if previous, exists := seen[folded]; exists {
			return &Error{Code: CodeCaseCollision, Path: entry.Path, Err: fmt.Errorf("collides with %q", previous)}
		}
		seen[folded] = entry.Path
	}
	return nil
}

func validateTreeEntryMode(entry TreeEntry) error {
	switch entry.Mode {
	case "100644", "100755":
		return nil
	case "120000":
		return &Error{Code: CodeSymlinkRejected, Path: entry.Path, Err: errors.New("symlink entries are not exportable")}
	default:
		return &Error{Code: CodeForbiddenPath, Path: entry.Path, Err: fmt.Errorf("unsupported Git mode %q", entry.Mode)}
	}
}

func loadTreeEntryData(ctx context.Context, runner gitRunner, repo string, entries []TreeEntry) error {
	for index := range entries {
		data, err := runner.Run(ctx, repo, "cat-file", "blob", entries[index].Hash)
		if err != nil {
			return policyError(entries[index].Path, err)
		}
		entries[index].Data = data
	}
	return nil
}
