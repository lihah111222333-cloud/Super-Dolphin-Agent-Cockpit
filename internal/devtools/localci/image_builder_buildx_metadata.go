package localci

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sourceexport"
)

const (
	buildxProvenanceType       = "https://mobyproject.org/buildkit@v1"
	buildxProvenanceBuilderID  = ""
	buildxImportedImageCreated = "1970-01-01T00:00:00Z"
	runtimeDepsManifestMedia   = "application/vnd.oci.image.manifest.v1+json"
	buildxNamedContextCaps     = "moby.buildkit.frontend.contexts+forward"
)

const runtimeDepsLockPath = "build/gate/runtime-deps.lock"

type toolchainLock struct {
	SchemaVersion      string             `json:"schema_version"`
	BuildKitVersion    string             `json:"buildkit_version"`
	BuildKitImage      string             `json:"buildkit_image"`
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
	if err := validateBuildKitVersion(lock.BuildKitVersion); err != nil {
		return fmt.Errorf("validate locked BuildKit version: %w", err)
	}
	if err := validateBuildKitImageReference(lock.BuildKitImage); err != nil {
		return fmt.Errorf("validate locked BuildKit image: %w", err)
	}
	if lock.DockerfileFrontend != "builtin:dockerfile.v1" {
		return errors.New("Dockerfile frontend must be the locked builtin:dockerfile.v1 frontend")
	}
	if err := validateSourceDateEpoch(lock.SourceDateEpoch); err != nil {
		return err
	}
	return nil
}

// loadRuntimeDepsLock 严格解码节点本地运行时依赖锁。
func loadRuntimeDepsLock(closure map[string]sourceexport.TreeEntry, platform string) (runtimeDepsLock, error) {
	entry, exists := closure[runtimeDepsLockPath]
	if !exists {
		return runtimeDepsLock{}, fmt.Errorf("candidate input closure is missing %s", runtimeDepsLockPath)
	}
	var lock runtimeDepsLock
	if err := decodeStrictJSON(entry.Data, &lock); err != nil {
		return runtimeDepsLock{}, fmt.Errorf("decode runtime dependencies lock: %w", err)
	}
	if err := validateRuntimeDepsLock(lock, platform, closure); err != nil {
		return runtimeDepsLock{}, err
	}
	return lock, nil
}

// validateRuntimeDepsLock 校验节点本地构建合同、目标平台和必需元数据。
func validateRuntimeDepsLock(lock runtimeDepsLock, platform string, closure map[string]sourceexport.TreeEntry) error {
	if err := validateRuntimeDepsLockHeader(lock); err != nil {
		return err
	}
	if !slices.Contains(runtimeDepsPlatforms, platform) {
		return fmt.Errorf("runtime dependencies target platform %q is unsupported", platform)
	}
	return validateRuntimeDepsClosure(lock, closure)
}

// validateRuntimeDepsLockHeader 约束节点本地 schema v11，不允许 registry 镜像输入。
func validateRuntimeDepsLockHeader(lock runtimeDepsLock) error {
	if lock.SchemaVersion != "12" {
		return fmt.Errorf("runtime dependencies lock schema version %q is unsupported", lock.SchemaVersion)
	}
	if lock.BuildMode != "node-local" || lock.CacheScope != "node" {
		return errors.New("runtime dependencies lock must use node-local build mode and node cache scope")
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
	MediaType   string            `json:"mediaType"`
	Digest      string            `json:"digest"`
	Size        int64             `json:"size"`
	Annotations map[string]string `json:"annotations"`
	Platform    buildxPlatform    `json:"platform"`
}

type buildxManifestAttachment struct {
	SchemaVersion int                               `json:"schemaVersion"`
	MediaType     string                            `json:"mediaType"`
	Config        buildxManifestContentDescriptor   `json:"config"`
	Layers        []buildxManifestContentDescriptor `json:"layers"`
}

type buildxManifestContentDescriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

type buildxProvenance struct {
	Builder     buildxProvenanceBuilder `json:"builder"`
	BuildType   string                  `json:"buildType"`
	Materials   []buildxMaterial        `json:"materials"`
	Invocation  buildxInvocation        `json:"invocation"`
	BuildConfig json.RawMessage         `json:"buildConfig"`
	Metadata    json.RawMessage         `json:"metadata"`
}

type buildxMaterial struct {
	URI    string               `json:"uri"`
	Digest buildxContextDigests `json:"digest"`
}

type buildxProvenanceBuilder struct {
	ID string `json:"id"`
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

// readRuntimeDepsBuildxDigest 读取并校验节点本地 runtime OCI layout 的单平台摘要。
func readRuntimeDepsBuildxDigest(metadataPath string, platformValue string) (string, error) {
	data, err := readBuildxMetadataFile(metadataPath)
	if err != nil {
		return "", fmt.Errorf("read runtime dependencies buildx metadata: %w", err)
	}
	var metadata buildxMetadata
	if err := decodeStrictJSON(data, &metadata); err != nil {
		return "", fmt.Errorf("decode runtime dependencies buildx metadata: %w", err)
	}
	descriptor := metadata.Descriptor
	if descriptor.MediaType != runtimeDepsManifestMedia {
		return "", errors.New("runtime dependencies result is not a single OCI platform manifest")
	}
	if err := validateDigest("runtime dependencies platform manifest digest", descriptor.Digest); err != nil {
		return "", err
	}
	if metadata.ImageDigest != descriptor.Digest {
		return "", errors.New("runtime dependencies metadata digest does not match its descriptor")
	}
	if descriptor.Size <= 0 {
		return "", errors.New("runtime dependencies platform manifest descriptor size is invalid")
	}
	expectedPlatform, err := parseBuildxPlatform(platformValue)
	if err != nil {
		return "", err
	}
	if descriptor.Platform != expectedPlatform {
		return "", errors.New("runtime dependencies platform manifest descriptor does not match the request")
	}
	return descriptor.Digest, nil
}

// validateBuildxMetadata 严格解码并复验 descriptor 与 provenance 的请求绑定。
func validateBuildxMetadata(data []byte, request BuildKitBuildRequest, builderName string, runtimeDepsDigest string) (buildxMetadata, error) {
	var metadata buildxMetadata
	if err := decodeStrictJSON(data, &metadata); err != nil {
		return buildxMetadata{}, fmt.Errorf("decode buildx metadata: %w", err)
	}
	if err := validateBuildxMetadataFields(metadata, builderName); err != nil {
		return buildxMetadata{}, err
	}
	var provenance buildxProvenance
	if err := decodeStrictJSON(metadata.Provenance, &provenance); err != nil {
		return buildxMetadata{}, fmt.Errorf("decode buildx provenance: %w", err)
	}
	if err := validateBuildxProvenance(provenance, request, runtimeDepsDigest); err != nil {
		return buildxMetadata{}, err
	}
	if err := validateBuildxDescriptor(metadata.Descriptor, request.Platform); err != nil {
		return buildxMetadata{}, err
	}
	if metadata.ImageDigest != metadata.Descriptor.Digest {
		return buildxMetadata{}, errors.New("buildx image digest does not match the platform manifest descriptor")
	}
	expectedTag, err := candidateImageTag(request.InputDigest)
	if err != nil {
		return buildxMetadata{}, err
	}
	if metadata.ImageName != expectedTag {
		return buildxMetadata{}, fmt.Errorf("buildx image name %q does not match fixed candidate tag %q", metadata.ImageName, expectedTag)
	}
	return metadata, nil
}

// validateBuildxMetadataFields 校验顶层必填字段。
func validateBuildxMetadataFields(metadata buildxMetadata, builderName string) error {
	if !rawJSONPresent(metadata.Provenance) {
		return errors.New("buildx metadata provenance is required")
	}
	if !rawJSONPresent(metadata.CacheManifest) {
		return errors.New("buildx cache manifest metadata is required")
	}
	if _, err := buildxHistoryRecordReference(metadata.BuildReference, builderName); err != nil {
		return err
	}
	if metadata.ConfigDigest != "" {
		if err := validateDigest("buildx metadata config digest", metadata.ConfigDigest); err != nil {
			return err
		}
	}
	if err := validateDigest("buildx metadata image digest", metadata.ImageDigest); err != nil {
		return err
	}
	if metadata.ImageName == "" {
		return errors.New("buildx metadata immutable image name is required")
	}
	return nil
}

// buildxHistoryRecordReference 校验受控 builder 绑定的规范 history record 引用。
func buildxHistoryRecordReference(buildReference string, builderName string) (string, error) {
	if strings.TrimSpace(buildReference) != buildReference {
		return "", errors.New("buildx metadata build reference is not canonical")
	}
	remainder, found := strings.CutPrefix(buildReference, builderName+"/")
	if !found {
		return "", errors.New("buildx metadata build reference is not bound to the controlled builder")
	}
	parts := strings.Split(remainder, "/")
	if len(parts) != 2 || !buildxHistoryNodePattern.MatchString(parts[0]) || !buildxHistoryRecordReferencePattern.MatchString(parts[1]) {
		return "", errors.New("buildx metadata build reference is not a canonical build record")
	}
	return parts[1], nil
}

// resolveBuildxConfigDigest 从同一受控 build record 的不可变 manifest attachment 解析并校验 config 摘要。
func (runner *DockerBuildxRunner) resolveBuildxConfigDigest(ctx context.Context, builderName string, metadata buildxMetadata) (string, error) {
	recordReference, err := buildxHistoryRecordReference(metadata.BuildReference, builderName)
	if err != nil {
		return "", err
	}
	output, err := runner.executor.Run(ctx, bytes.NewReader(nil),
		"buildx", "history", "inspect", "attachment", "--builder", builderName, recordReference, metadata.ImageDigest)
	if err != nil {
		return "", fmt.Errorf("inspect controlled buildx manifest attachment: %w", err)
	}
	configDigest, err := validateBuildxManifestAttachment([]byte(output), metadata.ImageDigest)
	if err != nil {
		return "", err
	}
	if metadata.ConfigDigest != "" && metadata.ConfigDigest != configDigest {
		return "", errors.New("buildx metadata config digest does not match the manifest attachment")
	}
	return configDigest, nil
}

// validateBuildxManifestAttachment 校验受控 build record 的 manifest attachment 并返回 config 摘要。
func validateBuildxManifestAttachment(data []byte, expectedManifestDigest string) (string, error) {
	if len(data) == 0 || len(data) > buildxMetadataLimit {
		return "", errors.New("buildx manifest attachment must be bounded and non-empty")
	}
	if buildxAttachmentDigest(data) != expectedManifestDigest {
		return "", errors.New("buildx manifest attachment content digest does not match the build result")
	}
	var manifest buildxManifestAttachment
	if err := decodeStrictJSON(data, &manifest); err != nil {
		return "", fmt.Errorf("decode buildx manifest attachment: %w", err)
	}
	if err := validateBuildxManifestEnvelope(manifest); err != nil {
		return "", err
	}
	if err := validateBuildxManifestLayers(manifest.Layers, manifest.Config.Digest); err != nil {
		return "", err
	}
	return manifest.Config.Digest, nil
}

// validateBuildxManifestEnvelope 校验 Docker manifest 格式及其 config descriptor。
func validateBuildxManifestEnvelope(manifest buildxManifestAttachment) error {
	if manifest.SchemaVersion != 2 || manifest.MediaType != buildxManifestMedia {
		return errors.New("buildx manifest attachment is not the controlled Docker manifest format")
	}
	if manifest.Config.MediaType != buildxConfigMedia || manifest.Config.Size <= 0 {
		return errors.New("buildx manifest attachment config descriptor is invalid")
	}
	return validateDigest("buildx manifest attachment config digest", manifest.Config.Digest)
}

// validateBuildxManifestLayers 校验非空 layers 及其与 config 摘要的隔离。
func validateBuildxManifestLayers(layers []buildxManifestContentDescriptor, configDigest string) error {
	if len(layers) == 0 {
		return errors.New("buildx manifest attachment layers are required")
	}
	for _, layer := range layers {
		if layer.MediaType != buildxLayerMedia || layer.Size <= 0 {
			return errors.New("buildx manifest attachment layer descriptor is invalid")
		}
		if err := validateDigest("buildx manifest attachment layer digest", layer.Digest); err != nil {
			return err
		}
		if layer.Digest == configDigest {
			return errors.New("buildx manifest attachment reuses the config digest as a layer")
		}
	}
	return nil
}

// buildxAttachmentDigest 计算 buildx attachment 内容的 sha256 摘要。
func buildxAttachmentDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// validateBuildxProvenance 校验 stdin context、平台和固定 frontend 参数。
func validateBuildxProvenance(provenance buildxProvenance, request BuildKitBuildRequest, runtimeDepsDigest string) error {
	if provenance.Builder.ID != buildxProvenanceBuilderID ||
		!rawJSONPresent(provenance.BuildConfig) || !rawJSONPresent(provenance.Metadata) {
		return errors.New("buildx provenance is missing required evidence")
	}
	if provenance.BuildType != buildxProvenanceType {
		return errors.New("buildx provenance build type is not supported")
	}
	if err := validateBuildxConfigSource(provenance.Invocation.ConfigSource, request); err != nil {
		return err
	}
	if err := validateBuildxMaterials(provenance.Materials, provenance.Invocation.ConfigSource, request, runtimeDepsDigest); err != nil {
		return err
	}
	if provenance.Invocation.Environment.Platform != request.Platform {
		return errors.New("buildx provenance platform does not match the request")
	}
	return validateBuildxParameters(provenance.Invocation.Parameters, request, runtimeDepsDigest)
}

// validateBuildxMaterials 校验 provenance material 集合严格闭合于受控构建输入。
func validateBuildxMaterials(materials []buildxMaterial, source buildxConfigSource, request BuildKitBuildRequest, runtimeDepsDigest string) error {
	expected, err := expectedBuildxMaterials(source, request, runtimeDepsDigest)
	if err != nil {
		return err
	}
	return validateBuildxMaterialClosure(materials, expected)
}

// expectedBuildxMaterials 从源码、运行时依赖和锁定基础镜像生成期望 material 集合。
func expectedBuildxMaterials(source buildxConfigSource, request BuildKitBuildRequest, runtimeDepsDigest string) (map[string]buildxContextDigests, error) {
	expected := map[string]buildxContextDigests{source.URI: source.Digest}
	runtimeMaterial, err := expectedRuntimeDepsBuildxMaterial(runtimeDepsDigest, request.Platform)
	if err != nil {
		return nil, err
	}
	expected[runtimeMaterial.URI] = runtimeMaterial.Digest
	for _, argument := range request.BuildArguments {
		if argument.Name == sourceDateEpochArgument || argument.Name == "RUNTIME_DEPS_IMAGE" {
			continue
		}
		material, err := expectedBuildxImageMaterial(argument.Value, request.Platform)
		if err != nil {
			return nil, err
		}
		if _, exists := expected[material.URI]; exists {
			return nil, errors.New("buildx request contains duplicate provenance materials")
		}
		expected[material.URI] = material.Digest
	}
	return expected, nil
}

// validateBuildxMaterialClosure 要求实际 material 与期望 URI 和摘要一一对应。
func validateBuildxMaterialClosure(materials []buildxMaterial, expected map[string]buildxContextDigests) error {
	if len(materials) != len(expected) {
		return fmt.Errorf(
			"buildx provenance material closure does not match the locked inputs: got %v, want %v",
			buildxMaterialURIs(materials),
			buildxExpectedMaterialURIs(expected),
		)
	}
	seen := make(map[string]struct{}, len(materials))
	for _, material := range materials {
		digest, exists := expected[material.URI]
		if !exists || material.Digest != digest {
			return fmt.Errorf(
				"buildx provenance contains an unlocked material: uri=%q digest=%+v expected=%+v exists=%t expected_uris=%v",
				material.URI,
				material.Digest,
				digest,
				exists,
				buildxExpectedMaterialURIs(expected),
			)
		}
		if _, duplicate := seen[material.URI]; duplicate {
			return errors.New("buildx provenance contains a duplicate material")
		}
		seen[material.URI] = struct{}{}
	}
	return nil
}

// buildxMaterialURIs 返回排序后的实际 material URI，供闭包错误稳定呈现。
func buildxMaterialURIs(materials []buildxMaterial) []string {
	uris := make([]string, 0, len(materials))
	for _, material := range materials {
		uris = append(uris, material.URI)
	}
	slices.Sort(uris)
	return uris
}

// buildxExpectedMaterialURIs 返回排序后的期望 material URI。
func buildxExpectedMaterialURIs(expected map[string]buildxContextDigests) []string {
	uris := make([]string, 0, len(expected))
	for uri := range expected {
		uris = append(uris, uri)
	}
	slices.Sort(uris)
	return uris
}

// expectedRuntimeDepsBuildxMaterial 绑定同一 builder 生成的私有 OCI layout 摘要和目标平台。
func expectedRuntimeDepsBuildxMaterial(digest string, platform string) (buildxMaterial, error) {
	if err := validateDigest("runtime dependencies platform manifest digest", digest); err != nil {
		return buildxMaterial{}, err
	}
	if _, err := parseBuildxPlatform(platform); err != nil {
		return buildxMaterial{}, err
	}
	return buildxMaterial{
		URI:    "pkg:oci/runtime-deps?digest=" + digest + "&platform=" + url.QueryEscape(platform),
		Digest: buildxContextDigests{SHA256: strings.TrimPrefix(digest, "sha256:")},
	}, nil
}

// expectedBuildxImageMaterial 将锁定镜像引用转换为对应的平台 provenance material。
func expectedBuildxImageMaterial(reference string, platform string) (buildxMaterial, error) {
	separator := strings.LastIndex(reference, "@")
	if separator <= 0 {
		return buildxMaterial{}, errors.New("buildx provenance image material reference is not immutable")
	}
	repository, digest := reference[:separator], reference[separator+1:]
	if err := validateDigest("buildx provenance image material digest", digest); err != nil {
		return buildxMaterial{}, err
	}
	return buildxMaterial{
		URI: "pkg:docker/" + repository + "?digest=" + digest + "&platform=" + url.QueryEscape(platform),
		Digest: buildxContextDigests{
			SHA256: strings.TrimPrefix(digest, "sha256:"),
		},
	}, nil
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

// validateBuildxParameters 校验 provenance 参数与受控命令完全一致且不含 secret 或 SSH 输入。
func validateBuildxParameters(parameters buildxParameters, request BuildKitBuildRequest, runtimeDepsDigest string) error {
	if parameters.Frontend != "dockerfile.v0" {
		return errors.New("buildx provenance frontend is not dockerfile.v0")
	}
	if len(parameters.Secrets) != 0 || len(parameters.SSH) != 0 {
		return errors.New("buildx provenance contains forbidden secret or SSH inputs")
	}
	runtimeContext, exists := parameters.Args["context:runtime-deps"]
	if !exists {
		return errors.New("buildx provenance is missing the runtime dependencies named context")
	}
	if err := validateRuntimeDepsBuildxContext(runtimeContext, runtimeDepsDigest); err != nil {
		return err
	}
	expected := expectedBuildxProvenanceArgs(request)
	expected["context:runtime-deps"] = runtimeContext
	expected["frontend.caps"] = buildxNamedContextCaps
	if !maps.Equal(parameters.Args, expected) {
		return errors.New("buildx provenance arguments do not match the locked command")
	}
	return nil
}

func validateRuntimeDepsBuildxContext(value string, digest string) error {
	if err := validateDigest("runtime dependencies named context digest", digest); err != nil {
		return err
	}
	const prefix = "oci-layout://"
	suffix := ":latest@" + digest
	if !strings.HasPrefix(value, prefix) || !strings.HasSuffix(value, suffix) {
		return errors.New("buildx runtime dependencies named context does not match its platform manifest digest")
	}
	reference := strings.TrimSuffix(strings.TrimPrefix(value, prefix), suffix)
	if !buildxHistoryRecordReferencePattern.MatchString(reference) {
		return errors.New("buildx runtime dependencies named context reference is not canonical")
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
	expectedAnnotations := map[string]string{"org.opencontainers.image.created": buildxImportedImageCreated}
	if !maps.Equal(descriptor.Annotations, expectedAnnotations) {
		return errors.New("buildx platform manifest descriptor annotations are not canonical")
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

// resolveDockerDescriptorConfigDigest 校验 Docker descriptor 的 config 摘要见证：接受直建摘要，
// 或受控 docker-container exporter 载入固定 candidate tag 产生的精确 containerd annotations。
func resolveDockerDescriptorConfigDigest(annotations map[string]string, repository string, inputDigest string, expectedConfigDigest string) (string, error) {
	if err := validateDigest("expected descriptor config digest", expectedConfigDigest); err != nil {
		return "", err
	}
	if configDigest, found := annotations["config.digest"]; found {
		if !maps.Equal(annotations, map[string]string{"config.digest": expectedConfigDigest}) || configDigest != expectedConfigDigest {
			return "", errors.New("Docker descriptor config annotation drifted")
		}
		return configDigest, nil
	}
	if repository != candidateImageRepository {
		return "", errors.New("Docker descriptor omitted config evidence for a non-candidate repository")
	}
	candidateTag, err := candidateImageTag(inputDigest)
	if err != nil {
		return "", err
	}
	tagName := strings.TrimPrefix(candidateTag, candidateImageRepository+":")
	expectedAnnotations := map[string]string{
		"io.containerd.image.name":          candidateTag,
		"org.opencontainers.image.created":  buildxImportedImageCreated,
		"org.opencontainers.image.ref.name": tagName,
	}
	if !maps.Equal(annotations, expectedAnnotations) {
		return "", errors.New("Docker descriptor containerd annotations are not bound to the controlled candidate tag")
	}
	return expectedConfigDigest, nil
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
	reference, err := candidateImageTag(result.InputDigest)
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
	return candidateIdentityFromInspect(document, candidate, result, configDigest, platform)
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
	if document.Config == nil || document.RootFS == nil {
		return "", buildxPlatform{}, errors.New("promotion candidate image inspect is incomplete")
	}
	configDigest := result.ConfigDigest
	if document.Descriptor != nil {
		if document.Descriptor.MediaType != buildxManifestMedia {
			return "", buildxPlatform{}, errors.New("promotion candidate image is not a Docker platform manifest")
		}
		var err error
		configDigest, err = resolveDockerDescriptorConfigDigest(
			document.Descriptor.Annotations,
			candidateImageRepository,
			result.InputDigest,
			result.ConfigDigest,
		)
		if err != nil {
			return "", buildxPlatform{}, fmt.Errorf("resolve promotion candidate config digest: %w", err)
		}
	}
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
	if document.Descriptor != nil && document.Descriptor.Digest != result.ImageDigest {
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
	return identity, nil
}
