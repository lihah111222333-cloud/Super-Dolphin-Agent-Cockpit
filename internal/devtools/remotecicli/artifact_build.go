package remotecicli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci/workerio"
)

const (
	artifactBuildSchema       = "source-baseline-artifact-build/v1"
	artifactBuildArtifactKind = remoteci.PortableSourceBaselineArtifactKind
	artifactBuildArtifactRef  = remoteci.PortableSourceBaselineArtifactRef
)

// artifactBuildFlags 是 _artifact-build 的完整显式输入，不携带凭据或宿主路径。
type artifactBuildFlags struct {
	executionProvider         string
	ossEndpoint               string
	ossBucket                 string
	ramRole                   string
	compilerPath              string
	artifactKind              string
	artifactRef               string
	artifactPrefix            string
	inputKey                  string
	inputSHA256               string
	inputSizeBytes            int64
	repositoryID              string
	objectFormat              string
	sourceCommitSHA           string
	sourceTreeSHA             string
	expectedBuilderDigest     string
	expectedToolchainDigest   string
	protocolABIDigest         string
	workloadManifestABIDigest string
}

type artifactBuildDependencies struct {
	httpClient       *http.Client
	metadataBaseURL  string
	observedIdentity func(context.Context, string) (remoteci.ObservedArtifactBuilderIdentity, error)
}

type strictStringFlag struct {
	value string
	set   bool
}

// Set 接收一次显式字符串参数并拒绝重复赋值。
func (value *strictStringFlag) Set(raw string) error {
	if value.set {
		return errors.New("duplicate flag")
	}
	value.value = raw
	value.set = true
	return nil
}

// String 返回已解析的字符串参数。
func (value *strictStringFlag) String() string { return value.value }

type strictInt64Flag struct {
	value int64
	set   bool
}

// Set 接收一次显式整数参数并拒绝重复赋值。
func (value *strictInt64Flag) Set(raw string) error {
	if value.set {
		return errors.New("duplicate flag")
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return errors.New("integer flag is invalid")
	}
	value.value = parsed
	value.set = true
	return nil
}

// String 返回已解析的整数参数。
func (value *strictInt64Flag) String() string {
	if !value.set {
		return ""
	}
	return strconv.FormatInt(value.value, 10)
}

// dispatchArtifactBuild 执行同一 binary 内部的 source baseline builder worker。
func dispatchArtifactBuild(args []string, stdout, stderr io.Writer) error {
	return dispatchArtifactBuildWithDependencies(args, stdout, stderr, artifactBuildDependencies{httpClient: http.DefaultClient, observedIdentity: observeArtifactBuilderIdentity})
}

func observeArtifactBuilderIdentity(ctx context.Context, compilerPath string) (remoteci.ObservedArtifactBuilderIdentity, error) {
	identity, err := ReadArtifactBuilderObservedIdentity(ctx, compilerPath)
	if err != nil {
		return remoteci.ObservedArtifactBuilderIdentity{}, err
	}
	return remoteci.ObservedArtifactBuilderIdentity{BuilderDigest: identity.BuilderSHA256, ToolchainDigest: identity.ToolchainDigest}, nil
}

// dispatchArtifactBuildWithDependencies 使用显式传输依赖执行一次 worker 构建。
func dispatchArtifactBuildWithDependencies(args []string, stdout, stderr io.Writer, dependencies artifactBuildDependencies) error {
	input, artifactIO, observer, err := prepareArtifactBuildWorker(args, stderr, dependencies)
	if err != nil {
		return err
	}
	if err := writeProgress(stderr, "artifact build started"); err != nil {
		return newCLIFailure("write progress", err)
	}
	if observer == nil {
		return newCLIFailure("artifact builder observed identity observer is required", nil)
	}
	result, err := remoteci.BuildSourceBaselineArtifact(context.Background(), artifactIO, input, observer)
	if err != nil {
		return newCLIFailure("artifact build failed", nil)
	}
	output, err := boundedArtifactBuildJSON(result)
	if err != nil {
		return newCLIFailure("render artifact build result", nil)
	}
	if _, err := stdout.Write(output); err != nil {
		return newCLIFailure("write result", err)
	}
	return writeProgress(stderr, "artifact build completed")
}

// prepareArtifactBuildWorker 校验输入并组装 worker 的下载与上传端口。
func prepareArtifactBuildWorker(args []string, stderr io.Writer, dependencies artifactBuildDependencies) (remoteci.SourceBaselineBuildInput, remoteci.ArtifactIO, remoteci.ArtifactBuilderIdentityObserver, error) {
	flags, err := parseArtifactBuildFlags(args)
	if err != nil {
		return remoteci.SourceBaselineBuildInput{}, remoteci.ArtifactIO{}, nil, err
	}
	if err := writeProgress(stderr, "artifact build arguments accepted"); err != nil {
		return remoteci.SourceBaselineBuildInput{}, remoteci.ArtifactIO{}, nil, newCLIFailure("write progress", err)
	}
	input, err := flags.sourceBaselineBuildInput()
	if err != nil {
		return remoteci.SourceBaselineBuildInput{}, remoteci.ArtifactIO{}, nil, newCLIFailure("validate artifact build input", nil)
	}
	inputClient, err := newArtifactBuildClient(flags, flags.inputKey, flags.inputSizeBytes, dependencies)
	if err != nil {
		return remoteci.SourceBaselineBuildInput{}, remoteci.ArtifactIO{}, nil, newCLIFailure("configure artifact input client", nil)
	}
	if err := writeProgress(stderr, "artifact input client configured"); err != nil {
		return remoteci.SourceBaselineBuildInput{}, remoteci.ArtifactIO{}, nil, newCLIFailure("write progress", err)
	}
	uploadState := &artifactBuildUploadState{}
	artifactIO := remoteci.ArtifactIO{
		DownloadInput: func(ctx context.Context, key string, destination io.Writer) (int64, error) {
			if key != flags.inputKey {
				return 0, errors.New("artifact input object key drifted")
			}
			return inputClient.Download(ctx, destination)
		},
		UploadArtifact: newArtifactBuildUpload(flags, dependencies, artifactBuildArtifactUpload, uploadState),
		UploadReceipt:  newArtifactBuildUpload(flags, dependencies, artifactBuildReceiptUpload, uploadState),
	}
	if dependencies.observedIdentity == nil {
		return remoteci.SourceBaselineBuildInput{}, remoteci.ArtifactIO{}, nil, newCLIFailure("artifact builder observed identity observer is required", nil)
	}
	observer := func(ctx context.Context) (remoteci.ObservedArtifactBuilderIdentity, error) {
		return dependencies.observedIdentity(ctx, flags.compilerPath)
	}
	return input, artifactIO, observer, nil
}

// parseArtifactBuildFlags 严格解析 _artifact-build 参数，拒绝未知、重复和缺失字段。
func parseArtifactBuildFlags(args []string) (artifactBuildFlags, error) {
	values, err := parseArtifactBuildFlagValues(args)
	if err != nil {
		return artifactBuildFlags{}, err
	}
	flags := artifactBuildFlagsFromValues(values)
	if err := validateCompilerPathShape(flags.compilerPath); err != nil {
		return artifactBuildFlags{}, newCLIFailure("artifact build compiler path is invalid", nil)
	}
	if flags.executionProvider != remoteci.PortableExecutionProvider || flags.artifactKind != artifactBuildArtifactKind || flags.artifactRef != artifactBuildArtifactRef {
		return artifactBuildFlags{}, newCLIFailure("artifact build protocol identity is invalid", nil)
	}
	return flags, nil
}

type artifactBuildFlagValues struct {
	executionProvider, ossEndpoint, ossBucket, ramRole                                           strictStringFlag
	artifactKind, artifactRef, artifactPrefix, inputKey, inputSHA256                             strictStringFlag
	compilerPath                                                                                 strictStringFlag
	repositoryID, objectFormat, sourceCommitSHA, sourceTreeSHA                                   strictStringFlag
	expectedBuilderDigest, expectedToolchainDigest, protocolABIDigest, workloadManifestABIDigest strictStringFlag
	inputSizeBytes                                                                               strictInt64Flag
}

// parseArtifactBuildFlagValues 解析完整 expected identity 与 ABI 参数集合并拒绝尾随参数。
func parseArtifactBuildFlagValues(args []string) (artifactBuildFlagValues, error) {
	var values artifactBuildFlagValues
	set := flag.NewFlagSet("_artifact-build", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	set.Var(&values.compilerPath, "compiler-path", "")
	set.Var(&values.executionProvider, "execution-provider", "")
	set.Var(&values.ossEndpoint, "oss-endpoint", "")
	set.Var(&values.ossBucket, "oss-bucket", "")
	set.Var(&values.ramRole, "ram-role-name", "")
	set.Var(&values.artifactKind, "artifact-kind", "")
	set.Var(&values.artifactRef, "artifact-ref", "")
	set.Var(&values.artifactPrefix, "artifact-prefix", "")
	set.Var(&values.inputKey, "input-key", "")
	set.Var(&values.inputSHA256, "input-sha256", "")
	set.Var(&values.inputSizeBytes, "input-size-bytes", "")
	set.Var(&values.repositoryID, "repository-id", "")
	set.Var(&values.objectFormat, "object-format", "")
	set.Var(&values.sourceCommitSHA, "source-commit-sha", "")
	set.Var(&values.sourceTreeSHA, "source-tree-sha", "")
	set.Var(&values.expectedBuilderDigest, "expected-builder-digest", "")
	set.Var(&values.expectedToolchainDigest, "expected-toolchain-digest", "")
	set.Var(&values.protocolABIDigest, "protocol-abi-digest", "")
	set.Var(&values.workloadManifestABIDigest, "workload-manifest-abi-digest", "")
	if err := set.Parse(args); err != nil || len(set.Args()) != 0 {
		return artifactBuildFlagValues{}, newCLIFailure("artifact build arguments are invalid", nil)
	}
	for _, field := range artifactBuildRequiredFlags(values) {
		if !field.set {
			return artifactBuildFlagValues{}, newCLIFailure("artifact build required flag is missing", nil)
		}
	}
	return values, nil
}

func artifactBuildRequiredFlags(values artifactBuildFlagValues) []struct {
	name string
	set  bool
} {
	return []struct {
		name string
		set  bool
	}{
		{"compiler-path", values.compilerPath.set}, {"execution-provider", values.executionProvider.set}, {"oss-endpoint", values.ossEndpoint.set}, {"oss-bucket", values.ossBucket.set}, {"ram-role-name", values.ramRole.set},
		{"artifact-kind", values.artifactKind.set}, {"artifact-ref", values.artifactRef.set}, {"artifact-prefix", values.artifactPrefix.set}, {"input-key", values.inputKey.set},
		{"input-sha256", values.inputSHA256.set}, {"input-size-bytes", values.inputSizeBytes.set}, {"repository-id", values.repositoryID.set}, {"object-format", values.objectFormat.set},
		{"source-commit-sha", values.sourceCommitSHA.set}, {"source-tree-sha", values.sourceTreeSHA.set}, {"expected-builder-digest", values.expectedBuilderDigest.set},
		{"expected-toolchain-digest", values.expectedToolchainDigest.set}, {"protocol-abi-digest", values.protocolABIDigest.set}, {"workload-manifest-abi-digest", values.workloadManifestABIDigest.set},
	}
}

func artifactBuildFlagsFromValues(values artifactBuildFlagValues) artifactBuildFlags {
	return artifactBuildFlags{
		compilerPath:              values.compilerPath.value,
		executionProvider:         values.executionProvider.value,
		ossEndpoint:               values.ossEndpoint.value,
		ossBucket:                 values.ossBucket.value,
		ramRole:                   values.ramRole.value,
		artifactKind:              values.artifactKind.value,
		artifactRef:               values.artifactRef.value,
		artifactPrefix:            values.artifactPrefix.value,
		inputKey:                  values.inputKey.value,
		inputSHA256:               values.inputSHA256.value,
		inputSizeBytes:            values.inputSizeBytes.value,
		repositoryID:              values.repositoryID.value,
		objectFormat:              values.objectFormat.value,
		sourceCommitSHA:           values.sourceCommitSHA.value,
		sourceTreeSHA:             values.sourceTreeSHA.value,
		expectedBuilderDigest:     values.expectedBuilderDigest.value,
		expectedToolchainDigest:   values.expectedToolchainDigest.value,
		protocolABIDigest:         values.protocolABIDigest.value,
		workloadManifestABIDigest: values.workloadManifestABIDigest.value,
	}
}

func (flags artifactBuildFlags) sourceBaselineBuildInput() (remoteci.SourceBaselineBuildInput, error) {
	input := remoteci.SourceBaselineBuildInput{
		SchemaVersion:             remoteci.SourceBaselineBuilderInputSchema,
		ArtifactPrefix:            flags.artifactPrefix,
		RepositoryID:              flags.repositoryID,
		ObjectFormat:              flags.objectFormat,
		CommitSHA:                 flags.sourceCommitSHA,
		TreeSHA:                   flags.sourceTreeSHA,
		InputObjectKey:            flags.inputKey,
		InputSHA256:               flags.inputSHA256,
		InputSizeBytes:            flags.inputSizeBytes,
		ExpectedBuilderDigest:     flags.expectedBuilderDigest,
		ExpectedToolchainDigest:   flags.expectedToolchainDigest,
		ProtocolABIDigest:         flags.protocolABIDigest,
		WorkloadManifestABIDigest: flags.workloadManifestABIDigest,
	}
	if err := input.Validate(); err != nil {
		return remoteci.SourceBaselineBuildInput{}, err
	}
	return input, nil
}

func newArtifactBuildClient(flags artifactBuildFlags, key string, maxBytes int64, dependencies artifactBuildDependencies) (*workerio.Client, error) {
	return workerio.NewClient(workerio.Config{
		RoleName: flags.ramRole,
		Endpoint: flags.ossEndpoint,
		Bucket:   flags.ossBucket,
		Key:      key,
		MaxBytes: maxBytes,
	}, workerio.Dependencies{
		HTTPClient:      dependencies.httpClient,
		MetadataBaseURL: dependencies.metadataBaseURL,
	})
}

const (
	artifactBuildArtifactUpload = iota
	artifactBuildReceiptUpload
)

type artifactBuildUploadState struct {
	artifactSHA256         string
	artifactIdentityDigest string
}

func newArtifactBuildUpload(flags artifactBuildFlags, dependencies artifactBuildDependencies, uploadKind int, state *artifactBuildUploadState) func(context.Context, string, string, remoteci.SourceBaselineArtifactMetadata) error {
	return func(ctx context.Context, localPath, key string, metadata remoteci.SourceBaselineArtifactMetadata) error {
		if err := validateArtifactBuildUpload(flags, uploadKind, state, key, metadata); err != nil {
			return err
		}
		if err := uploadArtifactBuildFile(ctx, flags, dependencies, localPath, key, metadata); err != nil {
			return err
		}
		if uploadKind == artifactBuildArtifactUpload {
			state.artifactSHA256 = metadata.SHA256
			state.artifactIdentityDigest = metadata.ArtifactIdentityDigest
		}
		return nil
	}
}

// validateArtifactBuildUpload 校验 worker 上传的内容身份与 canonical object key。
func validateArtifactBuildUpload(flags artifactBuildFlags, uploadKind int, state *artifactBuildUploadState, key string, metadata remoteci.SourceBaselineArtifactMetadata) error {
	if state == nil || metadata.SizeBytes <= 0 || !isDigest(metadata.SHA256) || !isDigest(metadata.ArtifactIdentityDigest) {
		return errors.New("artifact upload metadata is invalid")
	}
	switch uploadKind {
	case artifactBuildArtifactUpload:
		return validateArtifactBuildArtifactKey(flags, key, metadata.ArtifactIdentityDigest)
	case artifactBuildReceiptUpload:
		if metadata.ArtifactIdentityDigest != state.artifactIdentityDigest {
			return errors.New("receipt upload artifact identity digest drifted")
		}
		return validateArtifactBuildReceiptKey(flags, state, key)
	default:
		return errors.New("artifact upload kind is invalid")
	}
}

func validateArtifactBuildArtifactKey(flags artifactBuildFlags, key, artifactIdentityDigest string) error {
	artifactKey, _, err := remoteci.CanonicalSourceBaselineBuilderKeys(flags.artifactPrefix, flags.repositoryID, flags.objectFormat, flags.sourceTreeSHA, artifactIdentityDigest)
	if err != nil {
		return err
	}
	if key != artifactKey {
		return errors.New("artifact upload key is not canonical")
	}
	return nil
}

func validateArtifactBuildReceiptKey(flags artifactBuildFlags, state *artifactBuildUploadState, key string) error {
	if state.artifactSHA256 == "" || state.artifactIdentityDigest == "" {
		return errors.New("receipt upload was attempted before artifact upload")
	}
	_, receiptKey, err := remoteci.CanonicalSourceBaselineBuilderKeys(flags.artifactPrefix, flags.repositoryID, flags.objectFormat, flags.sourceTreeSHA, state.artifactIdentityDigest)
	if err != nil || key != receiptKey {
		return errors.New("receipt upload key is not canonical")
	}
	return nil
}

func uploadArtifactBuildFile(ctx context.Context, flags artifactBuildFlags, dependencies artifactBuildDependencies, localPath, key string, metadata remoteci.SourceBaselineArtifactMetadata) error {
	client, err := newArtifactBuildClient(flags, key, metadata.SizeBytes, dependencies)
	if err != nil {
		return err
	}
	file, err := os.Open(localPath)
	if err != nil {
		return err
	}
	uploadErr := client.Upload(ctx, readerWithoutCloser{Reader: file}, metadata.SizeBytes, strings.TrimPrefix(metadata.SHA256, "sha256:"))
	closeErr := file.Close()
	return errors.Join(uploadErr, closeErr)
}

type readerWithoutCloser struct {
	io.Reader
}

type artifactBuildOutput struct {
	Schema                 string `json:"schema"`
	Status                 string `json:"status"`
	ExecutionProvider      string `json:"execution_provider"`
	InputDigest            string `json:"input_digest"`
	ArtifactKey            string `json:"artifact_key"`
	ReceiptKey             string `json:"receipt_key"`
	ArtifactSHA256         string `json:"artifact_sha256"`
	ArtifactSizeBytes      int64  `json:"artifact_size_bytes"`
	ArtifactIdentityDigest string `json:"artifact_identity_digest"`
	ReceiptSHA256          string `json:"receipt_sha256"`
	ReceiptDigest          string `json:"receipt_digest"`
}

// boundedArtifactBuildJSON 仅渲染 NOT_VERIFIED 与内容摘要，不渲染路径或凭据。
func boundedArtifactBuildJSON(result remoteci.SourceBaselineBuilderWorkerResult) ([]byte, error) {
	if err := result.Receipt.Validate(); err != nil {
		return nil, err
	}
	receiptBytes, err := result.Receipt.CanonicalEncode()
	if err != nil {
		return nil, err
	}
	receiptDigest, err := remoteci.SourceBaselineProducerReceiptDigest(result.Receipt)
	if err != nil {
		return nil, err
	}
	for _, digest := range []string{result.InputDigest, result.Receipt.ArtifactSHA256, result.Receipt.ArtifactIdentityDigest, digestBytes(receiptBytes), receiptDigest} {
		if !isDigest(digest) {
			return nil, errors.New("artifact build result digest is invalid")
		}
	}
	output := artifactBuildOutput{
		Schema:                 artifactBuildSchema,
		Status:                 "NOT_VERIFIED",
		ExecutionProvider:      result.Receipt.ExecutionProvider,
		InputDigest:            result.InputDigest,
		ArtifactKey:            result.ArtifactKey,
		ReceiptKey:             result.ReceiptKey,
		ArtifactSHA256:         result.Receipt.ArtifactSHA256,
		ArtifactSizeBytes:      result.Receipt.ArtifactSizeBytes,
		ArtifactIdentityDigest: result.Receipt.ArtifactIdentityDigest,
		ReceiptSHA256:          digestBytes(receiptBytes),
		ReceiptDigest:          receiptDigest,
	}
	data, err := json.Marshal(output)
	if err != nil {
		return nil, errors.New("artifact build result exceeds bounded size")
	}
	data = append([]byte(artifactBuildRuntimePrefix), data...)
	data = append(data, '\n')
	if len(data) > outputMaxBytes {
		return nil, errors.New("artifact build result exceeds bounded size")
	}
	return data, nil
}
