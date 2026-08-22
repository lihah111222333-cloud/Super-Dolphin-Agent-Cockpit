// Package catalog owns the versioned mcp-lsp workload catalog contract.
package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	Schema        = "super-dolphin/mcp-lsp-workload-catalog/v1"
	ReceiptSchema = "super-dolphin/mcp-lsp-workload-receipt/v1"
	Path          = "scripts/mcp_lsp_workload_catalog.json"
)

const maxTimeoutSeconds = int64((1<<63 - 1) / int64(time.Second))

// TimeoutDuration 将目录中的秒数转换为受 time.Duration 上限约束的超时。
func TimeoutDuration(seconds int) (time.Duration, error) {
	if seconds <= 0 {
		return 0, errors.New("timeout_seconds must be positive")
	}
	if int64(seconds) > maxTimeoutSeconds {
		return 0, fmt.Errorf("timeout_seconds %d exceeds duration limit %d", seconds, maxTimeoutSeconds)
	}
	return time.Duration(seconds) * time.Second, nil
}

// Catalog is the only local workload decision source.
type Catalog struct {
	Schema        string     `json:"schema"`
	CatalogDigest string     `json:"catalog_digest"`
	Workloads     []Workload `json:"workloads"`
}

// Workload describes one canonical local or release prerequisite.
type Workload struct {
	ID                           string   `json:"id"`
	ImplementationStatus         string   `json:"implementation_status"`
	ProducerImplementationStatus string   `json:"producer_implementation_status"`
	RunnerTarget                 string   `json:"runner_target"`
	Platforms                    []string `json:"platforms"`
	TimeoutSeconds               int      `json:"timeout_seconds"`
	TriggerClass                 string   `json:"trigger_class"`
	ReceiptSchema                string   `json:"receipt_schema"`
	ProducerWorkflowPath         string   `json:"producer_workflow_path"`
	ProducerArtifactName         string   `json:"producer_artifact_name"`
	T6Blocking                   bool     `json:"t6_blocking"`
	ReleaseBlocking              bool     `json:"release_blocking"`
	ReceiptRequired              *bool    `json:"receipt_required"`
	Command                      []string `json:"command"`
}

// Receipt is the versioned local workload receipt consumed by catalog guards.
type Receipt struct {
	Schema                       string   `json:"schema"`
	WorkloadID                   string   `json:"workload_id"`
	CatalogDigest                string   `json:"catalog_digest"`
	RunnerTarget                 string   `json:"runner_target"`
	ProducerWorkflowPath         string   `json:"producer_workflow_path"`
	ProducerArtifactName         string   `json:"producer_artifact_name"`
	ProducerImplementationStatus string   `json:"producer_implementation_status"`
	ExecutionOrigin              string   `json:"execution_origin"`
	Platform                     string   `json:"platform"`
	TimeoutSeconds               int      `json:"timeout_seconds"`
	Command                      []string `json:"command"`
	StartedAt                    string   `json:"started_at"`
	FinishedAt                   string   `json:"finished_at"`
	Status                       string   `json:"status"`
	ExitCode                     int      `json:"exit_code"`
	// The following fields are required for the default-15m source E2E.  They
	// remain optional for the short local workloads, whose receipts predate the
	// root-cohort completion contract.
	GitHead                     string `json:"git_head"`
	SourceTreeDigest            string `json:"source_tree_digest"`
	CohortID                    string `json:"cohort_id"`
	RepositoryInstanceProofHash string `json:"repository_instance_proof_hash"`
	Epoch                       uint64 `json:"epoch"`
	DaemonOwnerReceiptHash      string `json:"daemon_owner_receipt_hash"`
	CompletionReceiptHash       string `json:"completion_receipt_hash"`
	// Remote authority fields are mandatory for a future producer, but no local
	// caller may synthesize them while the canonical artifact authority is N/V.
	RemoteRunID             string   `json:"remote_run_id"`
	RemoteJobID             string   `json:"remote_job_id"`
	RemoteArtifactName      string   `json:"remote_artifact_name"`
	RemoteArtifactDigest    string   `json:"remote_artifact_digest"`
	WorkloadStartedAt       string   `json:"workload_started_at"`
	WorkloadFinishedAt      string   `json:"workload_finished_at"`
	CompletionReceiptPath   string   `json:"completion_receipt_path"`
	ActionOrder             []string `json:"action_order"`
	ForwarderCountAfter     int      `json:"forwarder_count_after"`
	DaemonObservedAfter     bool     `json:"daemon_observed_after"`
	TelemetryIdentitiesGone bool     `json:"telemetry_identities_gone"`
	EndpointUnreachable     bool     `json:"endpoint_unreachable"`
	NativeOwnerReleased     bool     `json:"native_owner_released"`
	QuietWindowVerified     bool     `json:"quiet_window_verified"`
	NextEpoch               uint64   `json:"next_epoch"`
}

// Load 读取并校验仓库拥有的目录及其摘要。
func Load(repoRoot string) (Catalog, error) {
	path := filepath.Join(repoRoot, filepath.FromSlash(Path))
	raw, err := os.ReadFile(path)
	if err != nil {
		return Catalog{}, fmt.Errorf("read workload catalog %q: %w", path, err)
	}
	return decodeCatalog(raw, repoRoot)
}

// LoadAt 从受信 Git tree 读取目录，而不是从可变工作树读取目录。
// revision 必须已经由 gate 解析为完整 Git object id；不得接受用户提供的
// 任意 revision 表达式，以免 workload 决策与 candidate source 脱钩。
func LoadAt(repoRoot, revision string) (Catalog, error) {
	if strings.TrimSpace(repoRoot) == "" {
		return Catalog{}, errors.New("catalog repository root is required")
	}
	if !validGitOID(revision) {
		return Catalog{}, fmt.Errorf("catalog revision %q is not a resolved Git object id", revision)
	}
	raw, err := BoundedGitOutput(repoRoot, "show", "--format=", "--end-of-options", revision+":"+Path)
	if err != nil {
		return Catalog{}, fmt.Errorf("read workload catalog at Git tree %q: %w", revision, err)
	}
	document, err := decodeCatalogDocument(raw)
	if err != nil {
		return Catalog{}, err
	}
	validationRoot, cleanup, err := materializeCandidateProducerWorkflows(repoRoot, revision, document)
	if err != nil {
		return Catalog{}, err
	}
	defer cleanup()
	if err := Validate(document, raw, validationRoot); err != nil {
		return Catalog{}, err
	}
	return document, nil
}

func decodeCatalog(raw []byte, repoRoot string) (Catalog, error) {
	document, err := decodeCatalogDocument(raw)
	if err != nil {
		return Catalog{}, err
	}
	if err := Validate(document, raw, repoRoot); err != nil {
		return Catalog{}, err
	}
	return document, nil
}

func decodeCatalogDocument(raw []byte) (Catalog, error) {
	var document Catalog
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return Catalog{}, fmt.Errorf("decode workload catalog: %w", err)
	}
	if err := requireDecoderEOF(decoder); err != nil {
		return Catalog{}, fmt.Errorf("decode workload catalog: %w", err)
	}
	return document, nil
}

// materializeCandidateProducerWorkflows 将候选树中的生产者 workflow blob
// 物化到临时根目录；候选目录校验不得读取可变工作树中的 workflow/artifact。
func materializeCandidateProducerWorkflows(repoRoot, revision string, document Catalog) (string, func(), error) {
	validationRoot, err := os.MkdirTemp("", "mcp-lsp-catalog-candidate-")
	if err != nil {
		return "", nil, fmt.Errorf("create candidate catalog validation root: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(validationRoot) }
	seen := make(map[string]struct{})
	for _, workload := range document.Workloads {
		if workload.ProducerImplementationStatus != "implemented" {
			continue
		}
		if _, ok := seen[workload.ProducerWorkflowPath]; ok {
			continue
		}
		seen[workload.ProducerWorkflowPath] = struct{}{}
		if err := materializeCandidateProducerWorkflow(validationRoot, repoRoot, revision, workload); err != nil {
			cleanup()
			return "", nil, err
		}
	}
	return validationRoot, cleanup, nil
}

// materializeCandidateProducerWorkflow 读取并写出一个候选 workflow blob，
// 同时拒绝符号链接、目录和其他非普通文件模式。
func materializeCandidateProducerWorkflow(validationRoot, repoRoot, revision string, workload Workload) error {
	relative, err := resolveProducerWorkflowPath(validationRoot, workload.ProducerWorkflowPath)
	if err != nil {
		return fmt.Errorf("workload %q producer workflow path is unsafe: %w", workload.ID, err)
	}
	raw, mode, err := readCandidateTreeFile(repoRoot, revision, workload.ProducerWorkflowPath)
	if err != nil {
		return fmt.Errorf("workload %q producer workflow is not in candidate tree: %w", workload.ID, err)
	}
	if mode != "100644" && mode != "100755" {
		return fmt.Errorf("workload %q producer workflow has unsupported candidate mode %q", workload.ID, mode)
	}
	if err := os.MkdirAll(filepath.Dir(relative), 0o755); err != nil {
		return fmt.Errorf("materialize workload %q producer workflow directory: %w", workload.ID, err)
	}
	if err := os.WriteFile(relative, raw, 0o644); err != nil {
		return fmt.Errorf("materialize workload %q producer workflow: %w", workload.ID, err)
	}
	return nil
}

// readCandidateTreeFile 从已解析的 Git tree 读取普通 workflow blob 与模式。
func readCandidateTreeFile(repoRoot, revision, relative string) ([]byte, string, error) {
	path := filepath.ToSlash(relative)
	listing, err := BoundedGitOutput(repoRoot, "ls-tree", "-z", revision, "--", path)
	if err != nil {
		return nil, "", err
	}
	entry := strings.TrimSuffix(string(listing), "\x00")
	separator := strings.IndexByte(entry, '\t')
	if separator <= 0 {
		return nil, "", errors.New("candidate tree workflow entry is missing")
	}
	fields := strings.Fields(entry[:separator])
	if len(fields) != 3 || fields[1] != "blob" {
		return nil, "", errors.New("candidate tree workflow entry is not a regular blob")
	}
	object := revision + ":" + path
	raw, err := BoundedGitOutput(repoRoot, "show", "--format=", "--end-of-options", object)
	if err != nil {
		return nil, "", err
	}
	return raw, fields[0], nil
}

// Validate 强制执行 schema、摘要、生产者坐标和 fail-closed 状态规则。
func Validate(document Catalog, raw []byte, repoRoot string) error {
	if document.Schema != Schema {
		return fmt.Errorf("workload catalog schema %q does not match %q", document.Schema, Schema)
	}
	if !isSHA256Digest(document.CatalogDigest) {
		return fmt.Errorf("workload catalog digest %q is invalid", document.CatalogDigest)
	}
	digest, err := CanonicalDigest(raw)
	if err != nil {
		return fmt.Errorf("compute workload catalog digest: %w", err)
	}
	if document.CatalogDigest != digest {
		return fmt.Errorf("workload catalog digest mismatch: expected %s actual %s", document.CatalogDigest, digest)
	}
	if len(document.Workloads) == 0 {
		return errors.New("workload catalog must contain workloads")
	}
	seen := make(map[string]bool, len(document.Workloads))
	for _, workload := range document.Workloads {
		if err := validateWorkload(workload, repoRoot, seen); err != nil {
			return err
		}
	}
	return nil
}

// validateWorkload 校验单项 workload 的状态、命令、平台和生产者坐标。
func validateWorkload(workload Workload, repoRoot string, seen map[string]bool) error {
	if err := validateWorkloadIdentity(workload, seen); err != nil {
		return err
	}
	return validateWorkloadChecks(workload, repoRoot)
}

// validateWorkloadIdentity 校验 workload ID 并记录已见 ID。
func validateWorkloadIdentity(workload Workload, seen map[string]bool) error {
	if workload.ID == "" {
		return errors.New("workload ID is required")
	}
	if seen[workload.ID] {
		return fmt.Errorf("duplicate workload ID %q", workload.ID)
	}
	seen[workload.ID] = true
	return nil
}

// validateWorkloadChecks 按固定顺序执行 workload 的各项契约检查。
func validateWorkloadChecks(workload Workload, repoRoot string) error {
	checks := []func() error{
		func() error { return validateWorkloadStatus(workload) },
		func() error { return validateWorkloadProducerStatus(workload) },
		func() error { return validateWorkloadMetadata(workload) },
		func() error { return validateWorkloadReceipt(workload) },
		func() error { return validateWorkloadProducer(workload, repoRoot) },
		func() error { return validateWorkloadImplementation(workload) },
		func() error { return validateWorkloadCommand(workload) },
		func() error { return validateWorkloadPlatforms(workload) },
	}
	for _, check := range checks {
		if err := check(); err != nil {
			return err
		}
	}
	return nil
}

func validateWorkloadStatus(workload Workload) error {
	if workload.ImplementationStatus == "implemented" || workload.ImplementationStatus == "missing" {
		return nil
	}
	return fmt.Errorf("workload %q has unsupported implementation_status %q", workload.ID, workload.ImplementationStatus)
}

func validateWorkloadProducerStatus(workload Workload) error {
	switch workload.ProducerImplementationStatus {
	case "implemented":
		return nil
	case "missing":
		if !workload.ReleaseBlocking {
			return fmt.Errorf("workload %q missing producer implementation must set release_blocking", workload.ID)
		}
		return nil
	default:
		return fmt.Errorf("workload %q has unsupported producer_implementation_status %q", workload.ID, workload.ProducerImplementationStatus)
	}
}

func validateWorkloadMetadata(workload Workload) error {
	if strings.TrimSpace(workload.RunnerTarget) == "" {
		return fmt.Errorf("workload %q is missing runner, platform, timeout, or trigger class", workload.ID)
	}
	if len(workload.Platforms) == 0 {
		return fmt.Errorf("workload %q is missing runner, platform, timeout, or trigger class", workload.ID)
	}
	if _, err := TimeoutDuration(workload.TimeoutSeconds); err != nil {
		return fmt.Errorf("workload %q %w", workload.ID, err)
	}
	if strings.TrimSpace(workload.TriggerClass) == "" {
		return fmt.Errorf("workload %q is missing runner, platform, timeout, or trigger class", workload.ID)
	}
	return nil
}

func validateWorkloadReceipt(workload Workload) error {
	if workload.ReceiptSchema != ReceiptSchema {
		return fmt.Errorf("workload %q is missing required receipt schema/flag", workload.ID)
	}
	if workload.ReceiptRequired == nil || !*workload.ReceiptRequired {
		return fmt.Errorf("workload %q is missing required receipt schema/flag", workload.ID)
	}
	return nil
}

// validateWorkloadProducer 校验生产者 workflow 文件和 artifact 坐标。
func validateWorkloadProducer(workload Workload, repoRoot string) error {
	workflowValue := strings.TrimSpace(workload.ProducerWorkflowPath)
	artifactName := strings.TrimSpace(workload.ProducerArtifactName)
	if err := validateProducerCoordinates(workload.ID, workflowValue, artifactName); err != nil {
		return err
	}
	workflowPath, err := resolveProducerWorkflowPath(repoRoot, workflowValue)
	if err != nil {
		return fmt.Errorf("workload %q producer workflow path is unsafe: %w", workload.ID, err)
	}
	if workload.ProducerImplementationStatus == "missing" {
		return nil
	}
	if err := validateProducerWorkflowFile(repoRoot, workflowPath); err != nil {
		return fmt.Errorf("workload %q producer workflow path is unavailable: %w", workload.ID, err)
	}
	if err := validateProducerArtifact(workflowPath, artifactName); err != nil {
		return fmt.Errorf("workload %q producer artifact %q: %w", workload.ID, artifactName, err)
	}
	return nil
}

// validateProducerCoordinates 校验生产者 workflow 和 artifact 的非空安全坐标。
func validateProducerCoordinates(workloadID, workflowValue, artifactName string) error {
	if workflowValue == "" || !validProducerArtifactName(artifactName) {
		return fmt.Errorf("workload %q is missing producer coordinates", workloadID)
	}
	return nil
}

// validProducerArtifactName 判断 artifact 名称是否保持单一安全文件名边界。
func validProducerArtifactName(artifactName string) bool {
	return artifactName != "" &&
		artifactName != "." &&
		artifactName != ".." &&
		!strings.ContainsAny(artifactName, `/\\`) &&
		!strings.ContainsAny(artifactName, "\x00\r\n")
}

func resolveProducerWorkflowPath(repoRoot, value string) (string, error) {
	if hasUnsafeProducerWorkflowPrefix(value) {
		return "", errors.New("must be repository-relative")
	}
	normalized := strings.ReplaceAll(value, "\\", "/")
	if err := rejectParentProducerWorkflowSegments(normalized); err != nil {
		return "", err
	}
	clean := filepath.Clean(filepath.FromSlash(normalized))
	if isEmptyProducerWorkflowPath(clean) {
		return "", errors.New("workflow path is empty")
	}
	workflowPath := filepath.Join(repoRoot, clean)
	if err := ensureProducerWorkflowInsideRoot(repoRoot, workflowPath); err != nil {
		return "", err
	}
	return workflowPath, nil
}

// hasUnsafeProducerWorkflowPrefix 判断 workflow 输入是否为绝对或含 NUL 路径。
func hasUnsafeProducerWorkflowPrefix(value string) bool {
	return strings.ContainsRune(value, '\x00') ||
		filepath.IsAbs(filepath.FromSlash(value)) ||
		strings.HasPrefix(value, "/") ||
		strings.HasPrefix(value, "\\") ||
		isWindowsAbsolutePath(value)
}

// rejectParentProducerWorkflowSegments 拒绝显式 parent path segment。
func rejectParentProducerWorkflowSegments(normalized string) error {
	for part := range strings.SplitSeq(normalized, "/") {
		if part == ".." {
			return errors.New("parent path segments are forbidden")
		}
	}
	return nil
}

// isEmptyProducerWorkflowPath 判断清理后的 workflow 路径是否为空目录边界。
func isEmptyProducerWorkflowPath(clean string) bool {
	return clean == "." || clean == string(filepath.Separator)
}

// ensureProducerWorkflowInsideRoot 确认 workflow 路径仍位于仓库根目录内。
func ensureProducerWorkflowInsideRoot(repoRoot, workflowPath string) error {
	relative, err := filepath.Rel(filepath.Clean(repoRoot), workflowPath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("must remain inside repository root")
	}
	return nil
}

// isWindowsAbsolutePath 判断输入是否为 Windows 盘符绝对路径。
func isWindowsAbsolutePath(value string) bool {
	return len(value) >= 3 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':' && (value[2] == '/' || value[2] == '\\')
}

// validateProducerWorkflowFile 校验 workflow 路径沿线无 symlink 且目标为普通文件。
func validateProducerWorkflowFile(repoRoot, workflowPath string) error {
	relative, err := filepath.Rel(filepath.Clean(repoRoot), workflowPath)
	if err != nil {
		return err
	}
	cursor := filepath.Clean(repoRoot)
	parts := strings.Split(relative, string(filepath.Separator))
	for index, part := range parts {
		cursor = filepath.Join(cursor, part)
		info, statErr := os.Lstat(cursor)
		if statErr != nil {
			return statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("workflow path contains symlink")
		}
		if index == len(parts)-1 && !info.Mode().IsRegular() {
			return errors.New("workflow path is not a regular file")
		}
	}
	return nil
}

func validateProducerArtifact(workflowPath, artifactName string) error {
	file, err := os.Open(workflowPath)
	if err != nil {
		return fmt.Errorf("read workflow: %w", err)
	}
	defer file.Close()
	document, err := decodeWorkflowDocument(file)
	if err != nil {
		return err
	}
	if workflowUploadsArtifact(document, artifactName) {
		return nil
	}
	return errors.New("workflow does not declare and upload the artifact in one upload step")
}

// decodeWorkflowDocument 解码单一 workflow YAML 文档并拒绝尾随文档。
func decodeWorkflowDocument(reader io.Reader) (workflowDocument, error) {
	var document workflowDocument
	decoder := yaml.NewDecoder(reader)
	if err := decoder.Decode(&document); err != nil {
		return workflowDocument{}, fmt.Errorf("decode workflow: %w", err)
	}
	var trailing workflowDocument
	err := decoder.Decode(&trailing)
	if err == nil {
		return workflowDocument{}, errors.New("workflow contains multiple YAML documents")
	}
	if err != io.EOF {
		return workflowDocument{}, fmt.Errorf("decode trailing workflow document: %w", err)
	}
	return document, nil
}

// workflowUploadsArtifact 判断 workflow 是否存在同一步骤声明并上传目标 artifact。
func workflowUploadsArtifact(document workflowDocument, artifactName string) bool {
	for _, job := range document.Jobs {
		for _, step := range job.Steps {
			if workflowStepUploadsArtifact(step, artifactName) {
				return true
			}
		}
	}
	return false
}

// workflowStepUploadsArtifact 判断单个步骤是否为目标 artifact 上传步骤。
func workflowStepUploadsArtifact(step workflowStep, artifactName string) bool {
	if !strings.HasPrefix(strings.TrimSpace(step.Uses), "actions/upload-artifact@") {
		return false
	}
	nameNode, nameOK := step.With["name"]
	pathNode, pathOK := step.With["path"]
	return nameOK && strings.TrimSpace(nameNode.Value) == artifactName && pathOK && yamlNodeHasValue(pathNode)
}

type workflowDocument struct {
	Jobs map[string]workflowJob `yaml:"jobs"`
}

type workflowJob struct {
	Steps []workflowStep `yaml:"steps"`
}

type workflowStep struct {
	Uses string               `yaml:"uses"`
	With map[string]yaml.Node `yaml:"with"`
}

func yamlNodeHasValue(node yaml.Node) bool {
	if node.Kind == 0 {
		return false
	}
	if strings.TrimSpace(node.Value) != "" {
		return true
	}
	return len(node.Content) > 0
}

// validateWorkloadImplementation 校验缺失实现必须 commandless 且阻断发布。
func validateWorkloadImplementation(workload Workload) error {
	if workload.ImplementationStatus == "missing" {
		if !workload.T6Blocking || !workload.ReleaseBlocking || len(workload.Command) != 0 {
			return fmt.Errorf("workload %q missing implementation must be commandless and block T6/release", workload.ID)
		}
		return nil
	}
	if len(workload.Command) == 0 {
		return fmt.Errorf("workload %q implemented status requires catalog command", workload.ID)
	}
	return nil
}

func validateWorkloadCommand(workload Workload) error {
	for _, argument := range workload.Command {
		if strings.TrimSpace(argument) == "" {
			return fmt.Errorf("workload %q contains an unsafe command argument", workload.ID)
		}
		if filepath.IsAbs(argument) {
			return fmt.Errorf("workload %q contains an unsafe command argument", workload.ID)
		}
		if strings.Contains(filepath.ToSlash(argument), "../") {
			return fmt.Errorf("workload %q contains an unsafe command argument", workload.ID)
		}
	}
	return nil
}

func validateWorkloadPlatforms(workload Workload) error {
	for _, platform := range workload.Platforms {
		if !slices.Contains([]string{"darwin", "linux", "windows"}, platform) {
			return fmt.Errorf("workload %q has unsupported platform %q", workload.ID, platform)
		}
	}
	return nil
}

// CanonicalDigest 计算移除 catalog_digest 后规范 JSON 的摘要。
func CanonicalDigest(raw []byte) (string, error) {
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	if _, ok := value["catalog_digest"]; !ok {
		return "", errors.New("catalog_digest is required")
	}
	delete(value, "catalog_digest")
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(append(canonical, '\n'))
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

// isSHA256Digest 校验摘要是否为规范的小写 SHA-256 表示。
func isSHA256Digest(value string) bool {
	const prefix = "sha256:"
	if len(value) != len(prefix)+sha256.Size*2 || !strings.HasPrefix(value, prefix) {
		return false
	}
	for _, char := range value[len(prefix):] {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return false
		}
	}
	return true
}

// Find 按 ID 解析 workload，禁止调用方自造命令真相。
func (document Catalog) Find(id string) (Workload, error) {
	if strings.TrimSpace(id) == "" {
		return Workload{}, errors.New("workload ID is required")
	}
	for _, workload := range document.Workloads {
		if workload.ID == id {
			return workload, nil
		}
	}
	return Workload{}, fmt.Errorf("unknown workload ID %q", id)
}

// SupportsCurrentPlatform 判断 workload 是否支持当前主机平台。
func (workload Workload) SupportsCurrentPlatform() bool {
	return slices.Contains(workload.Platforms, runtime.GOOS)
}

// RemoteTestCommand 是 catalog-owned go test 命令的唯一解析结果。
type RemoteTestCommand struct {
	Packages  []string
	TestNames []string
	BuildTags []string
}

// RemoteTestSelector 将一个精确测试名绑定到唯一包。
type RemoteTestSelector struct{ Package, TestName string }

// RemoteTestProjection 是按目标平台解析后的精确 package+Test 投影。
type RemoteTestProjection struct {
	Selectors  []RemoteTestSelector
	InputFiles []string
}

// RemoteTestFile 是 exact tree 或本地仓库提供的测试源码对象。
type RemoteTestFile struct {
	Path, Name string
	Source     []byte
}

// RemoteTestPackageFiles 将测试源码绑定到规范包路径。
type RemoteTestPackageFiles struct {
	Package string
	Files   []RemoteTestFile
}

// RemoteTestEnvironment 冻结远程 worker 的 go/build 适用性身份。
type RemoteTestEnvironment struct {
	GOOS       string
	GOARCH     string
	CGOEnabled bool
}

const (
	RemoteTestGOOS   = "linux"
	RemoteTestGOARCH = "amd64"
)

// DefaultRemoteTestEnvironment 是唯一受支持的 MCP-LSP 远程选择器平台。
func DefaultRemoteTestEnvironment() RemoteTestEnvironment {
	return RemoteTestEnvironment{GOOS: RemoteTestGOOS, GOARCH: RemoteTestGOARCH, CGOEnabled: false}
}

// Validate 拒绝由主机推导或其他非规范的远程测试平台。
func (environment RemoteTestEnvironment) Validate() error {
	if environment.GOOS != RemoteTestGOOS || environment.GOARCH != RemoteTestGOARCH || environment.CGOEnabled {
		return fmt.Errorf("remote test environment must be %s/%s with CGO_ENABLED=0", RemoteTestGOOS, RemoteTestGOARCH)
	}
	return nil
}

// ImplementedRemoteTest 是 canonical raw catalog 中一个已实现 workload 的解析结果。
type ImplementedRemoteTest struct {
	WorkloadID, ProducerWorkflowPath, ProducerImplementationStatus string
	Command                                                        RemoteTestCommand
}

// WorkloadConsumerPaths 是真实 runner/consumer 源文件的唯一路径清单；ID 集合始终从 Catalog 派生。
func WorkloadConsumerPaths() []string {
	return []string{
		"Makefile",
		"scripts/check_mcp_lsp_workload_catalog.sh",
		"scripts/run_mcp_lsp_workload.sh",
		"scripts/mcp_lsp_workload_runner/main.go",
		"scripts/mcp_lsp_workload_runner/platform_other.go",
		"scripts/mcp_lsp_workload_runner/platform_windows.go",
		"scripts/ai_maintenance/owned_gate_execution.go",
		"scripts/ai_maintenance/main.go",
		"scripts/ai_maintenance/release_gate_rules.go",
		"scripts/ai_maintenance/gate_execution.go",
		"internal/devtools/gate/executor_mapping.go",
		"internal/devtools/gate/executor.go",
		"internal/devtools/gate/executor_catalog_tree.go",
		"internal/devtools/gate/workload_catalog.go",
		"internal/devtools/gate/workload_targets.go",
		"internal/devtools/mcplspguardcore/consumer_ast_helpers.go",
		"internal/devtools/mcplspguardcore/scheduler_validation.go",
		"internal/devtools/mcplspguardcore/consumer_validation.go",
		"internal/devtools/mcplspguardcore/executor_ast_helpers.go",
		"internal/devtools/mcplspguardcore/guard.go",
		"internal/devtools/mcplspguardcore/receipt_ast_helpers.go",
		"cmd/super-dolphin-gate/cli_common.go",
		"cmd/super-dolphin-gate/main.go",
	}
}

// WorkloadGateInputPaths derives the scheduler closure from the canonical
// consumer registry and the exact candidate catalog's implemented producers.
func WorkloadGateInputPaths(document Catalog) []string {
	paths := []string{
		".githooks/README.md", Path, "Makefile", "go.mod", "go.sum",
		"scripts/check_mcp_lsp_workload_catalog.sh", "scripts/run_mcp_lsp_workload.sh",
	}
	paths = append(paths, WorkloadConsumerPaths()...)
	for _, workload := range document.Workloads {
		if workload.ImplementationStatus == "implemented" &&
			workload.ProducerImplementationStatus == "implemented" && workload.ProducerWorkflowPath != "" {
			paths = append(paths, workload.ProducerWorkflowPath)
		}
	}
	slices.Sort(paths)
	return slices.Compact(paths)
}

// ParseImplementedRemoteTestsFromRaw 复用 canonical decode/digest 并解析全部 implemented workload。
func ParseImplementedRemoteTestsFromRaw(raw []byte) ([]ImplementedRemoteTest, error) {
	document, err := decodeCanonicalCatalogRaw(raw)
	if err != nil {
		return nil, err
	}
	result := make([]ImplementedRemoteTest, 0, len(document.Workloads))
	for _, workload := range document.Workloads {
		if workload.ImplementationStatus != "implemented" {
			continue
		}
		command, err := ParseRemoteTestCommand(workload.Command)
		if err != nil {
			return nil, fmt.Errorf("workload %q: %w", workload.ID, err)
		}
		result = append(result, ImplementedRemoteTest{WorkloadID: workload.ID, ProducerWorkflowPath: workload.ProducerWorkflowPath, ProducerImplementationStatus: workload.ProducerImplementationStatus, Command: command})
	}
	if len(result) == 0 {
		return nil, errors.New("workload catalog contains no implemented remote tests")
	}
	return result, nil
}

func decodeCanonicalCatalogRaw(raw []byte) (Catalog, error) {
	document, err := decodeCatalogDocument(raw)
	if err != nil {
		return Catalog{}, err
	}
	if document.Schema != Schema {
		return Catalog{}, fmt.Errorf("workload catalog schema %q does not match %q", document.Schema, Schema)
	}
	digest, err := CanonicalDigest(raw)
	if err != nil {
		return Catalog{}, fmt.Errorf("compute workload catalog digest: %w", err)
	}
	if document.CatalogDigest != digest {
		return Catalog{}, fmt.Errorf("workload catalog digest mismatch: expected %s actual %s", document.CatalogDigest, digest)
	}
	return document, nil
}
