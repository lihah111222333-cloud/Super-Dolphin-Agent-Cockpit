package archtest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

const (
	// GuardFreezeVersion 标识统一冻结文件结构版本。
	GuardFreezeVersion = 3
	guardFreezeMode    = 0o644
	guardReviewMaxAge  = 90 * 24 * time.Hour
	guardClockSkew     = 5 * time.Minute
)

// GuardMetricFreeze 保存普通 metrics 棘轮的生产和测试冻结区。
type GuardMetricFreeze struct {
	Production Baseline `json:"production"`
	Tests      Baseline `json:"tests"`
}

// GuardFreezeAcceptance 保存一次显式冻结所需的审批与 fail-first 证据。
type GuardFreezeAcceptance struct {
	Owner             string `json:"owner"`
	Reason            string `json:"reason"`
	ReviewedAt        string `json:"reviewed_at"`
	ReviewBy          string `json:"review_by"`
	FailFirstEvidence string `json:"fail_first_evidence"`
	EvidenceSHA256    string `json:"evidence_sha256"`
}

// GuardFreezeSnapshot 保存审批时不可扩张的守卫债务上界。
type GuardFreezeSnapshot struct {
	Metrics     GuardMetricFreeze   `json:"metrics"`
	PrioritySSA PrioritySSABaseline `json:"priority_ssa"`
}

// GuardFreeze 是所有守卫共用的统一冻结文件结构。
type GuardFreeze struct {
	Version     int                   `json:"version"`
	Acceptance  GuardFreezeAcceptance `json:"acceptance"`
	Approved    GuardFreezeSnapshot   `json:"approved"`
	Metrics     GuardMetricFreeze     `json:"metrics"`
	PrioritySSA PrioritySSABaseline   `json:"priority_ssa"`
}

// GuardFreezeInfo 封装统一冻结文件数据和文件元信息。
type GuardFreezeInfo struct {
	Data    GuardFreeze
	ModTime time.Time
}

// NewEmptyGuardFreeze 返回带审批信息和完整空分区的统一冻结结构。
func NewEmptyGuardFreeze(acceptance GuardFreezeAcceptance) GuardFreeze {
	return GuardFreeze{
		Version:    GuardFreezeVersion,
		Acceptance: acceptance,
		Approved:   newEmptyGuardFreezeSnapshot(),
		Metrics: GuardMetricFreeze{
			Production: Baseline{},
			Tests:      Baseline{},
		},
		PrioritySSA: PrioritySSABaseline{},
	}
}

// LoadGuardFreeze 读取统一冻结文件并校验所有分区存在。
func LoadGuardFreeze(path string) (GuardFreezeInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return GuardFreezeInfo{}, fmt.Errorf("stat guard freeze: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return GuardFreezeInfo{}, fmt.Errorf("read guard freeze: %w", err)
	}
	freeze, err := decodeGuardFreeze(data)
	if err != nil {
		return GuardFreezeInfo{}, fmt.Errorf("parse guard freeze: %w", err)
	}
	if err := validateGuardFreeze(path, freeze); err != nil {
		return GuardFreezeInfo{}, err
	}
	return GuardFreezeInfo{Data: freeze, ModTime: info.ModTime()}, nil
}

// SaveGuardFreeze 覆盖写入统一冻结文件。
func SaveGuardFreeze(path string, freeze GuardFreeze) error {
	if err := validateGuardFreeze(path, freeze); err != nil {
		return err
	}
	data, err := json.MarshalIndent(freeze, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal guard freeze: %w", err)
	}
	if err := os.WriteFile(path, data, guardFreezeMode); err != nil {
		return fmt.Errorf("write guard freeze: %w", err)
	}
	return nil
}

// FreezeGuardState 扫描当前仓库并生成带审批信息的统一冻结快照。
func FreezeGuardState(opts CheckOptions, acceptance GuardFreezeAcceptance) (GuardFreeze, error) {
	freeze := NewEmptyGuardFreeze(acceptance)
	freeze.Metrics.Production = FreezeBaseline(opts)
	freeze.Metrics.Tests = FreezeTestBaseline(opts)
	priorityViolations, err := CollectPrioritySSAViolations(opts)
	if err != nil {
		return GuardFreeze{}, err
	}
	freeze.PrioritySSA = prioritySSABaselineFromViolations(priorityViolations)
	freeze.Approved = currentGuardFreezeSnapshot(freeze)
	return freeze, nil
}

func newEmptyGuardFreezeSnapshot() GuardFreezeSnapshot {
	return GuardFreezeSnapshot{
		Metrics: GuardMetricFreeze{
			Production: Baseline{},
			Tests:      Baseline{},
		},
		PrioritySSA: PrioritySSABaseline{},
	}
}

func currentGuardFreezeSnapshot(freeze GuardFreeze) GuardFreezeSnapshot {
	return GuardFreezeSnapshot{Metrics: freeze.Metrics, PrioritySSA: freeze.PrioritySSA}
}

// CheckPrioritySSAWithBaseline 使用内存中的 priority SSA baseline 检查新增和失效违规。
func CheckPrioritySSAWithBaseline(opts CheckOptions, baseline PrioritySSABaseline) (PrioritySSABaselineResult, error) {
	current, err := CollectPrioritySSAViolations(opts)
	if err != nil {
		return PrioritySSABaselineResult{}, err
	}
	return comparePrioritySSABaseline(baseline, current), nil
}

// PrioritySSABaselineFromCurrent 返回结果中的当前违规集合，供统一冻结收缩写回。
func PrioritySSABaselineFromCurrent(result PrioritySSABaselineResult) PrioritySSABaseline {
	return prioritySSABaselineFromViolations(result.Current)
}

// ValidateGuardFreezeApproval 校验 CLI 提供的冻结审批字段和复审期限。
func ValidateGuardFreezeApproval(acceptance GuardFreezeAcceptance) error {
	if err := validateGuardFreezeAcceptanceFields(acceptance); err != nil {
		return err
	}
	if err := validateGuardFreezeAcceptanceDates(acceptance); err != nil {
		return err
	}
	return validateGuardFreezeEvidencePath(acceptance.FailFirstEvidence)
}

// ValidateGuardFreezeAcceptance 校验完整冻结审批及其证据摘要。
func ValidateGuardFreezeAcceptance(acceptance GuardFreezeAcceptance) error {
	if err := ValidateGuardFreezeApproval(acceptance); err != nil {
		return err
	}
	decoded, err := hex.DecodeString(acceptance.EvidenceSHA256)
	if err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != acceptance.EvidenceSHA256 {
		return fmt.Errorf("acceptance.evidence_sha256 must be lowercase SHA-256")
	}
	return nil
}

// BindGuardFreezeAcceptance 将审批绑定到当前源码 HEAD、冻结快照和不可变 fail-first 证据摘要。
func BindGuardFreezeAcceptance(repoRoot, sourceHead string, freeze GuardFreeze) (GuardFreezeAcceptance, error) {
	acceptance := freeze.Acceptance
	if err := ValidateGuardFreezeApproval(acceptance); err != nil {
		return GuardFreezeAcceptance{}, err
	}
	body, err := readGuardFreezeEvidence(repoRoot, acceptance.FailFirstEvidence)
	if err != nil {
		return GuardFreezeAcceptance{}, err
	}
	snapshotSHA256, err := guardFreezeSnapshotSHA256(freeze)
	if err != nil {
		return GuardFreezeAcceptance{}, err
	}
	if err := validateGuardFreezeEvidenceBody(body, acceptance, sourceHead, snapshotSHA256); err != nil {
		return GuardFreezeAcceptance{}, err
	}
	digest := sha256.Sum256(body)
	acceptance.EvidenceSHA256 = hex.EncodeToString(digest[:])
	return acceptance, nil
}

func validateGuardFreezeAcceptanceFields(acceptance GuardFreezeAcceptance) error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "owner", value: acceptance.Owner},
		{name: "reason", value: acceptance.Reason},
		{name: "reviewed_at", value: acceptance.ReviewedAt},
		{name: "review_by", value: acceptance.ReviewBy},
		{name: "fail_first_evidence", value: acceptance.FailFirstEvidence},
	} {
		if field.value == "" || strings.TrimSpace(field.value) != field.value {
			return fmt.Errorf("acceptance.%s must be non-empty and trimmed", field.name)
		}
	}
	return nil
}

// validateGuardFreezeAcceptanceDates 校验审批时间格式及复审日期的先后关系。
func validateGuardFreezeAcceptanceDates(acceptance GuardFreezeAcceptance) error {
	return validateGuardFreezeAcceptanceDatesAt(acceptance, time.Now().UTC())
}

// validateGuardFreezeAcceptanceDatesAt 使用显式当前时间校验审批日期，便于稳定测试到期阻断。
func validateGuardFreezeAcceptanceDatesAt(acceptance GuardFreezeAcceptance, now time.Time) error {
	reviewedAt, err := time.Parse(time.RFC3339, acceptance.ReviewedAt)
	if err != nil || reviewedAt.Location() != time.UTC || reviewedAt.Format(time.RFC3339) != acceptance.ReviewedAt {
		return fmt.Errorf("acceptance.reviewed_at must be UTC RFC3339 with second precision")
	}
	reviewBy, err := time.Parse(time.DateOnly, acceptance.ReviewBy)
	if err != nil {
		return fmt.Errorf("acceptance.review_by must use YYYY-MM-DD")
	}
	if !reviewBy.After(reviewedAt) {
		return fmt.Errorf("acceptance.review_by must be after reviewed_at")
	}
	if reviewedAt.After(now.Add(guardClockSkew)) {
		return fmt.Errorf("acceptance.reviewed_at must not be in the future")
	}
	if reviewBy.Sub(reviewedAt) > guardReviewMaxAge {
		return fmt.Errorf("acceptance review period must not exceed 90 days")
	}
	if reviewBy.Before(now.Truncate(24 * time.Hour)) {
		return fmt.Errorf("acceptance.review_by has expired")
	}
	return nil
}

// validateGuardFreezeEvidencePath 约束 fail-first 证据只能使用规范化仓库相对路径。
func validateGuardFreezeEvidencePath(evidence string) error {
	if filepath.IsAbs(evidence) {
		return fmt.Errorf("acceptance.fail_first_evidence must be repository-relative")
	}
	if strings.Contains(evidence, `\`) || evidence == "." || filepath.ToSlash(filepath.Clean(evidence)) != evidence ||
		strings.HasPrefix(evidence, "../") {
		return fmt.Errorf("acceptance.fail_first_evidence must be a normalized repository-relative path")
	}
	return nil
}

func decodeGuardFreeze(data []byte) (GuardFreeze, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var freeze GuardFreeze
	if err := decoder.Decode(&freeze); err != nil {
		return GuardFreeze{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return GuardFreeze{}, fmt.Errorf("trailing JSON content")
	}
	if err := validateGuardFreeze("<json>", freeze); err != nil {
		return GuardFreeze{}, err
	}
	return freeze, nil
}

// validateGuardFreeze 校验冻结文件版本、审批、证据及各项基线完整性。
func validateGuardFreeze(path string, freeze GuardFreeze) error {
	if freeze.Version != GuardFreezeVersion {
		return fmt.Errorf("guard freeze %s version=%d, want %d", path, freeze.Version, GuardFreezeVersion)
	}
	if err := ValidateGuardFreezeAcceptance(freeze.Acceptance); err != nil {
		return fmt.Errorf("guard freeze %s invalid acceptance: %w", path, err)
	}
	if path != "<json>" {
		if err := validateGuardFreezeEvidence(path, freeze); err != nil {
			return err
		}
	}
	if freeze.Metrics.Production == nil {
		return fmt.Errorf("guard freeze %s missing metrics.production", path)
	}
	if freeze.Metrics.Tests == nil {
		return fmt.Errorf("guard freeze %s missing metrics.tests", path)
	}
	if freeze.PrioritySSA == nil {
		return fmt.Errorf("guard freeze %s missing priority_ssa", path)
	}
	if err := validatePrioritySSABaseline(path, freeze.PrioritySSA); err != nil {
		return err
	}
	if err := validateGuardFreezeSnapshot(path, "approved", freeze.Approved); err != nil {
		return err
	}
	return validateGuardFreezeWithinApproved(path, freeze)
}

func validateGuardFreezeSnapshot(path, field string, snapshot GuardFreezeSnapshot) error {
	if snapshot.Metrics.Production == nil || snapshot.Metrics.Tests == nil || snapshot.PrioritySSA == nil {
		return fmt.Errorf("guard freeze %s %s snapshot contains null partition", path, field)
	}
	return validatePrioritySSABaseline(path+" "+field, snapshot.PrioritySSA)
}

// validateGuardFreezeWithinApproved 确保当前债务只能从审批快照收缩，不能新增或放宽。
func validateGuardFreezeWithinApproved(path string, freeze GuardFreeze) error {
	if err := validateMetricBaselineSubset("metrics.production", freeze.Metrics.Production, freeze.Approved.Metrics.Production); err != nil {
		return fmt.Errorf("guard freeze %s exceeds approved snapshot: %w", path, err)
	}
	if err := validateMetricBaselineSubset("metrics.tests", freeze.Metrics.Tests, freeze.Approved.Metrics.Tests); err != nil {
		return fmt.Errorf("guard freeze %s exceeds approved snapshot: %w", path, err)
	}
	for key, current := range freeze.PrioritySSA {
		approved, ok := freeze.Approved.PrioritySSA[key]
		if !ok || !reflect.DeepEqual(current, approved) {
			return fmt.Errorf("guard freeze %s exceeds approved snapshot: priority_ssa[%q] is not approved", path, key)
		}
	}
	return nil
}

// validateMetricBaselineSubset 使用指标注册表验证当前普通基线是审批基线的子集。
func validateMetricBaselineSubset(name string, current, approved Baseline) error {
	for path, currentMetrics := range current {
		approvedMetrics, ok := approved[path]
		if !ok {
			return fmt.Errorf("%s[%q] is not approved", name, path)
		}
		for _, rule := range metricRules() {
			if *rule.Access(&currentMetrics) > *rule.Access(&approvedMetrics) {
				return fmt.Errorf("%s[%q].%s exceeds approved value", name, path, rule.Field)
			}
		}
		if currentMetrics.HasInit && !approvedMetrics.HasInit {
			return fmt.Errorf("%s[%q].has_init exceeds approved value", name, path)
		}
	}
	return nil
}

// validateGuardFreezeEvidence 校验 fail-first 证据是普通文件且包含最小复现字段。
func validateGuardFreezeEvidence(freezePath string, freeze GuardFreeze) error {
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Clean(freezePath))))
	body, err := readGuardFreezeEvidence(repoRoot, freeze.Acceptance.FailFirstEvidence)
	if err != nil {
		return fmt.Errorf("guard freeze %s fail-first evidence: %w", freezePath, err)
	}
	digest := sha256.Sum256(body)
	if got := hex.EncodeToString(digest[:]); got != freeze.Acceptance.EvidenceSHA256 {
		return fmt.Errorf("guard freeze %s fail-first evidence SHA-256 mismatch", freezePath)
	}
	snapshotSHA256, err := guardFreezeSnapshotSHA256(freeze)
	if err != nil {
		return err
	}
	return validateGuardFreezeEvidenceBody(body, freeze.Acceptance, "", snapshotSHA256)
}

func guardFreezeSnapshotSHA256(freeze GuardFreeze) (string, error) {
	body, err := json.Marshal(freeze.Approved)
	if err != nil {
		return "", fmt.Errorf("marshal guard freeze snapshot: %w", err)
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

// readGuardFreezeEvidence 读取仓库内普通证据文件，并拒绝符号链接越界。
func readGuardFreezeEvidence(repoRoot, evidencePath string) ([]byte, error) {
	absEvidence := filepath.Join(repoRoot, filepath.FromSlash(evidencePath))
	resolvedRoot, err := filepath.EvalSymlinks(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve repository root: %w", err)
	}
	resolvedEvidence, err := filepath.EvalSymlinks(absEvidence)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedEvidence)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("fail-first evidence must remain inside repository")
	}
	info, err := os.Lstat(absEvidence)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("fail-first evidence must be a regular non-symlink file")
	}
	body, err := os.ReadFile(absEvidence)
	if err != nil {
		return nil, err
	}
	return body, nil
}

// validateGuardFreezeEvidenceBody 校验证据字段与审批、源码 HEAD 和冻结快照一致。
func validateGuardFreezeEvidenceBody(body []byte, acceptance GuardFreezeAcceptance, sourceHead, snapshotSHA256 string) error {
	fields, err := parseGuardFreezeEvidence(body)
	if err != nil {
		return err
	}
	if fields["reviewed_at"] != acceptance.ReviewedAt {
		return fmt.Errorf("fail-first evidence reviewed_at does not match acceptance")
	}
	if sourceHead != "" && fields["source_head"] != sourceHead {
		return fmt.Errorf("fail-first evidence source_head does not match current HEAD")
	}
	if snapshotSHA256 != "" && fields["snapshot_sha256"] != snapshotSHA256 {
		return fmt.Errorf("fail-first evidence snapshot_sha256 does not match freeze snapshot; expected %s", snapshotSHA256)
	}
	return nil
}

// parseGuardFreezeEvidence 严格解析唯一且非空的 fail-first 证据字段。
func parseGuardFreezeEvidence(body []byte) (map[string]string, error) {
	required := map[string]bool{
		"source_head": true, "reviewed_at": true, "snapshot_sha256": true, "working_directory": true,
		"command": true, "expected_exit": true, "observed_failure": true,
	}
	fields := make(map[string]string, len(required))
	for lineNumber, line := range strings.Split(strings.TrimSuffix(string(body), "\n"), "\n") {
		if err := addGuardFreezeEvidenceLine(fields, required, lineNumber+1, line); err != nil {
			return nil, err
		}
	}
	for key := range required {
		if fields[key] == "" {
			return nil, fmt.Errorf("fail-first evidence missing field %q", key)
		}
	}
	if fields["working_directory"] != "." {
		return nil, fmt.Errorf("fail-first evidence working_directory must be repository root '.'")
	}
	if fields["expected_exit"] != "1" {
		return nil, fmt.Errorf("fail-first evidence expected_exit must equal 1")
	}
	return fields, nil
}

// addGuardFreezeEvidenceLine 校验并登记一行唯一的证据键值。
func addGuardFreezeEvidenceLine(fields map[string]string, required map[string]bool, lineNumber int, line string) error {
	key, value, ok := strings.Cut(line, ":")
	if !ok || !required[key] {
		return fmt.Errorf("fail-first evidence line %d has unknown field", lineNumber)
	}
	if _, exists := fields[key]; exists {
		return fmt.Errorf("fail-first evidence duplicate field %q", key)
	}
	value = strings.TrimPrefix(value, " ")
	if value == "" || strings.TrimSpace(value) != value {
		return fmt.Errorf("fail-first evidence field %q must be non-empty and trimmed", key)
	}
	fields[key] = value
	return nil
}
