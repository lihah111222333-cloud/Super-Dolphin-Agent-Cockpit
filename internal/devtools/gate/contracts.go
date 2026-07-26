package gate

import "time"

// SourceKind identifies the single Git object form carried by a SourceSpec.
type SourceKind string

const (
	SourceKindCommit SourceKind = "commit"
	SourceKindTree   SourceKind = "tree"
	SourceKindRange  SourceKind = "range"
)

// BaseKind identifies the base semantics of a range source.
type BaseKind string

const (
	BaseKindCommit    BaseKind = "commit"
	BaseKindEmptyTree BaseKind = "empty_tree"
)

// UpdateKind identifies how a Git ref moves.
type UpdateKind string

const (
	UpdateKindCreate      UpdateKind = "create"
	UpdateKindFastForward UpdateKind = "fast_forward"
	UpdateKindForce       UpdateKind = "force"
)

// GitObjectFormat identifies the hash algorithm and OID width of a Git repository.
type GitObjectFormat string

const (
	GitObjectFormatSHA1   GitObjectFormat = "sha1"
	GitObjectFormatSHA256 GitObjectFormat = "sha256"
)

// CommitSource identifies a committed Git source.
type CommitSource struct {
	SHA string `json:"sha"`
}

// TreeSource identifies an explicit Git tree and its optional parent commit.
type TreeSource struct {
	SHA             string `json:"sha"`
	ParentCommitSHA string `json:"parent_commit_sha,omitempty"`
}

// RangeSource identifies one non-delete Git ref update.
type RangeSource struct {
	BaseKind          BaseKind   `json:"base_kind"`
	BaseSHA           string     `json:"base_sha,omitempty"`
	HeadSHA           string     `json:"head_sha"`
	LocalRef          string     `json:"local_ref"`
	RemoteRef         string     `json:"remote_ref"`
	ObservedRemoteSHA string     `json:"observed_remote_sha"`
	UpdateKind        UpdateKind `json:"update_kind"`
}

// SourceSpec is the canonical tagged union for commit, tree, or range input.
type SourceSpec struct {
	Kind          SourceKind      `json:"kind"`
	ObjectFormat  GitObjectFormat `json:"object_format"`
	Commit        *CommitSource   `json:"commit,omitempty"`
	Tree          *TreeSource     `json:"tree,omitempty"`
	Range         *RangeSource    `json:"range,omitempty"`
	SourceTreeSHA string          `json:"source_tree_sha"`
}

// SignatureAlgorithm identifies the signer algorithm used by a trusted host component.
type SignatureAlgorithm string

const SignatureAlgorithmEd25519 SignatureAlgorithm = "ed25519"

// SignerIdentity identifies the host key and rotation epoch used for a signature.
type SignerIdentity struct {
	KeyID     string             `json:"key_id"`
	KeyEpoch  uint64             `json:"key_epoch"`
	Algorithm SignatureAlgorithm `json:"algorithm"`
}

// ImageIdentity binds execution to a complete OCI platform identity.
type ImageIdentity struct {
	Registry               string   `json:"registry"`
	OCIIndexDigest         string   `json:"oci_index_digest"`
	PlatformManifestDigest string   `json:"platform_manifest_digest"`
	ConfigDigest           string   `json:"config_digest"`
	RootFSDiffIDs          []string `json:"rootfs_diff_ids"`
	OS                     string   `json:"os"`
	Architecture           string   `json:"architecture"`
	Variant                string   `json:"variant,omitempty"`
}

// TrustedRunnerIdentity binds the runner binary, signer, and accepted policy.
type TrustedRunnerIdentity struct {
	BinaryDigest string         `json:"binary_digest"`
	Signer       SignerIdentity `json:"signer"`
	PolicyDigest string         `json:"policy_digest"`
}

// TrustedAdapterIdentity identifies the signed adapter allowed to consume a grant.
type TrustedAdapterIdentity struct {
	Name         string         `json:"name"`
	BinaryDigest string         `json:"binary_digest"`
	Signer       SignerIdentity `json:"signer"`
}

// ActionAudience identifies the external authority requested by a grant.
type ActionAudience string

const (
	ActionAudienceGitPush      ActionAudience = "git.push"
	ActionAudienceRelease      ActionAudience = "release"
	ActionAudienceImagePromote ActionAudience = "image.promote"
)

// GrantRequest binds an external action request to a receipt, owner, adapter, attempt, and nonce.
type GrantRequest struct {
	ReceiptID            string                 `json:"receipt_id"`
	ReceiptDigest        string                 `json:"receipt_digest"`
	RepoID               string                 `json:"repo_id"`
	InvocationID         string                 `json:"invocation_id"`
	InvocationOwner      string                 `json:"invocation_owner"`
	SubscriberCapability string                 `json:"subscriber_capability"`
	Adapter              TrustedAdapterIdentity `json:"adapter"`
	ProcessChallenge     string                 `json:"process_challenge"`
	SourceTreeSHA        string                 `json:"source_tree_sha"`
	Generation           uint64                 `json:"generation"`
	Audience             ActionAudience         `json:"audience"`
	ActionPolicy         string                 `json:"action_policy"`
	RemoteURL            string                 `json:"remote_url,omitempty"`
	Ref                  string                 `json:"ref,omitempty"`
	OldSHA               string                 `json:"old_sha,omitempty"`
	NewSHA               string                 `json:"new_sha,omitempty"`
	ReleaseRepository    string                 `json:"release_repository,omitempty"`
	ReleaseTag           string                 `json:"release_tag,omitempty"`
	ReleaseCommitSHA     string                 `json:"release_commit_sha,omitempty"`
	ReleaseAssets        []ReleaseAsset         `json:"release_assets,omitempty"`
	ActionAttemptID      string                 `json:"action_attempt_id"`
	RequestNonce         string                 `json:"request_nonce"`
	RequestedAt          time.Time              `json:"requested_at"`
	ExpiresAt            time.Time              `json:"expires_at"`
}

// ReleaseAsset binds one GitHub release asset to its normalized upload name and bytes.
type ReleaseAsset struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// GateStatus identifies an individual gate outcome.
type GateStatus string

const (
	GateStatusPassed    GateStatus = "passed"
	GateStatusFailed    GateStatus = "failed"
	GateStatusCancelled GateStatus = "cancelled"
	GateStatusTimeout   GateStatus = "timeout"
)

// GateResult records directly observed process evidence for one gate.
type GateResult struct {
	GateID      string     `json:"gate_id"`
	Status      GateStatus `json:"status"`
	ExitCode    int        `json:"exit_code"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt time.Time  `json:"completed_at"`
	ArgvDigest  string     `json:"argv_digest"`
	LogDigest   string     `json:"log_digest"`
}

// EvidenceKind identifies a directly observed evidence class.
type EvidenceKind string

const (
	EvidenceKindProcess EvidenceKind = "process"
	EvidenceKindDocker  EvidenceKind = "docker_inspect"
	EvidenceKindLog     EvidenceKind = "log"
)

// Evidence binds an evidence kind to its immutable digest.
type Evidence struct {
	Kind   EvidenceKind `json:"kind"`
	Digest string       `json:"digest"`
}

// ContainerResourceWitness records the bounded resource fields observed before container start.
type ContainerResourceWitness struct {
	SchemaVersion uint32 `json:"schema_version"`
	NanoCPUs      int64  `json:"nano_cpus"`
	MemoryBytes   int64  `json:"memory_bytes"`
	PidsLimit     int64  `json:"pids_limit"`
}

const ContainerResourceWitnessSchemaVersion uint32 = 1

// ContainerEvidence records the isolated execution and removal proof.
type ContainerEvidence struct {
	ContainerID           string                   `json:"container_id"`
	NetworkID             string                   `json:"network_id"`
	HostConfigDigest      string                   `json:"host_config_digest"`
	ResourceWitness       ContainerResourceWitness `json:"resource_witness"`
	ResourceWitnessDigest string                   `json:"resource_witness_digest"`
	NetworkPolicyDigest   string                   `json:"network_policy_digest"`
	Removed               bool                     `json:"removed"`
	NetworkRemoved        bool                     `json:"network_removed"`
}

// ResultStatus identifies the terminal authority of a result receipt.
type ResultStatus string

const (
	ResultStatusPassed            ResultStatus = "passed"
	ResultStatusFailed            ResultStatus = "failed"
	ResultStatusCancelled         ResultStatus = "cancelled"
	ResultStatusTimeout           ResultStatus = "timeout"
	ResultStatusInfraFailed       ResultStatus = "infra_failed"
	ResultStatusPassedStalePolicy ResultStatus = "passed_stale_policy"
)

// ResultReceiptSchemaVersion binds dynamically sized canonical shard receipts.
const (
	ResultReceiptSchemaVersion       uint32 = 3
	legacyResultReceiptSchemaVersion uint32 = 2
)

// ResultReceipt is the canonical signed audit result. It never authorizes an action by itself.
type ResultReceipt struct {
	SchemaVersion        uint32                  `json:"schema_version"`
	ReceiptID            string                  `json:"receipt_id"`
	RepoID               string                  `json:"repo_id"`
	InvocationID         string                  `json:"invocation_id"`
	Entrypoint           CIEntrypointID          `json:"entrypoint"`
	AuthorityOwner       CIEntrypointOwner       `json:"authority_owner"`
	AuthorityAttestation string                  `json:"authority_attestation"`
	Source               SourceSpec              `json:"source"`
	PlanDigest           string                  `json:"plan_digest"`
	PolicyDigest         string                  `json:"policy_digest"`
	Runner               TrustedRunnerIdentity   `json:"runner"`
	Image                ImageIdentity           `json:"image"`
	Generation           uint64                  `json:"generation"`
	StartedAt            time.Time               `json:"started_at"`
	CompletedAt          time.Time               `json:"completed_at"`
	Deadline             time.Time               `json:"deadline"`
	Status               ResultStatus            `json:"status"`
	GateResults          []GateResult            `json:"gate_results"`
	ShardReceipts        []ContainerShardReceipt `json:"shard_receipts"`
	Evidence             []Evidence              `json:"evidence"`
	Container            ContainerEvidence       `json:"container"`
	Signer               SignerIdentity          `json:"signer"`
	Signature            string                  `json:"signature"`
}

// ActionGrantState identifies the durable two-phase lifecycle of an action grant.
type ActionGrantState string

const (
	ActionGrantStateIssued   ActionGrantState = "issued"
	ActionGrantStateConsumed ActionGrantState = "consumed"
	ActionGrantStateExpired  ActionGrantState = "expired"
	ActionGrantStateRevoked  ActionGrantState = "revoked"
)

// ActionGrant is the canonical signed, single-use authorization for an external action.
type ActionGrant struct {
	SchemaVersion uint32           `json:"schema_version"`
	GrantID       string           `json:"grant_id"`
	Request       GrantRequest     `json:"request"`
	State         ActionGrantState `json:"state"`
	IssuedAt      time.Time        `json:"issued_at"`
	ExpiresAt     time.Time        `json:"expires_at"`
	ConsumedAt    *time.Time       `json:"consumed_at,omitempty"`
	RevokedAt     *time.Time       `json:"revoked_at,omitempty"`
	Signer        SignerIdentity   `json:"signer"`
	Signature     string           `json:"signature"`
}

const (
	AcceptedImageRecordSchemaVersion uint32 = 1
	PromotionRecordSchemaVersion     uint32 = 1
)

// AcceptedImageRecord 是 accepted image authority 的完整签名状态。
type AcceptedImageRecord struct {
	SchemaVersion        uint32                `json:"schema_version"`
	RepoID               string                `json:"repo_id"`
	TrustedRef           string                `json:"trusted_ref"`
	TrustedCommit        string                `json:"trusted_commit"`
	SourceTree           string                `json:"source_tree"`
	PolicyDigest         string                `json:"policy_digest"`
	ImageInputDigest     string                `json:"image_input_digest"`
	Image                ImageIdentity         `json:"image"`
	Runner               TrustedRunnerIdentity `json:"runner"`
	Generation           uint64                `json:"generation"`
	PreviousRecordDigest string                `json:"previous_record_digest,omitempty"`
	AcceptedAt           time.Time             `json:"accepted_at"`
	Signer               SignerIdentity        `json:"signer"`
	Signature            string                `json:"signature"`
}

// PromotionRecord 携带调用方观察到的 CAS 状态和下一份已签 authority 记录。
type PromotionRecord struct {
	SchemaVersion        uint32              `json:"schema_version"`
	ExpectedRecordDigest string              `json:"expected_record_digest"`
	ExpectedGeneration   uint64              `json:"expected_generation"`
	Next                 AcceptedImageRecord `json:"next"`
}
