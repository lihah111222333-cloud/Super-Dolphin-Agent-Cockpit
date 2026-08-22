package remotecicli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

// RuntimeArtifactBuilderRequestFactory 从 exact input 构造唯一 aliyun-eci/v1 request。
type RuntimeArtifactBuilderRequestFactory func(remoteci.RuntimeArtifactBuildInput) (eci.ArtifactBuilderRequest, error)

// RuntimeArtifactBuilderRuntimeDependencies 显式注入既有 actor/store 与有界生命周期参数。
type RuntimeArtifactBuilderRuntimeDependencies struct {
	Actor                                     eci.ArtifactBuilderActor
	Store                                     ArtifactBuilderRuntimeStore
	RequestFactory                            RuntimeArtifactBuilderRequestFactory
	Clock                                     func() time.Time
	PollInterval, PollTimeout, CleanupTimeout time.Duration
	Observer                                  ArtifactBuilderRuntimeObserver
}

// RuntimeArtifactBuilderRuntime 只编排 artifact 供给与 cleanup，不接 Coordinator 或 executor。
type RuntimeArtifactBuilderRuntime struct {
	base    *ArtifactBuilderRuntime
	factory RuntimeArtifactBuilderRequestFactory
}

// NewRuntimeArtifactBuilderRuntime 复用既有 actor/store/lifecycle，且不创建 provider 资源。
func NewRuntimeArtifactBuilderRuntime(deps RuntimeArtifactBuilderRuntimeDependencies) (*RuntimeArtifactBuilderRuntime, error) {
	if isNilRuntimeDependency(deps.Actor) || isNilRuntimeDependency(deps.Store) || deps.RequestFactory == nil || deps.Clock == nil || deps.PollInterval <= 0 || deps.PollTimeout <= 0 || deps.CleanupTimeout <= 0 {
		return nil, errors.New("runtime artifact builder dependencies are incomplete")
	}
	base := &ArtifactBuilderRuntime{deps: ArtifactBuilderRuntimeDependencies{Actor: deps.Actor, Store: deps.Store, Clock: deps.Clock, PollInterval: deps.PollInterval, PollTimeout: deps.PollTimeout, CleanupTimeout: deps.CleanupTimeout, Observer: deps.Observer}}
	return &RuntimeArtifactBuilderRuntime{base: base, factory: deps.RequestFactory}, nil
}

// Build 创建 exactly one group，严格 readback artifact/receipt，并始终清理 group/input。
func (runtime *RuntimeArtifactBuilderRuntime) Build(ctx context.Context, input remoteci.RuntimeArtifactBuildInput) (evidence remoteci.RuntimeArtifactBuildEvidence, err error) {
	if !validRuntimeArtifactRuntime(runtime, ctx) {
		return evidence, errors.New("runtime artifact builder runtime is invalid")
	}
	if err := input.Validate(); err != nil {
		return evidence, err
	}
	var request eci.ArtifactBuilderRequest
	var requestReady bool
	var groupID string
	defer func() {
		if cleanupErr := runtime.base.cleanup(ctx, request, requestReady, groupID, input.InputObjectKey); cleanupErr != nil {
			err = errors.Join(err, cleanupErr)
		}
	}()
	inputDigest, err := remoteci.RuntimeArtifactBuilderInputDigest(input)
	if err != nil {
		return evidence, err
	}
	request, err = runtime.factory(input)
	if err != nil {
		return evidence, err
	}
	requestReady = true
	if err := validateRuntimeArtifactRequestBinding(input, request); err != nil {
		return evidence, err
	}
	pollCtx, cancel := context.WithTimeout(ctx, runtime.base.deps.PollTimeout)
	defer cancel()
	group, lifecycle, err := runtime.base.createBuilder(pollCtx, request, &groupID)
	if err != nil {
		return evidence, err
	}
	group, logs, err := runtime.base.observeBuilder(pollCtx, lifecycle, group)
	if err != nil {
		return evidence, err
	}
	return runtime.collect(ctx, pollCtx, input, inputDigest, group, logs)
}

func validRuntimeArtifactRuntime(runtime *RuntimeArtifactBuilderRuntime, ctx context.Context) bool {
	return runtime != nil && runtime.base != nil && ctx != nil
}

// validateRuntimeArtifactRequestBinding 逐字段核对 ECI request 未漂移且仍是唯一 runtime tuple。
func validateRuntimeArtifactRequestBinding(input remoteci.RuntimeArtifactBuildInput, request eci.ArtifactBuilderRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}
	material := request.Input
	if !validRuntimeArtifactRequestProtocol(request) {
		return errors.New("runtime artifact builder request protocol drifted")
	}
	want := eci.ArtifactBuilderInput{ArtifactPrefix: input.ArtifactPrefix, Key: input.InputObjectKey, SHA256: input.InputSHA256, SizeBytes: input.InputSizeBytes, RepositoryID: input.RepositoryID, ObjectFormat: input.ObjectFormat, SourceCommitSHA: input.CommitSHA, SourceTreeSHA: input.TreeSHA, ExpectedBuilderDigest: input.ExpectedBuilderDigest, ExpectedToolchainDigest: input.ExpectedToolchainDigest, ProtocolABIDigest: input.ProtocolABIDigest, WorkloadManifestABIDigest: input.WorkloadManifestABIDigest}
	if material != want {
		return errors.New("runtime artifact builder request input drifted")
	}
	return nil
}

func validRuntimeArtifactRequestProtocol(request eci.ArtifactBuilderRequest) bool {
	return request.ArtifactKind == eci.RuntimeArtifactBuilderArtifactKind && request.ArtifactRef == eci.RuntimeArtifactBuilderArtifactRef && len(request.Command) == 2 && request.Command[1] == eci.RuntimeArtifactBuilderSubcommand
}

// collect 严格读取 worker index、receipt、artifact bytes 与 provider terminal evidence。
func (runtime *RuntimeArtifactBuilderRuntime) collect(ctx, pollCtx context.Context, input remoteci.RuntimeArtifactBuildInput, inputDigest string, group eci.ArtifactBuilderGroup, logs string) (remoteci.RuntimeArtifactBuildEvidence, error) {
	result, err := decodeRuntimeArtifactBuildOutput(logs)
	if err != nil || result.InputDigest != inputDigest {
		return remoteci.RuntimeArtifactBuildEvidence{}, errors.New("runtime artifact builder output drifted")
	}
	if err := result.ValidateAgainst(input); err != nil {
		return remoteci.RuntimeArtifactBuildEvidence{}, err
	}
	receipt, err := runtime.readRuntimeReceipt(pollCtx, result, input)
	if err != nil {
		return remoteci.RuntimeArtifactBuildEvidence{}, err
	}
	artifact := receiptArtifactBinding(receipt, result.ReceiptKey, result.ReceiptSHA256)
	if err := runtime.validateArtifact(ctx, result, artifact); err != nil {
		return remoteci.RuntimeArtifactBuildEvidence{}, err
	}
	if group.Container.ExitCode == nil {
		return remoteci.RuntimeArtifactBuildEvidence{}, errors.New("runtime artifact builder terminal exit code is missing")
	}
	return remoteci.RuntimeArtifactBuildEvidence{Request: input, InputDigest: inputDigest, Provider: remoteci.PortableExecutionProvider, ContainerGroupID: group.ID, ContainerGroupStatus: group.Status, ContainerExitCode: *group.Container.ExitCode, StartedAt: group.Container.StartTime.UTC().Format(time.RFC3339Nano), FinishedAt: group.Container.FinishTime.UTC().Format(time.RFC3339Nano), Artifact: artifact, Receipt: receipt}, nil
}

// readRuntimeReceipt 严格校验 receipt raw bytes metadata 与 exact input binding。
func (runtime *RuntimeArtifactBuilderRuntime) readRuntimeReceipt(ctx context.Context, result remoteci.RuntimeArtifactBuilderWorkerResult, input remoteci.RuntimeArtifactBuildInput) (remoteci.RuntimeArtifactProducerReceipt, error) {
	data, metadata, err := runtime.base.deps.Store.ReadReceipt(ctx, result.ReceiptKey, 1<<20)
	if err != nil {
		return remoteci.RuntimeArtifactProducerReceipt{}, err
	}
	receipt, err := remoteci.DecodeRuntimeArtifactProducerReceipt(data)
	if err != nil || metadata.SHA256 != result.ReceiptSHA256 || metadata.SizeBytes != int64(len(data)) {
		return remoteci.RuntimeArtifactProducerReceipt{}, errors.New("runtime artifact receipt readback drifted")
	}
	if !runtimeArtifactReceiptMatchesInput(receipt, input) {
		return remoteci.RuntimeArtifactProducerReceipt{}, errors.New("runtime artifact receipt input binding drifted")
	}
	return receipt, nil
}

// runtimeArtifactReceiptMatchesInput 逐字段绑定 exact source、input bundle、observed identity 与 ABI。
func runtimeArtifactReceiptMatchesInput(receipt remoteci.RuntimeArtifactProducerReceipt, input remoteci.RuntimeArtifactBuildInput) bool {
	return receipt.RepositoryID == input.RepositoryID && receipt.ObjectFormat == input.ObjectFormat && receipt.SourceCommitSHA == input.CommitSHA && receipt.SourceTreeSHA == input.TreeSHA && receipt.InputSHA256 == input.InputSHA256 && receipt.InputSizeBytes == input.InputSizeBytes && receipt.ObservedBuilderDigest == input.ExpectedBuilderDigest && receipt.ObservedToolchainDigest == input.ExpectedToolchainDigest && receipt.ProtocolABIDigest == input.ProtocolABIDigest && receipt.WorkloadManifestABIDigest == input.WorkloadManifestABIDigest
}

func receiptArtifactBinding(receipt remoteci.RuntimeArtifactProducerReceipt, receiptKey, receiptSHA string) remoteci.PortableExecutionArtifact {
	return remoteci.PortableExecutionArtifact{ContractID: receipt.ContractID, Kind: receipt.ArtifactKind, ArtifactKey: receipt.ArtifactKey, ArtifactSHA256: receipt.ArtifactSHA256, ArtifactSizeBytes: receipt.ArtifactSizeBytes, ReceiptKey: receiptKey, ReceiptSHA256: receiptSHA, IdentityDigest: receipt.ArtifactIdentityDigest, BuilderDigest: receipt.ObservedBuilderDigest, ToolchainDigest: receipt.ObservedToolchainDigest, ProtocolABIDigest: receipt.ProtocolABIDigest, WorkloadManifestABIDigest: receipt.WorkloadManifestABIDigest}
}

// validateArtifact 复核 store metadata、bounded bytes、ELF header 与 raw SHA-256。
func (runtime *RuntimeArtifactBuilderRuntime) validateArtifact(ctx context.Context, result remoteci.RuntimeArtifactBuilderWorkerResult, artifact remoteci.PortableExecutionArtifact) error {
	body, size, metadata, err := runtime.base.deps.Store.Resolve(ctx, remoteci.PortableExecutionRuntimeArtifactKind, result.ArtifactKey)
	if err != nil {
		return err
	}
	if body == nil {
		return errors.New("runtime artifact store returned no body")
	}
	defer func() { _ = body.Close() }()
	if err := validateRuntimeArtifactReadbackMetadata(size, metadata.SHA256, metadata.SizeBytes, result, artifact); err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(body, size+1))
	if err != nil || int64(len(data)) != size {
		return errors.New("runtime artifact readback bytes drifted")
	}
	if err := validateRuntimeELFBytes(data); err != nil {
		return err
	}
	if digestRuntimeArtifactBytes(data) != result.ArtifactSHA256 {
		return errors.New("runtime artifact readback SHA-256 drifted")
	}
	return nil
}

// validateRuntimeArtifactReadbackMetadata 分离校验 store 与 receipt 的 byte metadata。
func validateRuntimeArtifactReadbackMetadata(size int64, metadataSHA string, metadataSize int64, result remoteci.RuntimeArtifactBuilderWorkerResult, artifact remoteci.PortableExecutionArtifact) error {
	if size != result.ArtifactSizeBytes || metadataSHA != result.ArtifactSHA256 || metadataSize != size {
		return errors.New("runtime artifact store metadata drifted")
	}
	if artifact.ArtifactSHA256 != result.ArtifactSHA256 || artifact.ArtifactSizeBytes != size {
		return errors.New("runtime artifact receipt metadata drifted")
	}
	return nil
}

// validateRuntimeELFBytes 只接受 ELF64 little-endian x86-64 bytes。
func validateRuntimeELFBytes(data []byte) error {
	if len(data) < 20 || !bytes.Equal(data[:4], []byte{0x7f, 'E', 'L', 'F'}) || data[4] != 2 || data[5] != 1 || data[18] != 62 || data[19] != 0 {
		return errors.New("runtime artifact readback is not linux/amd64 ELF")
	}
	return nil
}

func digestRuntimeArtifactBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// decodeRuntimeArtifactBuildOutput 从 bounded logs 中提取恰好一个 runtime worker index。
func decodeRuntimeArtifactBuildOutput(logs string) (remoteci.RuntimeArtifactBuilderWorkerResult, error) {
	var output runtimeArtifactBuildOutput
	found := false
	for line := range strings.SplitSeq(logs, "\n") {
		if !strings.HasPrefix(line, runtimeArtifactBuildOutputPrefix) {
			continue
		}
		if found {
			return output.Result, errors.New("runtime artifact build output is duplicated")
		}
		found = true
		decoder := json.NewDecoder(strings.NewReader(strings.TrimPrefix(line, runtimeArtifactBuildOutputPrefix)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&output); err != nil {
			return output.Result, fmt.Errorf("decode runtime artifact build output: %w", err)
		}
	}
	if !found || output.SchemaVersion != runtimeArtifactBuildOutputSchema || output.ExecutionProvider != remoteci.PortableExecutionProvider {
		return output.Result, errors.New("runtime artifact build output is missing or invalid")
	}
	return output.Result, nil
}
