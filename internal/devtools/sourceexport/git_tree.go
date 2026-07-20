package sourceexport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

const (
	maxGitTreeEntries = 100_000
	maxGitTreeBytes   = 512 << 20
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
	RunBatch(ctx context.Context, repo string, input []byte, maxOutput int64, args ...string) ([]byte, error)
}

type execGitRunner struct{}

// Run 在指定仓库根执行单个 Git plumbing 命令并保留可操作错误。
func (execGitRunner) Run(ctx context.Context, repo string, args ...string) ([]byte, error) {
	return runGitCommand(ctx, repo, nil, 0, args...)
}

// RunBatch 向单个 Git plumbing 进程写入批请求，并对标准输出施加硬字节上限。
func (execGitRunner) RunBatch(ctx context.Context, repo string, input []byte, maxOutput int64, args ...string) ([]byte, error) {
	return runGitCommand(ctx, repo, input, maxOutput, args...)
}

// runGitCommand 以受控 stdin 和 stdout 上限执行 Git plumbing，并保留取消根因。
func runGitCommand(ctx context.Context, repo string, input []byte, maxOutput int64, args ...string) ([]byte, error) {
	commandArgs := append([]string{"-C", repo}, args...)
	command := exec.CommandContext(ctx, "git", commandArgs...)
	if input != nil {
		command.Stdin = bytes.NewReader(input)
	}
	var output bytes.Buffer
	stdout := io.Writer(&output)
	if maxOutput > 0 {
		stdout = &limitedGitOutput{buffer: &output, remaining: maxOutput}
	}
	command.Stdout = stdout
	var stderr bytes.Buffer
	command.Stderr = &stderr
	err := command.Run()
	if err == nil {
		return output.Bytes(), nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, fmt.Errorf("git %s: %w", args[0], ctxErr)
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return nil, fmt.Errorf("git %s: %w", args[0], err)
}

type limitedGitOutput struct {
	buffer    *bytes.Buffer
	remaining int64
}

// Write 在达到批输出上限时立即终止 Git stdout 管道。
func (output *limitedGitOutput) Write(data []byte) (int, error) {
	if int64(len(data)) > output.remaining {
		if output.remaining > 0 {
			_, _ = output.buffer.Write(data[:output.remaining])
		}
		return 0, errors.New("Git batch output exceeds limit")
	}
	output.remaining -= int64(len(data))
	return output.buffer.Write(data)
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

// loadTreeEntryData 以单批 Git 进程加载去重 blob，并将同 OID 内容复用于所有路径。
func loadTreeEntryData(ctx context.Context, runner gitRunner, repo string, entries []TreeEntry) error {
	if len(entries) > maxGitTreeEntries {
		return policyError("git-tree", fmt.Errorf("entry count %d exceeds limit %d", len(entries), maxGitTreeEntries))
	}
	if len(entries) == 0 {
		return nil
	}
	unique := uniqueTreeEntryHashes(entries)
	requests := []byte(strings.Join(unique, "\n") + "\n")
	outputLimit := int64(maxGitTreeBytes) + int64(len(unique))*128
	output, err := runner.RunBatch(ctx, repo, requests, outputLimit, "cat-file", "--batch")
	if err != nil {
		return policyError("git-tree.batch", err)
	}
	blobs, err := parseGitBlobBatch(output, unique)
	if err != nil {
		return err
	}
	for index := range entries {
		entries[index].Data = blobs[entries[index].Hash]
	}
	return nil
}

// uniqueTreeEntryHashes 按首次出现顺序返回 OID，保持 batch 请求和 tree 顺序可验证。
func uniqueTreeEntryHashes(entries []TreeEntry) []string {
	unique := make([]string, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if _, exists := seen[entry.Hash]; exists {
			continue
		}
		seen[entry.Hash] = struct{}{}
		unique = append(unique, entry.Hash)
	}
	return unique
}

// parseGitBlobBatch 严格消费每个预期 OID 的 header、payload、终止符及最终 EOF。
func parseGitBlobBatch(output []byte, expected []string) (map[string][]byte, error) {
	blobs := make(map[string][]byte, len(expected))
	offset := 0
	totalBytes := 0
	for index, oid := range expected {
		blob, next, err := parseGitBlobBatchEntry(output, offset, oid, totalBytes)
		if err != nil {
			return nil, policyError(fmt.Sprintf("git-tree.batch[%d/%d]", index, len(expected)), err)
		}
		blobs[oid] = blob
		totalBytes += len(blob)
		offset = next
	}
	if offset != len(output) {
		return nil, policyError("git-tree.batch", errors.New("cat-file returned trailing data"))
	}
	return blobs, nil
}

// parseGitBlobBatchEntry 校验单条 cat-file batch 响应的 OID、类型、大小和边界。
func parseGitBlobBatchEntry(output []byte, offset int, expectedOID string, totalBytes int) ([]byte, int, error) {
	if offset < 0 || offset > len(output) {
		return nil, 0, errors.New("invalid batch offset")
	}
	headerEnd := bytes.IndexByte(output[offset:], '\n')
	if headerEnd < 0 {
		return nil, 0, errors.New("cat-file header is missing terminator")
	}
	headerEnd += offset
	size, err := parseGitBlobBatchHeader(output[offset:headerEnd], expectedOID)
	if err != nil {
		return nil, 0, err
	}
	dataStart := headerEnd + 1
	dataEnd, err := validateGitBlobBatchBounds(output, dataStart, size, totalBytes, expectedOID)
	if err != nil {
		return nil, 0, err
	}
	return bytes.Clone(output[dataStart:dataEnd]), dataEnd + 1, nil
}

// parseGitBlobBatchHeader 校验响应 OID/type 并解析受限十进制 payload 大小。
func parseGitBlobBatchHeader(header []byte, expectedOID string) (int64, error) {
	fields := bytes.Fields(header)
	if len(fields) != 3 || string(fields[0]) != expectedOID {
		return 0, fmt.Errorf("cat-file header does not match requested OID %s", expectedOID)
	}
	if string(fields[1]) != "blob" {
		return 0, fmt.Errorf("cat-file object %s has type %q, want blob", expectedOID, fields[1])
	}
	size, err := strconv.ParseInt(string(fields[2]), 10, 64)
	if err != nil || size < 0 || size > maxGitTreeBytes {
		return 0, fmt.Errorf("cat-file object %s has invalid or excessive size %q", expectedOID, fields[2])
	}
	return size, nil
}

// validateGitBlobBatchBounds 校验累计上限、整数边界、payload 长度和终止符。
func validateGitBlobBatchBounds(output []byte, dataStart int, size int64, totalBytes int, oid string) (int, error) {
	if int64(totalBytes)+size > maxGitTreeBytes {
		return 0, fmt.Errorf("cat-file object %s exceeds total tree byte limit", oid)
	}
	dataEnd64 := int64(dataStart) + size
	if dataEnd64 < int64(dataStart) || dataEnd64 >= int64(len(output)) {
		return 0, fmt.Errorf("cat-file object %s payload is truncated", oid)
	}
	dataEnd := int(dataEnd64)
	if output[dataEnd] != '\n' {
		return 0, fmt.Errorf("cat-file object %s payload is missing terminator", oid)
	}
	return dataEnd, nil
}
