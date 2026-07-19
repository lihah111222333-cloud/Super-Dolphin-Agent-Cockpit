package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const (
	cacheSchemaVersion      = 1
	maxCacheEntriesPerPhase = 8
)

type stringList []string

// String 将重复 CLI 参数转换成可诊断的稳定文本。
func (s *stringList) String() string { return strings.Join(*s, ",") }

// Set 拒绝空参数并保留调用方提供的每一个值。
func (s *stringList) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("value must not be empty")
	}
	*s = append(*s, value)
	return nil
}

type cacheRequest struct {
	action  string
	root    string
	name    string
	inputs  []string
	paths   []string
	outputs []string
}

type cacheManifest struct {
	SchemaVersion int      `json:"schema_version"`
	Name          string   `json:"name"`
	Key           string   `json:"key"`
	Outputs       []string `json:"outputs"`
	ArchiveSHA256 string   `json:"archive_sha256"`
}

// main 解析一次缓存请求并按明确动作执行恢复或发布。
func main() {
	request, err := parseRequest(os.Args[1:])
	if err != nil {
		fail(err)
	}
	switch request.action {
	case "restore":
		hit, restoreErr := restore(request)
		if restoreErr != nil {
			fail(restoreErr)
		}
		if hit {
			fmt.Println("hit")
		} else {
			fmt.Println("miss")
		}
	case "save":
		if err := save(request); err != nil {
			fail(err)
		}
		fmt.Println("saved")
	default:
		fail(fmt.Errorf("unsupported action %q", request.action))
	}
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "build phase cache: %v\n", err)
	os.Exit(1)
}

// parseRequest 校验 CLI 请求并将所有路径锚定到当前工作树。
func parseRequest(args []string) (cacheRequest, error) {
	var inputs stringList
	var paths stringList
	var outputs stringList
	flags := flag.NewFlagSet("build-phase-cache", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	action := flags.String("action", "", "restore or save")
	root := flags.String("root", "", "worktree root")
	name := flags.String("name", "", "phase name")
	flags.Var(&inputs, "input", "stable non-file input")
	flags.Var(&paths, "path", "file or directory input")
	flags.Var(&outputs, "output", "generated output to cache")
	if err := flags.Parse(args); err != nil {
		return cacheRequest{}, err
	}
	if flags.NArg() != 0 {
		return cacheRequest{}, fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	resolvedRoot, err := filepath.Abs(strings.TrimSpace(*root))
	if err != nil {
		return cacheRequest{}, fmt.Errorf("resolve root: %w", err)
	}
	if strings.TrimSpace(*root) == "" {
		return cacheRequest{}, errors.New("root is required")
	}
	if info, statErr := os.Stat(resolvedRoot); statErr != nil || !info.IsDir() {
		return cacheRequest{}, fmt.Errorf("root is not a directory: %s", resolvedRoot)
	}
	if strings.TrimSpace(*name) == "" {
		return cacheRequest{}, errors.New("name is required")
	}
	if len(paths) == 0 {
		return cacheRequest{}, errors.New("at least one path is required")
	}
	if len(outputs) == 0 {
		return cacheRequest{}, errors.New("at least one output is required")
	}
	return cacheRequest{
		action:  strings.TrimSpace(*action),
		root:    filepath.Clean(resolvedRoot),
		name:    strings.TrimSpace(*name),
		inputs:  inputs,
		paths:   paths,
		outputs: outputs,
	}, nil
}

// restore 校验共享缓存项后把声明产物恢复到当前工作树。
func restore(request cacheRequest) (bool, error) {
	key, relativeOutputs, err := requestKey(request)
	if err != nil {
		return false, err
	}
	entry, err := cacheEntry(request.root, request.name, key)
	if err != nil {
		return false, err
	}
	manifestPath := filepath.Join(entry, "manifest.json")
	archivePath := filepath.Join(entry, "artifact.tar.gz")
	_, manifestErr := os.Stat(manifestPath)
	_, archiveErr := os.Stat(archivePath)
	if errors.Is(manifestErr, os.ErrNotExist) && errors.Is(archiveErr, os.ErrNotExist) {
		return false, nil
	}
	if manifestErr != nil {
		return false, fmt.Errorf("read cache manifest %s: %w", manifestPath, manifestErr)
	}
	if archiveErr != nil {
		return false, fmt.Errorf("read cache archive %s: %w", archivePath, archiveErr)
	}
	manifest, err := readManifest(manifestPath)
	if err != nil {
		return false, err
	}
	if err := validateManifest(manifest, request.name, key, relativeOutputs, archivePath); err != nil {
		return false, err
	}
	if err := restoreArchive(request.root, archivePath, relativeOutputs); err != nil {
		return false, err
	}
	return true, nil
}

// save 将当前工作树产物发布成不可变的共享缓存项。
func save(request cacheRequest) error {
	key, relativeOutputs, err := requestKey(request)
	if err != nil {
		return err
	}
	if err := validateOutputsExist(request.root, relativeOutputs); err != nil {
		return err
	}
	entry, err := cacheEntry(request.root, request.name, key)
	if err != nil {
		return err
	}
	temp, alreadyPublished, err := prepareCachePublication(entry)
	if err != nil {
		return err
	}
	if alreadyPublished {
		return validateExistingEntry(entry, request.name, key, relativeOutputs)
	}
	defer os.RemoveAll(temp)
	if err := writeCachePayload(temp, request, key, relativeOutputs); err != nil {
		return err
	}
	if err := publishCacheEntry(temp, entry, request.name, key, relativeOutputs); err != nil {
		return err
	}
	return pruneCacheEntries(filepath.Dir(entry), entry, maxCacheEntriesPerPhase)
}

// validateOutputsExist 要求所有声明产物在发布前真实存在。
func validateOutputsExist(root string, outputs []string) error {
	for _, output := range outputs {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(output))); err != nil {
			return fmt.Errorf("required cache output is missing %s: %w", output, err)
		}
	}
	return nil
}

// prepareCachePublication 创建私有发布目录，或报告同键缓存已经存在。
func prepareCachePublication(entry string) (string, bool, error) {
	parent := filepath.Dir(entry)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", false, fmt.Errorf("create cache directory: %w", err)
	}
	exists, err := cacheEntryExists(entry)
	if err != nil || exists {
		return "", exists, err
	}
	temp, err := os.MkdirTemp(parent, ".publish-")
	if err != nil {
		return "", false, fmt.Errorf("create cache publication directory: %w", err)
	}
	return temp, false, nil
}

// writeCachePayload 生成归档摘要和严格 manifest。
func writeCachePayload(temp string, request cacheRequest, key string, outputs []string) error {
	archivePath := filepath.Join(temp, "artifact.tar.gz")
	if err := writeArchive(request.root, archivePath, outputs); err != nil {
		return err
	}
	archiveSHA, err := hashFile(archivePath)
	if err != nil {
		return err
	}
	manifest := cacheManifest{
		SchemaVersion: cacheSchemaVersion,
		Name:          request.name,
		Key:           key,
		Outputs:       outputs,
		ArchiveSHA256: archiveSHA,
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cache manifest: %w", err)
	}
	manifestBytes = append(manifestBytes, '\n')
	if err := os.WriteFile(filepath.Join(temp, "manifest.json"), manifestBytes, 0o644); err != nil {
		return fmt.Errorf("write cache manifest: %w", err)
	}
	return nil
}

// cacheEntryExists 区分缓存未发布与缓存目录不可读取两种状态。
func cacheEntryExists(entry string) (bool, error) {
	_, err := os.Stat(entry)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("inspect cache entry: %w", err)
}

// publishCacheEntry 原子发布缓存目录，并验证并发发布者留下的同键产物。
func publishCacheEntry(temp, entry, name, key string, outputs []string) error {
	if err := os.Rename(temp, entry); err == nil {
		return nil
	}
	exists, inspectErr := cacheEntryExists(entry)
	if inspectErr != nil {
		return inspectErr
	}
	if !exists {
		return fmt.Errorf("publish cache entry %s failed", entry)
	}
	return validateExistingEntry(entry, name, key, outputs)
}

type cacheEntryAge struct {
	path    string
	modTime int64
}

// pruneCacheEntries 限制单个 phase 的不可变条目数量并保留当前发布项。
func pruneCacheEntries(parent, preserve string, keep int) error {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return fmt.Errorf("read cache phase directory: %w", err)
	}
	cacheEntries, err := cacheEntryAges(parent, entries)
	if err != nil {
		return err
	}
	sort.Slice(cacheEntries, func(left, right int) bool {
		return cacheEntries[left].modTime > cacheEntries[right].modTime
	})
	remaining := keep - 1
	for _, entry := range cacheEntries {
		if entry.path == preserve {
			continue
		}
		if remaining > 0 {
			remaining--
			continue
		}
		if err := os.RemoveAll(entry.path); err != nil {
			return fmt.Errorf("prune cache entry %s: %w", entry.path, err)
		}
	}
	return nil
}

// cacheEntryAges 只选择完整 SHA-256 名称的已发布目录。
func cacheEntryAges(parent string, entries []os.DirEntry) ([]cacheEntryAge, error) {
	result := make([]cacheEntryAge, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !isSHA256Name(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("read cache entry metadata %s: %w", entry.Name(), err)
		}
		result = append(result, cacheEntryAge{
			path:    filepath.Join(parent, entry.Name()),
			modTime: info.ModTime().UnixNano(),
		})
	}
	return result, nil
}

// isSHA256Name 只接受 64 位小写十六进制缓存键。
func isSHA256Name(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			if character < 'a' || character > 'f' {
				return false
			}
		}
	}
	return true
}

// requestKey 使用相对路径、内容、模式与显式环境输入生成跨工作树稳定键。
func requestKey(request cacheRequest) (string, []string, error) {
	relativeOutputs, err := relativePaths(request.root, request.outputs)
	if err != nil {
		return "", nil, fmt.Errorf("validate outputs: %w", err)
	}
	sort.Strings(relativeOutputs)
	h := sha256.New()
	writeField(h, "schema", fmt.Sprintf("%d", cacheSchemaVersion))
	writeField(h, "name", request.name)
	inputs := append([]string(nil), request.inputs...)
	sort.Strings(inputs)
	for _, input := range inputs {
		writeField(h, "input", input)
	}
	for _, output := range relativeOutputs {
		writeField(h, "output", output)
	}
	relativeInputs, err := relativePaths(request.root, request.paths)
	if err != nil {
		return "", nil, fmt.Errorf("validate inputs: %w", err)
	}
	sort.Strings(relativeInputs)
	for _, relativeInput := range relativeInputs {
		if err := hashInput(h, request.root, relativeInput); err != nil {
			return "", nil, err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), relativeOutputs, nil
}

// relativePaths 拒绝根目录外路径和重复路径，避免缓存身份歧义。
func relativePaths(root string, paths []string) ([]string, error) {
	result := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, candidate := range paths {
		absolute, err := filepath.Abs(candidate)
		if err != nil {
			return nil, fmt.Errorf("resolve %q: %w", candidate, err)
		}
		relative, err := filepath.Rel(root, filepath.Clean(absolute))
		if err != nil {
			return nil, fmt.Errorf("make %q relative to root: %w", candidate, err)
		}
		if relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("path must be a child of root: %s", candidate)
		}
		canonical := filepath.ToSlash(relative)
		if err := rejectSymlinkComponents(root, canonical); err != nil {
			return nil, err
		}
		if _, duplicate := seen[canonical]; duplicate {
			return nil, fmt.Errorf("duplicate path: %s", canonical)
		}
		seen[canonical] = struct{}{}
		result = append(result, canonical)
	}
	return result, nil
}

// rejectSymlinkComponents 防止输入读取或输出替换经由父目录链接逃逸工作树。
func rejectSymlinkComponents(root, relative string) error {
	current := root
	for component := range strings.SplitSeq(filepath.FromSlash(relative), string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect path component %s: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("build phase path must not traverse symlink: %s", current)
		}
	}
	return nil
}

// hashInput 将一个普通文件或目录树的稳定内容写入缓存键。
func hashInput(h hash.Hash, root, relative string) error {
	absolute := filepath.Join(root, filepath.FromSlash(relative))
	info, err := os.Lstat(absolute)
	if err != nil {
		return fmt.Errorf("read build phase input %s: %w", relative, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("build phase input must not be a symlink: %s", relative)
	}
	if !info.IsDir() {
		return hashInputFile(h, absolute, relative, info)
	}
	files, err := collectInputFiles(absolute)
	if err != nil {
		return fmt.Errorf("walk build phase input %s: %w", relative, err)
	}
	writeField(h, "directory", relative)
	for _, file := range files {
		if err := hashWalkedInputFile(h, root, file); err != nil {
			return err
		}
	}
	return nil
}

// collectInputFiles 收集排序后的普通输入文件并拒绝符号链接。
func collectInputFiles(absolute string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(absolute, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == absolute {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("build phase input must not contain symlink: %s", path)
		}
		if entry.IsDir() {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

// hashWalkedInputFile 计算目录遍历得到的单个文件身份。
func hashWalkedInputFile(h hash.Hash, root, file string) error {
	fileInfo, err := os.Stat(file)
	if err != nil {
		return fmt.Errorf("stat build phase input %s: %w", file, err)
	}
	fileRelative, err := filepath.Rel(root, file)
	if err != nil {
		return err
	}
	return hashInputFile(h, file, filepath.ToSlash(fileRelative), fileInfo)
}

func hashInputFile(h hash.Hash, absolute, relative string, info os.FileInfo) error {
	if !info.Mode().IsRegular() {
		return fmt.Errorf("build phase input must be a regular file: %s", relative)
	}
	digest, err := hashFile(absolute)
	if err != nil {
		return err
	}
	writeField(h, "file", relative)
	writeField(h, "mode", fmt.Sprintf("%04o", info.Mode().Perm()))
	writeField(h, "sha256", digest)
	return nil
}

func writeField(h hash.Hash, label, value string) {
	fmt.Fprintf(h, "%s\x00%d\x00%s\x00", label, len(value), value)
}

// cacheEntry 将所有 linked worktree 映射到同一 Git common-dir 旁的共享 CAS。
func cacheEntry(root, name, key string) (string, error) {
	if strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return "", fmt.Errorf("invalid phase name %q", name)
	}
	command := exec.Command("git", "-C", root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("resolve Git common directory: %w\n%s", err, output)
	}
	commonDir := strings.TrimSpace(string(output))
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(root, commonDir)
	}
	commonDir, err = filepath.Abs(commonDir)
	if err != nil {
		return "", fmt.Errorf("resolve Git common directory path: %w", err)
	}
	cacheRoot := filepath.Join(filepath.Dir(commonDir), ".build-cache", "build-phase-artifacts-v1")
	return filepath.Join(cacheRoot, name, key), nil
}

// writeArchive 把声明产物写成不包含链接和特殊文件的压缩归档。
func writeArchive(root, archivePath string, outputs []string) error {
	file, err := os.OpenFile(archivePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create cache archive: %w", err)
	}
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	closer := archiveCloser{tarWriter: tarWriter, gzipWriter: gzipWriter, file: file}
	for _, output := range outputs {
		err := archiveOutput(root, output, tarWriter)
		if err != nil {
			return closer.close(fmt.Errorf("archive cache output %s: %w", output, err))
		}
	}
	if err := closer.close(nil); err != nil {
		return fmt.Errorf("finish cache archive: %w", err)
	}
	return nil
}

type archiveCloser struct {
	tarWriter  *tar.Writer
	gzipWriter *gzip.Writer
	file       *os.File
}

// close 关闭归档写入链并保留最先发生的错误。
func (c archiveCloser) close(current error) error {
	current = firstError(current, c.tarWriter.Close())
	current = firstError(current, c.gzipWriter.Close())
	return firstError(current, c.file.Close())
}

// firstError 保留已有根因，只在尚无错误时采用关闭错误。
func firstError(current, next error) error {
	if current != nil {
		return current
	}
	return next
}

// archiveOutput 将一个声明输出递归写入归档。
func archiveOutput(root, output string, writer *tar.Writer) error {
	absolute := filepath.Join(root, filepath.FromSlash(output))
	return filepath.Walk(absolute, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := validateArchiveSource(path, info); err != nil {
			return err
		}
		return writeArchiveEntry(root, path, info, writer)
	})
}

// validateArchiveSource 拒绝无法安全跨工作树恢复的链接和特殊文件。
func validateArchiveSource(path string, info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("cache output must not contain symlink: %s", path)
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return fmt.Errorf("cache output contains unsupported file: %s", path)
	}
	return nil
}

// writeArchiveEntry 写入目录头或普通文件内容并保留执行权限。
func writeArchiveEntry(root, path string, info os.FileInfo, writer *tar.Writer) error {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	header.Name = filepath.ToSlash(relative)
	if err := writer.WriteHeader(header); err != nil || info.IsDir() {
		return err
	}
	input, err := os.Open(path)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(writer, input)
	closeErr := input.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

// restoreArchive 在私有暂存目录验证归档，再替换当前工作树声明产物。
func restoreArchive(root, archivePath string, outputs []string) error {
	stagingRoot := filepath.Join(root, ".build-cache", "build-phase-restore")
	if err := os.MkdirAll(stagingRoot, 0o755); err != nil {
		return fmt.Errorf("create restore root: %w", err)
	}
	staging, err := os.MkdirTemp(stagingRoot, "restore-")
	if err != nil {
		return fmt.Errorf("create restore staging directory: %w", err)
	}
	defer os.RemoveAll(staging)
	if err := extractArchive(archivePath, staging, outputs); err != nil {
		return err
	}
	for _, output := range outputs {
		source := filepath.Join(staging, filepath.FromSlash(output))
		if _, err := os.Lstat(source); err != nil {
			return fmt.Errorf("cache archive is missing declared output %s: %w", output, err)
		}
		destination := filepath.Join(root, filepath.FromSlash(output))
		if err := os.RemoveAll(destination); err != nil {
			return fmt.Errorf("replace cached output %s: %w", output, err)
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return fmt.Errorf("create cached output parent %s: %w", output, err)
		}
		if err := os.Rename(source, destination); err != nil {
			return fmt.Errorf("restore cached output %s: %w", output, err)
		}
	}
	return nil
}

// extractArchive 逐项校验归档路径和类型后解压到私有暂存目录。
func extractArchive(archivePath, staging string, outputs []string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open cache archive: %w", err)
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("open compressed cache archive: %w", err)
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	for {
		header, done, err := nextArchiveHeader(reader)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		name, err := validateArchiveEntryName(header.Name, outputs)
		if err != nil {
			return err
		}
		if err := extractArchiveEntry(staging, name, header, reader); err != nil {
			return err
		}
	}
}

// nextArchiveHeader 将正常 EOF 与损坏归档错误明确区分。
func nextArchiveHeader(reader *tar.Reader) (*tar.Header, bool, error) {
	header, err := reader.Next()
	if errors.Is(err, io.EOF) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read cache archive: %w", err)
	}
	return header, false, nil
}

// validateArchiveEntryName 拒绝路径穿越和未声明输出。
func validateArchiveEntryName(rawName string, outputs []string) (string, error) {
	name := filepath.ToSlash(filepath.Clean(rawName))
	if name == "." || strings.HasPrefix(name, "/") || name == ".." || strings.HasPrefix(name, "../") {
		return "", fmt.Errorf("cache archive contains unsafe path %q", rawName)
	}
	if !belongsToOutput(name, outputs) {
		return "", fmt.Errorf("cache archive contains undeclared output %q", name)
	}
	return name, nil
}

// extractArchiveEntry 恢复一个已验证名称的目录或普通文件。
func extractArchiveEntry(staging, name string, header *tar.Header, reader io.Reader) error {
	target := filepath.Join(staging, filepath.FromSlash(name))
	if header.Typeflag == tar.TypeDir {
		if err := os.MkdirAll(target, fs.FileMode(header.Mode)&0o777); err != nil {
			return fmt.Errorf("restore cache directory %s: %w", name, err)
		}
		return nil
	}
	if header.Typeflag != tar.TypeReg {
		return fmt.Errorf("cache archive contains unsupported entry %q", name)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create cache file parent %s: %w", name, err)
	}
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fs.FileMode(header.Mode)&0o777)
	if err != nil {
		return fmt.Errorf("create cached file %s: %w", name, err)
	}
	_, copyErr := io.Copy(output, reader)
	closeErr := output.Close()
	if copyErr != nil {
		return fmt.Errorf("restore cached file %s: %w", name, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close cached file %s: %w", name, closeErr)
	}
	return nil
}

func belongsToOutput(name string, outputs []string) bool {
	for _, output := range outputs {
		if name == output || strings.HasPrefix(name, output+"/") {
			return true
		}
	}
	return false
}

func readManifest(path string) (cacheManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return cacheManifest{}, fmt.Errorf("read cache manifest: %w", err)
	}
	var manifest cacheManifest
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return cacheManifest{}, fmt.Errorf("decode cache manifest: %w", err)
	}
	return manifest, nil
}

// validateManifest 校验缓存身份、输出闭包和归档内容摘要。
func validateManifest(manifest cacheManifest, name, key string, outputs []string, archivePath string) error {
	if manifest.SchemaVersion != cacheSchemaVersion {
		return fmt.Errorf("cache schema mismatch: got %d want %d", manifest.SchemaVersion, cacheSchemaVersion)
	}
	if manifest.Name != name || manifest.Key != key {
		return fmt.Errorf("cache manifest identity mismatch")
	}
	if !equalStrings(manifest.Outputs, outputs) {
		return fmt.Errorf("cache manifest outputs mismatch")
	}
	actualSHA, err := hashFile(archivePath)
	if err != nil {
		return err
	}
	if manifest.ArchiveSHA256 == "" || manifest.ArchiveSHA256 != actualSHA {
		return fmt.Errorf("cache archive checksum mismatch")
	}
	return nil
}

func validateExistingEntry(entry, name, key string, outputs []string) error {
	manifest, err := readManifest(filepath.Join(entry, "manifest.json"))
	if err != nil {
		return fmt.Errorf("concurrent cache entry is invalid: %w", err)
	}
	return validateManifest(manifest, name, key, outputs, filepath.Join(entry, "artifact.tar.gz"))
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s for hashing: %w", path, err)
	}
	defer file.Close()
	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return "", fmt.Errorf("hash %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
