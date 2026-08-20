package remoteci

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"reflect"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

const (
	BuilderSimulationContractID        = "remote-ci-engine-builder-simulation/v1"
	BuilderSimulationRequestSchema     = "remote-ci-builder-simulation-request/v1"
	BuilderSimulationProtocolABIDomain = "remote-ci-builder-simulation-protocol-abi/v1"
	builderSimulationRequestMaxBytes   = 1 << 20
)

// BuilderSimulationRequest 是非权威 engine artifact 演练的完整显式输入。
type BuilderSimulationRequest struct {
	SchemaVersion     string                    `json:"schema_version"`
	ContractID        string                    `json:"contract_id"`
	OperationID       string                    `json:"operation_id"`
	ExecutionProvider string                    `json:"execution_provider"`
	Engine            BuilderSimulationEngine   `json:"engine"`
	Target            BuilderSimulationTarget   `json:"target"`
	Artifact          BuilderSimulationArtifact `json:"artifact"`
	ECI               BuilderSimulationECI      `json:"eci"`
	OSS               BuilderSimulationOSS      `json:"oss"`
}

// BuilderSimulationEngine 绑定独立引擎源码与 expected runtime identity。
type BuilderSimulationEngine struct {
	RepositoryRoot          string `json:"repository_root"`
	RepositoryID            string `json:"repository_id"`
	ObjectFormat            string `json:"object_format"`
	CommitSHA               string `json:"commit_sha"`
	TreeSHA                 string `json:"tree_sha"`
	ExpectedBuilderDigest   string `json:"expected_builder_digest"`
	ExpectedToolchainDigest string `json:"expected_toolchain_digest"`
	ProtocolABIDigest       string `json:"protocol_abi_digest"`
}

// BuilderSimulationTarget 绑定目标源码与 exact target-tree workload manifest。
type BuilderSimulationTarget struct {
	RepositoryRoot   string                   `json:"repository_root"`
	RepositoryID     string                   `json:"repository_id"`
	ObjectFormat     string                   `json:"object_format"`
	CommitSHA        string                   `json:"commit_sha"`
	TreeSHA          string                   `json:"tree_sha"`
	WorkloadManifest PortableWorkloadManifest `json:"workload_manifest"`
}

// BuilderSimulationArtifact 绑定 caller-owned OSS object namespace。
type BuilderSimulationArtifact struct {
	Prefix string `json:"prefix"`
}

// BuilderSimulationECI 绑定唯一 aliyun-eci/v1 builder 的显式云身份。
type BuilderSimulationECI struct {
	AliyunCLI         string                  `json:"aliyun_cli"`
	CredentialProfile string                  `json:"credential_profile"`
	RegionID          string                  `json:"region_id"`
	VSwitches         []cicontract.ECIVSwitch `json:"vswitches"`
	SecurityGroupID   string                  `json:"security_group_id"`
	RAMRoleName       string                  `json:"ram_role_name"`
	ImageCacheID      string                  `json:"image_cache_id"`
	ImageSnapshotID   string                  `json:"image_snapshot_id"`
	Image             string                  `json:"image"`
}

// BuilderSimulationOSS 绑定 host staging 与 ECI worker 各自使用的 endpoint。
type BuilderSimulationOSS struct {
	Bucket           string `json:"bucket"`
	Endpoint         string `json:"endpoint"`
	InternalEndpoint string `json:"internal_endpoint"`
}

// Validate 在任何 repository、OSS 或 ECI I/O 前校验 request 的完整 wire 身份。
func (request BuilderSimulationRequest) Validate() error {
	if request.SchemaVersion != BuilderSimulationRequestSchema || request.ContractID != BuilderSimulationContractID || request.ExecutionProvider != cicontract.ExecutionProviderID {
		return errors.New("builder simulation request header is invalid")
	}
	if !validBuilderSimulationToken(request.OperationID, 128) {
		return errors.New("builder simulation operation_id is invalid")
	}
	protocolABI, err := BuilderSimulationProtocolABIDigest()
	if err != nil || request.Engine.ProtocolABIDigest != protocolABI {
		return errors.New("builder simulation protocol ABI identity is invalid")
	}
	for _, validate := range []func() error{
		func() error { return validateBuilderSimulationEngine(request.Engine) },
		func() error { return validateBuilderSimulationTarget(request.Target) },
		func() error { return validateBuilderSimulationArtifact(request.Artifact) },
		func() error { return validateBuilderSimulationECI(request.ECI) },
		func() error { return validateBuilderSimulationOSS(request.OSS) },
	} {
		if err := validate(); err != nil {
			return err
		}
	}
	return nil
}

// CanonicalEncode 生成严格、bounded、无默认字段的 request JSON。
func (request BuilderSimulationRequest) CanonicalEncode() ([]byte, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(request)
	if err != nil {
		return nil, errors.New("encode builder simulation request")
	}
	if len(data) == 0 || len(data) > builderSimulationRequestMaxBytes {
		return nil, errors.New("builder simulation request exceeds bounded size")
	}
	return data, nil
}

// DecodeBuilderSimulationRequest 严格拒绝 duplicate、unknown、null、trailing 与非 canonical JSON。
func DecodeBuilderSimulationRequest(data []byte) (BuilderSimulationRequest, error) {
	if len(data) == 0 || len(data) > builderSimulationRequestMaxBytes || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return BuilderSimulationRequest{}, errors.New("builder simulation request JSON size or root is invalid")
	}
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return BuilderSimulationRequest{}, errors.New("builder simulation request JSON has duplicate fields")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var request BuilderSimulationRequest
	if err := decoder.Decode(&request); err != nil {
		return BuilderSimulationRequest{}, errors.New("decode builder simulation request")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return BuilderSimulationRequest{}, errors.New("builder simulation request JSON has trailing data")
	}
	canonical, err := request.CanonicalEncode()
	if err != nil || !bytes.Equal(canonical, data) {
		return BuilderSimulationRequest{}, errors.New("builder simulation request JSON is not canonical")
	}
	return request, nil
}

func validateBuilderSimulationEngine(engine BuilderSimulationEngine) error {
	if err := validateBuilderSimulationRepository("engine", engine.RepositoryRoot, engine.RepositoryID, engine.ObjectFormat, engine.CommitSHA, engine.TreeSHA); err != nil {
		return err
	}
	if !validPortableDigest(engine.ExpectedBuilderDigest) || !validPortableDigest(engine.ExpectedToolchainDigest) {
		return errors.New("builder simulation expected builder/toolchain digest is invalid")
	}
	return nil
}

func validateBuilderSimulationTarget(target BuilderSimulationTarget) error {
	if err := validateBuilderSimulationRepository("target", target.RepositoryRoot, target.RepositoryID, target.ObjectFormat, target.CommitSHA, target.TreeSHA); err != nil {
		return err
	}
	if target.WorkloadManifest.RepositoryID != target.RepositoryID {
		return errors.New("builder simulation target manifest repository identity drifted")
	}
	return validatePortableWorkloadManifestShape(target.WorkloadManifest)
}

func validateBuilderSimulationRepository(role, root, repositoryID, objectFormat, commitSHA, treeSHA string) error {
	if err := validatePortableAbsolutePath(role+".repository_root", root); err != nil {
		return err
	}
	if !validBuilderSimulationToken(repositoryID, 128) || !validBuilderSimulationObjectIdentity(objectFormat, commitSHA, treeSHA) {
		return fmt.Errorf("builder simulation %s repository identity is invalid", role)
	}
	return nil
}

// validBuilderSimulationObjectIdentity 校验 object format 与 exact commit/tree 长度一致。
func validBuilderSimulationObjectIdentity(objectFormat, commitSHA, treeSHA string) bool {
	wantLength := 0
	switch objectFormat {
	case "sha1":
		wantLength = 40
	case "sha256":
		wantLength = 64
	default:
		return false
	}
	return len(commitSHA) == wantLength && len(treeSHA) == wantLength && validPortableOID(commitSHA) && validPortableOID(treeSHA)
}

func validateBuilderSimulationArtifact(artifact BuilderSimulationArtifact) error {
	return validateBuilderArtifactPrefix(artifact.Prefix)
}

// validateBuilderSimulationECI 校验 caller 显式提供的唯一 ECI provider 身份。
func validateBuilderSimulationECI(config BuilderSimulationECI) error {
	if err := validatePortableAbsolutePath("eci.aliyun_cli", config.AliyunCLI); err != nil {
		return err
	}
	for _, value := range []string{config.CredentialProfile, config.RegionID, config.SecurityGroupID, config.RAMRoleName, config.ImageCacheID, config.ImageSnapshotID} {
		if !validBuilderSimulationToken(value, 256) {
			return errors.New("builder simulation ECI token is invalid")
		}
	}
	if err := cicontract.ValidateECIMultiZoneScheduling(cicontract.ECIMultiZoneScheduleStrategy, config.VSwitches); err != nil {
		return errors.New("builder simulation ECI vSwitch identity is invalid")
	}
	if !validBuilderSimulationImage(config.Image) {
		return errors.New("builder simulation ECI image is not immutable")
	}
	return nil
}

// validateBuilderSimulationOSS 校验 public/internal endpoint 与 bucket 的显式身份。
func validateBuilderSimulationOSS(config BuilderSimulationOSS) error {
	if !validBuilderSimulationToken(config.Bucket, 128) || strings.ContainsAny(config.Bucket, "/\\") {
		return errors.New("builder simulation OSS bucket is invalid")
	}
	if !validBuilderSimulationEndpoint(config.Endpoint) || !validBuilderSimulationEndpoint(config.InternalEndpoint) || config.Endpoint == config.InternalEndpoint {
		return errors.New("builder simulation OSS endpoints are invalid")
	}
	return nil
}

// validBuilderSimulationEndpoint 只接受无 user、query、fragment 与 path 的 HTTPS endpoint。
func validBuilderSimulationEndpoint(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" && parsed.Path == ""
}

func validBuilderSimulationImage(value string) bool {
	image, digest, found := strings.Cut(value, "@")
	return found && validBuilderSimulationToken(image, 512) && !strings.Contains(image, "@") && validPortableDigest(digest)
}

func validBuilderSimulationToken(value string, maxBytes int) bool {
	return len(value) > 0 && len(value) <= maxBytes && strings.TrimSpace(value) == value && !containsControl(value)
}

// BuilderSimulationProtocolABIDigest 动态派生 request wire DTO 与枚举的 ABI 摘要。
func BuilderSimulationProtocolABIDigest() (string, error) {
	material, err := builderSimulationProtocolABIMaterial()
	if err != nil {
		return "", err
	}
	hasher := sha256.New()
	writeRepositoryFrame(hasher, []byte(BuilderSimulationProtocolABIDomain))
	writeRepositoryFrame(hasher, material)
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil)), nil
}

func builderSimulationProtocolABIMaterial() ([]byte, error) {
	rootTypes := []reflect.Type{
		reflect.TypeFor[BuilderSimulationRequest](), reflect.TypeFor[BuilderSimulationEngine](),
		reflect.TypeFor[BuilderSimulationTarget](), reflect.TypeFor[BuilderSimulationArtifact](),
		reflect.TypeFor[BuilderSimulationECI](), reflect.TypeFor[BuilderSimulationOSS](),
		reflect.TypeFor[PortableWorkloadManifest](), reflect.TypeFor[cicontract.ECIVSwitch](),
	}
	var material bytes.Buffer
	for _, value := range []string{
		BuilderSimulationContractID, BuilderSimulationRequestSchema, cicontract.ExecutionProviderID,
		PortableWorkloadManifestSchemaVersion, cicontract.ECIMultiZoneScheduleStrategy, "sha1", "sha256",
	} {
		writeRepositoryFrame(&material, []byte(value))
	}
	for _, root := range rootTypes {
		if err := appendBuilderSimulationABIType(&material, root); err != nil {
			return nil, err
		}
	}
	return material.Bytes(), nil
}

// appendBuilderSimulationABIType 动态登记一个 public wire struct 的字段顺序与类型。
func appendBuilderSimulationABIType(material *bytes.Buffer, root reflect.Type) error {
	if root.Kind() != reflect.Struct || root.NumField() == 0 {
		return errors.New("builder simulation ABI root must be a non-empty struct")
	}
	writeRepositoryFrame(material, []byte(root.PkgPath()+"."+root.Name()))
	writeRepositoryFrame(material, []byte(fmt.Sprintf("%d", root.NumField())))
	seenJSONNames := make(map[string]struct{}, root.NumField())
	for field := range root.Fields() {
		values, err := builderSimulationABIFieldValues(field, seenJSONNames)
		if err != nil {
			return err
		}
		for _, value := range values {
			writeRepositoryFrame(material, []byte(value))
		}
	}
	return nil
}

// builderSimulationABIFieldValues 校验并投影一个 public JSON wire 字段。
func builderSimulationABIFieldValues(field reflect.StructField, seen map[string]struct{}) ([4]string, error) {
	if field.PkgPath != "" || field.Anonymous {
		return [4]string{}, errors.New("builder simulation ABI contains unsupported field visibility")
	}
	jsonName, _, hasOptions := strings.Cut(field.Tag.Get("json"), ",")
	if jsonName == "" || jsonName == "-" || hasOptions {
		return [4]string{}, errors.New("builder simulation ABI contains unsupported JSON tag")
	}
	if _, duplicate := seen[jsonName]; duplicate {
		return [4]string{}, errors.New("builder simulation ABI contains duplicate JSON field")
	}
	seen[jsonName] = struct{}{}
	if !validBuilderSimulationABIType(field.Type) {
		return [4]string{}, errors.New("builder simulation ABI contains unsupported wire type")
	}
	return [4]string{field.Name, jsonName, field.Type.String(), field.Type.Kind().String()}, nil
}

// validBuilderSimulationABIType 将 request ABI 限定为 string、nested struct 与 struct slice。
func validBuilderSimulationABIType(value reflect.Type) bool {
	if value.Kind() == reflect.String || value.Kind() == reflect.Struct {
		return true
	}
	return value.Kind() == reflect.Slice && value.Elem().Kind() == reflect.Struct
}
