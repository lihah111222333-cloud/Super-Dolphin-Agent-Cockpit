package localci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const buildxProvenanceType = "https://mobyproject.org/buildkit@v1"

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

var (
	// ErrTruthImageBootstrapTrustRoot reports that no signed accepted image exists.
	ErrTruthImageBootstrapTrustRoot = errors.New("truth image bootstrap trust root is not installed")
	// ErrTruthImageAwaitingTrustedRef keeps a built candidate non-runnable until trusted promotion.
	ErrTruthImageAwaitingTrustedRef = errors.New("truth image candidate is awaiting trusted ref promotion")
)

// TruthImageEnsureStatus 表示受信不可变镜像是否可以执行本次 job。
type TruthImageEnsureStatus string

const (
	TruthImageEnsureAccepted           TruthImageEnsureStatus = "accepted"
	TruthImageEnsureAwaitingTrustedRef TruthImageEnsureStatus = "awaiting_trusted_ref"
)

// AcceptedImageLoader 只暴露已验签 accepted image 的读取能力。
type AcceptedImageLoader interface {
	Load(context.Context) (gate.AcceptedImageRecord, error)
}

// CandidateImageBuilder 是现有候选镜像构建器的窄接口。
type CandidateImageBuilder interface {
	EnsureCandidate(context.Context, CandidateRequest) (CandidateResult, error)
}

// TruthImageEnsureRequest 将提交 job tree 绑定到规范镜像输入。
type TruthImageEnsureRequest struct {
	Tree         ReadOnlyGitTree
	PolicyDigest string
	Platform     string
}

// TruthImageEnsureResult 分离 job provenance 与镜像 build provenance；仅 accepted 状态携带 Image。
type TruthImageEnsureResult struct {
	Status                          TruthImageEnsureStatus
	SubmittedJobSourceTree          string
	AcceptedImageBuildSourceTree    string
	CandidateImageBuildSourceTree   string
	PolicyDigest                    string
	ImageSchemaVersion              string
	ImageInputDigest                string
	ContextDigest                   string
	InputManifestDigest             string
	ToolchainDigest                 string
	DockerfileDigest                string
	CandidatePlatformManifestDigest string
	Image                           gate.ImageIdentity
}

// TruthImageEnsurer 复用 accepted 镜像，或构建不可运行且不晋升状态的候选镜像。
type TruthImageEnsurer struct {
	accepted AcceptedImageLoader
	builder  CandidateImageBuilder
}

// NewTruthImageEnsurer 创建不持有签名能力且 fail-fast 的镜像 authority adapter。
func NewTruthImageEnsurer(accepted AcceptedImageLoader, builder CandidateImageBuilder) (*TruthImageEnsurer, error) {
	if interfaceValueIsNil(accepted) {
		return nil, errors.New("accepted image loader is required")
	}
	if interfaceValueIsNil(builder) {
		return nil, errors.New("candidate image builder is required")
	}
	return &TruthImageEnsurer{accepted: accepted, builder: builder}, nil
}

// EnsureImage 仅为已受信 accepted record 返回可运行镜像身份。
func (ensurer *TruthImageEnsurer) EnsureImage(ctx context.Context, request TruthImageEnsureRequest) (TruthImageEnsureResult, error) {
	if err := validateTruthImageEnsureCall(ensurer, ctx); err != nil {
		return TruthImageEnsureResult{}, err
	}
	accepted, err := loadAcceptedTruthImage(ctx, ensurer.accepted)
	if err != nil {
		return TruthImageEnsureResult{}, err
	}
	inputs, err := ResolveGateImageInputs(request.Tree, request.PolicyDigest, request.Platform)
	if err != nil {
		return TruthImageEnsureResult{}, err
	}
	if inputs.ImageInputDigest == accepted.ImageInputDigest && inputs.PolicyDigest == accepted.PolicyDigest {
		return acceptedTruthImageResult(inputs, accepted)
	}
	candidate, err := ensurer.builder.EnsureCandidate(ctx, candidateRequestFromInputs(inputs, accepted))
	if err != nil {
		return TruthImageEnsureResult{}, fmt.Errorf("ensure truth image candidate: %w", err)
	}
	if err := validateCandidateInputs(inputs, accepted, candidate); err != nil {
		return TruthImageEnsureResult{}, err
	}
	return truthImageResultFromCandidate(inputs, accepted, candidate)
}

// acceptedTruthImageResult 只从已验签 record 构造 runnable identity。
func acceptedTruthImageResult(
	inputs GateImageInputs,
	accepted gate.AcceptedImageRecord,
) (TruthImageEnsureResult, error) {
	candidate := candidateResultFromInputs(inputs, accepted.Image.PlatformManifestDigest)
	result := truthImageResult(inputs, accepted, candidate)
	result.Status = TruthImageEnsureAccepted
	result.Image = cloneImageIdentity(accepted.Image)
	if err := result.Validate(); err != nil {
		return TruthImageEnsureResult{}, err
	}
	return result, nil
}

// truthImageResultFromCandidate 保持 built candidate 非 runnable 并返回 awaiting 哨兵。
func truthImageResultFromCandidate(
	inputs GateImageInputs,
	accepted gate.AcceptedImageRecord,
	candidate CandidateResult,
) (TruthImageEnsureResult, error) {
	result := truthImageResult(inputs, accepted, candidate)
	if candidate.Built {
		result.Status = TruthImageEnsureAwaitingTrustedRef
		result.CandidateImageBuildSourceTree = candidate.SourceTreeSHA
		result.CandidatePlatformManifestDigest = candidate.ImageDigest
		if err := result.Validate(); err != nil {
			return TruthImageEnsureResult{}, err
		}
		return result, ErrTruthImageAwaitingTrustedRef
	}
	return acceptedTruthImageResult(inputs, accepted)
}

func candidateResultFromInputs(inputs GateImageInputs, imageDigest string) CandidateResult {
	return CandidateResult{
		SourceTreeSHA: inputs.SubmittedSourceTree, InputDigest: inputs.ImageInputDigest,
		ContextDigest: inputs.ContextDigest, InputManifestDigest: inputs.InputManifestDigest,
		ToolchainDigest: inputs.ToolchainDigest, DockerfileDigest: inputs.DockerfileDigest,
		ImageDigest: imageDigest,
	}
}

func validateTruthImageEnsureCall(ensurer *TruthImageEnsurer, ctx context.Context) error {
	if ensurer == nil || interfaceValueIsNil(ensurer.accepted) || interfaceValueIsNil(ensurer.builder) {
		return errors.New("truth image ensurer is not configured")
	}
	if ctx == nil {
		return errors.New("truth image ensure context is required")
	}
	return ctx.Err()
}

func loadAcceptedTruthImage(ctx context.Context, loader AcceptedImageLoader) (gate.AcceptedImageRecord, error) {
	accepted, err := loader.Load(ctx)
	if errors.Is(err, ErrAcceptedImageStateNotFound) {
		return gate.AcceptedImageRecord{}, ErrTruthImageBootstrapTrustRoot
	}
	if err != nil {
		return gate.AcceptedImageRecord{}, fmt.Errorf("load accepted truth image: %w", err)
	}
	return accepted, nil
}

// validateCandidateInputs 阻断 resolver 与 builder 之间的任何摘要或复用身份漂移。
func validateCandidateInputs(inputs GateImageInputs, accepted gate.AcceptedImageRecord, candidate CandidateResult) error {
	if candidate.SourceTreeSHA != inputs.SubmittedSourceTree || candidate.InputDigest != inputs.ImageInputDigest {
		return errors.New("candidate image source or input digest drifted from resolved Git inputs")
	}
	if candidate.ContextDigest != inputs.ContextDigest || candidate.InputManifestDigest != inputs.InputManifestDigest {
		return errors.New("candidate image context or manifest digest drifted from resolved Git inputs")
	}
	if candidate.ToolchainDigest != inputs.ToolchainDigest || candidate.DockerfileDigest != inputs.DockerfileDigest {
		return errors.New("candidate image toolchain or Dockerfile digest drifted from resolved Git inputs")
	}
	if !candidate.Built && candidate.ImageDigest != accepted.Image.PlatformManifestDigest {
		return errors.New("reused candidate image digest does not match accepted immutable identity")
	}
	return nil
}

func candidateRequestFromInputs(inputs GateImageInputs, accepted gate.AcceptedImageRecord) CandidateRequest {
	return CandidateRequest{
		SourceTreeSHA: inputs.SubmittedSourceTree, PolicyDigest: inputs.PolicyDigest,
		ImageSchemaVersion: inputs.ImageSchemaVersion, SourceEntries: cloneTreeEntries(inputs.SourceEntries),
		Platform: inputs.Platform, AcceptedInputDigest: accepted.ImageInputDigest,
		AcceptedPolicyDigest: accepted.PolicyDigest,
		AcceptedImageDigest:  accepted.Image.PlatformManifestDigest,
	}
}

func truthImageResult(inputs GateImageInputs, accepted gate.AcceptedImageRecord, candidate CandidateResult) TruthImageEnsureResult {
	return TruthImageEnsureResult{
		SubmittedJobSourceTree:       inputs.SubmittedSourceTree,
		AcceptedImageBuildSourceTree: accepted.SourceTree,
		PolicyDigest:                 inputs.PolicyDigest,
		ImageSchemaVersion:           inputs.ImageSchemaVersion,
		ImageInputDigest:             candidate.InputDigest, ContextDigest: candidate.ContextDigest,
		InputManifestDigest: candidate.InputManifestDigest, ToolchainDigest: candidate.ToolchainDigest,
		DockerfileDigest: candidate.DockerfileDigest,
	}
}

// Validate 在 coordinator 边界拒绝含糊的 runnable/awaiting 状态。
func (result TruthImageEnsureResult) Validate() error {
	if err := validateTruthImageResultIdentity(result); err != nil {
		return err
	}
	switch result.Status {
	case TruthImageEnsureAccepted:
		if result.CandidateImageBuildSourceTree != "" || result.CandidatePlatformManifestDigest != "" {
			return errors.New("accepted truth image result contains candidate authority")
		}
		return result.Image.Validate()
	case TruthImageEnsureAwaitingTrustedRef:
		if result.CandidateImageBuildSourceTree == "" || result.CandidatePlatformManifestDigest == "" {
			return errors.New("awaiting truth image result is missing candidate provenance")
		}
		if result.Image.Registry != "" {
			return errors.New("awaiting truth image result must not expose a runnable image")
		}
		return nil
	default:
		return fmt.Errorf("unsupported truth image ensure status %q", result.Status)
	}
}

func validateTruthImageResultIdentity(result TruthImageEnsureResult) error {
	values := []string{
		result.SubmittedJobSourceTree, result.AcceptedImageBuildSourceTree,
		result.PolicyDigest, result.ImageSchemaVersion,
	}
	if slices.Contains(values, "") {
		return errors.New("truth image result is missing job or accepted build provenance")
	}
	return nil
}

func cloneImageIdentity(identity gate.ImageIdentity) gate.ImageIdentity {
	identity.RootFSDiffIDs = append([]string(nil), identity.RootFSDiffIDs...)
	return identity
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
