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

// materializeCandidateProducerWorkflows materializes only producer workflow
// blobs from the resolved candidate tree. Validation must never read a
// mutable worktree workflow/artifact while deciding a candidate catalog.
func materializeCandidateProducerWorkflows(repoRoot, revision string, document Catalog) (string, func(), error) {
	validationRoot, err := os.MkdirTemp("", "mcp-lsp-catalog-candidate-")
	if err != nil {
		return "", nil, fmt.Errorf("create candidate catalog validation root: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(validationRoot) }
	seen := make(map[string]bool)
	for _, workload := range document.Workloads {
		if workload.ProducerImplementationStatus != "implemented" || seen[workload.ProducerWorkflowPath] {
			continue
		}
		seen[workload.ProducerWorkflowPath] = true
		relative, err := resolveProducerWorkflowPath(validationRoot, workload.ProducerWorkflowPath)
		if err != nil {
			cleanup()
			return "", nil, fmt.Errorf("workload %q producer workflow path is unsafe: %w", workload.ID, err)
		}
		raw, mode, err := readCandidateTreeFile(repoRoot, revision, workload.ProducerWorkflowPath)
		if err != nil {
			cleanup()
			return "", nil, fmt.Errorf("workload %q producer workflow is not in candidate tree: %w", workload.ID, err)
		}
		if mode != "100644" && mode != "100755" {
			cleanup()
			return "", nil, fmt.Errorf("workload %q producer workflow has unsupported candidate mode %q", workload.ID, mode)
		}
		if err := os.MkdirAll(filepath.Dir(relative), 0o755); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("materialize workload %q producer workflow directory: %w", workload.ID, err)
		}
		if err := os.WriteFile(relative, raw, 0o644); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("materialize workload %q producer workflow: %w", workload.ID, err)
		}
	}
	return validationRoot, cleanup, nil
}

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
	if len(command) < 3 || command[0] != "go" || command[1] != "test" {
		return nil, errors.New("workload command is not a catalog-owned go test command")
	}
	var packages []string
	var runSelector string
	for index := 2; index < len(command); index++ {
		argument := command[index]
		if strings.HasPrefix(argument, "./") && !strings.Contains(filepath.ToSlash(argument), "../") {
			packages = append(packages, argument)
			continue
		}
		if argument == "-run" {
			if index+1 >= len(command) {
				return nil, errors.New("workload command -run selector is missing")
			}
			runSelector = command[index+1]
			index++
			continue
		}
		if strings.HasPrefix(argument, "-") {
			// Flags with a separate value are consumed here; values cannot be
			// mistaken for package paths or selectors.
			if argument == "-tags" || argument == "-timeout" || argument == "-count" {
				if index+1 >= len(command) {
					return nil, fmt.Errorf("workload command flag %s is missing a value", argument)
				}
				index++
			}
			continue
		}
		return nil, fmt.Errorf("workload command contains an unsupported argument %q", argument)
	}
	if len(packages) == 0 {
		return nil, errors.New("workload command contains no repository-relative Go package")
	}
	names, err := remoteTestNames(runSelector)
	if err != nil {
		return nil, err
	}
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
	return selectors, nil
}

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

// ValidateReceipt 按精确目录摘要和 ID 校验已产出的回执。
func ValidateReceipt(document Catalog, id, path string) error {
	repoRoot, _ := receiptRepositoryRoot(path)
	return ValidateReceiptAt(document, repoRoot, id, path)
}

// ValidateReceiptAt 按指定仓库根校验回执，并把 source provenance 绑定到当前 Git。
// 生产者/发布消费者必须使用此入口；它不接受 receipt 自称的 HEAD/tree。
func ValidateReceiptAt(document Catalog, repoRoot, id, path string) error {
	workload, err := document.Find(id)
	if err != nil {
		return err
	}
	if workload.ImplementationStatus != "implemented" {
		return fmt.Errorf("workload %q receipt cannot satisfy implementation_status=%s", workload.ID, workload.ImplementationStatus)
	}
	if path == "" || !filepath.IsAbs(path) {
		return errors.New("workload receipt path must be absolute")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read workload receipt %q: %w", path, err)
	}
	value, err := decodeReceipt(raw)
	if err != nil {
		return err
	}
	if err := validateReceiptIdentity(value, workload, document); err != nil {
		return err
	}
	if err := validateReceiptProducer(value, workload); err != nil {
		return err
	}
	if err := validateReceiptExecution(value, workload); err != nil {
		return err
	}
	if err := validateReceiptProvenance(value, workload, repoRoot); err != nil {
		return err
	}
	return validateReceiptStatus(value, workload.ID)
}

// receiptRepositoryRoot 从回执路径向上解析 Git 根；guard 应优先调用
// ValidateReceiptAt 传入受信 root，以免在 linked worktree 中被路径猜测误导。
func receiptRepositoryRoot(receiptPath string) (string, error) {
	if receiptPath == "" || !filepath.IsAbs(receiptPath) {
		return "", errors.New("workload receipt path must be absolute")
	}
	for root := filepath.Dir(filepath.Clean(receiptPath)); ; root = filepath.Dir(root) {
		if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
			return root, nil
		}
		parent := filepath.Dir(root)
		if parent == root {
			return "", fmt.Errorf("repository root not found for workload receipt %q", receiptPath)
		}
	}
}

func decodeReceipt(raw []byte) (Receipt, error) {
	var value Receipt
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return Receipt{}, fmt.Errorf("decode workload receipt: %w", err)
	}
	if err := requireDecoderEOF(decoder); err != nil {
		return Receipt{}, fmt.Errorf("decode workload receipt: %w", err)
	}
	return value, nil
}

// requireDecoderEOF 要求 JSON 文档后只有空白，拒绝尾随第二个值。
func requireDecoderEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("trailing JSON document")
		}
		return fmt.Errorf("trailing JSON document: %w", err)
	}
	return nil
}

func validateReceiptIdentity(value Receipt, workload Workload, document Catalog) error {
	if value.Schema != ReceiptSchema {
		return fmt.Errorf("workload receipt identity or catalog digest mismatch for %q", workload.ID)
	}
	if value.WorkloadID != workload.ID {
		return fmt.Errorf("workload receipt identity or catalog digest mismatch for %q", workload.ID)
	}
	if value.CatalogDigest != document.CatalogDigest {
		return fmt.Errorf("workload receipt identity or catalog digest mismatch for %q", workload.ID)
	}
	return nil
}

func validateReceiptProducer(value Receipt, workload Workload) error {
	if value.ProducerImplementationStatus != workload.ProducerImplementationStatus {
		return fmt.Errorf("workload receipt producer implementation status mismatch for %q", workload.ID)
	}
	if value.RunnerTarget != workload.RunnerTarget {
		return fmt.Errorf("workload receipt producer coordinates mismatch for %q", workload.ID)
	}
	if value.ProducerWorkflowPath != workload.ProducerWorkflowPath {
		return fmt.Errorf("workload receipt producer coordinates mismatch for %q", workload.ID)
	}
	if value.ProducerArtifactName != workload.ProducerArtifactName {
		return fmt.Errorf("workload receipt producer coordinates mismatch for %q", workload.ID)
	}
	return nil
}

// validateReceiptExecution 校验本地来源、平台、命令、预算和时间序列。
func validateReceiptExecution(value Receipt, workload Workload) error {
	if err := validateReceiptExecutionOrigin(value, workload); err != nil {
		return err
	}
	if err := validateReceiptExecutionFields(value, workload); err != nil {
		return err
	}
	started, finished, err := parseReceiptExecutionTimes(value, workload)
	if err != nil {
		return err
	}
	if finished.Before(started) {
		return fmt.Errorf("workload receipt finished_at precedes started_at for %q", workload.ID)
	}
	timeout, err := TimeoutDuration(workload.TimeoutSeconds)
	if err != nil {
		return fmt.Errorf("workload receipt timeout is invalid for %q: %w", workload.ID, err)
	}
	if finished.Sub(started) > timeout {
		return fmt.Errorf("workload receipt duration exceeds timeout for %q", workload.ID)
	}
	return nil
}

// validateReceiptExecutionOrigin 校验回执只能由受信本地 runner 产生。
func validateReceiptExecutionOrigin(value Receipt, workload Workload) error {
	if workload.ProducerImplementationStatus != "implemented" && value.ExecutionOrigin != "local-runner" {
		return fmt.Errorf("workload receipt for %q cannot be trusted as CI/release: producer_implementation_status=%s", workload.ID, workload.ProducerImplementationStatus)
	}
	if value.ExecutionOrigin != "local-runner" {
		return fmt.Errorf("workload receipt for %q has unsupported execution origin %q", workload.ID, value.ExecutionOrigin)
	}
	return nil
}

// validateReceiptExecutionFields 校验回执平台、预算和命令与 workload 一致。
func validateReceiptExecutionFields(value Receipt, workload Workload) error {
	if !slices.Contains(workload.Platforms, value.Platform) {
		return fmt.Errorf("workload receipt platform %q is not registered for %q", value.Platform, workload.ID)
	}
	if value.Platform != runtime.GOOS {
		return fmt.Errorf("local workload receipt platform %q does not match host %q", value.Platform, runtime.GOOS)
	}
	if value.TimeoutSeconds != workload.TimeoutSeconds {
		return fmt.Errorf("workload receipt timeout mismatch for %q", workload.ID)
	}
	if !slices.Equal(value.Command, workload.Command) {
		return fmt.Errorf("workload receipt command mismatch for %q", workload.ID)
	}
	return nil
}

// parseReceiptExecutionTimes 解析回执的开始和结束时间戳。
func parseReceiptExecutionTimes(value Receipt, workload Workload) (time.Time, time.Time, error) {
	started, err := time.Parse(time.RFC3339Nano, value.StartedAt)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("workload receipt started_at is invalid for %q: %w", workload.ID, err)
	}
	finished, err := time.Parse(time.RFC3339Nano, value.FinishedAt)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("workload receipt finished_at is invalid for %q: %w", workload.ID, err)
	}
	return started, finished, nil
}

func validateReceiptStatus(value Receipt, workloadID string) error {
	if value.Status != "pass" {
		return fmt.Errorf("workload receipt for %q is not a passing receipt", workloadID)
	}
	if value.ExitCode != 0 {
		return fmt.Errorf("workload receipt for %q is not a passing receipt", workloadID)
	}
	return nil
}

const default15mTriggerClass = "default-15m-source-e2e"
const default15mWorkloadID = "mcp-lsp-default-15m"

var completionActionOrder = []string{
	"mark_draining",
	"shutdown_forwarders",
	"shutdown_daemon",
	"verify",
	"completed",
}

// validateReceiptProvenance 校验默认 15 分钟 E2E 的 Git 身份和 root-cohort
// completion chain。短 workload 没有 root daemon，因此保留旧 receipt 兼容边界。
func validateReceiptProvenance(value Receipt, workload Workload, repoRoot string) error {
	if workload.ID != default15mWorkloadID || workload.TriggerClass != default15mTriggerClass {
		return nil
	}
	if runtime.GOOS == "windows" {
		return fmt.Errorf("workload %q is N/V on Windows until native daemon owner receipt is implemented", workload.ID)
	}
	return fmt.Errorf("workload %q is N/V: remote run/job/artifact authority binding is unavailable", workload.ID)
}

// AttachCompletionProvenance 将显式提供的 root-cohort completion receipt
// 绑定到 workload receipt。它只读取 path，不接受环境变量或默认路径。
func AttachCompletionProvenance(value *Receipt, repoRoot, completionPath string) error {
	if value == nil {
		return errors.New("workload receipt is required")
	}
	if value.WorkloadID == default15mWorkloadID {
		return errors.New("default-15m completion receipt requires remote run/job/artifact authority")
	}
	if strings.TrimSpace(repoRoot) == "" {
		return errors.New("completion provenance repository root is required")
	}
	if completionPath == "" || !filepath.IsAbs(completionPath) {
		return errors.New("completion receipt path must be absolute")
	}
	raw, err := os.ReadFile(completionPath)
	if err != nil {
		return fmt.Errorf("read completion receipt: %w", err)
	}
	proof, err := decodeCompletionProof(raw)
	if err != nil {
		return fmt.Errorf("decode completion receipt: %w", err)
	}
	gitHead, sourceTree, err := currentGitIdentity(repoRoot)
	if err != nil {
		return fmt.Errorf("resolve current Git identity: %w", err)
	}
	if proof.GitHead != gitHead || proof.SourceTreeDigest != sourceTree {
		return errors.New("completion receipt Git HEAD/tree does not match current repository")
	}
	value.GitHead = gitHead
	value.SourceTreeDigest = sourceTree
	value.CohortID = proof.CohortID
	value.RepositoryInstanceProofHash = proof.RepositoryInstanceProofHash
	value.Epoch = proof.Epoch
	value.DaemonOwnerReceiptHash = proof.DaemonOwnerReceiptHash
	value.RemoteRunID = proof.RemoteRunID
	value.RemoteJobID = proof.RemoteJobID
	value.RemoteArtifactName = proof.RemoteArtifactName
	value.RemoteArtifactDigest = proof.RemoteArtifactDigest
	value.CompletionReceiptHash = digestBytes(raw)
	value.CompletionReceiptPath = filepath.Clean(completionPath)
	value.ActionOrder = append([]string(nil), proof.ActionOrder...)
	value.ForwarderCountAfter = proof.ForwarderCountAfter
	value.DaemonObservedAfter = proof.DaemonObservedAfter
	value.TelemetryIdentitiesGone = proof.TelemetryIdentitiesGone
	value.EndpointUnreachable = proof.EndpointUnreachable
	value.NativeOwnerReleased = proof.NativeOwnerReleased
	value.QuietWindowVerified = proof.QuietWindowVerified
	value.NextEpoch = proof.NextEpoch
	if err := validateReceiptProvenanceFields(*value, value.WorkloadID); err != nil {
		return err
	}
	return validateCompletionProof(proof, *value, value.WorkloadID)
}

// ValidateCompletionReceipt 保留兼容入口，但在远程 run/job/artifact authority
// 未接入前明确返回 N/V；本地 completion JSON 不能成为信任根。
func ValidateCompletionReceipt(repoRoot, completionPath string) error {
	if strings.TrimSpace(repoRoot) == "" {
		return errors.New("completion provenance repository root is required")
	}
	if completionPath == "" || !filepath.IsAbs(completionPath) {
		return errors.New("completion receipt path must be absolute")
	}
	return errors.New("completion receipt remote run/job/artifact authority binding is unavailable")
}

// ValidateCompletionReceiptForCandidate 保留候选身份参数以避免调用方自行
// 降级到工作树校验，但在 artifact authority 未提供时仍 fail-closed 为 N/V。
func ValidateCompletionReceiptForCandidate(gitHead, sourceTree, completionPath string) error {
	return errors.New("completion receipt remote run/job/artifact authority binding is unavailable")
}

func validateReceiptProvenanceFields(value Receipt, workloadID string) error {
	if !digestPattern.MatchString(value.GitHead) && !validGitOID(value.GitHead) {
		return fmt.Errorf("workload receipt git_head is invalid for %q", workloadID)
	}
	if !validGitOID(value.SourceTreeDigest) {
		return fmt.Errorf("workload receipt source_tree_digest is invalid for %q", workloadID)
	}
	for name, digest := range map[string]string{
		"cohort_id":                      value.CohortID,
		"repository_instance_proof_hash": value.RepositoryInstanceProofHash,
		"daemon_owner_receipt_hash":      value.DaemonOwnerReceiptHash,
		"completion_receipt_hash":        value.CompletionReceiptHash,
		"remote_artifact_digest":         value.RemoteArtifactDigest,
	} {
		if !digestPattern.MatchString(digest) {
			return fmt.Errorf("workload receipt %s is invalid for %q", name, workloadID)
		}
	}
	for name, authorityID := range map[string]string{
		"remote_run_id":        value.RemoteRunID,
		"remote_job_id":        value.RemoteJobID,
		"remote_artifact_name": value.RemoteArtifactName,
	} {
		if !validAuthorityID(authorityID) {
			return fmt.Errorf("workload receipt %s is invalid for %q", name, workloadID)
		}
	}
	if value.Epoch == 0 || value.NextEpoch <= value.Epoch {
		return fmt.Errorf("workload receipt epoch transition is invalid for %q", workloadID)
	}
	if value.WorkloadStartedAt == "" || value.WorkloadFinishedAt == "" {
		return fmt.Errorf("workload receipt workload timestamps are missing for %q", workloadID)
	}
	started, err := time.Parse(time.RFC3339Nano, value.WorkloadStartedAt)
	if err != nil {
		return fmt.Errorf("workload receipt workload_started_at is invalid for %q: %w", workloadID, err)
	}
	finished, err := time.Parse(time.RFC3339Nano, value.WorkloadFinishedAt)
	if err != nil {
		return fmt.Errorf("workload receipt workload_finished_at is invalid for %q: %w", workloadID, err)
	}
	if !strings.HasSuffix(value.WorkloadStartedAt, "Z") || !strings.HasSuffix(value.WorkloadFinishedAt, "Z") {
		return fmt.Errorf("workload receipt workload timestamps must be UTC for %q", workloadID)
	}
	if finished.Before(started) || value.WorkloadStartedAt != value.StartedAt || value.WorkloadFinishedAt != value.FinishedAt {
		return fmt.Errorf("workload receipt workload timestamps do not match execution timestamps for %q", workloadID)
	}
	if !slices.Equal(value.ActionOrder, completionActionOrder) {
		return fmt.Errorf("workload receipt action order is invalid for %q", workloadID)
	}
	if value.ForwarderCountAfter != 0 || value.DaemonObservedAfter || !value.TelemetryIdentitiesGone ||
		!value.EndpointUnreachable || !value.NativeOwnerReleased || !value.QuietWindowVerified {
		return fmt.Errorf("workload receipt completion verification is incomplete for %q", workloadID)
	}
	return nil
}

func validGitOID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func currentGitIdentity(repoRoot string) (string, string, error) {
	gitHead, err := runGitIdentity(repoRoot, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", "", err
	}
	tree, err := runGitIdentity(repoRoot, "rev-parse", "--verify", "HEAD^{tree}")
	if err != nil {
		return "", "", err
	}
	return gitHead, tree, nil
}

func runGitIdentity(repoRoot string, args ...string) (string, error) {
	command := exec.Command("git", append([]string{"-C", repoRoot}, args...)...)
	output, err := command.Output()
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(output))
	if value == "" {
		return "", errors.New("git returned an empty identity")
	}
	return value, nil
}

type completionProof struct {
	GitHead                     string
	SourceTreeDigest            string
	CohortID                    string
	RepositoryInstanceProofHash string
	Epoch                       uint64
	DaemonOwnerReceiptHash      string
	RemoteRunID                 string
	RemoteJobID                 string
	RemoteArtifactName          string
	RemoteArtifactDigest        string
	ActionOrder                 []string
	ForwarderCountAfter         int
	DaemonObservedAfter         bool
	TelemetryIdentitiesGone     bool
	EndpointUnreachable         bool
	NativeOwnerReleased         bool
	QuietWindowVerified         bool
	NextEpoch                   uint64
	Status                      string
}

func decodeCompletionProof(raw []byte) (completionProof, error) {
	var fields map[string]json.RawMessage
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	// Completion receipts are owned by the mcp-lsp controller and may grow
	// fields independently; retain strict JSON syntax while selecting only the
	// frozen provenance fields below.
	decoder = json.NewDecoder(strings.NewReader(string(raw)))
	if err := decoder.Decode(&fields); err != nil {
		return completionProof{}, err
	}
	if err := requireDecoderEOF(decoder); err != nil {
		return completionProof{}, err
	}
	var proof completionProof
	var err error
	proof.GitHead, err = requiredJSONField[string](fields, "git_head")
	if err != nil {
		return completionProof{}, err
	}
	proof.SourceTreeDigest, err = requiredJSONField[string](fields, "source_tree_digest")
	if err != nil {
		return completionProof{}, err
	}
	proof.CohortID, err = requiredJSONField[string](fields, "cohort_id")
	if err != nil {
		return completionProof{}, err
	}
	proof.RepositoryInstanceProofHash, err = requiredJSONField[string](fields, "repository_instance_proof_hash")
	if err != nil {
		return completionProof{}, err
	}
	proof.Epoch, err = requiredJSONField[uint64](fields, "epoch")
	if err != nil {
		return completionProof{}, err
	}
	proof.DaemonOwnerReceiptHash, err = requiredJSONField[string](fields, "daemon_owner_receipt_hash")
	if err != nil {
		return completionProof{}, err
	}
	proof.RemoteRunID, err = requiredJSONField[string](fields, "remote_run_id")
	if err != nil {
		return completionProof{}, err
	}
	proof.RemoteJobID, err = requiredJSONField[string](fields, "remote_job_id")
	if err != nil {
		return completionProof{}, err
	}
	proof.RemoteArtifactName, err = requiredJSONField[string](fields, "remote_artifact_name")
	if err != nil {
		return completionProof{}, err
	}
	proof.RemoteArtifactDigest, err = requiredJSONField[string](fields, "remote_artifact_digest")
	if err != nil {
		return completionProof{}, err
	}
	proof.ActionOrder, err = requiredJSONField[[]string](fields, "action_order")
	if err != nil {
		return completionProof{}, err
	}
	proof.ForwarderCountAfter, err = requiredJSONField[int](fields, "forwarder_count_after")
	if err != nil {
		return completionProof{}, err
	}
	proof.DaemonObservedAfter, err = requiredJSONField[bool](fields, "daemon_observed_after")
	if err != nil {
		return completionProof{}, err
	}
	proof.TelemetryIdentitiesGone, err = requiredJSONField[bool](fields, "telemetry_identities_gone")
	if err != nil {
		return completionProof{}, err
	}
	proof.EndpointUnreachable, err = requiredJSONField[bool](fields, "endpoint_unreachable")
	if err != nil {
		return completionProof{}, err
	}
	proof.NativeOwnerReleased, err = requiredJSONField[bool](fields, "native_owner_released")
	if err != nil {
		return completionProof{}, err
	}
	proof.QuietWindowVerified, err = requiredJSONField[bool](fields, "quiet_window_verified")
	if err != nil {
		return completionProof{}, err
	}
	proof.NextEpoch, err = requiredJSONField[uint64](fields, "next_epoch")
	if err != nil {
		return completionProof{}, err
	}
	proof.Status, err = requiredJSONField[string](fields, "status")
	if err != nil {
		return completionProof{}, err
	}
	return proof, nil
}

func requiredJSONField[T any](fields map[string]json.RawMessage, name string) (T, error) {
	var value T
	raw, ok := fields[name]
	if !ok {
		return value, fmt.Errorf("completion receipt field %q is required", name)
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return value, fmt.Errorf("completion receipt field %q: %w", name, err)
	}
	return value, nil
}

func validateCompletionProof(proof completionProof, value Receipt, workloadID string) error {
	if proof.GitHead != value.GitHead || proof.SourceTreeDigest != value.SourceTreeDigest ||
		proof.CohortID != value.CohortID || proof.RepositoryInstanceProofHash != value.RepositoryInstanceProofHash ||
		proof.Epoch != value.Epoch || proof.DaemonOwnerReceiptHash != value.DaemonOwnerReceiptHash ||
		proof.RemoteRunID != value.RemoteRunID || proof.RemoteJobID != value.RemoteJobID ||
		proof.RemoteArtifactName != value.RemoteArtifactName || proof.RemoteArtifactDigest != value.RemoteArtifactDigest {
		return fmt.Errorf("completion receipt provenance chain mismatch for %q", workloadID)
	}
	return validateCompletionProofFields(proof, workloadID)
}

func validateCompletionProofFields(proof completionProof, workloadID string) error {
	return fmt.Errorf("completion receipt for %q is N/V: remote run/job/artifact authority binding is unavailable", workloadID)
}

func validAuthorityID(value string) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed != "" && trimmed == value && !strings.ContainsAny(value, "\x00\r\n")
}

func digestBytes(raw []byte) string {
	digest := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(digest[:])
}
