package localci

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func validateBuildxPolicyBinding(request BuildKitBuildRequest) error {
	if err := validateDigest("buildx policy digest", request.PolicyDigest); err != nil {
		return err
	}
	if request.ImageSchemaVersion != imageInputSchemaVersion {
		return fmt.Errorf("buildx image schema version %q is unsupported", request.ImageSchemaVersion)
	}
	return nil
}

func validateBuildxDigests(request BuildKitBuildRequest) error {
	digests := []struct {
		name  string
		value string
	}{
		{name: "buildx context digest", value: request.ContextDigest},
		{name: "buildx input manifest digest", value: request.InputManifestDigest},
		{name: "buildx input digest", value: request.InputDigest},
		{name: "buildx toolchain digest", value: request.ToolchainDigest},
		{name: "buildx Dockerfile digest", value: request.DockerfileDigest},
		{name: "buildx cache namespace", value: request.CacheNamespace},
	}
	for _, digestValue := range digests {
		if err := validateDigest(digestValue.name, digestValue.value); err != nil {
			return err
		}
	}
	return nil
}

// validateBuildxArguments 要求工具链参数严格排序、唯一且值类型受锁约束。
func validateBuildxArguments(arguments []BuildArgument) error {
	if len(arguments) == 0 {
		return errors.New("buildx locked build arguments are required")
	}
	previous := ""
	foundSourceDateEpoch := false
	for _, argument := range arguments {
		isSourceDateEpoch, err := validateBuildxArgument(argument, previous)
		if err != nil {
			return err
		}
		foundSourceDateEpoch = foundSourceDateEpoch || isSourceDateEpoch
		previous = argument.Name
	}
	if !foundSourceDateEpoch {
		return errors.New("buildx SOURCE_DATE_EPOCH argument is required")
	}
	return nil
}

// validateBuildxArgument 校验单个受锁参数及其相对排序。
func validateBuildxArgument(argument BuildArgument, previous string) (bool, error) {
	if !validBuildxArgumentName(argument.Name) {
		return false, fmt.Errorf("buildx argument name %q is invalid", argument.Name)
	}
	if _, forbidden := forbiddenBuildArgumentNames[strings.ToUpper(argument.Name)]; forbidden {
		return false, fmt.Errorf("buildx proxy argument %q is forbidden", argument.Name)
	}
	if previous >= argument.Name {
		return false, errors.New("buildx arguments must be strictly sorted and unique")
	}
	switch argument.Name {
	case sourceDateEpochArgument:
		if err := validateSourceDateEpoch(argument.Value); err != nil {
			return false, err
		}
		return true, nil
	case "RUNTIME_DEPS_IMAGE":
		if argument.Value != "runtime-deps" && !immutableImageReference(argument.Value) {
			return false, errors.New("buildx RUNTIME_DEPS_IMAGE argument must name the controlled context or an immutable accepted image")
		}
		return false, nil
	default:
		if !immutableImageReference(argument.Value) {
			return false, fmt.Errorf("buildx argument %q must be an immutable image reference", argument.Name)
		}
		return false, nil
	}
}

// validateRuntimeDepsBuildRequest 校验运行时依赖构建只使用规范文件和完整锁定参数。
func validateRuntimeDepsBuildRequest(request BuildKitBuildRequest) error {
	if request.RuntimeDepsDockerfilePath != "build/gate/runtime-deps.Dockerfile" {
		return errors.New("runtime dependencies Dockerfile path is not canonical")
	}
	if err := validateDigest("runtime dependencies Dockerfile digest", request.RuntimeDepsDockerfileDigest); err != nil {
		return err
	}
	if err := validateDigest("runtime dependencies input digest", request.RuntimeDepsInputDigest); err != nil {
		return err
	}
	if len(request.RuntimeDepsBuildArguments) != 6 {
		return errors.New("runtime dependencies build arguments are incomplete")
	}
	return validateRuntimeDepsBuildArguments(request.RuntimeDepsBuildArguments)
}

// validateRuntimeDepsBuildArguments 校验参数集合完整、排序且各值符合锁类型。
func validateRuntimeDepsBuildArguments(arguments []BuildArgument) error {
	expectedNames := map[string]struct{}{
		"GO_IMAGE": {}, "NODE_IMAGE": {},
		"SQRUFF_ARCHIVE_SHA256_AMD64": {}, "SQRUFF_ARCHIVE_SHA256_ARM64": {},
		"SQRUFF_ARCHIVE_URL_AMD64": {}, "SQRUFF_ARCHIVE_URL_ARM64": {},
	}
	previous := ""
	for _, argument := range arguments {
		if argument.Name <= previous {
			return errors.New("runtime dependencies build arguments must be strictly sorted and unique")
		}
		if err := validateRuntimeDepsBuildArgument(argument); err != nil {
			return err
		}
		delete(expectedNames, argument.Name)
		previous = argument.Name
	}
	if len(expectedNames) != 0 {
		return errors.New("runtime dependencies build arguments are incomplete")
	}
	return nil
}

// validateRuntimeDepsBuildArgument 根据参数名称校验镜像、摘要或下载地址。
func validateRuntimeDepsBuildArgument(argument BuildArgument) error {
	switch argument.Name {
	case "GO_IMAGE", "NODE_IMAGE":
		if !immutableImageReference(argument.Value) {
			return fmt.Errorf("runtime dependencies argument %q must be an immutable image reference", argument.Name)
		}
	case "SQRUFF_ARCHIVE_SHA256_AMD64", "SQRUFF_ARCHIVE_SHA256_ARM64":
		if len(argument.Value) != sha256.Size*2 {
			return fmt.Errorf("runtime dependencies argument %q must be a SHA-256 hex digest", argument.Name)
		}
		decoded, err := hex.DecodeString(argument.Value)
		if err != nil || hex.EncodeToString(decoded) != argument.Value {
			return fmt.Errorf("runtime dependencies argument %q must be canonical SHA-256 hex", argument.Name)
		}
	case "SQRUFF_ARCHIVE_URL_AMD64", "SQRUFF_ARCHIVE_URL_ARM64":
		if !strings.HasPrefix(argument.Value, "https://github.com/quarylabs/sqruff/releases/download/") {
			return fmt.Errorf("runtime dependencies argument %q must be a locked Sqruff release URL", argument.Name)
		}
	default:
		return fmt.Errorf("runtime dependencies argument %q is not permitted", argument.Name)
	}
	return nil
}

// validateBuildxContextFileDigest 校验上下文中的目标文件与请求摘要完全一致。
func validateBuildxContextFileDigest(contextTar []byte, filePath string, expectedDigest string) error {
	reader := tar.NewReader(bytes.NewReader(contextTar))
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("buildx context is missing %q", filePath)
		}
		if err != nil {
			return fmt.Errorf("read buildx context tar: %w", err)
		}
		if header.Name != filePath {
			continue
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			return fmt.Errorf("read buildx context file %q: %w", filePath, err)
		}
		if bytesDigest(data) != expectedDigest {
			return fmt.Errorf("buildx context file %q digest does not match the request", filePath)
		}
		return nil
	}
}

const (
	dockerContextJSONFormat     = "{{json .}}"
	dockerContextNameCharacters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._+-"
	maxTLSMaterialBytes         = 1 << 20
)

// DockerDaemonIdentityCheckpoint 绑定 active context 观测和规范化 daemon identity。
type DockerDaemonIdentityCheckpoint struct {
	ContextName     string
	SchedulerConfig SchedulerConfig
	IdentityKey     string
}

type dockerDaemonIdentityProbe struct {
	runner     dockerRunner
	currentUID func() (int, error)
	lookupEnv  func(string) (string, bool)
	lstat      func(string) (os.FileInfo, error)
	readFile   func(string) ([]byte, error)
}

type dockerContextPayload struct {
	Name        string                   `json:"Name"`
	Metadata    json.RawMessage          `json:"Metadata"`
	Endpoints   dockerContextEndpoints   `json:"Endpoints"`
	TLSMaterial dockerContextTLSMaterial `json:"TLSMaterial"`
	Storage     dockerContextStorage     `json:"Storage"`
}

type dockerContextEndpoints struct {
	Docker dockerContextEndpoint `json:"docker"`
}

type dockerContextEndpoint struct {
	Host          string `json:"Host"`
	SkipTLSVerify bool   `json:"SkipTLSVerify"`
}

type dockerContextTLSMaterial struct {
	Docker []string `json:"docker"`
}

type dockerContextStorage struct {
	MetadataPath string `json:"MetadataPath"`
	TLSPath      string `json:"TLSPath"`
}

type dockerContextObservation struct {
	name           string
	endpoint       string
	tlsFingerprint string
}

type dockerEnvironmentValue struct {
	value string
	set   bool
}

type dockerCLIEnvironment struct {
	host      dockerEnvironmentValue
	context   dockerEnvironmentValue
	tls       dockerEnvironmentValue
	tlsVerify dockerEnvironmentValue
	certPath  dockerEnvironmentValue
}

func newDockerDaemonIdentityProbe(runner dockerRunner) (*dockerDaemonIdentityProbe, error) {
	if isNilDockerRunner(runner) {
		return nil, errors.New("docker daemon identity runner is nil")
	}
	return &dockerDaemonIdentityProbe{
		runner:     runner,
		currentUID: currentSchedulerOwnerUID,
		lookupEnv:  os.LookupEnv,
		lstat:      os.Lstat,
		readFile:   os.ReadFile,
	}, nil
}

// Probe 对 active context 和 daemon ID 做前后双读，拒绝观测期间的 identity 漂移。
func (probe *dockerDaemonIdentityProbe) Probe(ctx context.Context) (DockerDaemonIdentityCheckpoint, error) {
	if err := probe.validateInputs(ctx); err != nil {
		return DockerDaemonIdentityCheckpoint{}, err
	}
	environment := probe.environment()
	if err := validateDockerCLIEnvironment(environment); err != nil {
		return DockerDaemonIdentityCheckpoint{}, err
	}
	firstContext, err := probe.observeActiveContext(ctx, environment)
	if err != nil {
		return DockerDaemonIdentityCheckpoint{}, err
	}
	if err := validateSelectedDockerContext(environment, firstContext); err != nil {
		return DockerDaemonIdentityCheckpoint{}, err
	}
	firstDaemonID, err := probe.observeDaemonID(ctx, environment, firstContext.name)
	if err != nil {
		return DockerDaemonIdentityCheckpoint{}, err
	}
	namedContext, err := probe.observeNamedContext(ctx, environment, firstContext.name)
	if err != nil {
		return DockerDaemonIdentityCheckpoint{}, err
	}
	activeContext, err := probe.observeActiveContext(ctx, environment)
	if err != nil {
		return DockerDaemonIdentityCheckpoint{}, err
	}
	if err := validateDockerContextRecheck(firstContext, namedContext, activeContext); err != nil {
		return DockerDaemonIdentityCheckpoint{}, err
	}
	secondDaemonID, err := probe.observeDaemonID(ctx, environment, firstContext.name)
	if err != nil {
		return DockerDaemonIdentityCheckpoint{}, err
	}
	return probe.buildCheckpoint(firstContext, firstDaemonID, secondDaemonID)
}

// validateInputs 在执行 Docker CLI 前校验 probe 依赖和上下文。
func (probe *dockerDaemonIdentityProbe) validateInputs(ctx context.Context) error {
	if probe == nil {
		return errors.New("docker daemon identity probe is nil")
	}
	if ctx == nil {
		return errors.New("docker daemon identity context is nil")
	}
	if isNilDockerRunner(probe.runner) {
		return errors.New("docker daemon identity runner is nil")
	}
	if probe.currentUID == nil || probe.lookupEnv == nil || probe.lstat == nil || probe.readFile == nil {
		return errors.New("docker daemon identity probe dependencies are incomplete")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("probe docker daemon identity: %w", err)
	}
	return nil
}

func (probe *dockerDaemonIdentityProbe) observeActiveContext(
	ctx context.Context,
	environment dockerCLIEnvironment,
) (dockerContextObservation, error) {
	output, err := probe.runDocker(ctx, environment, "context", "inspect", "--format", dockerContextJSONFormat)
	if err != nil {
		return dockerContextObservation{}, fmt.Errorf("inspect active docker context: %w", err)
	}
	payload, err := decodeDockerContext(output)
	if err != nil {
		return dockerContextObservation{}, err
	}
	return probe.normalizeContext(payload)
}

func (probe *dockerDaemonIdentityProbe) observeNamedContext(
	ctx context.Context,
	environment dockerCLIEnvironment,
	contextName string,
) (dockerContextObservation, error) {
	output, err := probe.runDocker(ctx, environment, "context", "inspect", "--format", dockerContextJSONFormat, contextName)
	if err != nil {
		return dockerContextObservation{}, fmt.Errorf("inspect named docker context: %w", err)
	}
	payload, err := decodeDockerContext(output)
	if err != nil {
		return dockerContextObservation{}, err
	}
	return probe.normalizeContext(payload)
}

func (probe *dockerDaemonIdentityProbe) observeDaemonID(
	ctx context.Context,
	environment dockerCLIEnvironment,
	contextName string,
) (string, error) {
	output, err := probe.runDocker(ctx, environment, "--context", contextName, "info", "--format", dockerInfoJSONFormat)
	if err != nil {
		return "", fmt.Errorf("inspect active docker daemon: %w", err)
	}
	info, err := decodeDockerInfo(output)
	if err != nil {
		return "", err
	}
	if err := validateDaemonID(info.ID); err != nil {
		return "", fmt.Errorf("validate active docker daemon ID: %w", err)
	}
	return info.ID, nil
}

func (probe *dockerDaemonIdentityProbe) runDocker(
	ctx context.Context,
	environment dockerCLIEnvironment,
	args ...string,
) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	output, err := probe.runner.Run(ctx, args...)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if probe.environment() != environment {
		return "", errors.New("Docker CLI environment drifted during daemon identity probe")
	}
	return output, nil
}

// normalizeContext 将 context DTO 收敛为可比较的规范化 identity 观测。
func (probe *dockerDaemonIdentityProbe) normalizeContext(payload dockerContextPayload) (dockerContextObservation, error) {
	if err := validateDockerContextName(payload.Name); err != nil {
		return dockerContextObservation{}, err
	}
	parsed, err := url.Parse(payload.Endpoints.Docker.Host)
	if err != nil {
		return dockerContextObservation{}, fmt.Errorf("parse active docker context endpoint: %w", err)
	}
	fingerprint, err := probe.contextTLSFingerprint(parsed.Scheme, payload)
	if err != nil {
		return dockerContextObservation{}, err
	}
	endpoint, fingerprint, err := normalizeDaemonEndpoint(payload.Endpoints.Docker.Host, fingerprint)
	if err != nil {
		return dockerContextObservation{}, err
	}
	return dockerContextObservation{name: payload.Name, endpoint: endpoint, tlsFingerprint: fingerprint}, nil
}

// validateDockerContextName 将动态 context 参数限制为 Docker 的 ASCII canonical 名称。
func validateDockerContextName(name string) error {
	if name == "" || len(name) > 255 {
		return errors.New("docker context name is required and canonical")
	}
	for index := range len(name) {
		if strings.IndexByte(dockerContextNameCharacters, name[index]) < 0 {
			return errors.New("docker context name contains unsupported characters")
		}
	}
	return nil
}

// contextTLSFingerprint 按 endpoint scheme 强制 Unix 无 TLS、TCP 有可信 TLS 指纹。
func (probe *dockerDaemonIdentityProbe) contextTLSFingerprint(
	scheme string,
	payload dockerContextPayload,
) (string, error) {
	switch scheme {
	case "unix":
		if payload.Endpoints.Docker.SkipTLSVerify {
			return "", errors.New("unix docker context must not skip TLS verification")
		}
		if len(payload.TLSMaterial.Docker) != 0 {
			return "", errors.New("unix docker context must not declare TLS material")
		}
		return "", nil
	case "tcp":
		if payload.Endpoints.Docker.SkipTLSVerify {
			return "", errors.New("tcp docker context must enforce TLS verification")
		}
		return probe.fingerprintTLSMaterial(payload.Storage.TLSPath, payload.TLSMaterial.Docker)
	default:
		return "", nil
	}
}

// fingerprintTLSMaterial 对排序后的受信 TLS 文件做带边界 SHA-256。
func (probe *dockerDaemonIdentityProbe) fingerprintTLSMaterial(tlsPath string, materialNames []string) (string, error) {
	if len(materialNames) == 0 {
		return "", errors.New("tcp docker context requires TLS material")
	}
	if strings.TrimSpace(tlsPath) != tlsPath || !filepath.IsAbs(tlsPath) || filepath.Clean(tlsPath) != tlsPath {
		return "", errors.New("docker context TLS path must be canonical and absolute")
	}
	names := append([]string(nil), materialNames...)
	sort.Strings(names)
	digest := sha256.New()
	for index, name := range names {
		if err := validateTLSMaterialName(names, index); err != nil {
			return "", err
		}
		contents, err := probe.readTLSMaterial(tlsPath, name)
		if err != nil {
			return "", err
		}
		if err := writeTLSFingerprintField(digest, name, contents); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("sha256:%x", digest.Sum(nil)), nil
}

// validateTLSMaterialName 拒绝路径注入、空名称和重复材料条目。
func validateTLSMaterialName(sortedNames []string, index int) error {
	name := sortedNames[index]
	if strings.TrimSpace(name) == "" || strings.TrimSpace(name) != name ||
		strings.ContainsAny(name, "/\\\x00\r\n") || filepath.Base(name) != name || name == "." {
		return errors.New("docker context TLS material filename is invalid")
	}
	if index > 0 && sortedNames[index-1] == name {
		return fmt.Errorf("docker context TLS material filename %q is duplicated", name)
	}
	return nil
}

// readTLSMaterial 只读取 canonical TLS 根下的非链接常规文件。
func (probe *dockerDaemonIdentityProbe) readTLSMaterial(tlsPath, name string) ([]byte, error) {
	materialPath := filepath.Join(tlsPath, "docker", name)
	info, err := probe.lstat(materialPath)
	if err != nil {
		return nil, fmt.Errorf("inspect docker context TLS material %q: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("docker context TLS material %q must be a regular file", name)
	}
	if info.Size() <= 0 || info.Size() > maxTLSMaterialBytes {
		return nil, fmt.Errorf("docker context TLS material %q has invalid size", name)
	}
	contents, err := probe.readFile(materialPath)
	if err != nil {
		return nil, fmt.Errorf("read docker context TLS material %q: %w", name, err)
	}
	if int64(len(contents)) != info.Size() {
		return nil, fmt.Errorf("docker context TLS material %q changed while reading", name)
	}
	return contents, nil
}

func writeTLSFingerprintField(digest io.Writer, name string, contents []byte) error {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(name)))
	if _, err := digest.Write(length[:]); err != nil {
		return fmt.Errorf("hash docker context TLS material name length: %w", err)
	}
	if _, err := digest.Write([]byte(name)); err != nil {
		return fmt.Errorf("hash docker context TLS material name: %w", err)
	}
	binary.BigEndian.PutUint64(length[:], uint64(len(contents)))
	if _, err := digest.Write(length[:]); err != nil {
		return fmt.Errorf("hash docker context TLS material length: %w", err)
	}
	if _, err := digest.Write(contents); err != nil {
		return fmt.Errorf("hash docker context TLS material: %w", err)
	}
	return nil
}

func decodeDockerContext(output string) (dockerContextPayload, error) {
	decoder := json.NewDecoder(strings.NewReader(output))
	decoder.DisallowUnknownFields()
	var payload dockerContextPayload
	if err := decoder.Decode(&payload); err != nil {
		return dockerContextPayload{}, fmt.Errorf("decode active docker context JSON: %w", err)
	}
	var trailing json.RawMessage
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return payload, nil
	}
	if err == nil {
		return dockerContextPayload{}, errors.New("active docker context contains trailing JSON")
	}
	return dockerContextPayload{}, fmt.Errorf("active docker context contains trailing output: %w", err)
}

// buildCheckpoint 复核双读结果并映射为 SchedulerConfig 与稳定 key。
func (probe *dockerDaemonIdentityProbe) buildCheckpoint(
	contextObservation dockerContextObservation,
	firstDaemonID string,
	secondDaemonID string,
) (DockerDaemonIdentityCheckpoint, error) {
	if firstDaemonID != secondDaemonID {
		return DockerDaemonIdentityCheckpoint{}, errors.New("docker daemon identity mismatch during probe")
	}
	ownerUID, err := probe.currentUID()
	if err != nil {
		return DockerDaemonIdentityCheckpoint{}, fmt.Errorf("read current UID for docker daemon identity: %w", err)
	}
	identity, err := newDaemonIdentity(contextObservation.endpoint, contextObservation.tlsFingerprint, firstDaemonID, ownerUID)
	if err != nil {
		return DockerDaemonIdentityCheckpoint{}, fmt.Errorf("construct docker daemon identity: %w", err)
	}
	return DockerDaemonIdentityCheckpoint{
		ContextName: contextObservation.name,
		SchedulerConfig: SchedulerConfig{
			Endpoint:       identity.endpoint,
			TLSFingerprint: identity.tlsFingerprint,
			DaemonID:       identity.daemonID,
			OwnerUID:       identity.ownerUID,
		},
		IdentityKey: identity.key,
	}, nil
}

// validateSelectedDockerContext 复核显式 DOCKER_CONTEXT 与首次 active 观测一致。
func validateSelectedDockerContext(environment dockerCLIEnvironment, observation dockerContextObservation) error {
	if environment.context.set && environment.context.value != observation.name {
		return errors.New("DOCKER_CONTEXT does not match the inspected active context")
	}
	return nil
}

// validateDockerContextRecheck 同时复核命名 context 元数据和最终 active context。
func validateDockerContextRecheck(
	first dockerContextObservation,
	named dockerContextObservation,
	active dockerContextObservation,
) error {
	if first != named {
		return errors.New("named docker context metadata drifted during daemon identity probe")
	}
	if first != active {
		return errors.New("active docker context drifted during daemon identity probe")
	}
	return nil
}

func (probe *dockerDaemonIdentityProbe) environment() dockerCLIEnvironment {
	return dockerCLIEnvironment{
		host:      lookupDockerEnvironment(probe.lookupEnv, "DOCKER_HOST"),
		context:   lookupDockerEnvironment(probe.lookupEnv, "DOCKER_CONTEXT"),
		tls:       lookupDockerEnvironment(probe.lookupEnv, "DOCKER_TLS"),
		tlsVerify: lookupDockerEnvironment(probe.lookupEnv, "DOCKER_TLS_VERIFY"),
		certPath:  lookupDockerEnvironment(probe.lookupEnv, "DOCKER_CERT_PATH"),
	}
}

func lookupDockerEnvironment(lookup func(string) (string, bool), name string) dockerEnvironmentValue {
	value, set := lookup(name)
	return dockerEnvironmentValue{value: value, set: set}
}

// validateDockerCLIEnvironment 拒绝可绕过 active context metadata 的环境覆盖。
func validateDockerCLIEnvironment(environment dockerCLIEnvironment) error {
	if environment.host.set {
		return errors.New("DOCKER_HOST override is not allowed for active context identity probing")
	}
	if environment.tls.set || environment.tlsVerify.set || environment.certPath.set {
		return errors.New("Docker TLS environment overrides are not allowed for active context identity probing")
	}
	if environment.context.set && (strings.TrimSpace(environment.context.value) == "" ||
		strings.TrimSpace(environment.context.value) != environment.context.value) {
		return errors.New("DOCKER_CONTEXT must name one canonical active context")
	}
	return nil
}
