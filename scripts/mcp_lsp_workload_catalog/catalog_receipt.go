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
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

// ValidateReceipt 按精确目录摘要和 ID 校验已产出的回执。
func ValidateReceipt(document Catalog, id, path string) error {
	repoRoot, err := receiptRepositoryRoot(path)
	if err != nil {
		return err
	}
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
	return validateReceiptValue(value, workload, document)
}

// ValidateReceiptAtCandidate 校验回执并要求其 source tree 绑定请求的候选 tree。
func ValidateReceiptAtCandidate(document Catalog, repoRoot, tree, id, path string) error {
	if !validGitOID(tree) {
		return errors.New("candidate tree identity is invalid")
	}
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
	if value.SourceTreeDigest != tree {
		return fmt.Errorf("workload receipt source tree %q does not match candidate tree", value.SourceTreeDigest)
	}
	if err := validateCandidateReceiptGitHead(repoRoot, tree, value); err != nil {
		return err
	}
	return validateReceiptValue(value, workload, document)
}

// validateCandidateReceiptGitHead binds the optional-looking GitHead field to
// the requested tree without consulting mutable HEAD.  A receipt cannot claim
// a commit whose tree differs from the candidate source identity.
func validateCandidateReceiptGitHead(repoRoot, tree string, value Receipt) error {
	if !validGitOID(value.GitHead) {
		return errors.New("workload receipt git_head must be a commit object bound to candidate tree")
	}
	resolved, err := runGitIdentity(repoRoot, "rev-parse", "--verify", "--end-of-options", value.GitHead+"^{tree}")
	if err != nil {
		return fmt.Errorf("resolve workload receipt git_head tree: %w", err)
	}
	if resolved != tree {
		return errors.New("workload receipt git_head is not bound to candidate tree")
	}
	return nil
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
func validateReceiptValue(value Receipt, workload Workload, document Catalog) error {
	if err := validateReceiptIdentity(value, workload, document); err != nil {
		return err
	}
	if err := validateReceiptProducer(value, workload); err != nil {
		return err
	}
	if err := validateReceiptExecution(value, workload); err != nil {
		return err
	}
	if err := validateReceiptStatus(value, workload.ID); err != nil {
		return err
	}
	return rejectUnboundReceiptAuthority(workload)
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
const idleQuickWorkloadID = "mcp-lsp-idle-quick"
const idleQuickCommandSHA256 = "a75d4d2d5f393e8b38246f7b499f335ece6e5de2f36678a7cc5ae118e189df21"

// RequireIdleQuickReleaseRole freezes the release gate role before authority lookup.
func RequireIdleQuickReleaseRole(workload Workload) error {
	if !idleQuickIdentityMatches(workload) || !idleQuickExecutionMatches(workload) || !idleQuickReceiptMatches(workload) {
		return fmt.Errorf("workload %q does not match the canonical idle-quick release role", workload.ID)
	}
	return nil
}

func idleQuickIdentityMatches(workload Workload) bool {
	return workload.ID == idleQuickWorkloadID && workload.ImplementationStatus == "implemented" &&
		workload.ProducerImplementationStatus == "missing" && workload.TriggerClass == "quick"
}

func idleQuickExecutionMatches(workload Workload) bool {
	return workload.RunnerTarget == "remote-go-test" && workload.TimeoutSeconds == 240 &&
		slices.Equal(workload.Platforms, []string{"darwin", "linux", "windows"}) && idleQuickCommandDigest(workload.Command) == idleQuickCommandSHA256
}

func idleQuickReceiptMatches(workload Workload) bool {
	return workload.ReceiptSchema == ReceiptSchema && workload.ReceiptRequired != nil && *workload.ReceiptRequired &&
		workload.T6Blocking && workload.ReleaseBlocking && workload.ProducerWorkflowPath == ".github/workflows/ci.yml" &&
		workload.ProducerArtifactName == "mcp-lsp-idle-quick-receipt"
}

func idleQuickCommandDigest(command []string) string {
	raw, err := json.Marshal(command)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// RequireRemoteCompletionAuthority 在执行前拒绝缺少 canonical artifact owner 的
// 远程 workload；本地 host workload 仍可运行，但其结果不是发布回执。
func RequireRemoteCompletionAuthority(workload Workload) error {
	if !IsRemoteAuthoritativeWorkload(workload) {
		return nil
	}
	return fmt.Errorf("workload %q is N/V: remote run/job/artifact authority binding is unavailable", workload.ID)
}

// rejectUnboundReceiptAuthority 拒绝把本地 host 输出或未绑定的远程 artifact
// 当作发布 PASS。远程 executor 接入受信 receipt owner 前没有例外分支。
func rejectUnboundReceiptAuthority(workload Workload) error {
	return fmt.Errorf("workload %q is N/V: remote run/job/artifact authority binding is unavailable", workload.ID)
}

// IsLocalRunnerWorkload 判断 workload 是否只能由本地 runner 执行。
func IsLocalRunnerWorkload(workload Workload) bool {
	return strings.HasPrefix(strings.TrimSpace(workload.RunnerTarget), "local-")
}

// IsRemoteAuthoritativeWorkload 按 workload 身份、触发类别和 runner 目标分类。
// local-* runner（quick/native）只走本地回执，不要求远程 artifact authority。
func IsRemoteAuthoritativeWorkload(workload Workload) bool {
	if workload.ID == default15mWorkloadID || workload.TriggerClass == default15mTriggerClass {
		return true
	}
	return strings.TrimSpace(workload.RunnerTarget) != "" && !IsLocalRunnerWorkload(workload)
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
	output, err := BoundedGitOutput(repoRoot, args...)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(output))
	if value == "" {
		return "", errors.New("git returned an empty identity")
	}
	return value, nil
}
