// Package catalog owns receipt validation and completion provenance helpers.
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
	"runtime"
	"slices"
	"strings"
	"time"
)

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
	if err := requireImplementedReceiptWorkload(workload); err != nil {
		return err
	}
	value, err := readReceiptFile(path)
	if err != nil {
		return err
	}
	return validateReceiptValue(value, workload, document, repoRoot)
}

// requireImplementedReceiptWorkload 拒绝为缺失实现伪造可消费回执。
func requireImplementedReceiptWorkload(workload Workload) error {
	if workload.ImplementationStatus != "implemented" {
		return fmt.Errorf("workload %q receipt cannot satisfy implementation_status=%s", workload.ID, workload.ImplementationStatus)
	}
	return nil
}

// readReceiptFile 读取并严格解码指定的绝对回执路径。
func readReceiptFile(path string) (Receipt, error) {
	if path == "" || !filepath.IsAbs(path) {
		return Receipt{}, errors.New("workload receipt path must be absolute")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Receipt{}, fmt.Errorf("read workload receipt %q: %w", path, err)
	}
	return decodeReceipt(raw)
}

// validateReceiptValue 按固定顺序校验回执身份、生产者、执行和溯源。
func validateReceiptValue(value Receipt, workload Workload, document Catalog, repoRoot string) error {
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

// completionActionOrder 返回固定的 root-cohort 完成动作顺序。
func completionActionOrder() []string {
	return []string{
		"mark_draining",
		"shutdown_forwarders",
		"shutdown_daemon",
		"verify",
		"completed",
	}
}

// RequireRemoteCompletionAuthority 在任何远程或本地执行前检查 completion
// artifact 的权威绑定；默认 15 分钟 workload 尚无该能力时必须保持 N/V。
func RequireRemoteCompletionAuthority(workload Workload) error {
	if workload.ID == default15mWorkloadID {
		return fmt.Errorf("workload %q is N/V: remote run/job/artifact authority binding is unavailable", workload.ID)
	}
	return nil
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
	if err := validateCompletionAttachInput(value, repoRoot, completionPath); err != nil {
		return err
	}
	raw, proof, err := readCompletionProof(completionPath)
	if err != nil {
		return err
	}
	gitHead, sourceTree, err := currentGitIdentity(repoRoot)
	if err != nil {
		return fmt.Errorf("resolve current Git identity: %w", err)
	}
	if proof.GitHead != gitHead || proof.SourceTreeDigest != sourceTree {
		return errors.New("completion receipt Git HEAD/tree does not match current repository")
	}
	applyCompletionProvenance(value, proof, gitHead, sourceTree, raw, completionPath)
	if err := validateReceiptProvenanceFields(*value, value.WorkloadID); err != nil {
		return err
	}
	return validateCompletionProof(proof, *value, value.WorkloadID)
}

// validateCompletionAttachInput 校验 completion 绑定的输入边界和默认 workload 门禁。
func validateCompletionAttachInput(value *Receipt, repoRoot, completionPath string) error {
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
	return nil
}

// readCompletionProof 读取并解码指定 completion receipt，同时保留原始字节摘要输入。
func readCompletionProof(path string) ([]byte, completionProof, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, completionProof{}, fmt.Errorf("read completion receipt: %w", err)
	}
	proof, err := decodeCompletionProof(raw)
	if err != nil {
		return nil, completionProof{}, fmt.Errorf("decode completion receipt: %w", err)
	}
	return raw, proof, nil
}

// applyCompletionProvenance 将 proof 的远程坐标和 root-cohort 字段写入回执。
func applyCompletionProvenance(value *Receipt, proof completionProof, gitHead, sourceTree string, raw []byte, path string) {
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
	value.CompletionReceiptPath = filepath.Clean(path)
	value.ActionOrder = append([]string(nil), proof.ActionOrder...)
	value.ForwarderCountAfter = proof.ForwarderCountAfter
	value.DaemonObservedAfter = proof.DaemonObservedAfter
	value.TelemetryIdentitiesGone = proof.TelemetryIdentitiesGone
	value.EndpointUnreachable = proof.EndpointUnreachable
	value.NativeOwnerReleased = proof.NativeOwnerReleased
	value.QuietWindowVerified = proof.QuietWindowVerified
	value.NextEpoch = proof.NextEpoch
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

// validateReceiptProvenanceFields 校验回执的 Git、远程权威和完成链字段。
func validateReceiptProvenanceFields(value Receipt, workloadID string) error {
	checks := []func() error{
		func() error { return validateReceiptGitIdentity(value, workloadID) },
		func() error { return validateReceiptDigestFields(value, workloadID) },
		func() error { return validateReceiptAuthorityFields(value, workloadID) },
		func() error { return validateReceiptEpochFields(value, workloadID) },
		func() error { return validateReceiptTimeFields(value, workloadID) },
		func() error { return validateReceiptCompletionFields(value, workloadID) },
	}
	for _, check := range checks {
		if err := check(); err != nil {
			return err
		}
	}
	return nil
}

// validateReceiptGitIdentity 校验 receipt 声称的 Git HEAD/tree 标识。
func validateReceiptGitIdentity(value Receipt, workloadID string) error {
	if !digestPattern.MatchString(value.GitHead) && !validGitOID(value.GitHead) {
		return fmt.Errorf("workload receipt git_head is invalid for %q", workloadID)
	}
	if !validGitOID(value.SourceTreeDigest) {
		return fmt.Errorf("workload receipt source_tree_digest is invalid for %q", workloadID)
	}
	return nil
}

// validateReceiptDigestFields 校验 cohort、owner 和 artifact digest 字段。
func validateReceiptDigestFields(value Receipt, workloadID string) error {
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
	return nil
}

// validateReceiptAuthorityFields 校验远程 run、job、artifact 名称边界。
func validateReceiptAuthorityFields(value Receipt, workloadID string) error {
	for name, authorityID := range map[string]string{
		"remote_run_id":        value.RemoteRunID,
		"remote_job_id":        value.RemoteJobID,
		"remote_artifact_name": value.RemoteArtifactName,
	} {
		if !validAuthorityID(authorityID) {
			return fmt.Errorf("workload receipt %s is invalid for %q", name, workloadID)
		}
	}
	return nil
}

// validateReceiptEpochFields 校验 root-cohort epoch 单调递增。
func validateReceiptEpochFields(value Receipt, workloadID string) error {
	if value.Epoch == 0 || value.NextEpoch <= value.Epoch {
		return fmt.Errorf("workload receipt epoch transition is invalid for %q", workloadID)
	}
	return nil
}

// validateReceiptTimeFields 校验 workload 时间戳和 execution 时间戳一致。
func validateReceiptTimeFields(value Receipt, workloadID string) error {
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
	return nil
}

// validateReceiptCompletionFields 校验完成动作顺序、进程和隔离证明。
func validateReceiptCompletionFields(value Receipt, workloadID string) error {
	if !slices.Equal(value.ActionOrder, completionActionOrder()) {
		return fmt.Errorf("workload receipt action order is invalid for %q", workloadID)
	}
	if value.ForwarderCountAfter != 0 || value.DaemonObservedAfter || !value.TelemetryIdentitiesGone ||
		!value.EndpointUnreachable || !value.NativeOwnerReleased || !value.QuietWindowVerified {
		return fmt.Errorf("workload receipt completion verification is incomplete for %q", workloadID)
	}
	return nil
}

// validGitOID 判断值是否为小写十六进制 Git object id。
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

// decodeCompletionProof 解码 completion receipt 的固定溯源字段并拒绝缺失字段。
func decodeCompletionProof(raw []byte) (completionProof, error) {
	fields, err := decodeCompletionProofFields(raw)
	if err != nil {
		return completionProof{}, err
	}
	var proof completionProof
	if err := decodeCompletionIdentityFields(fields, &proof); err != nil {
		return completionProof{}, err
	}
	if err := decodeCompletionAuthorityFields(fields, &proof); err != nil {
		return completionProof{}, err
	}
	if err := decodeCompletionVerificationFields(fields, &proof); err != nil {
		return completionProof{}, err
	}
	return proof, nil
}

// decodeCompletionProofFields 将 completion receipt 解析为可选择的 JSON 字段。
func decodeCompletionProofFields(raw []byte) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	// Completion receipts are owned by the mcp-lsp controller and may grow
	// fields independently; retain strict JSON syntax while selecting only the
	// frozen provenance fields below.
	if err := decoder.Decode(&fields); err != nil {
		return nil, err
	}
	if err := requireDecoderEOF(decoder); err != nil {
		return nil, err
	}
	return fields, nil
}

// decodeCompletionIdentityFields 读取 Git、cohort、epoch 等源身份字段。
func decodeCompletionIdentityFields(fields map[string]json.RawMessage, proof *completionProof) error {
	var err error
	proof.GitHead, err = requiredJSONField[string](fields, "git_head")
	if err != nil {
		return err
	}
	proof.SourceTreeDigest, err = requiredJSONField[string](fields, "source_tree_digest")
	if err != nil {
		return err
	}
	proof.CohortID, err = requiredJSONField[string](fields, "cohort_id")
	if err != nil {
		return err
	}
	proof.RepositoryInstanceProofHash, err = requiredJSONField[string](fields, "repository_instance_proof_hash")
	if err != nil {
		return err
	}
	proof.Epoch, err = requiredJSONField[uint64](fields, "epoch")
	if err != nil {
		return err
	}
	proof.DaemonOwnerReceiptHash, err = requiredJSONField[string](fields, "daemon_owner_receipt_hash")
	if err != nil {
		return err
	}
	return nil
}

// decodeCompletionAuthorityFields 读取远程 run、job 和 artifact 权威坐标。
func decodeCompletionAuthorityFields(fields map[string]json.RawMessage, proof *completionProof) error {
	var err error
	proof.RemoteRunID, err = requiredJSONField[string](fields, "remote_run_id")
	if err != nil {
		return err
	}
	proof.RemoteJobID, err = requiredJSONField[string](fields, "remote_job_id")
	if err != nil {
		return err
	}
	proof.RemoteArtifactName, err = requiredJSONField[string](fields, "remote_artifact_name")
	if err != nil {
		return err
	}
	proof.RemoteArtifactDigest, err = requiredJSONField[string](fields, "remote_artifact_digest")
	if err != nil {
		return err
	}
	return nil
}

// decodeCompletionVerificationFields 读取 completion 链的动作、状态和隔离证明。
func decodeCompletionVerificationFields(fields map[string]json.RawMessage, proof *completionProof) error {
	var err error
	proof.ActionOrder, err = requiredJSONField[[]string](fields, "action_order")
	if err != nil {
		return err
	}
	proof.ForwarderCountAfter, err = requiredJSONField[int](fields, "forwarder_count_after")
	if err != nil {
		return err
	}
	proof.DaemonObservedAfter, err = requiredJSONField[bool](fields, "daemon_observed_after")
	if err != nil {
		return err
	}
	proof.TelemetryIdentitiesGone, err = requiredJSONField[bool](fields, "telemetry_identities_gone")
	if err != nil {
		return err
	}
	proof.EndpointUnreachable, err = requiredJSONField[bool](fields, "endpoint_unreachable")
	if err != nil {
		return err
	}
	proof.NativeOwnerReleased, err = requiredJSONField[bool](fields, "native_owner_released")
	if err != nil {
		return err
	}
	proof.QuietWindowVerified, err = requiredJSONField[bool](fields, "quiet_window_verified")
	if err != nil {
		return err
	}
	proof.NextEpoch, err = requiredJSONField[uint64](fields, "next_epoch")
	if err != nil {
		return err
	}
	proof.Status, err = requiredJSONField[string](fields, "status")
	if err != nil {
		return err
	}
	return nil
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

// validateCompletionProof 比较 completion proof 与已写入 receipt 的所有权威坐标。
func validateCompletionProof(proof completionProof, value Receipt, workloadID string) error {
	if !completionProofMatchesReceipt(proof, value) {
		return fmt.Errorf("completion receipt provenance chain mismatch for %q", workloadID)
	}
	return validateCompletionProofFields(proof, workloadID)
}

// completionProofMatchesReceipt 汇总 root-cohort 与远程 artifact 坐标比较。
func completionProofMatchesReceipt(proof completionProof, value Receipt) bool {
	return completionProofRootMatchesReceipt(proof, value) && completionProofAuthorityMatchesReceipt(proof, value)
}

// completionProofRootMatchesReceipt 比较 Git、cohort、epoch 和 owner 字段。
func completionProofRootMatchesReceipt(proof completionProof, value Receipt) bool {
	return proof.GitHead == value.GitHead && proof.SourceTreeDigest == value.SourceTreeDigest &&
		proof.CohortID == value.CohortID && proof.RepositoryInstanceProofHash == value.RepositoryInstanceProofHash &&
		proof.Epoch == value.Epoch && proof.DaemonOwnerReceiptHash == value.DaemonOwnerReceiptHash
}

// completionProofAuthorityMatchesReceipt 比较远程 run、job 和 artifact 字段。
func completionProofAuthorityMatchesReceipt(proof completionProof, value Receipt) bool {
	return proof.RemoteRunID == value.RemoteRunID && proof.RemoteJobID == value.RemoteJobID &&
		proof.RemoteArtifactName == value.RemoteArtifactName && proof.RemoteArtifactDigest == value.RemoteArtifactDigest
}

// validateCompletionProofFields 在远程 authority 尚未接入时保持 N/V。
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
