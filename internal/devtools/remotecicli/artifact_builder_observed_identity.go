package remotecicli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"runtime"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/toolchainidentity"
)

const (
	// ArtifactBuilderObservedIdentitySchema identifies the measured builder identity.
	ArtifactBuilderObservedIdentitySchema = "artifact-builder-observed-identity/v1"
	// ArtifactBuilderObservedIdentityMaxBytes bounds the canonical identity document.
	ArtifactBuilderObservedIdentityMaxBytes = 4 << 10
	artifactBuilderObservedIdentityMaxBytes = ArtifactBuilderObservedIdentityMaxBytes
)

type artifactBuilderIdentityStageError struct{ stage string }

// Error 只返回稳定阶段码，不暴露底层路径或 provider 错误。
func (failure *artifactBuilderIdentityStageError) Error() string {
	return "artifact builder observed identity stage failed: " + failure.stage
}

func observedIdentityStageError(stage string) error {
	return &artifactBuilderIdentityStageError{stage: stage}
}

// ArtifactBuilderObservedIdentity is the non-secret identity measured by the
// builder worker from its actual executable and compiler closure.
type ArtifactBuilderObservedIdentity struct {
	SchemaVersion             string `json:"schema_version"`
	BuilderSHA256             string `json:"builder_sha256"`
	ToolchainDigest           string `json:"toolchain_digest"`
	ProtocolABIDigest         string `json:"protocol_abi_digest"`
	WorkloadManifestABIDigest string `json:"workload_manifest_abi_digest"`
}

// Validate 严格校验五字段身份 schema 与 canonical digest wire shape。
func (identity ArtifactBuilderObservedIdentity) Validate() error {
	if identity.SchemaVersion != ArtifactBuilderObservedIdentitySchema {
		return errors.New("artifact builder observed identity schema is invalid")
	}
	for _, digest := range []string{identity.BuilderSHA256, identity.ToolchainDigest, identity.ProtocolABIDigest, identity.WorkloadManifestABIDigest} {
		if !canonicalObservedDigest(digest) {
			return errors.New("artifact builder observed identity digest is invalid")
		}
	}
	return nil
}

// CanonicalEncode 生成 machine output channel 使用的 bounded canonical JSON 字节。
func (identity ArtifactBuilderObservedIdentity) CanonicalEncode() ([]byte, error) {
	if err := identity.Validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(identity)
	if err != nil {
		return nil, errors.New("encode artifact builder observed identity failed")
	}
	if len(data) > artifactBuilderObservedIdentityMaxBytes {
		return nil, errors.New("artifact builder observed identity exceeds the bounded size")
	}
	return data, nil
}

// DecodeArtifactBuilderObservedIdentity 严格解码一个 canonical identity object。
func DecodeArtifactBuilderObservedIdentity(data []byte) (ArtifactBuilderObservedIdentity, error) {
	var identity ArtifactBuilderObservedIdentity
	if len(data) == 0 || len(data) > artifactBuilderObservedIdentityMaxBytes || rejectRuntimeJSONDuplicates(data) != nil {
		return identity, errors.New("artifact builder observed identity JSON is malformed")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&identity); err != nil {
		return ArtifactBuilderObservedIdentity{}, errors.New("artifact builder observed identity JSON is not strict")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return ArtifactBuilderObservedIdentity{}, errors.New("artifact builder observed identity JSON has trailing data")
	}
	canonical, err := identity.CanonicalEncode()
	if err != nil || !bytes.Equal(canonical, data) {
		return ArtifactBuilderObservedIdentity{}, errors.New("artifact builder observed identity JSON is not canonical")
	}
	return identity, nil
}

// ArtifactBuilderObservedIdentityDependencies is the injectable measurement seam.
type artifactBuilderObservedIdentityDependencies struct {
	OpenExecutable        func() (io.ReadSeeker, error)
	CompilerPath          func() (string, error)
	CompilerClosureDigest func(context.Context, string) (string, error)
}

// ReadArtifactBuilderObservedIdentity 从 Linux worker 的 /proc/self/exe 与运行中 Go compiler closure 测量身份。
// Linux 只使用 /proc/self/exe 作为 process authority，不接受 caller path、environment 或 cwd fallback。
func ReadArtifactBuilderObservedIdentity(ctx context.Context) (identity ArtifactBuilderObservedIdentity, resultErr error) {
	if ctx == nil {
		return identity, observedIdentityStageError("preflight")
	}
	if runtime.GOOS != "linux" {
		return identity, observedIdentityStageError("platform")
	}
	file, err := os.Open("/proc/self/exe")
	if err != nil {
		return identity, observedIdentityStageError("executable_open")
	}
	info, statErr := file.Stat()
	if statErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		_ = file.Close()
		return identity, observedIdentityStageError("executable_stat")
	}
	deps := artifactBuilderObservedIdentityDependencies{
		OpenExecutable:        func() (io.ReadSeeker, error) { return file, nil },
		CompilerPath:          gateprivate.CanonicalRuntimeGoExecutable,
		CompilerClosureDigest: toolchainidentity.CompilerClosureDigest,
	}
	identity, resultErr = measureArtifactBuilderObservedIdentity(ctx, deps)
	if closeErr := file.Close(); closeErr != nil {
		resultErr = errors.Join(resultErr, observedIdentityStageError("executable_close"))
	}
	if resultErr != nil {
		return ArtifactBuilderObservedIdentity{}, resultErr
	}
	return identity, nil
}

// measureArtifactBuilderObservedIdentity 通过注入 seam 测量已打开 executable 与显式 compiler closure。
func measureArtifactBuilderObservedIdentity(ctx context.Context, deps artifactBuilderObservedIdentityDependencies) (ArtifactBuilderObservedIdentity, error) {
	if err := validateObservedIdentityMeasurement(ctx, deps); err != nil {
		return ArtifactBuilderObservedIdentity{}, observedIdentityStageError("preflight")
	}
	executable, err := openObservedExecutable(deps)
	if err != nil {
		return ArtifactBuilderObservedIdentity{}, observedIdentityStageError("executable_open")
	}
	builderDigest, err := measureObservedBuilderDigest(executable)
	if err != nil {
		return ArtifactBuilderObservedIdentity{}, observedIdentityStageError("executable_digest")
	}
	toolchainDigest, err := measureObservedToolchainDigest(ctx, deps)
	if err != nil {
		return ArtifactBuilderObservedIdentity{}, observedIdentityStageError("toolchain_closure")
	}
	protocolABIDigest, err := remoteci.BuilderSimulationProtocolABIDigest()
	if err != nil || !canonicalObservedDigest(protocolABIDigest) {
		return ArtifactBuilderObservedIdentity{}, observedIdentityStageError("protocol_abi")
	}
	workloadManifestABIDigest, err := remoteci.RepositoryWorkloadManifestABIDigest()
	if err != nil || !canonicalObservedDigest(workloadManifestABIDigest) {
		return ArtifactBuilderObservedIdentity{}, observedIdentityStageError("workload_manifest_abi")
	}
	identity := ArtifactBuilderObservedIdentity{
		SchemaVersion:             ArtifactBuilderObservedIdentitySchema,
		BuilderSHA256:             builderDigest,
		ToolchainDigest:           toolchainDigest,
		ProtocolABIDigest:         protocolABIDigest,
		WorkloadManifestABIDigest: workloadManifestABIDigest,
	}
	if err := identity.Validate(); err != nil {
		return ArtifactBuilderObservedIdentity{}, err
	}
	return identity, nil
}

// validateObservedIdentityMeasurement 校验测量 context、依赖和取消状态。
func validateObservedIdentityMeasurement(ctx context.Context, deps artifactBuilderObservedIdentityDependencies) error {
	if ctx == nil {
		return errors.New("artifact builder observed identity context is required")
	}
	if deps.OpenExecutable == nil || deps.CompilerPath == nil || deps.CompilerClosureDigest == nil {
		return errors.New("artifact builder observed identity dependencies are incomplete")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

// openObservedExecutable 从注入 seam 获取非 nil 的已打开 executable reader。
func openObservedExecutable(deps artifactBuilderObservedIdentityDependencies) (io.ReadSeeker, error) {
	executable, err := deps.OpenExecutable()
	if err != nil || isNilObservedReader(executable) {
		return nil, errors.New("open executable reader failed")
	}
	return executable, nil
}

// measureObservedBuilderDigest 从同一个 reader 的当前字节计算 builder SHA-256。
func measureObservedBuilderDigest(executable io.ReadSeeker) (string, error) {
	if _, err := executable.Seek(0, io.SeekStart); err != nil {
		return "", errors.New("seek executable reader failed")
	}
	digest, err := toolchainidentity.DigestExecutable(executable)
	if err != nil || !canonicalObservedDigest(digest) {
		return "", errors.New("measure builder executable failed")
	}
	return digest, nil
}

// measureObservedToolchainDigest 从实际 compiler closure 计算 toolchain digest。
func measureObservedToolchainDigest(ctx context.Context, deps artifactBuilderObservedIdentityDependencies) (string, error) {
	compilerPath, err := deps.CompilerPath()
	if err != nil || compilerPath == "" {
		return "", errors.New("resolve compiler identity failed")
	}
	digest, err := deps.CompilerClosureDigest(ctx, compilerPath)
	if err != nil || !canonicalObservedDigest(digest) {
		return "", errors.New("measure compiler closure failed")
	}
	return digest, nil
}

func canonicalObservedDigest(value string) bool {
	return isDigest(value) && value == strings.ToLower(value)
}

func isNilObservedReader(reader io.ReadSeeker) bool {
	if reader == nil {
		return true
	}
	value := reflect.ValueOf(reader)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func observedIdentityFailureStage(err error) string {
	var failure *artifactBuilderIdentityStageError
	if errors.As(err, &failure) && failure.stage != "" {
		return failure.stage
	}
	return "unknown"
}

// dispatchArtifactIdentity 输出一个 bounded canonical identity object，不写 progress text。
// 所有失败都留在既有 CLI-safe error boundary 内。
func dispatchArtifactIdentity(args []string, stdout, _ io.Writer) error {
	if len(args) != 0 {
		return newCLIFailure("artifact identity arguments are invalid", nil)
	}
	identity, err := ReadArtifactBuilderObservedIdentity(context.Background())
	if err != nil {
		return newCLIFailure("measure artifact builder identity: "+observedIdentityFailureStage(err), nil)
	}
	data, err := identity.CanonicalEncode()
	if err != nil || len(data)+1 > outputMaxBytes {
		return newCLIFailure("render artifact builder identity", nil)
	}
	data = append(data, '\n')
	if _, err := stdout.Write(data); err != nil {
		return newCLIFailure("write artifact builder identity", err)
	}
	return nil
}
