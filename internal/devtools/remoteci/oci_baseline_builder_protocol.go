package remoteci

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// OCIBaselineBuilderRequestSchemaVersion is the sole admitted OCI baseline builder request schema.
const OCIBaselineBuilderRequestSchemaVersion uint32 = 4

// OCIBaselineBuilderResultSchemaVersion is the sole admitted OCI baseline builder result schema.
const OCIBaselineBuilderResultSchemaVersion uint32 = 5

// OCIBuilderRefreshReceiptArtifactSchemaVersion is the sole admitted schema for
// the generator-owned refresh build receipt artifact exported from BuildKit.
const OCIBuilderRefreshReceiptArtifactSchemaVersion uint32 = 2

// OCIBuilderRefreshReceiptArtifact is exported by the Dockerfile's dedicated
// refresh-build-receipt target. It is deliberately distinct from human build
// logs: authority is derived only from this structured build artifact.
type OCIBuilderRefreshReceiptArtifact struct {
	SchemaVersion      uint32                               `json:"schema_version"`
	SourceTree         string                               `json:"source_tree"`
	AcceptedSnapshotID string                               `json:"accepted_snapshot_id"`
	PlanDigest         string                               `json:"plan_digest"`
	RefreshReceipts    []cicontract.RefreshCheckObservation `json:"refresh_receipts"`
}

// Validate keeps the exported artifact independently strict before its
// request-specific identity is checked by DecodeOCIBuilderRefreshReceiptArtifact.
func (artifact OCIBuilderRefreshReceiptArtifact) Validate() error {
	if artifact.SchemaVersion != OCIBuilderRefreshReceiptArtifactSchemaVersion || strings.TrimSpace(artifact.SourceTree) == "" || strings.TrimSpace(artifact.AcceptedSnapshotID) == "" || strings.TrimSpace(artifact.PlanDigest) == "" {
		return errors.New("OCI builder refresh build receipt artifact identity is incomplete")
	}
	if err := cicontract.ValidateRefreshChecksObservedPass(artifact.RefreshReceipts); err != nil {
		return fmt.Errorf("OCI builder refresh build receipt artifact checks: %w", err)
	}
	return nil
}

// OCIBaselineBuilderRequest binds an ECI image refresh to immutable source,
// plans, toolchain, and cache inputs. It deliberately contains no retired
// cache-bundle, host-builder, or second-authority fields.
type OCIBaselineBuilderRequest struct {
	SchemaVersion           uint32                         `json:"schema_version"`
	JobID                   string                         `json:"job_id"`
	TransferMode            cicontract.RefreshTransferMode `json:"transfer_mode"`
	ParentGeneration        uint64                         `json:"parent_generation"`
	ParentStateSHA256       string                         `json:"parent_state_sha256"`
	OutputRepository        string                         `json:"output_repository"`
	ParentImage             string                         `json:"parent_image"`
	ParentImageCacheID      string                         `json:"parent_image_cache_id"`
	ParentImageSnapshotID   string                         `json:"parent_image_snapshot_id"`
	ParentSourceManifest    string                         `json:"parent_source_manifest_digest"`
	ParentSourceImagePath   string                         `json:"parent_source_image_path"`
	ParentSourceClosure     string                         `json:"parent_source_closure_digest"`
	TargetCommit            string                         `json:"target_commit"`
	TargetTree              string                         `json:"target_tree"`
	TargetSourceManifest    string                         `json:"target_source_manifest_digest"`
	TargetSourceClosure     string                         `json:"target_source_closure_digest"`
	ImageInputDigest        string                         `json:"image_input_digest"`
	PolicyDigest            string                         `json:"policy_digest"`
	ToolchainDigest         string                         `json:"toolchain_digest"`
	Platform                string                         `json:"platform"`
	RuntimeDependencyDigest string                         `json:"runtime_dependency_digest"`
	DeltaArchiveKey         string                         `json:"delta_archive_key"`
	DeltaArchiveSHA256      string                         `json:"delta_archive_sha256"`
	DeltaArchiveSize        int64                          `json:"delta_archive_size"`
	JobKey                  string                         `json:"job_key"`
}

// OCIBaselineBuilderResult is the terminal-log receipt for one ECI OCI image refresh.
type OCIBaselineBuilderResult struct {
	SchemaVersion           uint32                               `json:"schema_version"`
	JobID                   string                               `json:"job_id"`
	TransferMode            cicontract.RefreshTransferMode       `json:"transfer_mode"`
	ParentGeneration        uint64                               `json:"parent_generation"`
	ParentStateSHA256       string                               `json:"parent_state_sha256"`
	OutputRepository        string                               `json:"output_repository"`
	ParentImage             string                               `json:"parent_image"`
	ParentImageCacheID      string                               `json:"parent_image_cache_id"`
	ParentImageSnapshotID   string                               `json:"parent_image_snapshot_id"`
	ParentSourceManifest    string                               `json:"parent_source_manifest_digest"`
	ParentSourceImagePath   string                               `json:"parent_source_image_path"`
	ParentSourceClosure     string                               `json:"parent_source_closure_digest"`
	TargetCommit            string                               `json:"target_commit"`
	TargetTree              string                               `json:"target_tree"`
	TargetSourceManifest    string                               `json:"target_source_manifest_digest"`
	TargetSourceClosure     string                               `json:"target_source_closure_digest"`
	ImageInputDigest        string                               `json:"image_input_digest"`
	PolicyDigest            string                               `json:"policy_digest"`
	ToolchainDigest         string                               `json:"toolchain_digest"`
	Platform                string                               `json:"platform"`
	RuntimeDependencyDigest string                               `json:"runtime_dependency_digest"`
	DeltaArchiveKey         string                               `json:"delta_archive_key"`
	DeltaArchiveSHA256      string                               `json:"delta_archive_sha256"`
	DeltaArchiveSize        int64                                `json:"delta_archive_size"`
	RefreshReceipts         []cicontract.RefreshCheckObservation `json:"refresh_receipts"`
	JobKey                  string                               `json:"job_key"`
	Repository              string                               `json:"repository"`
	Image                   string                               `json:"image"`
	ConfigDigest            string                               `json:"config_digest"`
}

// Validate rejects incomplete, mutable, or cross-job OCI builder requests.
func (request OCIBaselineBuilderRequest) Validate() error {
	if request.SchemaVersion != OCIBaselineBuilderRequestSchemaVersion || request.TransferMode != cicontract.RefreshTransferAcceptedSnapshotDelta || !remoteIDPattern.MatchString(request.JobID) || request.ParentGeneration == 0 ||
		!baselineOIDPattern.MatchString(request.TargetCommit) || !baselineOIDPattern.MatchString(request.TargetTree) ||
		ValidateOCIOutputRepository(request.OutputRepository) != nil ||
		ValidateOCIRefreshImage(request.ParentImage) != nil || cicontract.ValidateTargetPlatform(request.Platform) != nil || request.DeltaArchiveSize <= 0 || request.DeltaArchiveSize > 4<<30 {
		return errors.New("remote OCI baseline builder request identity is invalid")
	}
	if !validImageCacheIdentifier(request.ParentImageCacheID) || !validImageCacheIdentifier(request.ParentImageSnapshotID) || request.ParentSourceImagePath != cicontract.SourceSnapshotManifestPath {
		return errors.New("remote OCI baseline builder ImageCache authority is invalid")
	}
	if err := cicontract.ValidateIncrementalRefreshTransfer(cicontract.RefreshTransferAcceptedSnapshotDelta, request.ParentGeneration, request.ParentImageSnapshotID, request.DeltaArchiveSHA256); err != nil {
		return fmt.Errorf("remote OCI baseline builder incremental transfer: %w", err)
	}
	for name, value := range map[string]string{
		"parent state": request.ParentStateSHA256, "parent source manifest": request.ParentSourceManifest, "parent source closure": request.ParentSourceClosure,
		"target source manifest": request.TargetSourceManifest, "target source closure": request.TargetSourceClosure, "image input": request.ImageInputDigest, "policy": request.PolicyDigest, "delta archive": request.DeltaArchiveSHA256,
		"toolchain": request.ToolchainDigest, "runtime dependency": request.RuntimeDependencyDigest,
	} {
		if !remoteDigestPattern.MatchString(value) {
			return fmt.Errorf("remote OCI baseline builder request %s digest is invalid", name)
		}
	}
	return validateOCIBaselineBuilderKeys(request.JobID, request.DeltaArchiveKey, request.JobKey)
}

func validateOCIBaselineBuilderKeys(jobID, deltaArchiveKey, jobKey string) error {
	keys := []struct{ key, suffix string }{
		{deltaArchiveKey, ".snapshot.delta.tar"}, {jobKey, ".job.json"},
	}
	prefix := ""
	for _, item := range keys {
		if item.key == "" || path.Dir(item.key) == "." || path.Base(path.Dir(item.key)) != jobID {
			return errors.New("remote OCI baseline builder object key is not bound to job_id")
		}
		if prefix == "" {
			prefix = path.Dir(item.key) + "/"
		}
		if err := validateObjectBinding(item.key, strings.Repeat("0", sha256.Size*2), item.suffix, prefix); err != nil {
			return fmt.Errorf("remote OCI baseline builder object key: %w", err)
		}
	}
	return nil
}

// Validate rejects incomplete or mutable OCI image build receipts.
func (result OCIBaselineBuilderResult) Validate() error {
	request := OCIBaselineBuilderRequest{
		SchemaVersion: OCIBaselineBuilderRequestSchemaVersion, JobID: result.JobID, TransferMode: result.TransferMode, ParentGeneration: result.ParentGeneration, ParentStateSHA256: result.ParentStateSHA256, OutputRepository: result.OutputRepository,
		ParentImage: result.ParentImage, ParentImageCacheID: result.ParentImageCacheID, ParentImageSnapshotID: result.ParentImageSnapshotID, ParentSourceManifest: result.ParentSourceManifest, ParentSourceImagePath: result.ParentSourceImagePath, ParentSourceClosure: result.ParentSourceClosure,
		TargetCommit: result.TargetCommit, TargetTree: result.TargetTree, TargetSourceManifest: result.TargetSourceManifest, TargetSourceClosure: result.TargetSourceClosure, ImageInputDigest: result.ImageInputDigest, PolicyDigest: result.PolicyDigest, ToolchainDigest: result.ToolchainDigest, Platform: result.Platform,
		RuntimeDependencyDigest: result.RuntimeDependencyDigest, DeltaArchiveKey: result.DeltaArchiveKey, DeltaArchiveSHA256: result.DeltaArchiveSHA256, DeltaArchiveSize: result.DeltaArchiveSize, JobKey: result.JobKey,
	}
	if result.SchemaVersion != OCIBaselineBuilderResultSchemaVersion || request.Validate() != nil || ValidateOCIOutputRepository(result.Repository) != nil || ValidateOCIRefreshImage(result.Image) != nil {
		return errors.New("remote OCI baseline builder result identity is invalid")
	}
	imageRepository, _, _ := strings.Cut(result.Image, "@")
	if imageRepository != result.Repository {
		return errors.New("remote OCI baseline builder result repository does not match image")
	}
	for name, value := range map[string]string{"config": result.ConfigDigest} {
		if !remoteDigestPattern.MatchString(value) {
			return fmt.Errorf("remote OCI baseline builder result %s digest is invalid", name)
		}
	}
	if err := validateOCIBuilderRefreshReceipts(request, result.RefreshReceipts); err != nil {
		return fmt.Errorf("remote OCI baseline builder result checks: %w", err)
	}
	return nil
}

func validateOCIBuilderRefreshReceipts(request OCIBaselineBuilderRequest, receipts []cicontract.RefreshCheckObservation) error {
	if err := cicontract.ValidateRefreshChecksObservedPass(receipts); err != nil {
		return err
	}
	for _, receipt := range receipts {
		if receipt.SourceTree != request.TargetTree || receipt.AcceptedSnapshotID != request.ParentImageSnapshotID || receipt.PlanDigest != request.ImageInputDigest {
			return errors.New("OCI builder refresh build receipt does not match immutable request identity")
		}
	}
	return nil
}

// DecodeOCIBuilderRefreshReceiptArtifact strictly decodes the BuildKit local
// export and binds every observation to the immutable submitted request.
func DecodeOCIBuilderRefreshReceiptArtifact(data []byte, request OCIBaselineBuilderRequest) ([]cicontract.RefreshCheckObservation, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	var artifact OCIBuilderRefreshReceiptArtifact
	if err := gate.DecodeStrictJSON(data, &artifact); err != nil {
		return nil, fmt.Errorf("decode OCI builder refresh build receipt artifact: %w", err)
	}
	if artifact.SchemaVersion != OCIBuilderRefreshReceiptArtifactSchemaVersion || artifact.SourceTree != request.TargetTree || artifact.AcceptedSnapshotID != request.ParentImageSnapshotID || artifact.PlanDigest != request.ImageInputDigest {
		return nil, errors.New("OCI builder refresh build receipt artifact does not match immutable request identity")
	}
	if err := validateOCIBuilderRefreshReceipts(request, artifact.RefreshReceipts); err != nil {
		return nil, fmt.Errorf("validate OCI builder refresh build receipt artifact: %w", err)
	}
	return artifact.RefreshReceipts, nil
}

// ValidateOCIOutputRepository validates the generic, non-ACR OCI successor target.
func ValidateOCIOutputRepository(repository string) error {
	if repository == "" || repository != strings.ToLower(repository) || strings.ContainsAny(repository, "@ \t\r\n\\?#") || strings.Contains(repository, "://") || cicontract.ValidateNonACRRegistryHost(repository) != nil {
		return errors.New("OCI output repository is invalid or uses a forbidden Aliyun registry host")
	}
	return nil
}

// ValidateOCIRefreshImage accepts immutable generic OCI images but rejects ACR hosts.
func ValidateOCIRefreshImage(image string) error {
	if !validRemoteImageReference(image) {
		return errors.New("OCI refresh image is invalid or uses a forbidden Aliyun registry host")
	}
	return nil
}

// ValidateAgainst rejects output that is not bound exactly to the submitted image build request.
func (result OCIBaselineBuilderResult) ValidateAgainst(request OCIBaselineBuilderRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if err := result.Validate(); err != nil {
		return err
	}
	if result.JobID != request.JobID || result.TransferMode != request.TransferMode || result.ParentGeneration != request.ParentGeneration || result.ParentStateSHA256 != request.ParentStateSHA256 || result.OutputRepository != request.OutputRepository ||
		result.ParentImage != request.ParentImage || result.ParentImageCacheID != request.ParentImageCacheID || result.ParentImageSnapshotID != request.ParentImageSnapshotID || result.ParentSourceManifest != request.ParentSourceManifest || result.ParentSourceImagePath != request.ParentSourceImagePath || result.ParentSourceClosure != request.ParentSourceClosure ||
		result.TargetCommit != request.TargetCommit || result.TargetTree != request.TargetTree || result.TargetSourceManifest != request.TargetSourceManifest || result.TargetSourceClosure != request.TargetSourceClosure || result.ImageInputDigest != request.ImageInputDigest || result.PolicyDigest != request.PolicyDigest ||
		result.ToolchainDigest != request.ToolchainDigest || result.Platform != request.Platform || result.RuntimeDependencyDigest != request.RuntimeDependencyDigest ||
		result.DeltaArchiveKey != request.DeltaArchiveKey || result.DeltaArchiveSHA256 != request.DeltaArchiveSHA256 || result.DeltaArchiveSize != request.DeltaArchiveSize || result.JobKey != request.JobKey {
		return errors.New("remote OCI baseline builder result does not match request")
	}
	if err := cicontract.ValidateDeltaRebuild(cicontract.RefreshTransferAcceptedSnapshotDelta, result.ParentGeneration, result.ParentImageSnapshotID, result.DeltaArchiveSHA256, result.TargetTree, result.TargetSourceClosure); err != nil {
		return fmt.Errorf("remote OCI baseline builder delta rebuild: %w", err)
	}
	return nil
}

// EncodeOCIBaselineBuilderRequest validates and encodes a canonical builder request.
func EncodeOCIBaselineBuilderRequest(request OCIBaselineBuilderRequest) ([]byte, string, error) {
	if err := request.Validate(); err != nil {
		return nil, "", err
	}
	data, err := json.Marshal(request)
	if err != nil {
		return nil, "", fmt.Errorf("encode remote OCI baseline builder request: %w", err)
	}
	return data, fmt.Sprintf("sha256:%x", sha256.Sum256(data)), nil
}

// DecodeOCIBaselineBuilderRequest strictly decodes an ECI builder request.
func DecodeOCIBaselineBuilderRequest(data []byte) (OCIBaselineBuilderRequest, error) {
	var request OCIBaselineBuilderRequest
	if err := gate.DecodeStrictJSON(data, &request); err != nil {
		return request, fmt.Errorf("decode remote OCI baseline builder request: %w", err)
	}
	return request, nil
}

// EncodeOCIBaselineBuilderResult validates and encodes a canonical builder receipt.
func EncodeOCIBaselineBuilderResult(result OCIBaselineBuilderResult) ([]byte, string, error) {
	if err := result.Validate(); err != nil {
		return nil, "", err
	}
	data, err := json.Marshal(result)
	if err != nil {
		return nil, "", fmt.Errorf("encode remote OCI baseline builder result: %w", err)
	}
	return data, fmt.Sprintf("sha256:%x", sha256.Sum256(data)), nil
}

// DecodeOCIBaselineBuilderResult strictly decodes and request-binds an ECI builder receipt.
func DecodeOCIBaselineBuilderResult(data []byte, request OCIBaselineBuilderRequest) (OCIBaselineBuilderResult, error) {
	var result OCIBaselineBuilderResult
	if err := gate.DecodeStrictJSON(data, &result); err != nil {
		return result, fmt.Errorf("decode remote OCI baseline builder result: %w", err)
	}
	if err := result.ValidateAgainst(request); err != nil {
		return result, err
	}
	return result, nil
}
