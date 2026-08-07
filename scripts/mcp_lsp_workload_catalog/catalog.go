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
	"os/exec"
	"path/filepath"
	"regexp"
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

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

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
	command := exec.Command("git", "-C", repoRoot, "show", "--format=", "--end-of-options", revision+":"+Path)
	raw, err := command.Output()
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
	listing, err := exec.Command("git", "-C", repoRoot, "ls-tree", "-z", revision, "--", path).Output()
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
	raw, err := exec.Command("git", "-C", repoRoot, "show", "--format=", "--end-of-options", object).Output()
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
	if !digestPattern.MatchString(document.CatalogDigest) {
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

// RemoteTestSelectors 将 catalog-owned `go test` 命令投影为 Gate CLI 的精确
// package[#Test] 选择器。它只接受可静态证明的命令，不把任意 shell 命令送入
// remote coordinator。
func RemoteTestSelectors(command []string) ([]string, error) {
	parsed, err := parseRemoteTestCommand(command)
	if err != nil {
		return nil, err
	}
	names, err := remoteTestNames(parsed.runSelector)
	if err != nil {
		return nil, err
	}
	return expandRemoteTestSelectors(parsed.packages, names), nil
}

type remoteTestCommand struct {
	packages    []string
	runSelector string
}

// parseRemoteTestCommand 校验 catalog-owned go test 命令并提取包与 -run 选择器。
func parseRemoteTestCommand(command []string) (remoteTestCommand, error) {
	if len(command) < 3 || command[0] != "go" || command[1] != "test" {
		return remoteTestCommand{}, errors.New("workload command is not a catalog-owned go test command")
	}
	parsed := remoteTestCommand{}
	for index := 2; index < len(command); index++ {
		next, packagePath, runSelector, err := parseRemoteTestArgument(command, index)
		if err != nil {
			return remoteTestCommand{}, err
		}
		if packagePath != "" {
			parsed.packages = append(parsed.packages, packagePath)
		}
		if runSelector != "" {
			parsed.runSelector = runSelector
		}
		index = next
	}
	if len(parsed.packages) == 0 {
		return remoteTestCommand{}, errors.New("workload command contains no repository-relative Go package")
	}
	return parsed, nil
}

// parseRemoteTestArgument 解析单个 go test 参数，并返回下一个待处理位置。
func parseRemoteTestArgument(command []string, index int) (int, string, string, error) {
	argument := command[index]
	if strings.HasPrefix(argument, "./") && !strings.Contains(filepath.ToSlash(argument), "../") {
		return index, argument, "", nil
	}
	if argument == "-run" {
		if index+1 >= len(command) {
			return index, "", "", errors.New("workload command -run selector is missing")
		}
		return index + 1, "", command[index+1], nil
	}
	if !strings.HasPrefix(argument, "-") {
		return index, "", "", fmt.Errorf("workload command contains an unsupported argument %q", argument)
	}
	if argument == "-tags" || argument == "-timeout" || argument == "-count" {
		if index+1 >= len(command) {
			return index, "", "", fmt.Errorf("workload command flag %s is missing a value", argument)
		}
		return index + 1, "", "", nil
	}
	return index, "", "", nil
}

// expandRemoteTestSelectors 将包路径和精确测试名组合为 Gate 选择器。
func expandRemoteTestSelectors(packages, names []string) []string {
	selectors := make([]string, 0, len(packages)*max(1, len(names)))
	for _, packagePath := range packages {
		if len(names) == 0 {
			selectors = append(selectors, packagePath)
			continue
		}
		for _, name := range names {
			selectors = append(selectors, packagePath+"#"+name)
		}
	}
	return selectors
}

// remoteTestNames 将受限 -run 表达式展开为固定测试名。
func remoteTestNames(selector string) ([]string, error) {
	if selector == "" {
		return nil, nil
	}
	if !strings.HasPrefix(selector, "^Test") || !strings.HasSuffix(selector, "$") {
		return nil, fmt.Errorf("workload command -run selector %q is not an exact Test selector", selector)
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(selector, "^Test"), "$")
	if strings.HasPrefix(inner, "(") && strings.HasSuffix(inner, ")") {
		inner = strings.TrimSuffix(strings.TrimPrefix(inner, "("), ")")
	}
	parts := strings.Split(inner, "|")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || strings.ContainsAny(part, "()[]{}*+?.\\") {
			return nil, fmt.Errorf("workload command -run selector %q is not exact", selector)
		}
		result = append(result, "Test"+part)
	}
	return result, nil
}
