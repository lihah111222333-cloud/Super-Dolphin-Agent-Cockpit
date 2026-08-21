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
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
	"golang.org/x/mod/modfile"
)

const remoteGitFailureDiagnosticMaxRunes = 256

func loadRemoteGitTreeSnapshot(ctx context.Context, repositoryRoot string, tree string) (*remoteGitTreeSnapshot, error) {
	return loadRemoteGitTreeSnapshotWithCandidateObjectAuthority(ctx, repositoryRoot, tree, gate.CandidateObjectAuthority{})
}

// remoteGitTreeListingObserverContextKey 仅供同包测试观察严格 tree listing；生产 context 不携带该值。
type remoteGitTreeListingObserverContextKey struct{}

// loadRemoteGitTreeSnapshotWithCandidateObjectAuthority 使用一次性 ODB proof 读取精确 tree。
func loadRemoteGitTreeSnapshotWithCandidateObjectAuthority(ctx context.Context, repositoryRoot string, tree string, authority gate.CandidateObjectAuthority) (*remoteGitTreeSnapshot, error) {
	if !validRemoteGitTreeRequest(ctx, repositoryRoot, tree) {
		return nil, errors.New("remote workload fingerprint Git identity is incomplete")
	}
	command, err := gateprivate.TrustedGitCommandWithCandidateObjectAuthority(ctx, repositoryRoot, authority, "ls-tree", "-r", "-z", "--full-tree", tree, "--")
	if err != nil {
		return nil, fmt.Errorf("resolve trusted Git for remote workload fingerprint tree: %w", err)
	}
	if err := gate.ConfigureBoundedCommand(command); err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("list remote workload fingerprint tree %s: %w: %s", tree, remoteGitContextError(ctx, err), safeRemoteGitFailure(stderr.String(), repositoryRoot, tree))
	}
	entries, byPath, err := parseRemoteGitTreeEntries(output)
	if err != nil {
		return nil, err
	}
	if observer, ok := ctx.Value(remoteGitTreeListingObserverContextKey{}).(func()); ok && observer != nil {
		observer()
	}
	return &remoteGitTreeSnapshot{
		repositoryRoot:           repositoryRoot,
		tree:                     tree,
		candidateObjectAuthority: authority,
		entries:                  entries,
		byPath:                   byPath,
		productionClosureCache:   make(map[string]remoteProductionClosureCache),
		goTestDeclarationCache:   make(map[string]remoteGoTestDeclarationCache),
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

// prepareGoSources 读取精确 Git tree 的 Go 源码、模块元数据和符号链接目标。
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
	if symlinkPaths := snapshot.gitSymlinkPaths(); len(symlinkPaths) != 0 {
		if _, err := snapshot.readGitBlobs(ctx, symlinkPaths); err != nil {
			return fmt.Errorf("read Git symlink targets for Go workload fingerprint: %w", err)
		}
	}
	mappings, err := remoteGoModuleMappings(sources)
	if err != nil {
		return err
	}
	workspaceDirectories, err := remoteGoWorkspaceDirectories(sources)
	if err != nil {
		return err
	}
	if err := snapshot.validatePortableWorkspaceTree(ctx, sources[PortableGoWorkspaceDescriptorPath]); err != nil {
		return err
	}
	snapshot.goSources = sources
	snapshot.moduleMappings = mappings
	snapshot.workspaceModuleDirectories = workspaceDirectories
	return nil
}

// gitSymlinkPaths 按确定顺序返回已跟踪符号链接，运行时观察只解析精确 Git blob。
func (snapshot *remoteGitTreeSnapshot) gitSymlinkPaths() []string {
	paths := make([]string, 0)
	for _, entry := range snapshot.entries {
		if entry.kind == "blob" && entry.mode == "120000" {
			paths = append(paths, entry.path)
		}
	}
	sort.Strings(paths)
	return paths
}

// goSourcePaths 返回解析本地 Go 依赖闭包所需的源码与模块文件路径。
func (snapshot *remoteGitTreeSnapshot) goSourcePaths() []string {
	paths := make([]string, 0)
	for _, entry := range snapshot.entries {
		base := path.Base(entry.path)
		if entry.kind == "blob" && (path.Ext(entry.path) == ".go" || base == "go.mod" || base == "go.sum" || base == "go.work" || entry.path == PortableGoWorkspaceDescriptorPath) {
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
	if goWork, hasWork := sources["go.work"]; hasWork {
		if _, hasPortable := sources[PortableGoWorkspaceDescriptorPath]; hasPortable {
			return nil, errors.New("Go workload fingerprint accepts exactly one workspace model")
		}
		return remoteGoWorkspaceModuleMappings(sources, goWork)
	}
	if portable, ok := sources[PortableGoWorkspaceDescriptorPath]; ok {
		descriptor, err := DecodePortableGoWorkspaceDescriptor(portable)
		if err != nil {
			return nil, err
		}
		return remoteGoWorkspaceModuleMappings(sources, descriptor.GoWork())
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

// remoteGoWorkspaceModuleMappings 严格绑定 tree 内 go.work 的 use 集合与每个模块元数据。
func remoteGoWorkspaceModuleMappings(sources map[string][]byte, data []byte) ([]remoteGoModuleMapping, error) {
	workspace, err := modfile.ParseWork("go.work", data, nil)
	if err != nil || len(workspace.Use) == 0 || len(workspace.Replace) != 0 {
		return nil, errors.New("Go workload fingerprint workspace is invalid")
	}
	mappings := make([]remoteGoModuleMapping, 0, len(workspace.Use))
	directories := make(map[string]struct{}, len(workspace.Use))
	modules := make(map[string]struct{}, len(workspace.Use))
	hasRoot := false
	for _, use := range workspace.Use {
		directory, err := canonicalRemoteGoModuleDirectory(use.Path)
		if err != nil {
			return nil, fmt.Errorf("Go workload fingerprint workspace use: %w", err)
		}
		hasRoot = hasRoot || directory == "."
		if err := appendRemoteWorkspaceModuleMappings(sources, directory, directories, modules, &mappings); err != nil {
			return nil, err
		}
	}
	if !hasRoot {
		return nil, errors.New("Go workload fingerprint workspace must include the engine root module")
	}
	sort.Slice(mappings, func(left, right int) bool { return len(mappings[left].importPath) > len(mappings[right].importPath) })
	return mappings, nil
}

// remoteGoWorkspaceDirectories 返回已建模 workspace 的 canonical use 目录。
func remoteGoWorkspaceDirectories(sources map[string][]byte) ([]string, error) {
	data := sources["go.work"]
	if len(data) == 0 {
		portable := sources[PortableGoWorkspaceDescriptorPath]
		if len(portable) == 0 {
			return nil, nil
		}
		descriptor, err := DecodePortableGoWorkspaceDescriptor(portable)
		if err != nil {
			return nil, err
		}
		return descriptor.WorkspaceDirectories(), nil
	}
	workspace, err := modfile.ParseWork("go.work", data, nil)
	if err != nil {
		return nil, errors.New("Go workload fingerprint workspace is invalid")
	}
	directories := make([]string, 0, len(workspace.Use))
	for _, use := range workspace.Use {
		directory, err := canonicalRemoteGoModuleDirectory(use.Path)
		if err != nil {
			return nil, fmt.Errorf("Go workload fingerprint workspace use: %w", err)
		}
		directories = append(directories, directory)
	}
	sort.Strings(directories)
	return directories, nil
}

// validatePortableWorkspaceTree 核对 descriptor target tree 恰为组合树中的绑定子树。
func (snapshot *remoteGitTreeSnapshot) validatePortableWorkspaceTree(ctx context.Context, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	descriptor, err := DecodePortableGoWorkspaceDescriptor(data)
	if err != nil {
		return err
	}
	command, err := gateprivate.TrustedGitCommandWithCandidateObjectAuthority(ctx, snapshot.repositoryRoot, snapshot.candidateObjectAuthority, "rev-parse", "--verify", "--end-of-options", snapshot.tree+":"+descriptor.TargetPath)
	if err != nil {
		return fmt.Errorf("resolve portable Go workspace target tree: %w", err)
	}
	if err := gate.ConfigureBoundedCommand(command); err != nil {
		return err
	}
	output, err := command.Output()
	if err != nil {
		return fmt.Errorf("read portable Go workspace target tree: %w", remoteGitContextError(ctx, err))
	}
	targetTree, err := strictGitLine(output)
	if err != nil {
		return errors.New("portable Go workspace target tree output is invalid")
	}
	if targetTree != descriptor.TargetTree {
		return errors.New("portable Go workspace target tree binding drifted")
	}
	return nil
}

// appendRemoteWorkspaceModuleMappings 校验 use/module 唯一性并追加模块与本地 replace 映射。
func appendRemoteWorkspaceModuleMappings(sources map[string][]byte, directory string, directories, modules map[string]struct{}, mappings *[]remoteGoModuleMapping) error {
	if _, exists := directories[directory]; exists {
		return errors.New("Go workload fingerprint workspace contains a duplicate module directory")
	}
	directories[directory] = struct{}{}
	mapping, local, err := remoteWorkspaceModuleMapping(sources, directory)
	if err != nil {
		return err
	}
	if _, exists := modules[mapping.importPath]; exists {
		return errors.New("Go workload fingerprint workspace contains a duplicate module path")
	}
	modules[mapping.importPath] = struct{}{}
	*mappings = append(*mappings, mapping)
	*mappings = append(*mappings, local...)
	return nil
}

// remoteWorkspaceModuleMapping 解析一个已校验 workspace use 目录的 module 与本地 replace。
func remoteWorkspaceModuleMapping(sources map[string][]byte, directory string) (remoteGoModuleMapping, []remoteGoModuleMapping, error) {
	moduleFile := "go.mod"
	if directory != "." {
		moduleFile = path.Join(directory, "go.mod")
	}
	data, ok := sources[moduleFile]
	if !ok {
		return remoteGoModuleMapping{}, nil, errors.New("Go workload fingerprint workspace module metadata is missing")
	}
	parsed, err := modfile.Parse(moduleFile, data, nil)
	if err != nil || parsed.Module == nil || parsed.Module.Mod.Path == "" {
		return remoteGoModuleMapping{}, nil, errors.New("Go workload fingerprint workspace module path is invalid")
	}
	local, err := localRemoteGoModuleMappingsAt(parsed, directory)
	return remoteGoModuleMapping{importPath: parsed.Module.Mod.Path, directory: directory}, local, err
}

func localRemoteGoModuleMappings(parsed *modfile.File) ([]remoteGoModuleMapping, error) {
	return localRemoteGoModuleMappingsAt(parsed, ".")
}

// localRemoteGoModuleMappingsAt 将 replace 相对路径绑定到所属 workspace module 目录。
func localRemoteGoModuleMappingsAt(parsed *modfile.File, moduleDirectory string) ([]remoteGoModuleMapping, error) {
	mappings := make([]remoteGoModuleMapping, 0, len(parsed.Replace))
	for _, replacement := range parsed.Replace {
		mapping, ok, err := localRemoteGoModuleMappingAt(replacement, moduleDirectory)
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
	return localRemoteGoModuleMappingAt(replacement, ".")
}

// localRemoteGoModuleMappingAt 解析 module-relative replace 并拒绝逃逸组合 tree。
func localRemoteGoModuleMappingAt(replacement *modfile.Replace, moduleDirectory string) (remoteGoModuleMapping, bool, error) {
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
	if moduleDirectory != "." {
		directory, err = canonicalRemoteGoModuleDirectory(path.Join(moduleDirectory, directory))
		if err != nil {
			return remoteGoModuleMapping{}, false, err
		}
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
	if entry, ok := snapshot.byPath[PortableGoWorkspaceDescriptorPath]; ok {
		if entry.kind != "blob" {
			return nil, errors.New("portable Go workspace descriptor is not a blob")
		}
		entries = append(entries, entry)
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
	command, err := gateprivate.TrustedGitCommandWithCandidateObjectAuthority(ctx, snapshot.repositoryRoot, snapshot.candidateObjectAuthority, "cat-file", "--batch")
	if err != nil {
		return nil, fmt.Errorf("resolve trusted Git for remote workload blobs: %w", err)
	}
	command.Stdin = bytes.NewReader(query)
	if err := gate.ConfigureBoundedCommand(command); err != nil {
		return nil, err
	}
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
			waitErr := command.Wait()
			return nil, errors.Join(remoteGitContextError(ctx, err), waitErr)
		}
		contents[filePath] = data
		entry, ok := snapshot.byPath[filePath]
		if ok && entry.mode == "120000" {
			snapshot.rememberRemoteGitBlob(entry.objectID, data)
		}
		total += size
	}
	if err := command.Wait(); err != nil {
		return nil, fmt.Errorf("read remote workload Git blobs: %w: %s", remoteGitContextError(ctx, err), strings.TrimSpace(stderr.String()))
	}
	return contents, nil
}

func remoteGitContextError(ctx context.Context, commandErr error) error {
	if ctx.Err() != nil {
		return errors.Join(ctx.Err(), commandErr)
	}
	return commandErr
}

// safeRemoteGitFailure 保留 Git 失败分类，同时移除仓库、对象路径、tree 与控制字符。
func safeRemoteGitFailure(stderr string, repositoryRoot string, tree string) string {
	diagnostic := strings.ReplaceAll(stderr, repositoryRoot, "<repository>")
	diagnostic = strings.ReplaceAll(diagnostic, tree, "<tree>")
	fields := strings.Fields(diagnostic)
	for index, field := range fields {
		if strings.Contains(field, "/") {
			fields[index] = "<path>"
		}
	}
	diagnostic = strings.Join(fields, " ")
	if diagnostic == "" {
		return "stderr unavailable"
	}
	runes := []rune(diagnostic)
	if len(runes) > remoteGitFailureDiagnosticMaxRunes {
		return string(runes[:remoteGitFailureDiagnosticMaxRunes]) + "..."
	}
	return diagnostic
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

func (snapshot *remoteGitTreeSnapshot) digestEntries(entries []remoteGitTreeEntry) (string, error) {
	if len(entries) == 0 {
		return "", errors.New("remote workload production input set is empty")
	}
	hasher := sha256.New()
	fmt.Fprintf(hasher, "domain %s\n", cicontract.WorkloadInputFingerprintDomain)
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
	fmt.Fprintf(hasher, "domain %s\n", cicontract.WorkloadInputFingerprintDomain)
	fmt.Fprintf(hasher, "schema %d\n", remoteWorkloadInputSchemaVersion)
	for _, entry := range entries {
		fmt.Fprintf(hasher, "%s %s %s\t%s\n", entry.mode, entry.kind, entry.objectID, entry.path)
	}
	for _, source := range testSources {
		fmt.Fprintf(hasher, "test %s\t%x\n", source.path, sha256.Sum256(source.text))
	}
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil)), nil
}
