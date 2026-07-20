package localci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sourceexport"
)

const buildxProvenanceType = "https://mobyproject.org/buildkit@v1"

const runtimeDepsLockPath = "build/gate/runtime-deps.lock"

type toolchainLock struct {
	SchemaVersion      string             `json:"schema_version"`
	BuildKitVersion    string             `json:"buildkit_version"`
	DockerfileFrontend string             `json:"dockerfile_frontend"`
	SourceDateEpoch    string             `json:"source_date_epoch"`
	TargetPlatforms    []string           `json:"target_platforms"`
	BaseImages         []lockedBaseImage  `json:"base_images"`
	DependencySources  []string           `json:"dependency_sources"`
	RuntimeDepsLock    string             `json:"runtime_deps_lock"`
	RuntimeTools       lockedRuntimeTools `json:"runtime_tools"`
	NetworkPolicy      string             `json:"network_policy"`
}

type lockedRuntimeTools struct {
	NodeVersion     string                 `json:"node_version"`
	NPMVersion      string                 `json:"npm_version"`
	PythonVersion   string                 `json:"python_version"`
	Ripgrep         string                 `json:"ripgrep"`
	Sqruff          string                 `json:"sqruff"`
	SqruffArtifacts []lockedSqruffArtifact `json:"sqruff_artifacts"`
	Gopls           string                 `json:"gopls"`
	SQLC            string                 `json:"sqlc"`
	NPMPackages     []string               `json:"npm_lsp_packages"`
}

type lockedSqruffArtifact struct {
	Platform string `json:"platform"`
	URL      string `json:"url"`
	SHA256   string `json:"sha256"`
}

type runtimeDepsImage struct {
	Platform  string             `json:"platform"`
	Image     gate.ImageIdentity `json:"image"`
	ImageSize int64              `json:"image_size_bytes"`
}

var runtimeDepsPlatforms = []string{"linux/amd64", "linux/arm64"}

type lockedBaseImage struct {
	Name      string `json:"name"`
	Reference string `json:"reference"`
}

// validateToolchainVersions 校验工具链 schema、BuildKit 前端和确定性时间戳。
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
	if err := validateSourceDateEpoch(lock.SourceDateEpoch); err != nil {
		return err
	}
	return nil
}

// loadRuntimeDepsImageIdentity 严格解码运行时依赖锁并返回已验证的平台镜像身份。
func loadRuntimeDepsImageIdentity(closure map[string]sourceexport.TreeEntry, platform string) (gate.ImageIdentity, error) {
	entry, exists := closure[runtimeDepsLockPath]
	if !exists {
		return gate.ImageIdentity{}, fmt.Errorf("candidate input closure is missing %s", runtimeDepsLockPath)
	}
	var lock runtimeDepsLock
	if err := decodeStrictJSON(entry.Data, &lock); err != nil {
		return gate.ImageIdentity{}, fmt.Errorf("decode runtime dependencies lock: %w", err)
	}
	if err := validateRuntimeDepsLock(lock, platform, closure); err != nil {
		return gate.ImageIdentity{}, err
	}
	for _, image := range lock.Images {
		if image.Platform == platform {
			return image.Image, nil
		}
	}
	return gate.ImageIdentity{}, fmt.Errorf("runtime dependencies image for target %q is not locked", platform)
}

// validateRuntimeDepsLock 校验 schema、OCI 身份、目标平台和必需元数据。
func validateRuntimeDepsLock(lock runtimeDepsLock, platform string, closure map[string]sourceexport.TreeEntry) error {
	if err := validateRuntimeDepsLockHeader(lock); err != nil {
		return err
	}
	if err := validateRuntimeDepsImages(lock.Images); err != nil {
		return err
	}
	if !slices.Contains(runtimeDepsPlatforms, platform) {
		return fmt.Errorf("runtime dependencies target platform %q is unsupported", platform)
	}
	return validateRuntimeDepsClosure(lock, closure)
}

// validateRuntimeDepsLockHeader 约束 schema3、匿名拉取策略和固定双平台镜像数量。
func validateRuntimeDepsLockHeader(lock runtimeDepsLock) error {
	if lock.SchemaVersion != "3" {
		return fmt.Errorf("runtime dependencies lock schema version %q is unsupported", lock.SchemaVersion)
	}
	if lock.RegistryPullPolicy != "anonymous" {
		return fmt.Errorf("runtime dependencies registry pull policy %q is unsupported", lock.RegistryPullPolicy)
	}
	if len(lock.Images) != len(runtimeDepsPlatforms) {
		return fmt.Errorf("runtime dependencies image count = %d, want %d", len(lock.Images), len(runtimeDepsPlatforms))
	}
	return nil
}

// validateRuntimeDepsImages 校验双平台镜像共享 repository/index 且摘要互不重复。
func validateRuntimeDepsImages(images []runtimeDepsImage) error {
	repository := images[0].Image.Registry
	indexDigest := images[0].Image.OCIIndexDigest
	manifestDigests := make(map[string]struct{}, len(images))
	configDigests := make(map[string]struct{}, len(images))
	for index, expectedPlatform := range runtimeDepsPlatforms {
		if err := validateRuntimeDepsImage(images[index], expectedPlatform, index, repository, indexDigest, manifestDigests, configDigests); err != nil {
			return err
		}
	}
	return nil
}

// validateRuntimeDepsImage 校验单个平台镜像身份、共享来源和正向镜像大小。
func validateRuntimeDepsImage(image runtimeDepsImage, expectedPlatform string, index int, repository string, indexDigest string, manifestDigests map[string]struct{}, configDigests map[string]struct{}) error {
	if image.Platform != expectedPlatform {
		return fmt.Errorf("runtime dependencies image platform %q at index %d, want %q", image.Platform, index, expectedPlatform)
	}
	if err := image.Image.Validate(); err != nil {
		return fmt.Errorf("validate runtime dependencies image identity for %s: %w", expectedPlatform, err)
	}
	if err := validateRemoteImageRegistry(image.Image.Registry); err != nil {
		return fmt.Errorf("validate runtime dependencies image registry for %s: %w", expectedPlatform, err)
	}
	if image.Image.Registry != repository || image.Image.OCIIndexDigest != indexDigest {
		return errors.New("runtime dependency platforms must share one registry repository and OCI index digest")
	}
	if err := recordRuntimeDepsImageDigests(image.Image, manifestDigests, configDigests); err != nil {
		return err
	}
	if image.ImageSize <= 0 {
		return fmt.Errorf("runtime dependencies image size for %s must be positive", expectedPlatform)
	}
	return validateRuntimeDepsPlatform(image.Image, expectedPlatform)
}

// recordRuntimeDepsImageDigests 拒绝平台 manifest 与 config 摘要重复。
func recordRuntimeDepsImageDigests(image gate.ImageIdentity, manifestDigests map[string]struct{}, configDigests map[string]struct{}) error {
	if _, exists := manifestDigests[image.PlatformManifestDigest]; exists {
		return errors.New("runtime dependency platform manifest digest is duplicated")
	}
	manifestDigests[image.PlatformManifestDigest] = struct{}{}
	if _, exists := configDigests[image.ConfigDigest]; exists {
		return errors.New("runtime dependency config digest is duplicated")
	}
	configDigests[image.ConfigDigest] = struct{}{}
	return nil
}

// validateRuntimeDepsPlatform 将 OCI 平台身份严格绑定到候选目标平台。
func validateRuntimeDepsPlatform(image gate.ImageIdentity, platform string) error {
	actual := image.OS + "/" + image.Architecture
	if actual != platform || image.Variant != "" {
		return fmt.Errorf("runtime dependencies image platform %q does not match target %q", actual, platform)
	}
	return nil
}

// validateLockedDependencies 校验依赖真值文件、工具版本和受限构建网络策略。
func validateLockedDependencies(lock toolchainLock, closure map[string]sourceexport.TreeEntry) error {
	if err := validateRuntimeDepsLockBinding(lock.RuntimeDepsLock, closure); err != nil {
		return err
	}
	if err := validateLockedRuntimeTools(lock.RuntimeTools); err != nil {
		return err
	}
	if err := validateDependencySources(lock.DependencySources, closure); err != nil {
		return err
	}
	return validateLockedNetworkPolicy(lock.NetworkPolicy)
}

// validateRuntimeDepsLockBinding 要求唯一规范锁路径属于候选输入闭包。
func validateRuntimeDepsLockBinding(path string, closure map[string]sourceexport.TreeEntry) error {
	if path != runtimeDepsLockPath {
		return fmt.Errorf("runtime dependencies lock must be %q", runtimeDepsLockPath)
	}
	if _, exists := closure[runtimeDepsLockPath]; !exists {
		return fmt.Errorf("runtime dependencies lock %q is outside the input closure", runtimeDepsLockPath)
	}
	return nil
}

// validateLockedRuntimeTools 要求每个工具版本和 LSP npm 包均被显式锁定。
func validateLockedRuntimeTools(tools lockedRuntimeTools) error {
	if tools.NodeVersion == "" || tools.NPMVersion == "" || tools.PythonVersion == "" ||
		tools.Ripgrep == "" || tools.Sqruff == "" || tools.Gopls == "" || tools.SQLC == "" {
		return errors.New("runtime tool versions must all be locked")
	}
	if err := validateLockedSqruffArtifacts(tools); err != nil {
		return err
	}
	return validateSortedUnique("runtime npm LSP packages", tools.NPMPackages)
}

// validateLockedSqruffArtifact 将 SQL diagnostics 工具绑定到规范 release 和归档摘要。
func validateLockedSqruffArtifacts(tools lockedRuntimeTools) error {
	if len(tools.SqruffArtifacts) != len(runtimeDepsPlatforms) {
		return errors.New("sqruff artifacts must contain exactly linux/amd64 and linux/arm64")
	}
	for index, platform := range runtimeDepsPlatforms {
		artifact := tools.SqruffArtifacts[index]
		if artifact.Platform != platform {
			return fmt.Errorf("sqruff artifact platform %q at index %d, want %q", artifact.Platform, index, platform)
		}
		wantURL := "https://github.com/quarylabs/sqruff/releases/download/v0.38.0/sqruff-linux-"
		if platform == "linux/amd64" {
			wantURL += "x86_64-musl.tar.gz"
		} else {
			wantURL += "aarch64-musl.tar.gz"
		}
		if artifact.URL != wantURL || len(artifact.SHA256) != sha256.Size*2 {
			return fmt.Errorf("sqruff artifact for %s is not canonically locked", platform)
		}
		decoded, err := hex.DecodeString(artifact.SHA256)
		if err != nil || hex.EncodeToString(decoded) != artifact.SHA256 {
			return fmt.Errorf("sqruff artifact SHA-256 for %s is not canonical", platform)
		}
	}
	return nil
}

// validateDependencySources 要求有序依赖源全部属于候选输入闭包。
func validateDependencySources(dependencies []string, closure map[string]sourceexport.TreeEntry) error {
	if err := validateSortedUnique("dependency sources", dependencies); err != nil {
		return err
	}
	for _, dependency := range dependencies {
		if _, exists := closure[dependency]; !exists {
			return fmt.Errorf("locked dependency source %q is outside the input closure", dependency)
		}
	}
	return nil
}

// validateLockedNetworkPolicy 只接受离线或已锁定依赖网络策略。
func validateLockedNetworkPolicy(policy string) error {
	if policy != "none" && policy != "locked-dependencies" {
		return fmt.Errorf("network policy %q is not permitted", policy)
	}
	return nil
}

// buildxMetadata 固化 docker buildx --metadata-file 的受支持顶层字段。
type buildxMetadata struct {
	Provenance     json.RawMessage  `json:"buildx.build.provenance"`
	BuildReference string           `json:"buildx.build.ref"`
	CacheManifest  json.RawMessage  `json:"cache.manifest"`
	ConfigDigest   string           `json:"containerimage.config.digest"`
	Descriptor     buildxDescriptor `json:"containerimage.descriptor"`
	ImageDigest    string           `json:"containerimage.digest"`
	ImageName      string           `json:"image.name"`
}

type buildxDescriptor struct {
	MediaType string         `json:"mediaType"`
	Digest    string         `json:"digest"`
	Size      int64          `json:"size"`
	Platform  buildxPlatform `json:"platform"`
}

type buildxProvenance struct {
	Builder     json.RawMessage  `json:"builder"`
	BuildType   string           `json:"buildType"`
	Materials   json.RawMessage  `json:"materials"`
	Invocation  buildxInvocation `json:"invocation"`
	BuildConfig json.RawMessage  `json:"buildConfig"`
	Metadata    json.RawMessage  `json:"metadata"`
}

type buildxInvocation struct {
	ConfigSource buildxConfigSource `json:"configSource"`
	Parameters   buildxParameters   `json:"parameters"`
	Environment  buildxEnvironment  `json:"environment"`
}

type buildxConfigSource struct {
	URI        string               `json:"uri"`
	Digest     buildxContextDigests `json:"digest"`
	EntryPoint string               `json:"entryPoint"`
}

type buildxContextDigests struct {
	SHA256 string `json:"sha256"`
}

type buildxParameters struct {
	Frontend string            `json:"frontend"`
	Args     map[string]string `json:"args"`
	Secrets  []json.RawMessage `json:"secrets,omitempty"`
	SSH      []json.RawMessage `json:"ssh,omitempty"`
}

type buildxEnvironment struct {
	Platform string `json:"platform"`
}

// validateBuildxMetadata 严格解码并复验输出、descriptor 与 provenance 的请求绑定。
func validateBuildxMetadata(data []byte, request BuildKitBuildRequest, configOutput string) (string, error) {
	var metadata buildxMetadata
	if err := decodeStrictJSON(data, &metadata); err != nil {
		return "", fmt.Errorf("decode buildx metadata: %w", err)
	}
	if err := validateBuildxMetadataFields(metadata, configOutput); err != nil {
		return "", err
	}
	var provenance buildxProvenance
	if err := decodeStrictJSON(metadata.Provenance, &provenance); err != nil {
		return "", fmt.Errorf("decode buildx provenance: %w", err)
	}
	if err := validateBuildxProvenance(provenance, request); err != nil {
		return "", err
	}
	if err := validateBuildxDescriptor(metadata.Descriptor, request.Platform); err != nil {
		return "", err
	}
	if metadata.ImageDigest != metadata.Descriptor.Digest {
		return "", errors.New("buildx image digest does not match the platform manifest descriptor")
	}
	expectedTag, err := candidateImageTag(request.InputDigest)
	if err != nil {
		return "", err
	}
	if metadata.ImageName != expectedTag {
		return "", fmt.Errorf("buildx image name %q does not match fixed candidate tag %q", metadata.ImageName, expectedTag)
	}
	return metadata.ImageDigest, nil
}

// validateBuildxMetadataFields 校验顶层必填字段和 quiet 输出 config digest。
func validateBuildxMetadataFields(metadata buildxMetadata, configOutput string) error {
	if !rawJSONPresent(metadata.Provenance) {
		return errors.New("buildx metadata provenance is required")
	}
	if !rawJSONPresent(metadata.CacheManifest) {
		return errors.New("buildx cache manifest metadata is required")
	}
	if metadata.BuildReference == "" || strings.TrimSpace(metadata.BuildReference) != metadata.BuildReference {
		return errors.New("buildx metadata build reference is required")
	}
	if err := validateDigest("buildx metadata config digest", metadata.ConfigDigest); err != nil {
		return err
	}
	if metadata.ConfigDigest != configOutput {
		return errors.New("buildx stdout config digest does not match metadata")
	}
	if err := validateDigest("buildx metadata image digest", metadata.ImageDigest); err != nil {
		return err
	}
	if metadata.ImageName == "" {
		return errors.New("buildx metadata immutable image name is required")
	}
	return nil
}

// validateBuildxProvenance 校验 stdin context、平台和固定 frontend 参数。
func validateBuildxProvenance(provenance buildxProvenance, request BuildKitBuildRequest) error {
	if !rawJSONPresent(provenance.Builder) || !rawJSONPresent(provenance.Materials) ||
		!rawJSONPresent(provenance.BuildConfig) || !rawJSONPresent(provenance.Metadata) {
		return errors.New("buildx provenance is missing required evidence")
	}
	if provenance.BuildType != buildxProvenanceType {
		return errors.New("buildx provenance build type is not supported")
	}
	if err := validateBuildxConfigSource(provenance.Invocation.ConfigSource, request); err != nil {
		return err
	}
	if provenance.Invocation.Environment.Platform != request.Platform {
		return errors.New("buildx provenance platform does not match the request")
	}
	return validateBuildxParameters(provenance.Invocation.Parameters, request)
}

func validateBuildxConfigSource(source buildxConfigSource, request BuildKitBuildRequest) error {
	if !strings.HasPrefix(source.URI, "http://buildkit-session/") {
		return errors.New("buildx provenance context is not the stdin session")
	}
	if source.Digest.SHA256 != strings.TrimPrefix(request.ContextDigest, "sha256:") {
		return errors.New("buildx provenance context digest does not match the canonical tar")
	}
	if source.EntryPoint != request.DockerfilePath {
		return errors.New("buildx provenance Dockerfile path does not match the request")
	}
	return nil
}

func validateBuildxParameters(parameters buildxParameters, request BuildKitBuildRequest) error {
	if parameters.Frontend != "dockerfile.v0" {
		return errors.New("buildx provenance frontend is not dockerfile.v0")
	}
	if len(parameters.Secrets) != 0 || len(parameters.SSH) != 0 {
		return errors.New("buildx provenance contains forbidden secret or SSH inputs")
	}
	expected := expectedBuildxProvenanceArgs(request)
	if !maps.Equal(parameters.Args, expected) {
		return errors.New("buildx provenance arguments do not match the locked command")
	}
	return nil
}

func expectedBuildxProvenanceArgs(request BuildKitBuildRequest) map[string]string {
	arguments := map[string]string{"force-network-mode": "none"}
	for _, argument := range request.BuildArguments {
		arguments["build-arg:"+argument.Name] = argument.Value
	}
	for _, label := range sortedBuildxBindingLabels(request) {
		name, value, _ := strings.Cut(label, "=")
		arguments["label:"+name] = value
	}
	return arguments
}

// validateBuildxDescriptor 只接受目标平台的单一 Docker manifest descriptor。
func validateBuildxDescriptor(descriptor buildxDescriptor, platformValue string) error {
	if descriptor.MediaType != buildxManifestMedia {
		return errors.New("buildx result is not a single Docker platform manifest")
	}
	if err := validateDigest("buildx platform manifest digest", descriptor.Digest); err != nil {
		return err
	}
	if descriptor.Size <= 0 {
		return errors.New("buildx platform manifest descriptor size is invalid")
	}
	expected, err := parseBuildxPlatform(platformValue)
	if err != nil {
		return err
	}
	if descriptor.Platform != expected {
		return errors.New("buildx platform manifest descriptor does not match the request")
	}
	return nil
}

func rawJSONPresent(value json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(value))
	return trimmed != "" && trimmed != "null"
}

// DockerCandidateIdentityResolver derives a complete identity from the host Docker content store.
type DockerCandidateIdentityResolver struct {
	runner dockerRunner
}

// NewDockerCandidateIdentityResolver 构造生产宿主侧 candidate identity inspector。
func NewDockerCandidateIdentityResolver() *DockerCandidateIdentityResolver {
	return &DockerCandidateIdentityResolver{runner: execDockerRunner{}}
}

// ResolveCandidateIdentity 复核 descriptor、config、rootfs、labels 与目标平台。
func (resolver *DockerCandidateIdentityResolver) ResolveCandidateIdentity(
	ctx context.Context,
	candidate PromotionCandidate,
	result CandidateResult,
) (gate.ImageIdentity, error) {
	if resolver == nil || interfaceValueIsNil(resolver.runner) || ctx == nil {
		return gate.ImageIdentity{}, errors.New("candidate Docker identity resolver is not configured")
	}
	reference, err := CandidateImageReference(result.ImageDigest)
	if err != nil {
		return gate.ImageIdentity{}, err
	}
	document, err := resolver.inspectCandidate(ctx, reference)
	if err != nil {
		return gate.ImageIdentity{}, err
	}
	configDigest, platform, err := validateCandidateInspect(document, candidate, result)
	if err != nil {
		return gate.ImageIdentity{}, err
	}
	return candidateIdentityFromInspect(reference, document, candidate, result, configDigest, platform)
}

// inspectCandidate 严格读取单一 host Docker image inspect 文档。
func (resolver *DockerCandidateIdentityResolver) inspectCandidate(
	ctx context.Context,
	reference string,
) (imageInspectDocument, error) {
	output, err := resolver.runner.Run(ctx, "image", "inspect", reference)
	if err != nil {
		return imageInspectDocument{}, fmt.Errorf("inspect promotion candidate image: %w", err)
	}
	var document imageInspectDocument
	if err := decodeSingleInspect(output, &document); err != nil {
		return imageInspectDocument{}, fmt.Errorf("decode promotion candidate image inspect: %w", err)
	}
	return document, nil
}

// validateCandidateInspect 校验候选 descriptor、config、rootfs 与平台闭包。
func validateCandidateInspect(
	document imageInspectDocument,
	candidate PromotionCandidate,
	result CandidateResult,
) (string, buildxPlatform, error) {
	if document.Descriptor == nil || document.Config == nil || document.RootFS == nil {
		return "", buildxPlatform{}, errors.New("promotion candidate image inspect is incomplete")
	}
	configDigest := document.Descriptor.Annotations["config.digest"]
	if err := validateCandidateInspectDigests(document, result, configDigest); err != nil {
		return "", buildxPlatform{}, err
	}
	if err := validateCandidateRootFS(document); err != nil {
		return "", buildxPlatform{}, err
	}
	platform, err := parseBuildxPlatform(candidate.Platform)
	if err != nil {
		return "", buildxPlatform{}, err
	}
	if !candidateInspectPlatformMatches(document, platform) {
		return "", buildxPlatform{}, errors.New("promotion candidate inspect platform drifted")
	}
	return configDigest, platform, nil
}

// validateCandidateRootFS 要求非空 layers diff-ID 集合。
func validateCandidateRootFS(document imageInspectDocument) error {
	if document.RootFS.Type != "layers" || len(document.RootFS.Layers) == 0 {
		return errors.New("promotion candidate rootfs layers are required")
	}
	return nil
}

// candidateInspectPlatformMatches 比较 inspect 与请求的完整平台三元组。
func candidateInspectPlatformMatches(document imageInspectDocument, platform buildxPlatform) bool {
	return document.OS == platform.OS && document.Architecture == platform.Architecture && document.Variant == platform.Variant
}

// validateCandidateInspectDigests 拒绝 manifest、config 或展示 ID 语义漂移。
func validateCandidateInspectDigests(document imageInspectDocument, result CandidateResult, configDigest string) error {
	if document.Descriptor.Digest != result.ImageDigest {
		return errors.New("promotion candidate descriptor digest drifted from build result")
	}
	if err := validateDigest("promotion candidate config digest", configDigest); err != nil {
		return err
	}
	if document.ID != result.ImageDigest && document.ID != configDigest {
		return errors.New("promotion candidate inspect ID matches neither manifest nor config")
	}
	return nil
}

// candidateIdentityFromInspect 构造完整 identity 并复核 labels 与 immutable reference。
func candidateIdentityFromInspect(
	reference string,
	document imageInspectDocument,
	candidate PromotionCandidate,
	result CandidateResult,
	configDigest string,
	platform buildxPlatform,
) (gate.ImageIdentity, error) {
	expected := expectedImageMetadata{
		PolicyDigest: candidate.PolicyDigest, SourceTreeSHA: candidate.SourceTree,
		InputDigest: candidate.ImageInputDigest, ToolchainDigest: candidate.ToolchainDigest,
		SchemaVersion: candidate.ImageSchemaVersion, OS: platform.OS,
		Architecture: platform.Architecture, Variant: platform.Variant,
	}
	identity := gate.ImageIdentity{
		Registry: candidateImageRepository, OCIIndexDigest: result.ImageDigest,
		PlatformManifestDigest: result.ImageDigest, ConfigDigest: configDigest,
		RootFSDiffIDs: append([]string(nil), document.RootFS.Layers...),
		OS:            document.OS, Architecture: document.Architecture, Variant: document.Variant,
	}
	if err := validateImageIdentity(gateImageIdentityReader{ImageIdentity: identity}, document.Config.Labels, expected); err != nil {
		return gate.ImageIdentity{}, fmt.Errorf("verify promotion candidate identity: %w", err)
	}
	if !strings.HasSuffix(reference, "@"+identity.PlatformManifestDigest) {
		return gate.ImageIdentity{}, errors.New("promotion candidate immutable reference drifted")
	}
	return identity, nil
}
