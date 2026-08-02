package remoteci

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// OCIBaselineBuilderRequestSchemaVersion is the sole admitted OCI baseline builder request schema.
const OCIBaselineBuilderRequestSchemaVersion uint32 = 1

// OCIBaselineBuilderResultSchemaVersion is the sole admitted OCI baseline builder result schema.
const OCIBaselineBuilderResultSchemaVersion uint32 = 1

// OCIBaselineBuilderRequest binds an ECI image refresh to immutable source,
// plans, toolchain, and cache inputs. It deliberately contains no DataCache,
// seed, or local Docker authority.
type OCIBaselineBuilderRequest struct {
	SchemaVersion           uint32 `json:"schema_version"`
	JobID                   string `json:"job_id"`
	ContextKey              string `json:"context_key"`
	ContextSHA256           string `json:"context_sha256"`
	SourceArchiveSize       int64  `json:"source_archive_size"`
	RegistryRepository      string `json:"registry_repository"`
	ParentImage             string `json:"parent_image"`
	ImageCacheID            string `json:"image_cache_id"`
	ImageCacheSnapshotID    string `json:"image_cache_snapshot_id"`
	MainCommit              string `json:"main_commit"`
	MainTree                string `json:"main_tree"`
	ToolchainDigest         string `json:"toolchain_digest"`
	Platform                string `json:"platform"`
	RuntimeDependencyDigest string `json:"runtime_dependency_digest"`
	JobKey                  string `json:"job_key"`
}

// OCIBaselineBuilderResult is the terminal-log receipt for one ECI OCI image refresh.
type OCIBaselineBuilderResult struct {
	SchemaVersion           uint32 `json:"schema_version"`
	JobID                   string `json:"job_id"`
	ContextKey              string `json:"context_key"`
	ContextSHA256           string `json:"context_sha256"`
	RegistryRepository      string `json:"registry_repository"`
	ParentImage             string `json:"parent_image"`
	ImageCacheID            string `json:"image_cache_id"`
	ImageCacheSnapshotID    string `json:"image_cache_snapshot_id"`
	MainCommit              string `json:"main_commit"`
	MainTree                string `json:"main_tree"`
	ToolchainDigest         string `json:"toolchain_digest"`
	Platform                string `json:"platform"`
	RuntimeDependencyDigest string `json:"runtime_dependency_digest"`
	JobKey                  string `json:"job_key"`
	Repository              string `json:"repository"`
	Image                   string `json:"image"`
	ConfigDigest            string `json:"config_digest"`
	InputDigest             string `json:"input_digest"`
}

// Validate rejects incomplete, mutable, or cross-job OCI builder requests.
func (request OCIBaselineBuilderRequest) Validate() error {
	if request.SchemaVersion != OCIBaselineBuilderRequestSchemaVersion || !remoteIDPattern.MatchString(request.JobID) ||
		!baselineOIDPattern.MatchString(request.MainCommit) || !baselineOIDPattern.MatchString(request.MainTree) ||
		!validOCIRepository(request.RegistryRepository) ||
		!validRemoteImageReference(request.ParentImage) || request.Platform != "linux/amd64" || request.SourceArchiveSize <= 0 || request.SourceArchiveSize > 4<<30 {
		return errors.New("remote OCI baseline builder request identity is invalid")
	}
	if !validImageCacheIdentifier(request.ImageCacheID) || !validImageCacheIdentifier(request.ImageCacheSnapshotID) {
		return errors.New("remote OCI baseline builder ImageCache authority is invalid")
	}
	for name, value := range map[string]string{
		"context": request.ContextSHA256, "toolchain": request.ToolchainDigest,
		"runtime dependency": request.RuntimeDependencyDigest,
	} {
		if !remoteDigestPattern.MatchString(value) {
			return fmt.Errorf("remote OCI baseline builder request %s digest is invalid", name)
		}
	}
	return validateOCIBaselineBuilderKeys(request.JobID, request.ContextKey, request.JobKey)
}

func validateOCIBaselineBuilderKeys(jobID, contextKey, jobKey string) error {
	keys := []struct{ key, suffix string }{
		{contextKey, ".context.tar"}, {jobKey, ".job.json"},
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
		SchemaVersion: OCIBaselineBuilderRequestSchemaVersion, JobID: result.JobID, ContextKey: result.ContextKey, ContextSHA256: result.ContextSHA256, SourceArchiveSize: 1, RegistryRepository: result.RegistryRepository,
		ParentImage: result.ParentImage, ImageCacheID: result.ImageCacheID, ImageCacheSnapshotID: result.ImageCacheSnapshotID, MainCommit: result.MainCommit, MainTree: result.MainTree, ToolchainDigest: result.ToolchainDigest, Platform: result.Platform,
		RuntimeDependencyDigest: result.RuntimeDependencyDigest,
		JobKey:                  result.JobKey,
	}
	if result.SchemaVersion != OCIBaselineBuilderResultSchemaVersion || request.Validate() != nil || !validOCIRepository(result.Repository) || !validRemoteImageReference(result.Image) {
		return errors.New("remote OCI baseline builder result identity is invalid")
	}
	imageRepository, _, _ := strings.Cut(result.Image, "@")
	if imageRepository != result.Repository {
		return errors.New("remote OCI baseline builder result repository does not match image")
	}
	for name, value := range map[string]string{"config": result.ConfigDigest, "input": result.InputDigest} {
		if !remoteDigestPattern.MatchString(value) {
			return fmt.Errorf("remote OCI baseline builder result %s digest is invalid", name)
		}
	}
	return nil
}

func validOCIRepository(repository string) bool {
	return repository != "" && repository == strings.ToLower(repository) && !strings.ContainsAny(repository, "@ \t\r\n\\?#") && !strings.Contains(repository, "://")
}

// ValidateAgainst rejects output that is not bound exactly to the submitted image build request.
func (result OCIBaselineBuilderResult) ValidateAgainst(request OCIBaselineBuilderRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if err := result.Validate(); err != nil {
		return err
	}
	if result.JobID != request.JobID || result.ContextKey != request.ContextKey || result.ContextSHA256 != request.ContextSHA256 || result.RegistryRepository != request.RegistryRepository ||
		result.ParentImage != request.ParentImage || result.MainCommit != request.MainCommit || result.MainTree != request.MainTree ||
		result.ImageCacheID != request.ImageCacheID || result.ImageCacheSnapshotID != request.ImageCacheSnapshotID ||
		result.ToolchainDigest != request.ToolchainDigest || result.Platform != request.Platform ||
		result.RuntimeDependencyDigest != request.RuntimeDependencyDigest || result.InputDigest != request.ContextSHA256 ||
		result.JobKey != request.JobKey {
		return errors.New("remote OCI baseline builder result does not match request")
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
