package localci

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
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
