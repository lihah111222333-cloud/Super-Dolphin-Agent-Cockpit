package remoteportable

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

// DecodePortableRunDescriptor 严格解码外部 manifest binding descriptor。
func DecodePortableRunDescriptor(data []byte) (PortableRunInput, error) {
	if len(data) == 0 || len(data) > PortableWorkloadManifestMaxBytes {
		return PortableRunInput{}, errors.New("portable manifest descriptor size is invalid")
	}
	if err := walkPortableManifestJSON(data); err != nil {
		return PortableRunInput{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var input PortableRunInput
	if err := decoder.Decode(&input); err != nil {
		return PortableRunInput{}, fmt.Errorf("decode portable manifest descriptor: %w", err)
	}
	if err := ensurePortableManifestEOF(decoder); err != nil {
		return PortableRunInput{}, err
	}
	if err := validatePortableRunInput(input); err != nil {
		return PortableRunInput{}, err
	}
	canonical, err := json.Marshal(input)
	if err != nil || !bytes.Equal(canonical, data) {
		return PortableRunInput{}, errors.New("portable manifest descriptor is not canonical")
	}
	return input, nil
}

// BindPortableRun 读取 exact target-tree manifest blob 并重算 raw blob digest。
func BindPortableRun(ctx context.Context, input PortableRunInput, resolver GitObjectResolver, engineFacts PortableEngineFacts) (BoundPortableRun, error) {
	if err := validatePortableRunContext(ctx, input, engineFacts); err != nil {
		return BoundPortableRun{}, err
	}
	engineFacts.Capabilities = slices.Clone(engineFacts.Capabilities)
	repositoryFacts, err := readPortableRepositoryFacts(ctx, resolver)
	if err != nil {
		return BoundPortableRun{}, err
	}
	if repositoryFacts.ObjectFormat != input.ObjectFormat {
		return BoundPortableRun{}, errors.New("portable repository facts do not match request")
	}
	decoded, err := readPortableManifest(ctx, input, resolver)
	if err != nil {
		return BoundPortableRun{}, err
	}
	if err := validatePortableBindingManifest(decoded, input, engineFacts); err != nil {
		return BoundPortableRun{}, err
	}
	manifestDigest, err := PortableManifestContentDigest(decoded)
	if err != nil {
		return BoundPortableRun{}, err
	}
	bindingDigest, err := portableDescriptorDigest(input, engineFacts)
	if err != nil {
		return BoundPortableRun{}, err
	}
	return BoundPortableRun{Manifest: decoded, OperationID: input.OperationID, EngineFacts: engineFacts, ManifestDigest: manifestDigest, ManifestBlobSHA256: input.ManifestBlobSHA256, RepositoryID: input.RepositoryID, ObjectFormat: input.ObjectFormat, CommitSHA: input.CommitSHA, TreeSHA: input.TreeSHA, ManifestPath: input.ManifestPath, BindingSHA256: bindingDigest}, nil
}

func readPortableRepositoryFacts(ctx context.Context, resolver GitObjectResolver) (GitRepositoryFacts, error) {
	if portableInterfaceNil(resolver) {
		return GitRepositoryFacts{}, errors.New("portable manifest Git resolver is required")
	}
	facts, err := resolver.RepositoryFacts(ctx)
	if err != nil {
		return GitRepositoryFacts{}, fmt.Errorf("read repository facts: %w", err)
	}
	return facts, nil
}

func readPortableManifest(ctx context.Context, input PortableRunInput, resolver GitObjectResolver) (PortableManifest, error) {
	if err := resolvePortableTargetTree(ctx, input, resolver); err != nil {
		return PortableManifest{}, err
	}
	blob, err := resolver.ReadTreeBlob(ctx, input.TreeSHA, input.ManifestPath)
	if err != nil {
		return PortableManifest{}, fmt.Errorf("read target manifest blob: %w", err)
	}
	if !validPortableRegularBlob(blob.Mode) {
		return PortableManifest{}, errors.New("target manifest blob is not regular")
	}
	if portableBytesDigest(blob.Data) != input.ManifestBlobSHA256 {
		return PortableManifest{}, errors.New("target manifest raw blob digest does not match binding")
	}
	decoded, err := DecodePortableManifest(blob.Data)
	if err != nil {
		return PortableManifest{}, fmt.Errorf("decode target manifest blob: %w", err)
	}
	return decoded, nil
}

func resolvePortableTargetTree(ctx context.Context, input PortableRunInput, resolver GitObjectResolver) error {
	if portableInterfaceNil(resolver) {
		return errors.New("portable manifest Git resolver is required")
	}
	resolvedTree, err := resolver.ResolveCommitTree(ctx, input.CommitSHA)
	if err != nil {
		return fmt.Errorf("resolve target commit tree: %w", err)
	}
	if resolvedTree != input.TreeSHA || !validPortableOID(resolvedTree) {
		return errors.New("target commit does not resolve to the bound tree")
	}
	return nil
}

func validPortableRegularBlob(mode string) bool {
	return mode == "100644" || mode == "100755"
}

func validatePortableBindingManifest(manifest PortableManifest, input PortableRunInput, engineFacts PortableEngineFacts) error {
	if manifest.Target.RepositoryID != input.RepositoryID || manifest.Target.ObjectFormat != input.ObjectFormat {
		return errors.New("target manifest blob does not match descriptor manifest")
	}
	return validatePortableEnginePolicy(manifest.Engine, engineFacts)
}

func validatePortableRunInput(input PortableRunInput) error {
	if input.SchemaVersion != PortableRunRequestSchemaVersion || input.ContractID != PortableManifestContractID {
		return errors.New("portable run request schema or contract identity is invalid")
	}
	if err := validatePortableOperationID(input.OperationID); err != nil {
		return err
	}
	if !validPortableDigest(input.ManifestBlobSHA256) {
		return errors.New("manifest_blob_sha256 is invalid")
	}
	return validatePortableRunTarget(input)
}

func validatePortableRunTarget(input PortableRunInput) error {
	if !validPortableObjectFormat(input.ObjectFormat) {
		return errors.New("portable descriptor target identity is invalid")
	}
	if !validPortableOIDForFormat(input.CommitSHA, input.ObjectFormat) || !validPortableOIDForFormat(input.TreeSHA, input.ObjectFormat) {
		return errors.New("portable descriptor target identity is invalid")
	}
	if err := validatePortableIdentifier(input.RepositoryID, "repository_id"); err != nil {
		return errors.New("portable descriptor target locator is invalid")
	}
	if !validPortableRepoRelativePath(input.ManifestPath) {
		return errors.New("portable descriptor target locator is invalid")
	}
	return nil
}

func validPortableObjectFormat(value string) bool {
	return value == "sha1" || value == "sha256"
}

func validatePortableRunContext(ctx context.Context, input PortableRunInput, engineFacts PortableEngineFacts) error {
	if ctx == nil {
		return errors.New("portable manifest binding context is nil")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("portable manifest binding context: %w", err)
	}
	if err := validatePortableRunInput(input); err != nil {
		return err
	}
	return validatePortableEngineFacts(engineFacts)
}

func validatePortableEngineFacts(facts PortableEngineFacts) error {
	if !validPortableDigest(facts.BinarySHA256) || !validPortableDigest(facts.ProtocolABIDigest) || !validPortableDigest(facts.ToolchainDigest) || facts.Capabilities == nil {
		return errors.New("portable actual engine facts are invalid")
	}
	for index, capability := range facts.Capabilities {
		if !validPortableToken(capability) || index > 0 && facts.Capabilities[index-1] >= capability {
			return errors.New("portable actual engine capabilities must be sorted and unique")
		}
	}
	return nil
}

func validatePortableEnginePolicy(policy PortableManifestEngine, facts PortableEngineFacts) error {
	if policy.RequiredProtocolABIDigest != facts.ProtocolABIDigest || policy.RequiredToolchainDigest != facts.ToolchainDigest {
		return errors.New("portable actual engine protocol or toolchain facts do not satisfy manifest policy")
	}
	for _, required := range policy.RequiredCapabilities {
		if !slices.Contains(facts.Capabilities, required) {
			return errors.New("portable actual engine capabilities do not satisfy manifest policy")
		}
	}
	return nil
}

func portableDescriptorDigest(input PortableRunInput, facts PortableEngineFacts) (string, error) {
	locator := struct {
		SchemaVersion      string   `json:"schema_version"`
		ContractID         string   `json:"contract_id"`
		OperationID        string   `json:"operation_id"`
		RepositoryID       string   `json:"repository_id"`
		ObjectFormat       string   `json:"object_format"`
		CommitSHA          string   `json:"commit_sha"`
		TreeSHA            string   `json:"tree_sha"`
		ManifestPath       string   `json:"manifest_path"`
		ManifestBlobSHA256 string   `json:"manifest_blob_sha256"`
		EngineBinarySHA256 string   `json:"engine_binary_sha256"`
		ProtocolABIDigest  string   `json:"protocol_abi_digest"`
		ToolchainDigest    string   `json:"toolchain_digest"`
		Capabilities       []string `json:"capabilities"`
	}{input.SchemaVersion, input.ContractID, input.OperationID, input.RepositoryID, input.ObjectFormat, input.CommitSHA, input.TreeSHA, input.ManifestPath, input.ManifestBlobSHA256, facts.BinarySHA256, facts.ProtocolABIDigest, facts.ToolchainDigest, facts.Capabilities}
	encoded, err := json.Marshal(locator)
	if err != nil {
		return "", fmt.Errorf("encode portable manifest binding: %w", err)
	}
	return portableFramedDigest(PortableRunBindingDigestDomain, encoded), nil
}
