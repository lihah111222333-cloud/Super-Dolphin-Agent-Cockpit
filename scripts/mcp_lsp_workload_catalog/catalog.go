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
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"
)

const (
	Schema        = "super-dolphin/mcp-lsp-workload-catalog/v1"
	ReceiptSchema = "super-dolphin/mcp-lsp-workload-receipt/v1"
	Path          = "scripts/mcp_lsp_workload_catalog.json"
)

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Catalog is the only local workload decision source.
type Catalog struct {
	Schema        string     `json:"schema"`
	CatalogDigest string     `json:"catalog_digest"`
	Workloads     []Workload `json:"workloads"`
}

// Workload describes one canonical local or release prerequisite.
type Workload struct {
	ID                   string   `json:"id"`
	ImplementationStatus string   `json:"implementation_status"`
	RunnerTarget         string   `json:"runner_target"`
	Platforms            []string `json:"platforms"`
	TimeoutSeconds       int      `json:"timeout_seconds"`
	TriggerClass         string   `json:"trigger_class"`
	ReceiptSchema        string   `json:"receipt_schema"`
	ProducerWorkflowPath string   `json:"producer_workflow_path"`
	ProducerArtifactName string   `json:"producer_artifact_name"`
	T6Blocking           bool     `json:"t6_blocking"`
	ReleaseBlocking      bool     `json:"release_blocking"`
	ReceiptRequired      *bool    `json:"receipt_required"`
	Command              []string `json:"command"`
}

// Receipt is the versioned local workload receipt consumed by catalog guards.
type Receipt struct {
	Schema               string   `json:"schema"`
	WorkloadID           string   `json:"workload_id"`
	CatalogDigest        string   `json:"catalog_digest"`
	RunnerTarget         string   `json:"runner_target"`
	ProducerWorkflowPath string   `json:"producer_workflow_path"`
	ProducerArtifactName string   `json:"producer_artifact_name"`
	ExecutionOrigin      string   `json:"execution_origin"`
	Platform             string   `json:"platform"`
	TimeoutSeconds       int      `json:"timeout_seconds"`
	Command              []string `json:"command"`
	StartedAt            string   `json:"started_at"`
	FinishedAt           string   `json:"finished_at"`
	Status               string   `json:"status"`
	ExitCode             int      `json:"exit_code"`
}

// Load 读取并校验仓库拥有的目录及其摘要。
func Load(repoRoot string) (Catalog, error) {
	path := filepath.Join(repoRoot, filepath.FromSlash(Path))
	raw, err := os.ReadFile(path)
	if err != nil {
		return Catalog{}, fmt.Errorf("read workload catalog %q: %w", path, err)
	}
	var document Catalog
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return Catalog{}, fmt.Errorf("decode workload catalog: %w", err)
	}
	if err := requireDecoderEOF(decoder); err != nil {
		return Catalog{}, fmt.Errorf("decode workload catalog: %w", err)
	}
	if err := Validate(document, raw, repoRoot); err != nil {
		return Catalog{}, err
	}
	return document, nil
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
	if workload.ID == "" {
		return errors.New("workload ID is required")
	}
	if seen[workload.ID] {
		return fmt.Errorf("duplicate workload ID %q", workload.ID)
	}
	seen[workload.ID] = true
	if err := validateWorkloadStatus(workload); err != nil {
		return err
	}
	if err := validateWorkloadMetadata(workload); err != nil {
		return err
	}
	if err := validateWorkloadReceipt(workload); err != nil {
		return err
	}
	if err := validateWorkloadProducer(workload, repoRoot); err != nil {
		return err
	}
	if err := validateWorkloadImplementation(workload); err != nil {
		return err
	}
	if err := validateWorkloadCommand(workload); err != nil {
		return err
	}
	if err := validateWorkloadPlatforms(workload); err != nil {
		return err
	}
	return nil
}

func validateWorkloadStatus(workload Workload) error {
	if workload.ImplementationStatus == "implemented" || workload.ImplementationStatus == "missing" {
		return nil
	}
	return fmt.Errorf("workload %q has unsupported implementation_status %q", workload.ID, workload.ImplementationStatus)
}

func validateWorkloadMetadata(workload Workload) error {
	if strings.TrimSpace(workload.RunnerTarget) == "" {
		return fmt.Errorf("workload %q is missing runner, platform, timeout, or trigger class", workload.ID)
	}
	if len(workload.Platforms) == 0 {
		return fmt.Errorf("workload %q is missing runner, platform, timeout, or trigger class", workload.ID)
	}
	if workload.TimeoutSeconds <= 0 {
		return fmt.Errorf("workload %q is missing runner, platform, timeout, or trigger class", workload.ID)
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
	if strings.TrimSpace(workload.ProducerWorkflowPath) == "" {
		return fmt.Errorf("workload %q is missing producer coordinates", workload.ID)
	}
	if strings.TrimSpace(workload.ProducerArtifactName) == "" || strings.ContainsAny(workload.ProducerArtifactName, `/\\`) {
		return fmt.Errorf("workload %q is missing producer coordinates", workload.ID)
	}
	workflowPath := filepath.Join(repoRoot, filepath.FromSlash(workload.ProducerWorkflowPath))
	info, err := os.Stat(workflowPath)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("workload %q producer workflow path is unavailable: %s", workload.ID, workload.ProducerWorkflowPath)
	}
	return nil
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

// ValidateReceipt 按精确目录摘要和 ID 校验已产出的回执。
func ValidateReceipt(document Catalog, id, path string) error {
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
	return validateReceiptStatus(value, workload.ID)
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
	if value.ExecutionOrigin != "local-runner" {
		return fmt.Errorf("workload receipt for %q has unsupported execution origin %q", workload.ID, value.ExecutionOrigin)
	}
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
	started, err := time.Parse(time.RFC3339Nano, value.StartedAt)
	if err != nil {
		return fmt.Errorf("workload receipt started_at is invalid for %q: %w", workload.ID, err)
	}
	finished, err := time.Parse(time.RFC3339Nano, value.FinishedAt)
	if err != nil {
		return fmt.Errorf("workload receipt finished_at is invalid for %q: %w", workload.ID, err)
	}
	if finished.Before(started) {
		return fmt.Errorf("workload receipt finished_at precedes started_at for %q", workload.ID)
	}
	if finished.Sub(started) > time.Duration(workload.TimeoutSeconds)*time.Second {
		return fmt.Errorf("workload receipt duration exceeds timeout for %q", workload.ID)
	}
	return nil
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
