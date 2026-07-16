package localci

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sourceexport"
)

const (
	buildInputManifestPath = "build/gate/inputs.json"
	toolchainLockPath      = "build/gate/toolchain.lock"
)

// BuildKitRunner 只接收经过安全收敛的候选镜像构建请求。
type BuildKitRunner interface {
	Build(ctx context.Context, request BuildKitBuildRequest) (imageDigest string, err error)
}

// BuildArgument 表示由工具链锁生成的确定性 BuildKit 参数。
type BuildArgument struct {
	Name  string
	Value string
}

// BuildKitBuildRequest 是不暴露 secret、SSH、entitlement 或宿主网络开关的构建合同。
type BuildKitBuildRequest struct {
	SourceTreeSHA       string
	ContextTar          []byte
	ContextDigest       string
	InputManifestDigest string
	InputDigest         string
	ToolchainDigest     string
	DockerfilePath      string
	DockerfileDigest    string
	Platform            string
	BuildKitVersion     string
	DockerfileFrontend  string
	BuildArguments      []BuildArgument
	NetworkPolicy       string
	CacheNamespace      string
}

// CandidateRequest 绑定单一 Git tree、已验证输入条目和当前 accepted 镜像。
type CandidateRequest struct {
	SourceTreeSHA       string
	SourceEntries       []sourceexport.TreeEntry
	Platform            string
	AcceptedInputDigest string
	AcceptedImageDigest string
}

// CandidateResult 返回候选输入闭包和唯一不可变镜像产物。
type CandidateResult struct {
	SourceTreeSHA       string
	InputDigest         string
	ContextDigest       string
	InputManifestDigest string
	ToolchainDigest     string
	DockerfileDigest    string
	ImageDigest         string
	Built               bool
}

// ImageBuilder 负责候选输入闭包、摘要和 BuildKit 调用边界。
type ImageBuilder struct {
	runner BuildKitRunner
}

type buildInputManifest struct {
	SchemaVersion string   `json:"schema_version"`
	Dockerfile    string   `json:"dockerfile"`
	Inputs        []string `json:"inputs"`
}

type toolchainLock struct {
	SchemaVersion      string            `json:"schema_version"`
	BuildKitVersion    string            `json:"buildkit_version"`
	DockerfileFrontend string            `json:"dockerfile_frontend"`
	TargetPlatforms    []string          `json:"target_platforms"`
	BaseImages         []lockedBaseImage `json:"base_images"`
	DependencySources  []string          `json:"dependency_sources"`
	NetworkPolicy      string            `json:"network_policy"`
}

type lockedBaseImage struct {
	Name      string `json:"name"`
	Reference string `json:"reference"`
}

type preparedCandidate struct {
	result       CandidateResult
	buildRequest BuildKitBuildRequest
}

type dockerfileStageTracker struct {
	current int
	aliases map[string]int
}

// NewImageBuilder 创建 fail-fast 的候选镜像构建器。
func NewImageBuilder(runner BuildKitRunner) (*ImageBuilder, error) {
	if buildKitRunnerIsNil(runner) {
		return nil, errors.New("BuildKit runner is required")
	}
	return &ImageBuilder{runner: runner}, nil
}

// EnsureCandidate 在输入摘要变化时构建候选镜像，否则复用不可变 accepted digest。
func (builder *ImageBuilder) EnsureCandidate(ctx context.Context, request CandidateRequest) (CandidateResult, error) {
	if err := validateImageBuilderEntry(builder, ctx); err != nil {
		return CandidateResult{}, err
	}
	if err := validateDigest("accepted input digest", request.AcceptedInputDigest); err != nil {
		return CandidateResult{}, err
	}
	if err := validateDigest("accepted image digest", request.AcceptedImageDigest); err != nil {
		return CandidateResult{}, err
	}
	prepared, err := prepareCandidate(request)
	if err != nil {
		return CandidateResult{}, err
	}
	if prepared.result.InputDigest == request.AcceptedInputDigest {
		prepared.result.ImageDigest = request.AcceptedImageDigest
		return prepared.result, nil
	}
	imageDigest, err := builder.runner.Build(ctx, prepared.buildRequest)
	if err != nil {
		return CandidateResult{}, fmt.Errorf("build candidate image: %w", err)
	}
	if err := validateDigest("candidate image digest", imageDigest); err != nil {
		return CandidateResult{}, err
	}
	prepared.result.ImageDigest = imageDigest
	prepared.result.Built = true
	return prepared.result, nil
}

// prepareCandidate 从单一 Git tree 构造闭包、策略摘要和安全 BuildKit 请求。
func prepareCandidate(request CandidateRequest) (preparedCandidate, error) {
	if !gitObjectPattern.MatchString(request.SourceTreeSHA) {
		return preparedCandidate{}, errors.New("candidate source tree must be a canonical Git object ID")
	}
	if request.Platform == "" {
		return preparedCandidate{}, errors.New("candidate target platform is required")
	}
	entriesByPath, err := indexCandidateEntries(request.SourceEntries)
	if err != nil {
		return preparedCandidate{}, err
	}
	manifest, manifestData, err := loadBuildInputManifest(entriesByPath)
	if err != nil {
		return preparedCandidate{}, err
	}
	closure, closureByPath, err := expandInputClosure(manifest, entriesByPath)
	if err != nil {
		return preparedCandidate{}, err
	}
	lock, lockData, err := loadToolchainLock(entriesByPath, closureByPath, request.Platform)
	if err != nil {
		return preparedCandidate{}, err
	}
	dockerfile := closureByPath[manifest.Dockerfile].Data
	arguments := lockedBuildArguments(lock.BaseImages)
	if err := validateCandidateDockerfile(dockerfile, arguments, closureByPath, entriesByPath); err != nil {
		return preparedCandidate{}, err
	}
	canonicalContext, err := buildCanonicalContext(closure)
	if err != nil {
		return preparedCandidate{}, fmt.Errorf("build canonical candidate context: %w", err)
	}
	return assemblePreparedCandidate(request, manifest, lock, manifestData, lockData, dockerfile, arguments, canonicalContext), nil
}

func indexCandidateEntries(entries []sourceexport.TreeEntry) (map[string]sourceexport.TreeEntry, error) {
	if len(entries) == 0 {
		return nil, errors.New("candidate source entries are required")
	}
	indexed := make(map[string]sourceexport.TreeEntry, len(entries))
	seenPaths := make(map[string]string, len(entries))
	for _, entry := range entries {
		if err := validateContextEntry(entry, seenPaths); err != nil {
			return nil, err
		}
		indexed[entry.Path] = entry
	}
	return indexed, nil
}

func loadBuildInputManifest(entries map[string]sourceexport.TreeEntry) (buildInputManifest, []byte, error) {
	entry, exists := entries[buildInputManifestPath]
	if !exists {
		return buildInputManifest{}, nil, fmt.Errorf("candidate source is missing %s", buildInputManifestPath)
	}
	var manifest buildInputManifest
	if err := decodeStrictJSON(entry.Data, &manifest); err != nil {
		return buildInputManifest{}, nil, fmt.Errorf("decode build input manifest: %w", err)
	}
	if err := validateBuildInputManifest(manifest); err != nil {
		return buildInputManifest{}, nil, err
	}
	return manifest, entry.Data, nil
}

// validateBuildInputManifest 校验 manifest 版本、顺序和必需构建输入。
func validateBuildInputManifest(manifest buildInputManifest) error {
	if manifest.SchemaVersion != "1" {
		return fmt.Errorf("build input manifest schema version %q is unsupported", manifest.SchemaVersion)
	}
	if err := validateContextPath(manifest.Dockerfile, make(map[string]string)); err != nil {
		return fmt.Errorf("manifest Dockerfile: %w", err)
	}
	if err := validateSortedUnique("build input manifest", manifest.Inputs); err != nil {
		return err
	}
	for _, required := range []string{manifest.Dockerfile, buildInputManifestPath, toolchainLockPath} {
		if !inputPatternsCover(manifest.Inputs, required) {
			return fmt.Errorf("build input manifest does not cover required path %q", required)
		}
	}
	for _, input := range manifest.Inputs {
		if err := validateInputPattern(input); err != nil {
			return err
		}
	}
	return nil
}

// validateInputPattern 仅接受规范路径和目录递归模式。
func validateInputPattern(pattern string) error {
	cleaned := strings.TrimSuffix(pattern, "/**")
	if cleaned == pattern && strings.ContainsAny(pattern, "*?[") {
		return fmt.Errorf("build input pattern %q uses an unsupported glob", pattern)
	}
	if cleaned == "" || strings.ContainsAny(cleaned, "*?[") {
		return fmt.Errorf("build input pattern %q is not canonical", pattern)
	}
	if err := validateContextPath(cleaned, make(map[string]string)); err != nil {
		return fmt.Errorf("build input pattern %q: %w", pattern, err)
	}
	return nil
}

func inputPatternsCover(patterns []string, target string) bool {
	for _, pattern := range patterns {
		if pattern == target || strings.HasSuffix(pattern, "/**") && strings.HasPrefix(target, strings.TrimSuffix(pattern, "**")) {
			return true
		}
	}
	return false
}

func expandInputClosure(manifest buildInputManifest, entries map[string]sourceexport.TreeEntry) ([]sourceexport.TreeEntry, map[string]sourceexport.TreeEntry, error) {
	closure := make(map[string]sourceexport.TreeEntry)
	for _, pattern := range manifest.Inputs {
		matched := expandInputPattern(pattern, entries, closure)
		if matched == 0 {
			return nil, nil, fmt.Errorf("build input pattern %q matched no Git entries", pattern)
		}
	}
	ordered := make([]sourceexport.TreeEntry, 0, len(closure))
	for _, entry := range closure {
		ordered = append(ordered, entry)
	}
	sort.Slice(ordered, func(left int, right int) bool { return ordered[left].Path < ordered[right].Path })
	return ordered, closure, nil
}

func expandInputPattern(pattern string, entries map[string]sourceexport.TreeEntry, closure map[string]sourceexport.TreeEntry) int {
	if !strings.HasSuffix(pattern, "/**") {
		entry, exists := entries[pattern]
		if exists {
			closure[pattern] = entry
			return 1
		}
		return 0
	}
	prefix := strings.TrimSuffix(pattern, "**")
	matched := 0
	for name, entry := range entries {
		if strings.HasPrefix(name, prefix) {
			closure[name] = entry
			matched++
		}
	}
	return matched
}

func loadToolchainLock(entries map[string]sourceexport.TreeEntry, closure map[string]sourceexport.TreeEntry, platform string) (toolchainLock, []byte, error) {
	entry, exists := entries[toolchainLockPath]
	if !exists {
		return toolchainLock{}, nil, fmt.Errorf("candidate source is missing %s", toolchainLockPath)
	}
	var lock toolchainLock
	if err := decodeStrictJSON(entry.Data, &lock); err != nil {
		return toolchainLock{}, nil, fmt.Errorf("decode toolchain lock: %w", err)
	}
	if err := validateToolchainLock(lock, closure, platform); err != nil {
		return toolchainLock{}, nil, err
	}
	return lock, entry.Data, nil
}

// validateToolchainLock 校验构建器、平台、镜像、依赖和网络策略闭包。
func validateToolchainLock(lock toolchainLock, closure map[string]sourceexport.TreeEntry, platform string) error {
	if err := validateToolchainVersions(lock); err != nil {
		return err
	}
	if err := validateSortedUnique("target platforms", lock.TargetPlatforms); err != nil {
		return err
	}
	if !containsString(lock.TargetPlatforms, platform) {
		return fmt.Errorf("target platform %q is not locked", platform)
	}
	if err := validateLockedBaseImages(lock.BaseImages); err != nil {
		return err
	}
	return validateLockedDependencies(lock, closure)
}

func validateToolchainVersions(lock toolchainLock) error {
	if lock.SchemaVersion != "1" {
		return fmt.Errorf("toolchain schema version %q is unsupported", lock.SchemaVersion)
	}
	if lock.BuildKitVersion == "" {
		return errors.New("BuildKit version is required")
	}
	if lock.DockerfileFrontend != "builtin:dockerfile.v1" {
		return errors.New("Dockerfile frontend must be the locked builtin:dockerfile.v1 frontend")
	}
	return nil
}

// validateLockedDependencies 校验依赖真值文件和受限构建网络策略。
func validateLockedDependencies(lock toolchainLock, closure map[string]sourceexport.TreeEntry) error {
	if err := validateSortedUnique("dependency sources", lock.DependencySources); err != nil {
		return err
	}
	for _, dependency := range lock.DependencySources {
		if _, exists := closure[dependency]; !exists {
			return fmt.Errorf("locked dependency source %q is outside the input closure", dependency)
		}
	}
	if lock.NetworkPolicy != "none" && lock.NetworkPolicy != "locked-dependencies" {
		return fmt.Errorf("network policy %q is not permitted", lock.NetworkPolicy)
	}
	return nil
}

// validateLockedBaseImages 校验有序 Build ARG 与不可变基础镜像引用。
func validateLockedBaseImages(images []lockedBaseImage) error {
	if len(images) == 0 {
		return errors.New("at least one immutable base image is required")
	}
	previous := ""
	for _, image := range images {
		if image.Name == "" || image.Name <= previous {
			return errors.New("base image names must be non-empty, unique, and sorted")
		}
		if err := validateImmutableReference("base image "+image.Name, image.Reference); err != nil {
			return err
		}
		previous = image.Name
	}
	return nil
}

func lockedBuildArguments(images []lockedBaseImage) []BuildArgument {
	arguments := make([]BuildArgument, len(images))
	for index, image := range images {
		arguments[index] = BuildArgument{Name: image.Name, Value: image.Reference}
	}
	return arguments
}

func assemblePreparedCandidate(request CandidateRequest, manifest buildInputManifest, lock toolchainLock, manifestData []byte, lockData []byte, dockerfile []byte, arguments []BuildArgument, canonical canonicalContext) preparedCandidate {
	manifestDigest := bytesDigest(manifestData)
	toolchainDigest := bytesDigest(lockData)
	dockerfileDigest := bytesDigest(dockerfile)
	fields := []string{canonical.ContextDigest, canonical.InputDigest, manifestDigest, toolchainDigest, dockerfileDigest, request.Platform, lock.BuildKitVersion, lock.DockerfileFrontend, lock.NetworkPolicy}
	for _, argument := range arguments {
		fields = append(fields, argument.Name, argument.Value)
	}
	for _, dependency := range lock.DependencySources {
		fields = append(fields, dependency)
	}
	inputDigest := fieldsDigest(fields...)
	result := CandidateResult{SourceTreeSHA: request.SourceTreeSHA, InputDigest: inputDigest, ContextDigest: canonical.ContextDigest, InputManifestDigest: manifestDigest, ToolchainDigest: toolchainDigest, DockerfileDigest: dockerfileDigest}
	buildRequest := BuildKitBuildRequest{
		SourceTreeSHA: request.SourceTreeSHA, ContextTar: append([]byte(nil), canonical.Tar...), ContextDigest: canonical.ContextDigest,
		InputManifestDigest: manifestDigest, InputDigest: inputDigest, ToolchainDigest: toolchainDigest,
		DockerfilePath: manifest.Dockerfile, DockerfileDigest: dockerfileDigest, Platform: request.Platform,
		BuildKitVersion: lock.BuildKitVersion, DockerfileFrontend: lock.DockerfileFrontend,
		BuildArguments: append([]BuildArgument(nil), arguments...), NetworkPolicy: lock.NetworkPolicy, CacheNamespace: inputDigest,
	}
	return preparedCandidate{result: result, buildRequest: buildRequest}
}

func validateCandidateDockerfile(data []byte, arguments []BuildArgument, closure map[string]sourceexport.TreeEntry, allEntries map[string]sourceexport.TreeEntry) error {
	lines, err := logicalDockerfileLines(data)
	if err != nil {
		return err
	}
	if err := rejectForbiddenDockerfileCapabilities(lines); err != nil {
		return err
	}
	if err := validateDockerfileFrom(lines, arguments); err != nil {
		return err
	}
	return validateDockerfileCopies(lines, closure, allEntries)
}

// logicalDockerfileLines 规范化 Dockerfile 续行并拒绝不完整输入。
func logicalDockerfileLines(data []byte) ([]string, error) {
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, errors.New("Dockerfile contains a NUL byte")
	}
	var lines []string
	pending := ""
	for _, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		continued := strings.HasSuffix(line, "\\")
		line = strings.TrimSpace(strings.TrimSuffix(line, "\\"))
		pending = strings.TrimSpace(pending + " " + line)
		if !continued {
			lines = append(lines, pending)
			pending = ""
		}
	}
	if pending != "" {
		return nil, errors.New("Dockerfile ends with an incomplete continuation")
	}
	if len(lines) == 0 {
		return nil, errors.New("Dockerfile has no instructions")
	}
	return lines, nil
}

// rejectForbiddenDockerfileCapabilities 拒绝候选构建的高权限和宿主访问能力。
func rejectForbiddenDockerfileCapabilities(lines []string) error {
	forbidden := []string{"--mount=type=secret", "--mount=type=ssh", "--network=host", "--security=insecure", "security.insecure", "/var/run/docker.sock", "docker_host", "http_proxy", "https_proxy", "all_proxy", "no_proxy"}
	for _, line := range lines {
		lowered := strings.ToLower(line)
		for _, fragment := range forbidden {
			if strings.Contains(lowered, fragment) {
				return fmt.Errorf("Dockerfile requests forbidden capability %q", fragment)
			}
		}
	}
	return nil
}

// validateDockerfileFrom 要求基础镜像参数默认值和 FROM 引用都来自工具链锁。
func validateDockerfileFrom(lines []string, arguments []BuildArgument) error {
	locked := make(map[string]string, len(arguments))
	allowed := make(map[string]struct{}, len(arguments))
	for _, argument := range arguments {
		locked[argument.Name] = argument.Value
		allowed[argument.Value] = struct{}{}
	}
	if err := validateLockedImageArgumentDefaults(lines, locked); err != nil {
		return err
	}
	fromCount := 0
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) == 0 || !strings.EqualFold(fields[0], "FROM") {
			continue
		}
		fromCount++
		reference, err := dockerfileFromReference(fields, locked)
		if err != nil {
			return err
		}
		if reference != "scratch" {
			if _, exists := allowed[reference]; !exists {
				return fmt.Errorf("Dockerfile FROM %q is not present in the toolchain lock", reference)
			}
		}
	}
	if fromCount == 0 {
		return errors.New("Dockerfile must contain at least one FROM instruction")
	}
	return nil
}

// dockerfileFromReference 解析单条 FROM 并展开受锁定的 ARG。
func dockerfileFromReference(fields []string, locked map[string]string) (string, error) {
	index := 1
	for index < len(fields) && strings.HasPrefix(fields[index], "--") {
		index++
	}
	if index >= len(fields) {
		return "", errors.New("Dockerfile FROM instruction is missing an image")
	}
	reference := fields[index]
	if strings.HasPrefix(reference, "${") && strings.HasSuffix(reference, "}") {
		name := strings.TrimSuffix(strings.TrimPrefix(reference, "${"), "}")
		value, exists := locked[name]
		if !exists {
			return "", fmt.Errorf("Dockerfile FROM argument %q is not locked", name)
		}
		return value, nil
	}
	if reference != "scratch" {
		if err := validateImmutableReference("Dockerfile FROM", reference); err != nil {
			return "", err
		}
	}
	return reference, nil
}

// validateDockerfileCopies 证明每个本地 COPY/ADD 来源均位于输入闭包。
func validateDockerfileCopies(lines []string, closure map[string]sourceexport.TreeEntry, allEntries map[string]sourceexport.TreeEntry) error {
	tracker := dockerfileStageTracker{current: -1, aliases: make(map[string]int)}
	for _, line := range lines {
		if err := tracker.validateLine(line, closure, allEntries); err != nil {
			return err
		}
	}
	return nil
}

func (tracker *dockerfileStageTracker) validateLine(line string, closure map[string]sourceexport.TreeEntry, allEntries map[string]sourceexport.TreeEntry) error {
	instruction, body, found := strings.Cut(line, " ")
	if !found {
		return nil
	}
	switch strings.ToUpper(instruction) {
	case "FROM":
		return tracker.addStage(strings.Fields(line))
	case "COPY", "ADD":
		return tracker.validateCopy(strings.ToUpper(instruction), body, closure, allEntries)
	default:
		return nil
	}
}

func (tracker *dockerfileStageTracker) addStage(fields []string) error {
	alias, err := dockerfileStageAlias(fields)
	if err != nil {
		return err
	}
	tracker.current++
	if alias == "" {
		return nil
	}
	if _, exists := tracker.aliases[alias]; exists {
		return fmt.Errorf("Dockerfile stage alias %q is duplicated", alias)
	}
	tracker.aliases[alias] = tracker.current
	return nil
}

// dockerfileStageAlias 解析 FROM 的可选 alias 并拒绝歧义语法。
func dockerfileStageAlias(fields []string) (string, error) {
	imageIndex := 1
	for imageIndex < len(fields) && strings.HasPrefix(fields[imageIndex], "--") {
		imageIndex++
	}
	if imageIndex >= len(fields) {
		return "", errors.New("Dockerfile FROM instruction is missing an image")
	}
	if len(fields) == imageIndex+1 {
		return "", nil
	}
	if len(fields) != imageIndex+3 || !strings.EqualFold(fields[imageIndex+1], "AS") {
		return "", errors.New("Dockerfile FROM stage alias syntax is invalid")
	}
	alias := strings.ToLower(fields[imageIndex+2])
	if err := validateDockerfileStageAlias(alias); err != nil {
		return "", err
	}
	return alias, nil
}

func validateDockerfileStageAlias(alias string) error {
	if alias == "" || strings.ContainsAny(alias, "/:@${}") {
		return fmt.Errorf("Dockerfile stage alias %q is invalid", alias)
	}
	if _, err := strconv.Atoi(alias); err == nil {
		return fmt.Errorf("Dockerfile stage alias %q must not be numeric", alias)
	}
	return nil
}

// validateCopy 校验 stage 引用或本地输入闭包，不允许两者混用。
func (tracker *dockerfileStageTracker) validateCopy(instruction string, body string, closure map[string]sourceexport.TreeEntry, allEntries map[string]sourceexport.TreeEntry) error {
	sources, fromReference, err := dockerfileCopySources(body)
	if err != nil {
		return err
	}
	if fromReference != "" {
		if instruction == "ADD" {
			return errors.New("Dockerfile ADD must not use --from")
		}
		return tracker.validateCopyFrom(fromReference)
	}
	for _, source := range sources {
		if err := validateDockerfileCopySource(source, closure, allEntries); err != nil {
			return fmt.Errorf("Dockerfile %s source %q: %w", instruction, source, err)
		}
	}
	return nil
}

// validateCopyFrom 只接受严格早于当前 stage 的 alias 或数字 index。
func (tracker *dockerfileStageTracker) validateCopyFrom(reference string) error {
	if index, err := strconv.Atoi(reference); err == nil {
		if index < 0 || index >= tracker.current {
			return fmt.Errorf("Dockerfile COPY --from=%s does not reference a previous stage", reference)
		}
		return nil
	}
	index, exists := tracker.aliases[strings.ToLower(reference)]
	if !exists || index >= tracker.current {
		return fmt.Errorf("Dockerfile COPY --from=%s does not reference a previous stage alias", reference)
	}
	return nil
}

// dockerfileCopySources 解析 COPY/ADD 来源并区分前序 stage 复制。
func dockerfileCopySources(body string) ([]string, string, error) {
	fields := strings.Fields(body)
	fields, fromReference, err := dockerfileCopyOptions(fields)
	if err != nil {
		return nil, "", err
	}
	if fromReference != "" {
		return nil, fromReference, nil
	}
	remaining := strings.Join(fields, " ")
	if strings.HasPrefix(remaining, "[") {
		var paths []string
		if err := json.Unmarshal([]byte(remaining), &paths); err != nil || len(paths) < 2 {
			return nil, "", errors.New("Dockerfile JSON COPY/ADD must contain sources and a destination")
		}
		return paths[:len(paths)-1], "", nil
	}
	if len(fields) < 2 || strings.ContainsAny(remaining, "'\"") {
		return nil, "", errors.New("Dockerfile shell COPY/ADD must use unquoted canonical paths")
	}
	return fields[:len(fields)-1], "", nil
}

// dockerfileCopyOptions 提取唯一的 --from 参数并保留其他受支持 option。
func dockerfileCopyOptions(fields []string) ([]string, string, error) {
	fromReference := ""
	for len(fields) > 0 && strings.HasPrefix(fields[0], "--") {
		option := fields[0]
		lowered := strings.ToLower(option)
		if lowered == "--from" {
			return nil, "", errors.New("Dockerfile COPY --from must use --from=<stage>")
		}
		if strings.HasPrefix(lowered, "--from=") {
			if fromReference != "" {
				return nil, "", errors.New("Dockerfile COPY contains duplicate --from options")
			}
			fromReference = strings.TrimSpace(option[len("--from="):])
			if fromReference == "" {
				return nil, "", errors.New("Dockerfile COPY --from reference is empty")
			}
		}
		fields = fields[1:]
	}
	return fields, fromReference, nil
}

// validateDockerfileCopySource 校验单一来源路径及目录内全部 Git 条目。
func validateDockerfileCopySource(source string, closure map[string]sourceexport.TreeEntry, allEntries map[string]sourceexport.TreeEntry) error {
	cleaned, err := canonicalCopySource(source)
	if err != nil {
		return err
	}
	if _, exists := closure[cleaned]; exists {
		return nil
	}
	return validateCopyDirectoryClosure(cleaned, closure, allEntries)
}

// canonicalCopySource 拒绝动态、远程、glob 和非规范 COPY/ADD 来源。
func canonicalCopySource(source string) (string, error) {
	cleaned := strings.TrimSuffix(source, "/")
	if source == "" || source == "." || path.IsAbs(source) {
		return "", errors.New("path is not canonical")
	}
	if path.Clean(source) != cleaned {
		return "", errors.New("path is not canonical")
	}
	if strings.ContainsAny(source, "*?[${}") {
		return "", errors.New("dynamic, remote, and glob sources are forbidden")
	}
	if strings.Contains(source, "://") {
		return "", errors.New("dynamic, remote, and glob sources are forbidden")
	}
	return cleaned, nil
}

func validateCopyDirectoryClosure(cleaned string, closure map[string]sourceexport.TreeEntry, allEntries map[string]sourceexport.TreeEntry) error {
	prefix := cleaned + "/"
	matched := false
	for name := range allEntries {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		matched = true
		if _, exists := closure[name]; !exists {
			return fmt.Errorf("Git entry %q is outside the declared input closure", name)
		}
	}
	if !matched {
		return errors.New("source does not exist in the Git tree")
	}
	return nil
}

func validateImmutableReference(name string, reference string) error {
	separator := strings.LastIndex(reference, "@")
	if separator <= 0 {
		return fmt.Errorf("%s must use an immutable repository@sha256 reference", name)
	}
	return validateDigest(name, reference[separator+1:])
}

func validateSortedUnique(name string, values []string) error {
	if len(values) == 0 {
		return fmt.Errorf("%s must not be empty", name)
	}
	previous := ""
	for _, value := range values {
		if value == "" || value <= previous {
			return fmt.Errorf("%s must be non-empty, unique, and sorted", name)
		}
		previous = value
	}
	return nil
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("JSON document contains trailing data")
	}
	return nil
}

func bytesDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func fieldsDigest(fields ...string) string {
	var payload []byte
	for _, field := range fields {
		payload = append(payload, field...)
		payload = append(payload, 0)
	}
	return bytesDigest(payload)
}
