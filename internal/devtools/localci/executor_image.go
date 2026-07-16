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
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const (
	labelPolicySHA       = "org.super-dolphin.policy-sha"
	labelSourceTreeSHA   = "org.super-dolphin.source-tree-sha"
	labelInputDigest     = "org.super-dolphin.image-input-digest"
	labelToolchainDigest = "org.super-dolphin.toolchain-digest"
	labelSchemaVersion   = "org.super-dolphin.schema-version"
)

var sha256DigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var gitObjectPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// imageIdentityReader is the narrow caller contract for Task 1A's canonical ImageIdentity.
// 依赖 task-1a：canonical shared type 落地后补充适配器。
type imageIdentityReader interface {
	OCIIndexDigest() string
	PlatformManifestDigest() string
	ConfigDigest() string
	RootFSDiffIDs() []string
	OS() string
	Architecture() string
	Variant() string
}

type expectedImageMetadata struct {
	PolicyDigest    string
	SourceTreeSHA   string
	InputDigest     string
	ToolchainDigest string
	SchemaVersion   string
	OS              string
	Architecture    string
	Variant         string
}

func (expected expectedImageMetadata) labels() map[string]string {
	return map[string]string{
		labelPolicySHA:       expected.PolicyDigest,
		labelSourceTreeSHA:   expected.SourceTreeSHA,
		labelInputDigest:     expected.InputDigest,
		labelToolchainDigest: expected.ToolchainDigest,
		labelSchemaVersion:   expected.SchemaVersion,
	}
}

// validateImageIdentity 校验不可变 OCI 身份、平台和镜像标签闭包。
func validateImageIdentity(identity imageIdentityReader, labels map[string]string, expected expectedImageMetadata) error {
	if identity == nil {
		return errors.New("image identity is required")
	}
	if err := validateImageDescriptorDigests(identity); err != nil {
		return err
	}
	if err := validateExpectedImageMetadata(expected); err != nil {
		return err
	}
	if identity.OS() != expected.OS || identity.Architecture() != expected.Architecture || identity.Variant() != expected.Variant {
		return fmt.Errorf("image platform %s/%s/%s does not match expected %s/%s/%s", identity.OS(), identity.Architecture(), identity.Variant(), expected.OS, expected.Architecture, expected.Variant)
	}
	return validateImageLabels(labels, expected.labels())
}

// validateImageDescriptorDigests 校验 OCI descriptor 与 rootfs diff ID 闭包。
func validateImageDescriptorDigests(identity imageIdentityReader) error {
	for name, value := range map[string]string{
		"oci index digest":         identity.OCIIndexDigest(),
		"platform manifest digest": identity.PlatformManifestDigest(),
		"config digest":            identity.ConfigDigest(),
	} {
		if err := validateDigest(name, value); err != nil {
			return err
		}
	}
	diffIDs := identity.RootFSDiffIDs()
	if len(diffIDs) == 0 {
		return errors.New("image identity rootfs diff IDs are required")
	}
	for index, diffID := range diffIDs {
		if err := validateDigest(fmt.Sprintf("rootfs diff ID %d", index), diffID); err != nil {
			return err
		}
	}
	return nil
}

// validateExpectedImageMetadata 校验调用方提供的 Git 与输入真值。
func validateExpectedImageMetadata(expected expectedImageMetadata) error {
	if err := validateDigest("expected policy digest", expected.PolicyDigest); err != nil {
		return err
	}
	if !gitObjectPattern.MatchString(expected.SourceTreeSHA) {
		return errors.New("expected source tree must be a canonical Git object ID")
	}
	if err := validateDigest("expected image input digest", expected.InputDigest); err != nil {
		return err
	}
	if err := validateDigest("expected toolchain digest", expected.ToolchainDigest); err != nil {
		return err
	}
	if expected.SchemaVersion == "" || expected.OS == "" || expected.Architecture == "" {
		return errors.New("expected schema version and platform are required")
	}
	return nil
}

func validateImageLabels(labels map[string]string, expectedLabels map[string]string) error {
	for name, wanted := range expectedLabels {
		if wanted == "" {
			return fmt.Errorf("expected image label %s is empty", name)
		}
		if actual, exists := labels[name]; !exists || actual != wanted {
			return fmt.Errorf("image label %s = %q, want %q", name, actual, wanted)
		}
	}
	return nil
}

func validateDigest(name string, value string) error {
	if !sha256DigestPattern.MatchString(value) {
		return fmt.Errorf("%s must be an immutable sha256 digest", name)
	}
	return nil
}

type gateImageIdentityReader struct{ gate.ImageIdentity }

// OCIIndexDigest 返回请求绑定的 OCI index digest。
func (identity gateImageIdentityReader) OCIIndexDigest() string {
	return identity.ImageIdentity.OCIIndexDigest
}

// PlatformManifestDigest 返回执行平台 manifest digest。
func (identity gateImageIdentityReader) PlatformManifestDigest() string {
	return identity.ImageIdentity.PlatformManifestDigest
}

// ConfigDigest 返回镜像 config digest。
func (identity gateImageIdentityReader) ConfigDigest() string {
	return identity.ImageIdentity.ConfigDigest
}

// RootFSDiffIDs 返回完整 rootfs diff ID 链。
func (identity gateImageIdentityReader) RootFSDiffIDs() []string {
	return identity.ImageIdentity.RootFSDiffIDs
}

// OS 返回目标操作系统。
func (identity gateImageIdentityReader) OS() string { return identity.ImageIdentity.OS }

// Architecture 返回目标 CPU 架构。
func (identity gateImageIdentityReader) Architecture() string {
	return identity.ImageIdentity.Architecture
}

// Variant 返回目标平台变体。
func (identity gateImageIdentityReader) Variant() string { return identity.ImageIdentity.Variant }

type imageInspectDocument struct {
	ID           string   `json:"Id"`
	RepoDigests  []string `json:"RepoDigests"`
	OS           string   `json:"Os"`
	Architecture string   `json:"Architecture"`
	Variant      string   `json:"Variant"`
	Descriptor   *struct {
		Digest      string            `json:"digest"`
		MediaType   string            `json:"mediaType"`
		Size        int64             `json:"size"`
		Annotations map[string]string `json:"annotations"`
	} `json:"Descriptor"`
	Config *struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	RootFS *struct {
		Type   string   `json:"Type"`
		Layers []string `json:"Layers"`
	} `json:"RootFS"`
}

// inspectAndVerifyImage 解析固定 Docker image inspect JSON 并复验全部执行身份。
func (runner *FreshContainerRunner) inspectAndVerifyImage(ctx context.Context, prepared preparedFreshContainerRequest) (string, error) {
	output, err := runner.docker.runner.Run(ctx, "image", "inspect", prepared.imageReference)
	if err != nil {
		return "", fmt.Errorf("inspect gate image: %w", err)
	}
	var document imageInspectDocument
	if err := decodeSingleInspect(output, &document); err != nil {
		return "", fmt.Errorf("decode gate image inspect: %w", err)
	}
	if document.Config == nil || document.RootFS == nil {
		return "", errors.New("gate image inspect omitted config or rootfs")
	}
	manifestDigest, configDigest, err := validateImageInspectDescriptor(document, prepared.expectedIdentity)
	if err != nil {
		return "", err
	}
	if !slices.Equal(document.RootFS.Layers, prepared.expectedIdentity.RootFSDiffIDs) {
		return "", errors.New("gate image inspect rootfs diff IDs drifted")
	}
	identity := gateImageIdentityReader{ImageIdentity: gate.ImageIdentity{
		OCIIndexDigest: preparedIdentityIndex(prepared), PlatformManifestDigest: manifestDigest,
		ConfigDigest: configDigest, RootFSDiffIDs: document.RootFS.Layers,
		OS: document.OS, Architecture: document.Architecture, Variant: document.Variant,
	}}
	if document.RootFS.Type != "layers" {
		return "", fmt.Errorf("gate image rootfs type %q is not layers", document.RootFS.Type)
	}
	if !slices.Contains(document.RepoDigests, prepared.imageReference) {
		return "", errors.New("gate image inspect does not contain the requested platform manifest reference")
	}
	if err := validateImageIdentity(identity, document.Config.Labels, prepared.expectedImage); err != nil {
		return "", fmt.Errorf("verify gate image inspect: %w", err)
	}
	return digestJSON(document)
}

// validateImageInspectDescriptor 从 Docker descriptor 读取 manifest/config 身份，绝不把展示 ID 当作 config。
func validateImageInspectDescriptor(document imageInspectDocument, expected gate.ImageIdentity) (string, string, error) {
	if document.Descriptor == nil || document.Descriptor.MediaType == "" || document.Descriptor.Size <= 0 {
		return "", "", errors.New("gate image inspect omitted a complete descriptor")
	}
	manifestDigest := document.Descriptor.Digest
	configDigest := document.Descriptor.Annotations["config.digest"]
	if err := validateDigest("inspected platform manifest digest", manifestDigest); err != nil {
		return "", "", err
	}
	if err := validateDigest("inspected config digest", configDigest); err != nil {
		return "", "", err
	}
	if manifestDigest != expected.PlatformManifestDigest {
		return "", "", errors.New("gate image inspect platform manifest digest drifted")
	}
	if configDigest != expected.ConfigDigest {
		return "", "", errors.New("gate image inspect config digest drifted")
	}
	if document.ID != manifestDigest && document.ID != configDigest {
		return "", "", errors.New("gate image inspect ID matches neither manifest nor config identity")
	}
	return manifestDigest, configDigest, nil
}

func preparedIdentityIndex(prepared preparedFreshContainerRequest) string {
	return prepared.expectedImageIndex
}

type containerInspectDocument struct {
	ID     string   `json:"Id"`
	Image  string   `json:"Image"`
	Path   string   `json:"Path"`
	Args   []string `json:"Args"`
	Config *struct {
		Image  string            `json:"Image"`
		User   string            `json:"User"`
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	HostConfig *containerHostConfig `json:"HostConfig"`
	Mounts     []struct {
		Type        string `json:"Type"`
		Source      string `json:"Source"`
		Destination string `json:"Destination"`
		RW          bool   `json:"RW"`
	} `json:"Mounts"`
	State *struct {
		Status     string `json:"Status"`
		Running    bool   `json:"Running"`
		ExitCode   int    `json:"ExitCode"`
		OOMKilled  bool   `json:"OOMKilled"`
		Error      string `json:"Error"`
		FinishedAt string `json:"FinishedAt"`
	} `json:"State"`
}

type containerHostConfig struct {
	NanoCPUs       int64             `json:"NanoCpus"`
	Memory         int64             `json:"Memory"`
	PidsLimit      int64             `json:"PidsLimit"`
	ReadonlyRootfs bool              `json:"ReadonlyRootfs"`
	CapDrop        []string          `json:"CapDrop"`
	SecurityOpt    []string          `json:"SecurityOpt"`
	NetworkMode    string            `json:"NetworkMode"`
	StorageOpt     map[string]string `json:"StorageOpt"`
	Tmpfs          map[string]string `json:"Tmpfs"`
	LogConfig      struct {
		Type   string            `json:"Type"`
		Config map[string]string `json:"Config"`
	} `json:"LogConfig"`
}

type canonicalHostConfig struct {
	ImageReference  string
	ConfigDigest    string
	Command         []string
	User            string
	NanoCPUs        int64
	Memory          int64
	PidsLimit       int64
	ReadonlyRootfs  bool
	CapDrop         []string
	NoNewPrivileges bool
	SeccompDigest   string
	NetworkMode     string
	StorageSize     string
	Source          string
	TmpfsOptions    []string
	LogDriver       string
	LogOptions      map[string]string
}

// inspectCreatedContainer 在启动前复验容器身份、命令、资源和隔离配置。
func (runner *FreshContainerRunner) inspectCreatedContainer(ctx context.Context, containerID string, imageReference string, configDigest string, sourceDirectory string, command []string, labels map[string]string) (string, string, error) {
	document, err := runner.inspectContainer(ctx, containerID)
	if err != nil {
		return "", "", err
	}
	canonical, err := runner.validateContainerContract(document, containerID, imageReference, configDigest, sourceDirectory, command)
	if err != nil {
		return "", "", err
	}
	if err := validateExpectedContainerLabels(document, labels); err != nil {
		return "", "", err
	}
	if document.State == nil || document.State.Status != "created" || document.State.Running {
		return "", "", errors.New("new gate container is not in created state")
	}
	hostDigest, err := digestJSON(canonical)
	if err != nil {
		return "", "", fmt.Errorf("digest gate host config: %w", err)
	}
	inspectDigest, err := digestJSON(document)
	if err != nil {
		return "", "", fmt.Errorf("digest created gate container inspect: %w", err)
	}
	return hostDigest, inspectDigest, nil
}

// validateContainerContract 校验 inspect 观测值并生成 host config 摘要输入。
func (runner *FreshContainerRunner) validateContainerContract(document containerInspectDocument, containerID string, imageReference string, configDigest string, sourceDirectory string, command []string) (canonicalHostConfig, error) {
	var canonical canonicalHostConfig
	if err := validateContainerIdentity(document, containerID, imageReference, configDigest, command); err != nil {
		return canonical, err
	}
	host := document.HostConfig
	if err := validateContainerHostIsolation(host); err != nil {
		return canonical, err
	}
	if err := validateContainerMount(document, sourceDirectory); err != nil {
		return canonical, err
	}
	tmpfs := splitOptionSet(host.Tmpfs["/tmp"])
	if err := validateContainerTmpfsAndLogs(host, tmpfs); err != nil {
		return canonical, err
	}
	canonical = canonicalHostConfig{
		ImageReference: imageReference, ConfigDigest: configDigest, Command: append([]string(nil), command...), User: document.Config.User,
		NanoCPUs: host.NanoCPUs, Memory: host.Memory, PidsLimit: host.PidsLimit, ReadonlyRootfs: host.ReadonlyRootfs,
		CapDrop: sortedStrings(host.CapDrop), NoNewPrivileges: true, SeccompDigest: runner.docker.seccompDigest,
		NetworkMode: host.NetworkMode, StorageSize: host.StorageOpt["size"], Source: sourceDirectory,
		TmpfsOptions: sortedStrings(tmpfs), LogDriver: host.LogConfig.Type, LogOptions: host.LogConfig.Config,
	}
	return canonical, nil
}

// validateContainerIdentity 校验容器、镜像、用户与固定命令身份。
func validateContainerIdentity(document containerInspectDocument, containerID string, imageReference string, configDigest string, command []string) error {
	if document.ID != containerID || document.Config == nil || document.HostConfig == nil {
		return errors.New("gate container inspect identity or config is incomplete")
	}
	if !containerImageIdentityMatches(document.Image, imageReference, configDigest) {
		return errors.New("gate container inspect image matches neither manifest nor config identity")
	}
	if document.Config.Image != imageReference || document.Config.User != "65532:65532" {
		return errors.New("gate container inspect image or user drifted")
	}
	if len(command) == 0 || document.Path != command[0] || !slices.Equal(document.Args, command[1:]) {
		return errors.New("gate container inspect command drifted")
	}
	return nil
}

func validateExpectedContainerLabels(document containerInspectDocument, labels map[string]string) error {
	if document.Config == nil {
		return errors.New("gate container config is missing")
	}
	for key, expected := range labels {
		if document.Config.Labels[key] != expected {
			return fmt.Errorf("gate container label %q drifted", key)
		}
	}
	return nil
}

// validateContainerHostIsolation 校验资源、安全和 network none 合同。
func validateContainerHostIsolation(host *containerHostConfig) error {
	if host.NanoCPUs != 4_000_000_000 || host.Memory != 8*1024*1024*1024 || host.PidsLimit != 512 || !host.ReadonlyRootfs {
		return errors.New("gate container resource or readonly contract drifted")
	}
	if !equalStringSet(host.CapDrop, []string{"ALL"}) || host.NetworkMode != noContainerNetwork || host.StorageOpt["size"] != "10G" {
		return errors.New("gate container capability, network, or storage contract drifted")
	}
	if !slices.Contains(host.SecurityOpt, "no-new-privileges") || !containsSeccomp(host.SecurityOpt) {
		return errors.New("gate container security options drifted")
	}
	return nil
}

// validateContainerMount 校验唯一只读源码 bind mount。
func validateContainerMount(document containerInspectDocument, sourceDirectory string) error {
	if len(document.Mounts) != 1 {
		return errors.New("gate container must have exactly one source mount")
	}
	mount := document.Mounts[0]
	if mount.Type != "bind" || mount.Source != sourceDirectory || mount.Destination != "/workspace/source" || mount.RW {
		return errors.New("gate container source mount contract drifted")
	}
	return nil
}

func validateContainerTmpfsAndLogs(host *containerHostConfig, tmpfs []string) error {
	if !equalStringSet(tmpfs, []string{"rw", "noexec", "nosuid", "nodev", "size=2147483648"}) {
		return errors.New("gate container tmpfs contract drifted")
	}
	if host.LogConfig.Type != "local" || host.LogConfig.Config["max-size"] != "10m" || host.LogConfig.Config["max-file"] != "3" {
		return errors.New("gate container log contract drifted")
	}
	return nil
}

// inspectFinishedContainer 复验终态容器身份、退出码和完成时间。
func (runner *FreshContainerRunner) inspectFinishedContainer(parentContext context.Context, containerID string, imageReference string, configDigest string, exitCode int) (string, error) {
	output, err := runner.runCleanup(parentContext, "inspect", "--type=container", containerID)
	if err != nil {
		return "", fmt.Errorf("inspect finished gate container: %w", err)
	}
	var document containerInspectDocument
	if err := decodeSingleInspect(output, &document); err != nil {
		return "", fmt.Errorf("decode finished gate container inspect: %w", err)
	}
	if err := validateFinishedContainerIdentity(document, containerID, imageReference, configDigest); err != nil {
		return "", err
	}
	if err := validateFinishedContainerState(document, exitCode); err != nil {
		return "", err
	}
	return digestJSON(document)
}

// validateFinishedContainerIdentity 校验终态容器仍绑定原始镜像身份。
func validateFinishedContainerIdentity(document containerInspectDocument, containerID string, imageReference string, configDigest string) error {
	if document.ID != containerID || document.Config == nil || document.Config.Image != imageReference || document.State == nil {
		return errors.New("finished gate container identity drifted")
	}
	if !containerImageIdentityMatches(document.Image, imageReference, configDigest) {
		return errors.New("finished gate container image matches neither manifest nor config identity")
	}
	return nil
}

// containerImageIdentityMatches 兼容 Docker inspect 展示 manifest 或 config，但不混淆二者语义。
func containerImageIdentityMatches(observed string, imageReference string, configDigest string) bool {
	separator := strings.LastIndex(imageReference, "@")
	if separator <= 0 || separator == len(imageReference)-1 {
		return false
	}
	manifestDigest := imageReference[separator+1:]
	return observed == manifestDigest || observed == configDigest
}

// validateFinishedContainerState 校验退出状态、退出码和完成时间。
func validateFinishedContainerState(document containerInspectDocument, exitCode int) error {
	state := document.State
	if state.Status != "exited" || state.Running || state.ExitCode != exitCode {
		return errors.New("finished gate container exit state drifted")
	}
	if state.OOMKilled || state.Error != "" || state.FinishedAt == "" {
		return errors.New("finished gate container terminal evidence drifted")
	}
	return nil
}

func (runner *FreshContainerRunner) inspectContainer(ctx context.Context, containerID string) (containerInspectDocument, error) {
	output, err := runner.docker.runner.Run(ctx, "inspect", "--type=container", containerID)
	if err != nil {
		return containerInspectDocument{}, fmt.Errorf("inspect created gate container: %w", err)
	}
	var document containerInspectDocument
	if err := decodeSingleInspect(output, &document); err != nil {
		return containerInspectDocument{}, fmt.Errorf("decode created gate container inspect: %w", err)
	}
	return document, nil
}

func decodeSingleInspect(output string, target any) error {
	var documents []json.RawMessage
	decoder := json.NewDecoder(strings.NewReader(output))
	if err := decoder.Decode(&documents); err != nil {
		return err
	}
	if len(documents) != 1 {
		return fmt.Errorf("Docker inspect returned %d documents, want 1", len(documents))
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("Docker inspect returned trailing JSON")
		}
		return fmt.Errorf("decode trailing Docker inspect JSON: %w", err)
	}
	return json.Unmarshal(documents[0], target)
}

func containsSeccomp(options []string) bool {
	for _, option := range options {
		if strings.HasPrefix(option, "seccomp=") && len(option) > len("seccomp=") {
			return true
		}
	}
	return false
}

func splitOptionSet(options string) []string {
	if options == "" {
		return nil
	}
	return strings.Split(options, ",")
}

func equalStringSet(left []string, right []string) bool {
	return slices.Equal(sortedStrings(left), sortedStrings(right))
}

func sortedStrings(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}

func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func digestJSON(value any) (string, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	return digestBytes(buffer.Bytes()), nil
}
