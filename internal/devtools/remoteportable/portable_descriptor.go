package remoteportable

import "context"

const (
	// PortableRunRequestSchemaVersion identifies the external operation/locator request.
	PortableRunRequestSchemaVersion = "remote-ci-run-request/v1"
	// PortableRunBindingDigestDomain separates external locator and actual engine facts.
	PortableRunBindingDigestDomain = "remote-ci-run-binding/v1"
)

// GitObjectResolver 绑定一个显式 repository root 后读取 exact commit/tree/blob。
type GitObjectResolver interface {
	ResolveCommitTree(context.Context, string) (string, error)
	ReadTreeBlob(context.Context, string, string) (GitTreeBlob, error)
	RepositoryFacts(context.Context) (GitRepositoryFacts, error)
}

// GitRepositoryFacts is the semantic repository object-format fact observed by composition.
type GitRepositoryFacts struct {
	ObjectFormat string `json:"object_format"`
}

// GitTreeBlob is a regular blob read from an exact Git tree.
type GitTreeBlob struct {
	Mode string
	Data []byte
}

// PortableRunInput 是 execute/validate 的外部 binding descriptor。
type PortableRunInput struct {
	SchemaVersion      string `json:"schema_version"`
	ContractID         string `json:"contract_id"`
	OperationID        string `json:"operation_id"`
	RepositoryID       string `json:"repository_id"`
	ObjectFormat       string `json:"object_format"`
	CommitSHA          string `json:"commit_sha"`
	TreeSHA            string `json:"tree_sha"`
	ManifestPath       string `json:"manifest_path"`
	ManifestBlobSHA256 string `json:"manifest_blob_sha256"`
}

// PortableEngineFacts 是 composition 现场计算的 actual engine identity，不来自 descriptor。
type PortableEngineFacts struct {
	BinarySHA256      string   `json:"binary_sha256"`
	ProtocolABIDigest string   `json:"protocol_abi_digest"`
	ToolchainDigest   string   `json:"toolchain_digest"`
	Capabilities      []string `json:"capabilities"`
}

// BoundPortableRun 是 exact tree blob 复核后的不可变 binding。
type BoundPortableRun struct {
	Manifest           PortableManifest    `json:"manifest"`
	OperationID        string              `json:"operation_id"`
	EngineFacts        PortableEngineFacts `json:"engine_facts"`
	ManifestDigest     string              `json:"manifest_digest"`
	ManifestBlobSHA256 string              `json:"manifest_blob_sha256"`
	RepositoryID       string              `json:"repository_id"`
	ObjectFormat       string              `json:"object_format"`
	CommitSHA          string              `json:"commit_sha"`
	TreeSHA            string              `json:"tree_sha"`
	ManifestPath       string              `json:"manifest_path"`
	BindingSHA256      string              `json:"binding_sha256"`
}

const (
	// PortableValidationResultSchema 是 validate stdout 的命名 wire schema。
	PortableValidationResultSchema = "remote-ci-validation-result/v1"
	// PortableValidationResultStatusValidated 是成功绑定但尚未执行的状态。
	PortableValidationResultStatusValidated = "validated"
	// PortableValidationResultAuthority 是 validate 的本地非权威声明。
	PortableValidationResultAuthority = "LOCAL_NON_AUTHORITATIVE"
)

// PortableValidationResult 是 validate stdout 的唯一 named canonical DTO。
type PortableValidationResult struct {
	SchemaVersion         string              `json:"schema_version"`
	Status                string              `json:"status"`
	ContractID            string              `json:"contract_id"`
	Authority             string              `json:"authority"`
	ExecutionPoolContract string              `json:"execution_pool_contract"`
	OperationID           string              `json:"operation_id"`
	RepositoryID          string              `json:"repository_id"`
	ObjectFormat          string              `json:"object_format"`
	TargetCommitSHA       string              `json:"target_commit_sha"`
	TargetTreeSHA         string              `json:"target_tree_sha"`
	ManifestPath          string              `json:"manifest_path"`
	ManifestBlobSHA256    string              `json:"manifest_blob_sha256"`
	ManifestDigest        string              `json:"manifest_digest"`
	CatalogDigest         string              `json:"catalog_digest"`
	WorkloadCount         int                 `json:"workload_count"`
	LinuxWorkloadCount    int                 `json:"linux_workload_count"`
	WindowsWorkloadCount  int                 `json:"windows_workload_count"`
	EngineFacts           PortableEngineFacts `json:"engine_facts"`
	DescriptorSHA256      string              `json:"descriptor_sha256"`
	BindingSHA256         string              `json:"binding_sha256"`
}
