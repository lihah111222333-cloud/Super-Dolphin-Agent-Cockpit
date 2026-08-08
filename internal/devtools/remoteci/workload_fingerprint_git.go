package remoteci

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"os/exec"
	"path"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"
)

func loadRemoteGitTreeSnapshot(ctx context.Context, repositoryRoot string, tree string) (*remoteGitTreeSnapshot, error) {
	if !validRemoteGitTreeRequest(ctx, repositoryRoot, tree) {
		return nil, errors.New("remote workload fingerprint Git identity is incomplete")
	}
	command := exec.CommandContext(ctx, "git", "ls-tree", "-r", "-z", "--full-tree", tree, "--")
	command.Dir = repositoryRoot
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("list remote workload fingerprint tree: %w", err)
	}
	entries, byPath, err := parseRemoteGitTreeEntries(output)
	if err != nil {
		return nil, err
	}
	return &remoteGitTreeSnapshot{
		repositoryRoot:         repositoryRoot,
		tree:                   tree,
		entries:                entries,
		byPath:                 byPath,
		productionClosureCache: make(map[string]remoteProductionClosureCache),
		goTestDeclarationCache: make(map[string]remoteGoTestDeclarationCache),
	}, nil
}

func validRemoteGitTreeRequest(ctx context.Context, repositoryRoot string, tree string) bool {
	return ctx != nil && strings.TrimSpace(repositoryRoot) != "" && remoteOIDPattern.MatchString(tree)
}

// parseRemoteGitTreeEntries 严格解析零分隔 Git tree 列表并建立唯一的路径索引。
func parseRemoteGitTreeEntries(output []byte) ([]remoteGitTreeEntry, map[string]remoteGitTreeEntry, error) {
	if len(output) == 0 || len(output) > remoteGitTreeListingMaxBytes {
		return nil, nil, errors.New("remote workload fingerprint tree listing size is invalid")
	}
	records := bytes.Split(output, []byte{0})
	entries := make([]remoteGitTreeEntry, 0, len(records)-1)
	byPath := make(map[string]remoteGitTreeEntry, len(records)-1)
	for _, record := range records {
		if len(record) == 0 {
			continue
		}
		entry, err := parseRemoteGitTreeEntry(string(record))
		if err != nil {
			return nil, nil, err
		}
		if _, duplicate := byPath[entry.path]; duplicate {
			return nil, nil, fmt.Errorf("remote workload fingerprint tree repeats path %q", entry.path)
		}
		entries = append(entries, entry)
		byPath[entry.path] = entry
	}
	if len(entries) == 0 {
		return nil, nil, errors.New("remote workload fingerprint tree is empty")
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].path < entries[right].path })
	return entries, byPath, nil
}

// parseRemoteGitTreeEntry 校验并解码一条 Git ls-tree 记录。
func parseRemoteGitTreeEntry(record string) (remoteGitTreeEntry, error) {
	header, filePath, ok := strings.Cut(record, "\t")
	fields := strings.Fields(header)
	if !validRemoteGitTreeHeader(ok, fields, header) {
		return remoteGitTreeEntry{}, errors.New("remote workload fingerprint tree entry is malformed")
	}
	if !validRemoteGitTreePath(filePath) {
		return remoteGitTreeEntry{}, errors.New("remote workload fingerprint tree path is invalid")
	}
	if fields[1] != "blob" && fields[1] != "commit" {
		return remoteGitTreeEntry{}, fmt.Errorf("remote workload fingerprint tree object type %q is unsupported", fields[1])
	}
	if !validRemoteGitObjectID(fields[2]) {
		return remoteGitTreeEntry{}, errors.New("remote workload fingerprint tree object ID is invalid")
	}
	return remoteGitTreeEntry{mode: fields[0], kind: fields[1], objectID: fields[2], path: filePath}, nil
}

func validRemoteGitTreeHeader(ok bool, fields []string, header string) bool {
	return ok && len(fields) == 3 && strings.Join(fields, " ") == header
}

func validRemoteGitTreePath(filePath string) bool {
	return strings.TrimSpace(filePath) != "" && !path.IsAbs(filePath) && path.Clean(filePath) == filePath &&
		!strings.ContainsAny(filePath, "\\\x00\r\n\t")
}

func validRemoteGitObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
func (snapshot *remoteGitTreeSnapshot) prepareGoSources(ctx context.Context) error {
	snapshot.goSourcesMu.Lock()
	defer snapshot.goSourcesMu.Unlock()
	if snapshot.goSources != nil {
		return nil
	}
	sources, err := snapshot.readGitBlobs(ctx, snapshot.goSourcePaths())
	if err != nil {
		return err
	}
	mappings, err := remoteGoModuleMappings(sources)
	if err != nil {
		return err
	}
	snapshot.goSources = sources
	snapshot.moduleMappings = mappings
	return nil
}

// goSourcePaths 返回解析本地 Go 依赖闭包所需的源码与模块文件路径。
func (snapshot *remoteGitTreeSnapshot) goSourcePaths() []string {
	paths := make([]string, 0)
	for _, entry := range snapshot.entries {
		base := path.Base(entry.path)
		if entry.kind == "blob" && (path.Ext(entry.path) == ".go" || base == "go.mod" || base == "go.sum" || base == "go.work") {
			paths = append(paths, entry.path)
		}
	}
	return paths
}

// remoteGoModuleMappings 从精确 Git blob 构建模块及本地 replace 路径映射。
func remoteGoModuleMappings(sources map[string][]byte) ([]remoteGoModuleMapping, error) {
	goMod, ok := sources["go.mod"]
	if !ok {
		return nil, errors.New("Go workload fingerprint requires go.mod")
	}
	if _, hasWork := sources["go.work"]; hasWork {
		return nil, errors.New("Go workload fingerprint does not accept an unmodelled go.work workspace")
	}
	parsed, err := modfile.Parse("go.mod", goMod, nil)
	if err != nil || parsed.Module == nil || parsed.Module.Mod.Path == "" {
		return nil, errors.New("Go workload fingerprint module path is invalid")
	}
	localMappings, err := localRemoteGoModuleMappings(parsed)
	if err != nil {
		return nil, err
	}
	mappings := append([]remoteGoModuleMapping{{importPath: parsed.Module.Mod.Path, directory: "."}}, localMappings...)
	sort.Slice(mappings, func(left, right int) bool {
		return len(mappings[left].importPath) > len(mappings[right].importPath)
	})
	return mappings, nil
}

func localRemoteGoModuleMappings(parsed *modfile.File) ([]remoteGoModuleMapping, error) {
	mappings := make([]remoteGoModuleMapping, 0, len(parsed.Replace))
	for _, replacement := range parsed.Replace {
		mapping, ok, err := localRemoteGoModuleMapping(replacement)
		if err != nil {
			return nil, err
		}
		if ok {
			mappings = append(mappings, mapping)
		}
	}
	return mappings, nil
}

// localRemoteGoModuleMapping 保留版本化模块替换不参与仓库文件映射，并拒绝越出候选 tree 的本地目录。
func localRemoteGoModuleMapping(replacement *modfile.Replace) (remoteGoModuleMapping, bool, error) {
	if replacement.New.Version != "" {
		return remoteGoModuleMapping{}, false, nil
	}
	if !modfile.IsDirectoryPath(replacement.New.Path) && !isHomeRelativeGoModulePath(replacement.New.Path) {
		return remoteGoModuleMapping{}, false, nil
	}
	directory, err := canonicalRemoteGoModuleDirectory(replacement.New.Path)
	if err != nil {
		return remoteGoModuleMapping{}, false, err
	}
	return remoteGoModuleMapping{
		importPath: replacement.Old.Path,
		directory:  directory,
	}, true, nil
}

// isHomeRelativeGoModulePath 识别依赖用户主目录展开的本地路径，避免把主目录内容带入远程候选 tree。
func isHomeRelativeGoModulePath(value string) bool {
	normalized := strings.ReplaceAll(value, `\`, "/")
	return normalized == "~" || strings.HasPrefix(normalized, "~/")
}

// canonicalRemoteGoModuleDirectory 将本地替换归一化为仓库相对目录，并在路径逃逸时 fail-fast。
func canonicalRemoteGoModuleDirectory(value string) (string, error) {
	normalized := strings.ReplaceAll(value, `\`, "/")
	if isHomeRelativeGoModulePath(normalized) || path.IsAbs(normalized) || isWindowsDriveGoModulePath(normalized) {
		return "", errors.New("Go workload fingerprint local replacement escapes the repository")
	}
	directory := path.Clean(normalized)
	if directory == ".." || strings.HasPrefix(directory, "../") {
		return "", errors.New("Go workload fingerprint local replacement escapes the repository")
	}
	return strings.TrimPrefix(directory, "./"), nil
}

// isWindowsDriveGoModulePath 识别盘符开头的本地路径，防止跨平台解析漏掉外部文件系统。
func isWindowsDriveGoModulePath(value string) bool {
	return len(value) >= 2 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':'
}

// localGoModuleMetadataEntries 返回根模块及本地替换模块的 go.mod/go.sum tree entries，缺少 go.mod 或非 blob 立即失败。
func (snapshot *remoteGitTreeSnapshot) localGoModuleMetadataEntries() ([]remoteGitTreeEntry, error) {
	if snapshot == nil {
		return nil, errors.New("Go workload fingerprint snapshot is required")
	}
	entries := make([]remoteGitTreeEntry, 0, len(snapshot.moduleMappings)*2)
	seen := make(map[string]struct{}, len(snapshot.moduleMappings)*2)
	for _, mapping := range snapshot.moduleMappings {
		for _, base := range []string{"go.mod", "go.sum"} {
			filePath := path.Join(mapping.directory, base)
			entry, exists := snapshot.byPath[filePath]
			if !exists {
				if base == "go.sum" {
					continue
				}
				return nil, fmt.Errorf("Go workload fingerprint local module %q is missing %s", mapping.importPath, filePath)
			}
			if entry.kind != "blob" {
				return nil, fmt.Errorf("Go workload fingerprint local module metadata %q is not a blob", filePath)
			}
			if _, duplicate := seen[filePath]; duplicate {
				continue
			}
			seen[filePath] = struct{}{}
			entries = append(entries, entry)
		}
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].path < entries[right].path })
	return entries, nil
}

// localGoImports 解析目录源码并返回仓库内依赖目录的确定性列表。
func (snapshot *remoteGitTreeSnapshot) localGoImports(directory string) ([]string, error) {
	imports := make(map[string]struct{})
	for filePath, source := range snapshot.goSources {
		if path.Ext(filePath) != ".go" || path.Dir(filePath) != directory {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filePath, source, parser.ImportsOnly)
		if err != nil {
			return nil, fmt.Errorf("parse Go workload source %q: %w", filePath, err)
		}
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return nil, fmt.Errorf("parse Go workload import in %q: %w", filePath, err)
			}
			if local, ok := snapshot.resolveLocalGoImport(importPath); ok {
				imports[local] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(imports))
	for imported := range imports {
		result = append(result, imported)
	}
	sort.Strings(result)
	return result, nil
}

func (snapshot *remoteGitTreeSnapshot) resolveLocalGoImport(importPath string) (string, bool) {
	for _, mapping := range snapshot.moduleMappings {
		if importPath != mapping.importPath && !strings.HasPrefix(importPath, mapping.importPath+"/") {
			continue
		}
		suffix := strings.TrimPrefix(strings.TrimPrefix(importPath, mapping.importPath), "/")
		directory := path.Clean(path.Join(mapping.directory, suffix))
		if directory == "." {
			return ".", true
		}
		return strings.TrimPrefix(directory, "./"), true
	}
	return "", false
}

// readGitBlobs 通过一次有界 git cat-file 批处理读取指定 tree blob。
func (snapshot *remoteGitTreeSnapshot) readGitBlobs(ctx context.Context, paths []string) (map[string][]byte, error) {
	query := snapshot.gitBlobQuery(paths)
	command := exec.CommandContext(ctx, "git", "cat-file", "--batch")
	command.Dir = snapshot.repositoryRoot
	command.Stdin = bytes.NewReader(query)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := command.Start(); err != nil {
		return nil, err
	}
	reader := bufio.NewReader(stdout)
	contents := make(map[string][]byte, len(paths))
	var total int64
	for _, filePath := range paths {
		data, size, err := readRemoteGitBlob(reader, filePath, total)
		if err != nil {
			_ = command.Wait()
			return nil, err
		}
		contents[filePath] = data
		total += size
	}
	if err := command.Wait(); err != nil {
		return nil, fmt.Errorf("read remote workload Git blobs: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return contents, nil
}

func (snapshot *remoteGitTreeSnapshot) gitBlobQuery(paths []string) []byte {
	query := make([]byte, 0, len(paths)*65)
	for _, filePath := range paths {
		query = append(query, snapshot.byPath[filePath].objectID...)
		query = append(query, '\n')
	}
	return query
}

// readRemoteGitBlob 校验批处理头、大小和结尾后读取一个 Git blob。
func readRemoteGitBlob(reader *bufio.Reader, filePath string, total int64) ([]byte, int64, error) {
	header, err := reader.ReadString('\n')
	if err != nil {
		return nil, 0, fmt.Errorf("read Git blob header for %q: %w", filePath, err)
	}
	fields := strings.Fields(strings.TrimSuffix(header, "\n"))
	if !validRemoteGitBlobHeader(fields) {
		return nil, 0, fmt.Errorf("Git blob header for %q is invalid", filePath)
	}
	size, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil || !validRemoteGitBlobSize(size, total) {
		return nil, 0, fmt.Errorf("Git blob size for %q is invalid", filePath)
	}
	data := make([]byte, size)
	if _, err := io.ReadFull(reader, data); err != nil {
		return nil, 0, fmt.Errorf("read Git blob %q: %w", filePath, err)
	}
	terminator, err := reader.ReadByte()
	if err != nil || !validRemoteGitBlobTerminator(terminator) {
		return nil, 0, fmt.Errorf("Git blob trailer for %q is invalid", filePath)
	}
	return data, size, nil
}

func validRemoteGitBlobHeader(fields []string) bool {
	return len(fields) == 3 && fields[1] == "blob"
}

func validRemoteGitBlobSize(size int64, total int64) bool {
	return size >= 0 && size <= remoteGitBlobMaxBytes && total+size <= remoteGitSourceTotalMaxBytes
}

func validRemoteGitBlobTerminator(terminator byte) bool {
	return terminator == '\n'
}

func (snapshot *remoteGitTreeSnapshot) digestMatching(match func(remoteGitTreeEntry) bool) (string, error) {
	var selected []remoteGitTreeEntry
	for _, entry := range snapshot.entries {
		if match(entry) {
			selected = append(selected, entry)
		}
	}
	return snapshot.digestEntries(selected)
}

func (snapshot *remoteGitTreeSnapshot) digestDomainMatching(
	domain string,
	match func(remoteGitTreeEntry) bool,
) (string, error) {
	var selected []remoteGitTreeEntry
	for _, entry := range snapshot.entries {
		if match(entry) {
			selected = append(selected, entry)
		}
	}
	if len(selected) == 0 {
		return "", errors.New("remote workload production input set is empty")
	}
	hasher := sha256.New()
	fmt.Fprintf(hasher, "schema %d\ndomain %s\n", remoteWorkloadInputSchemaVersion, domain)
	for _, entry := range selected {
		fmt.Fprintf(hasher, "%s %s %s\t%s\n", entry.mode, entry.kind, entry.objectID, entry.path)
	}
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil)), nil
}

func (snapshot *remoteGitTreeSnapshot) digestEntries(entries []remoteGitTreeEntry) (string, error) {
	snapshot.captureInputClosure(entries)
	if len(entries) == 0 {
		return "", errors.New("remote workload production input set is empty")
	}
	hasher := sha256.New()
	fmt.Fprintf(hasher, "schema %d\n", remoteWorkloadInputSchemaVersion)
	for _, entry := range entries {
		fmt.Fprintf(hasher, "%s %s %s\t%s\n", entry.mode, entry.kind, entry.objectID, entry.path)
	}
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil)), nil
}

func digestGoTestEntries(entries []remoteGitTreeEntry, testSources []remoteGoTestSource) (string, error) {
	if len(entries) == 0 || len(testSources) == 0 {
		return "", errors.New("remote Go test input set is empty")
	}
	hasher := sha256.New()
	fmt.Fprintf(hasher, "schema %d\n", remoteWorkloadInputSchemaVersion)
	for _, entry := range entries {
		fmt.Fprintf(hasher, "%s %s %s\t%s\n", entry.mode, entry.kind, entry.objectID, entry.path)
	}
	for _, source := range testSources {
		fmt.Fprintf(hasher, "test %s\t%x\n", source.path, sha256.Sum256(source.text))
	}
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil)), nil
}
