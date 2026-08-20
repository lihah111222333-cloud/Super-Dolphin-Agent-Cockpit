package remoteci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

const (
	// BuilderSimulationRequestDigestDomain separates the full request identity from
	// the narrower engine and target binding identities.
	BuilderSimulationRequestDigestDomain = "remote-ci-builder-simulation-request-binding/v1"
	// BuilderSimulationEngineBindingDigestDomain excludes host-local repository paths.
	BuilderSimulationEngineBindingDigestDomain = "remote-ci-builder-simulation-engine-binding/v1"
	// BuilderSimulationTargetBindingDigestDomain excludes host-local repository paths.
	BuilderSimulationTargetBindingDigestDomain = "remote-ci-builder-simulation-target-binding/v1"
)

// BuilderSimulationGitResolver is the read-only Git identity boundary for a
// builder-simulation request. Implementations must resolve the explicit
// repository root and must not consult cwd, HEAD, or a fallback repository.
type BuilderSimulationGitResolver interface {
	GitObjectResolver
	ResolveObjectFormat(ctx context.Context, repositoryRoot string) (string, error)
}

// BoundBuilderSimulationRequest is the validated request and its three
// content-addressed identities for composition code. It performs no I/O.
type BoundBuilderSimulationRequest struct {
	Request             BuilderSimulationRequest `json:"-"`
	RequestDigest       string                   `json:"request_digest"`
	EngineBindingDigest string                   `json:"engine_binding_digest"`
	TargetBindingDigest string                   `json:"target_binding_digest"`
}

type builderSimulationEngineBindingMaterial struct {
	RepositoryID            string `json:"repository_id"`
	ObjectFormat            string `json:"object_format"`
	CommitSHA               string `json:"commit_sha"`
	TreeSHA                 string `json:"tree_sha"`
	ExpectedBuilderDigest   string `json:"expected_builder_digest"`
	ExpectedToolchainDigest string `json:"expected_toolchain_digest"`
	ProtocolABIDigest       string `json:"protocol_abi_digest"`
}

type builderSimulationTargetBindingMaterial struct {
	RepositoryID      string `json:"repository_id"`
	ObjectFormat      string `json:"object_format"`
	CommitSHA         string `json:"commit_sha"`
	TreeSHA           string `json:"tree_sha"`
	ManifestPath      string `json:"manifest_path"`
	ManifestSHA256    string `json:"manifest_sha256"`
	ManifestSchema    string `json:"manifest_schema"`
	ManifestABIDigest string `json:"manifest_abi_digest"`
}

// BindBuilderSimulationRequest 校验显式 Git 身份并冻结三个内容摘要，不产生副作用。
func BindBuilderSimulationRequest(ctx context.Context, request BuilderSimulationRequest, resolver BuilderSimulationGitResolver) (BoundBuilderSimulationRequest, error) {
	if err := validateBuilderSimulationBindingInput(ctx, request, resolver); err != nil {
		return BoundBuilderSimulationRequest{}, err
	}
	if err := bindBuilderSimulationRepository(ctx, resolver, "engine", request.Engine.RepositoryRoot, request.Engine.ObjectFormat, request.Engine.CommitSHA, request.Engine.TreeSHA); err != nil {
		return BoundBuilderSimulationRequest{}, err
	}
	if err := bindBuilderSimulationRepository(ctx, resolver, "target", request.Target.RepositoryRoot, request.Target.ObjectFormat, request.Target.CommitSHA, request.Target.TreeSHA); err != nil {
		return BoundBuilderSimulationRequest{}, err
	}
	if err := bindBuilderSimulationManifest(ctx, resolver, request); err != nil {
		return BoundBuilderSimulationRequest{}, err
	}
	requestBytes, err := request.CanonicalEncode()
	if err != nil {
		return BoundBuilderSimulationRequest{}, err
	}
	requestDigest := repositoryFramedDigest(BuilderSimulationRequestDigestDomain, requestBytes)
	engineDigest, err := builderSimulationEngineBindingDigest(request.Engine)
	if err != nil {
		return BoundBuilderSimulationRequest{}, err
	}
	targetDigest, err := builderSimulationTargetBindingDigest(request.Target)
	if err != nil {
		return BoundBuilderSimulationRequest{}, err
	}
	return BoundBuilderSimulationRequest{
		Request:             request,
		RequestDigest:       requestDigest,
		EngineBindingDigest: engineDigest,
		TargetBindingDigest: targetDigest,
	}, nil
}

func validateBuilderSimulationBindingInput(ctx context.Context, request BuilderSimulationRequest, resolver BuilderSimulationGitResolver) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if ctx == nil {
		return errors.New("builder simulation binding context is nil")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("builder simulation binding context: %w", err)
	}
	if interfaceValueIsNil(resolver) {
		return errors.New("builder simulation Git resolver is required")
	}
	return nil
}

// bindBuilderSimulationRepository 复核 actual object format 与 commit-to-tree 身份。
func bindBuilderSimulationRepository(ctx context.Context, resolver BuilderSimulationGitResolver, role, root, objectFormat, commitSHA, treeSHA string) error {
	if err := builderSimulationContextErr(ctx, "resolve "+role+" object format"); err != nil {
		return err
	}
	actualFormat, err := resolver.ResolveObjectFormat(ctx, root)
	if err != nil {
		return fmt.Errorf("resolve %s object format: %w", role, err)
	}
	if err := builderSimulationContextErr(ctx, "resolve "+role+" object format"); err != nil {
		return err
	}
	if actualFormat != objectFormat {
		return fmt.Errorf("%s object format differs from the bound request", role)
	}
	if err := builderSimulationContextErr(ctx, "resolve "+role+" commit tree"); err != nil {
		return err
	}
	actualTree, err := resolver.ResolveCommitTree(ctx, root, commitSHA)
	if err != nil {
		return fmt.Errorf("resolve %s commit tree: %w", role, err)
	}
	if err := builderSimulationContextErr(ctx, "resolve "+role+" commit tree"); err != nil {
		return err
	}
	if actualTree != treeSHA || !validPortableOID(actualTree) {
		return fmt.Errorf("%s commit does not resolve to the bound tree", role)
	}
	return nil
}

// bindBuilderSimulationManifest 从 exact target tree 读取并验证 repository-owned manifest。
func bindBuilderSimulationManifest(ctx context.Context, resolver BuilderSimulationGitResolver, request BuilderSimulationRequest) error {
	if err := builderSimulationContextErr(ctx, "read workload manifest blob"); err != nil {
		return err
	}
	blob, err := resolver.ReadTreeBlob(ctx, request.Target.RepositoryRoot, request.Target.TreeSHA, request.Target.WorkloadManifest.RepoRelativePath)
	if err != nil {
		return fmt.Errorf("read workload manifest blob: %w", err)
	}
	if err := builderSimulationContextErr(ctx, "read workload manifest blob"); err != nil {
		return err
	}
	return validateBuilderSimulationManifestBlob(blob, request)
}

// validateBuilderSimulationManifestBlob 校验 regular blob、字节摘要与内部 manifest identity。
func validateBuilderSimulationManifestBlob(blob GitTreeBlob, request BuilderSimulationRequest) error {
	if blob.Mode != "100644" && blob.Mode != "100755" {
		return errors.New("workload manifest is not a regular target-tree blob")
	}
	if len(blob.Data) == 0 || bytesDigest(blob.Data) != request.Target.WorkloadManifest.SHA256 {
		return errors.New("workload manifest blob digest does not match the bound target tree")
	}
	manifest, err := DecodeRepositoryWorkloadManifest(blob.Data)
	if err != nil {
		return fmt.Errorf("decode workload manifest from target tree: %w", err)
	}
	if manifest.SchemaVersion != request.Target.WorkloadManifest.SchemaVersion || manifest.RepositoryID != request.Target.RepositoryID {
		return errors.New("workload manifest identity does not match the bound target")
	}
	return nil
}

func builderSimulationContextErr(ctx context.Context, operation string) error {
	if ctx == nil {
		return errors.New("builder simulation binding context is nil")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return nil
}

func builderSimulationEngineBindingDigest(engine BuilderSimulationEngine) (string, error) {
	material := builderSimulationEngineBindingMaterial{
		RepositoryID: engine.RepositoryID, ObjectFormat: engine.ObjectFormat,
		CommitSHA: engine.CommitSHA, TreeSHA: engine.TreeSHA,
		ExpectedBuilderDigest:   engine.ExpectedBuilderDigest,
		ExpectedToolchainDigest: engine.ExpectedToolchainDigest,
		ProtocolABIDigest:       engine.ProtocolABIDigest,
	}
	return builderSimulationFramedJSONDigest(BuilderSimulationEngineBindingDigestDomain, material)
}

func builderSimulationTargetBindingDigest(target BuilderSimulationTarget) (string, error) {
	material := builderSimulationTargetBindingMaterial{
		RepositoryID: target.RepositoryID, ObjectFormat: target.ObjectFormat,
		CommitSHA: target.CommitSHA, TreeSHA: target.TreeSHA,
		ManifestPath:      target.WorkloadManifest.RepoRelativePath,
		ManifestSHA256:    target.WorkloadManifest.SHA256,
		ManifestSchema:    target.WorkloadManifest.SchemaVersion,
		ManifestABIDigest: target.WorkloadManifest.ABIDigest,
	}
	return builderSimulationFramedJSONDigest(BuilderSimulationTargetBindingDigestDomain, material)
}

func builderSimulationFramedJSONDigest(domain string, material any) (string, error) {
	encoded, err := json.Marshal(material)
	if err != nil {
		return "", fmt.Errorf("encode builder simulation binding: %w", err)
	}
	return repositoryFramedDigest(domain, encoded), nil
}
