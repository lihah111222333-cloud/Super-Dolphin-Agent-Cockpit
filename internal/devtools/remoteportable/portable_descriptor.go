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
