package eci

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

var artifactBuilderOSSBucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)

func validArtifactBuilderOSSBucket(value string) bool {
	return artifactBuilderOSSBucketPattern.MatchString(value)
}

// validateArtifactBuilderOSSIdentity 校验 OSS bucket 与 internal endpoint 的非 secret 绑定。
func validateArtifactBuilderOSSIdentity(bucket, endpoint string) error {
	if !validArtifactBuilderOSSBucket(bucket) {
		return errors.New("artifact builder OSS bucket is required and must be a canonical bucket name")
	}
	return validateArtifactBuilderOSSEndpoint(endpoint)
}

// validateArtifactBuilderOSSEndpoint 只接受无 userinfo/query 的 HTTPS 根地址。
func validateArtifactBuilderOSSEndpoint(value string) error {
	if value == "" || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n\t") {
		return errors.New("artifact builder OSS endpoint must be a strict HTTPS root")
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return errors.New("artifact builder OSS endpoint must be a strict HTTPS root without query or userinfo")
	}
	if err := validateArtifactBuilderOSSEndpointURL(parsed); err != nil {
		return err
	}
	return nil
}

// validateArtifactBuilderOSSEndpointURL 校验已解析 URL 的 HTTPS root 约束。
func validateArtifactBuilderOSSEndpointURL(parsed *url.URL) error {
	if parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return errors.New("artifact builder OSS endpoint must be a strict HTTPS root without query or userinfo")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return errors.New("artifact builder OSS endpoint must be a strict HTTPS root without query or userinfo")
	}
	if !artifactBuilderOSSEndpointRootPath(parsed) || parsed.Hostname() == "" || parsed.Port() != "" {
		return errors.New("artifact builder OSS endpoint must use an HTTPS root host without a port")
	}
	return nil
}

func artifactBuilderOSSEndpointRootPath(parsed *url.URL) bool {
	return (parsed.Path == "" || parsed.Path == "/") && parsed.RawPath == ""
}

// artifactBuilderCommandPartsMatch 兼容 ECI 将 binary 与 subcommand 分栏回读的形态。
func artifactBuilderCommandPartsMatch(want, command, args []string) bool {
	if slices.Equal(command, want) {
		return true
	}
	return len(command) == 1 && len(want) == 2 && command[0] == want[0] && len(args) > 0 && args[0] == want[1]
}

// validateArtifactBuilderLiveResources 校验真实资源与已请求资源及选中 vSwitch 的绑定。
func validateArtifactBuilderLiveResources(request ArtifactBuilderRequest, group ArtifactBuilderGroup) error {
	if group.CPU != request.CPU || group.MemoryGiB != request.MemoryGiB {
		return fmt.Errorf("%w: artifact builder live resources do not match the bound request", ErrArtifactBuilderIdentityDrift)
	}
	if group.EphemeralStorageGiB != 0 && group.EphemeralStorageGiB != request.EphemeralStorageGiB {
		return fmt.Errorf("%w: artifact builder live ephemeral storage does not match the bound request", ErrArtifactBuilderIdentityDrift)
	}
	if len(group.VSwitches) == 0 {
		return fmt.Errorf("%w: artifact builder live vSwitch bindings are missing", ErrArtifactBuilderIdentityDrift)
	}
	for _, live := range group.VSwitches {
		if !artifactBuilderLiveVSwitchMatches(request.VSwitches, live) {
			return fmt.Errorf("%w: artifact builder live vSwitch bindings do not match the bound request", ErrArtifactBuilderIdentityDrift)
		}
	}
	return nil
}

func artifactBuilderLiveVSwitchMatches(requested []cicontract.ECIVSwitch, live cicontract.ECIVSwitch) bool {
	for _, candidate := range requested {
		if live.ID == candidate.ID && (live.ZoneID == "" || live.ZoneID == candidate.ZoneID) {
			return true
		}
	}
	return false
}

// validateArtifactBuilderLiveInfrastructure 校验 provider 返回的网络、角色与命令绑定字段。
func validateArtifactBuilderLiveInfrastructure(request ArtifactBuilderRequest, group ArtifactBuilderGroup) error {
	if group.ScheduleStrategy != "" && group.ScheduleStrategy != request.ScheduleStrategy {
		return fmt.Errorf("%w: artifact builder live schedule strategy does not match the bound request", ErrArtifactBuilderIdentityDrift)
	}
	if group.SecurityGroupID != request.SecurityGroupID || group.RAMRoleName != request.RAMRoleName {
		return fmt.Errorf("%w: artifact builder live security or RAM identity does not match the bound request", ErrArtifactBuilderIdentityDrift)
	}
	if group.OSSBucket != "" && group.OSSBucket != request.OSSBucket {
		return fmt.Errorf("%w: artifact builder live OSS bucket does not match the bound request", ErrArtifactBuilderIdentityDrift)
	}
	if group.OSSInternalEndpoint != "" && group.OSSInternalEndpoint != request.OSSInternalEndpoint {
		return fmt.Errorf("%w: artifact builder live OSS endpoint does not match the bound request", ErrArtifactBuilderIdentityDrift)
	}
	return nil
}

type artifactBuilderParsedCommand struct {
	provider     string
	compilerPath string
	input        ArtifactBuilderInput
	bucket       string
	endpoint     string
	role         string
}

// mapArtifactBuilderGroup 将 Describe 的真实字段投影为 builder live group。
func mapArtifactBuilderGroup(live ContainerGroup) (ArtifactBuilderGroup, error) {
	if err := validateArtifactBuilderLiveShape(live); err != nil {
		return ArtifactBuilderGroup{}, err
	}
	container := live.Containers[0]
	parsed, err := parseArtifactBuilderContainerCommand(container.Command, container.Args)
	if err != nil {
		return ArtifactBuilderGroup{}, err
	}
	if parsed.role != live.RAMRoleName {
		return ArtifactBuilderGroup{}, fmt.Errorf("%w: live RAM role does not match command identity", ErrArtifactBuilderIdentityDrift)
	}
	tags, err := artifactBuilderTagsFromLive(live.Tags)
	if err != nil {
		return ArtifactBuilderGroup{}, err
	}
	vSwitches := artifactBuilderLiveVSwitches(live.VSwitchID)
	if len(vSwitches) == 0 {
		return ArtifactBuilderGroup{}, fmt.Errorf("%w: live vSwitch identity is missing", ErrArtifactBuilderIdentityDrift)
	}
	return ArtifactBuilderGroup{
		ID: live.ID, Name: live.Name, Provider: parsed.provider, RegionID: live.RegionID,
		Image: container.Image, ImageSnapshotID: live.ImageSnapshotID, CompilerPath: parsed.compilerPath, Command: artifactBuilderGroupCommand(container.Command, container.Args),
		Input: parsed.input, EphemeralStorageGiB: live.EphemeralStorageGiB, CPU: live.CPU, MemoryGiB: live.MemoryGiB,
		VSwitches: vSwitches, SecurityGroupID: live.SecurityGroupID, RAMRoleName: live.RAMRoleName,
		OSSBucket: parsed.bucket, OSSInternalEndpoint: parsed.endpoint, Tags: tags, Status: live.Status,
		Container: ArtifactBuilderContainer{
			Name: container.Name, Image: container.Image, Command: append([]string(nil), container.Command...),
			Args: append([]string(nil), container.Args...), State: container.CurrentState.State,
			ExitCode: container.CurrentState.ExitCode, StartTime: container.CurrentState.StartTime, FinishTime: container.CurrentState.FinishTime,
		},
	}, nil
}

func artifactBuilderGroupCommand(command, args []string) []string {
	result := append([]string(nil), command...)
	if len(command) == 1 && len(args) > 0 && args[0] == ArtifactBuilderSubcommand {
		result = append(result, ArtifactBuilderSubcommand)
	}
	return result
}

// validateArtifactBuilderLiveShape 校验 Describe 返回的 builder group 必填结构。
func validateArtifactBuilderLiveShape(live ContainerGroup) error {
	if err := validateArtifactBuilderLiveGroupFields(live); err != nil {
		return err
	}
	if len(live.Containers) != 1 || len(live.InitContainers) != 0 {
		return fmt.Errorf("%w: builder group must contain exactly one main container and no init container", ErrArtifactBuilderIdentityDrift)
	}
	container := live.Containers[0]
	if strings.TrimSpace(container.Name) == "" || strings.TrimSpace(container.Image) == "" || len(container.Args) == 0 {
		return fmt.Errorf("%w: DescribeContainerGroups omitted builder container command identity", ErrArtifactBuilderIdentityDrift)
	}
	return nil
}

// validateArtifactBuilderLiveGroupFields 校验 group 的真实基础设施字段。
func validateArtifactBuilderLiveGroupFields(live ContainerGroup) error {
	if strings.TrimSpace(live.ID) == "" || strings.TrimSpace(live.Name) == "" || strings.TrimSpace(live.RegionID) == "" || strings.TrimSpace(live.Status) == "" {
		return fmt.Errorf("%w: DescribeContainerGroups omitted builder group identity fields", ErrArtifactBuilderIdentityDrift)
	}
	if strings.TrimSpace(live.VSwitchID) == "" || strings.TrimSpace(live.SecurityGroupID) == "" || strings.TrimSpace(live.RAMRoleName) == "" {
		return fmt.Errorf("%w: DescribeContainerGroups omitted builder infrastructure fields", ErrArtifactBuilderIdentityDrift)
	}
	return nil
}

// parseArtifactBuilderContainerCommand 严格解析 live 容器中的 builder argv。
func parseArtifactBuilderContainerCommand(command, args []string) (artifactBuilderParsedCommand, error) {
	if err := validateArtifactBuilderContainerCommandShape(command, args); err != nil {
		return artifactBuilderParsedCommand{}, err
	}
	values, err := parseArtifactBuilderFlags(args[1:])
	if err != nil {
		return artifactBuilderParsedCommand{}, err
	}
	if err := validateArtifactBuilderParsedFlags(values); err != nil {
		return artifactBuilderParsedCommand{}, err
	}
	if err := validateArtifactBuilderCompilerPath(values["--compiler-path"]); err != nil {
		return artifactBuilderParsedCommand{}, fmt.Errorf("%w: live compiler path is invalid", ErrArtifactBuilderIdentityDrift)
	}
	size, err := strconv.ParseInt(values["--input-size-bytes"], 10, 64)
	if err != nil {
		return artifactBuilderParsedCommand{}, fmt.Errorf("%w: live builder input size is invalid", ErrArtifactBuilderIdentityDrift)
	}
	// 从 live argv 逐字段重建 expected 与 ABI 输入，拒绝 caller 伪造 observed 身份。
	input := ArtifactBuilderInput{ArtifactPrefix: values["--artifact-prefix"], Key: values["--input-key"], SHA256: values["--input-sha256"], SizeBytes: size, RepositoryID: values["--repository-id"], ObjectFormat: values["--object-format"], SourceCommitSHA: values["--source-commit-sha"], SourceTreeSHA: values["--source-tree-sha"], ExpectedBuilderDigest: values["--expected-builder-digest"], ExpectedToolchainDigest: values["--expected-toolchain-digest"], ProtocolABIDigest: values["--protocol-abi-digest"], WorkloadManifestABIDigest: values["--workload-manifest-abi-digest"]}
	if err := input.Validate(); err != nil {
		return artifactBuilderParsedCommand{}, fmt.Errorf("%w: live builder input is invalid: %v", ErrArtifactBuilderIdentityDrift, err)
	}
	if err := validateArtifactBuilderOSSIdentity(values["--oss-bucket"], values["--oss-endpoint"]); err != nil || !artifactBuilderTokenPattern.MatchString(values["--ram-role-name"]) {
		return artifactBuilderParsedCommand{}, fmt.Errorf("%w: live builder OSS or RAM identity is invalid", ErrArtifactBuilderIdentityDrift)
	}
	return artifactBuilderParsedCommand{provider: values["--execution-provider"], compilerPath: values["--compiler-path"], input: input, bucket: values["--oss-bucket"], endpoint: values["--oss-endpoint"], role: values["--ram-role-name"]}, nil
}

// validateArtifactBuilderContainerCommandShape 校验 provider 可选 command 与必需完整 args 的实时形状。
func validateArtifactBuilderContainerCommandShape(command, args []string) error {
	if len(command) > 0 && (len(command) != 1 || command[0] != ArtifactBuilderCommand) {
		return fmt.Errorf("%w: live builder command is missing or drifted", ErrArtifactBuilderIdentityDrift)
	}
	if len(args) < 2 || args[0] != ArtifactBuilderSubcommand {
		return fmt.Errorf("%w: live builder subcommand is missing or drifted", ErrArtifactBuilderIdentityDrift)
	}
	return nil
}

// parseArtifactBuilderFlags 解析并拒绝未知、重复或不成对的 live argv。
func parseArtifactBuilderFlags(args []string) (map[string]string, error) {
	allowed := map[string]struct{}{}
	for _, key := range []string{"--compiler-path", "--execution-provider", "--artifact-kind", "--artifact-ref", "--artifact-prefix", "--input-key", "--input-sha256", "--input-size-bytes", "--repository-id", "--object-format", "--source-commit-sha", "--source-tree-sha", "--expected-builder-digest", "--expected-toolchain-digest", "--protocol-abi-digest", "--workload-manifest-abi-digest", "--oss-bucket", "--oss-endpoint", "--ram-role-name"} {
		allowed[key] = struct{}{}
	}
	values := make(map[string]string)
	seen := make(map[string]struct{}, len(allowed))
	for index := 0; index < len(args); index += 2 {
		if index+1 >= len(args) || !strings.HasPrefix(args[index], "--") {
			return nil, fmt.Errorf("%w: live builder command arguments are malformed", ErrArtifactBuilderIdentityDrift)
		}
		if _, ok := allowed[args[index]]; !ok {
			return nil, fmt.Errorf("%w: live builder command contains an unsupported flag", ErrArtifactBuilderIdentityDrift)
		}
		if _, duplicate := seen[args[index]]; duplicate {
			return nil, fmt.Errorf("%w: live builder command contains a duplicate flag", ErrArtifactBuilderIdentityDrift)
		}
		seen[args[index]] = struct{}{}
		values[args[index]] = args[index+1]
	}
	return values, nil
}

// validateArtifactBuilderParsedFlags 校验 live argv 的固定协议字段集合。
func validateArtifactBuilderParsedFlags(values map[string]string) error {
	required := []string{"--compiler-path", "--execution-provider", "--artifact-kind", "--artifact-ref", "--artifact-prefix", "--input-key", "--input-sha256", "--input-size-bytes", "--repository-id", "--object-format", "--source-commit-sha", "--source-tree-sha", "--expected-builder-digest", "--expected-toolchain-digest", "--protocol-abi-digest", "--workload-manifest-abi-digest", "--oss-bucket", "--oss-endpoint", "--ram-role-name"}
	for _, key := range required {
		if strings.TrimSpace(values[key]) == "" {
			return fmt.Errorf("%w: live builder command omitted %s", ErrArtifactBuilderIdentityDrift, key)
		}
	}
	if values["--execution-provider"] != ArtifactBuilderProvider || values["--artifact-kind"] != ArtifactBuilderArtifactKind || values["--artifact-ref"] != ArtifactBuilderArtifactRef {
		return fmt.Errorf("%w: live builder artifact protocol drifted", ErrArtifactBuilderIdentityDrift)
	}
	return nil
}

func artifactBuilderTagsFromLive(tags []ContainerGroupTag) (map[string]string, error) {
	result := make(map[string]string, len(tags))
	for _, tag := range tags {
		if _, exists := result[tag.Key]; exists {
			return nil, fmt.Errorf("%w: live builder tags contain duplicate keys", ErrArtifactBuilderIdentityDrift)
		}
		result[tag.Key] = tag.Value
	}
	return result, nil
}

func artifactBuilderLiveVSwitches(value string) []cicontract.ECIVSwitch {
	ids := strings.Split(value, ",")
	result := make([]cicontract.ECIVSwitch, 0, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			result = append(result, cicontract.ECIVSwitch{ID: id})
		}
	}
	return result
}

// validateArtifactBuilderGroupIdentity 校验实时 group 与固定 request 的不可变身份一致。
func validateArtifactBuilderGroupIdentity(request ArtifactBuilderRequest, group ArtifactBuilderGroup) error {
	if !artifactBuilderTokenPattern.MatchString(group.ID) {
		return errors.New("artifact builder live group ID is invalid")
	}
	if err := validateArtifactBuilderLiveStrings(request, group); err != nil {
		return err
	}
	if err := validateArtifactBuilderLiveCommand(request, group); err != nil {
		return err
	}
	if err := validateArtifactBuilderLiveInput(request, group); err != nil {
		return err
	}
	if err := validateArtifactBuilderLiveResources(request, group); err != nil {
		return err
	}
	if err := validateArtifactBuilderLiveInfrastructure(request, group); err != nil {
		return err
	}
	return validateArtifactBuilderLiveTags(request, group)
}

func validateArtifactBuilderLiveStrings(request ArtifactBuilderRequest, group ArtifactBuilderGroup) error {
	identity := []struct {
		name string
		got  string
		want string
	}{
		{"group name", group.Name, request.ContainerGroupName},
		{"provider", group.Provider, request.Provider},
		{"region", group.RegionID, request.RegionID},
		{"image", group.Image, request.Image},
		{"compiler path", group.CompilerPath, request.CompilerPath},
		{"container name", group.Container.Name, request.ContainerName},
		{"container image", group.Container.Image, request.Image},
	}
	for _, field := range identity {
		if field.got != field.want {
			return fmt.Errorf("%w: artifact builder live %s identity does not match the bound request", ErrArtifactBuilderIdentityDrift, field.name)
		}
	}
	if group.ImageSnapshotID != "" && group.ImageSnapshotID != request.ImageSnapshotID {
		return fmt.Errorf("%w: artifact builder live image snapshot identity does not match the bound request", ErrArtifactBuilderIdentityDrift)
	}
	return nil
}

// validateArtifactBuilderLiveCommand 严格核对 group 或容器回读的固定 builder 命令。
func validateArtifactBuilderLiveCommand(request ArtifactBuilderRequest, group ArtifactBuilderGroup) error {
	if len(group.Command) > 0 && !artifactBuilderCommandPartsMatch(request.Command, group.Command, nil) {
		return fmt.Errorf("%w: artifact builder live group command does not match the bound request", ErrArtifactBuilderIdentityDrift)
	}
	if len(group.Container.Command) > 0 && !artifactBuilderCommandPartsMatch(request.Command, group.Container.Command, group.Container.Args) {
		return fmt.Errorf("%w: artifact builder live container command does not match the bound request", ErrArtifactBuilderIdentityDrift)
	}
	return nil
}

func validateArtifactBuilderLiveInput(request ArtifactBuilderRequest, group ArtifactBuilderGroup) error {
	if group.Input == (ArtifactBuilderInput{}) {
		return fmt.Errorf("%w: artifact builder live typed input is missing", ErrArtifactBuilderIdentityDrift)
	}
	return compareArtifactBuilderInput(request.Input, group.Input)
}

// compareArtifactBuilderInput 逐项核对 live typed input，确保摘要、大小及 source commit/tree 均绑定请求。
func compareArtifactBuilderInput(want, got ArtifactBuilderInput) error {
	if !artifactBuilderInputMaterialMatches(want, got) || !artifactBuilderInputGitIdentityMatches(want, got) || !artifactBuilderInputDigestMatches(want, got) {
		return fmt.Errorf("%w: artifact builder live typed input does not match the bound request", ErrArtifactBuilderIdentityDrift)
	}
	return nil
}

func artifactBuilderInputMaterialMatches(want, got ArtifactBuilderInput) bool {
	return got.ArtifactPrefix == want.ArtifactPrefix && got.Key == want.Key && got.SHA256 == want.SHA256 && got.SizeBytes == want.SizeBytes
}

func artifactBuilderInputGitIdentityMatches(want, got ArtifactBuilderInput) bool {
	return got.RepositoryID == want.RepositoryID && got.ObjectFormat == want.ObjectFormat && got.SourceCommitSHA == want.SourceCommitSHA && got.SourceTreeSHA == want.SourceTreeSHA
}

func artifactBuilderInputDigestMatches(want, got ArtifactBuilderInput) bool {
	return got.ExpectedBuilderDigest == want.ExpectedBuilderDigest && got.ExpectedToolchainDigest == want.ExpectedToolchainDigest && got.ProtocolABIDigest == want.ProtocolABIDigest && got.WorkloadManifestABIDigest == want.WorkloadManifestABIDigest
}

func validateArtifactBuilderLiveTags(request ArtifactBuilderRequest, group ArtifactBuilderGroup) error {
	expected, err := artifactBuilderExpectedTags(request)
	if err != nil {
		return err
	}
	return compareArtifactBuilderTags(expected, group.Tags)
}

func compareArtifactBuilderTags(expected, got map[string]string) error {
	if len(got) != len(expected) {
		return fmt.Errorf("%w: artifact builder live tags are not the canonical digest-only set", ErrArtifactBuilderIdentityDrift)
	}
	for key, want := range expected {
		if got[key] != want || !validArtifactBuilderDigest(got[key]) {
			return fmt.Errorf("%w: artifact builder live tag %q does not match the canonical digest", ErrArtifactBuilderIdentityDrift, key)
		}
	}
	return nil
}
