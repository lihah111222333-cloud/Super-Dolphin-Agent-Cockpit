package archtest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
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

// GuardFreezeAcceptance 保存一次显式冻结所需的审批信息。
type GuardFreezeAcceptance struct {
	Owner      string `json:"owner"`
	Reason     string `json:"reason"`
	ReviewedAt string `json:"reviewed_at"`
	ReviewBy   string `json:"review_by"`
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

func checkPrioritySSAWithBaselinePackages(pkgs []*prioritySSAPackage, baseline PrioritySSABaseline) (PrioritySSABaselineResult, error) {
	current, err := collectPrioritySSAViolationsFromPackages(pkgs)
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
	return nil
}

// ValidateGuardFreezeAcceptance 校验完整冻结审批。
func ValidateGuardFreezeAcceptance(acceptance GuardFreezeAcceptance) error {
	return ValidateGuardFreezeApproval(acceptance)
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

// validateGuardFreeze 校验冻结文件版本、审批及各项基线完整性。
func validateGuardFreeze(path string, freeze GuardFreeze) error {
	if freeze.Version != GuardFreezeVersion {
		return fmt.Errorf("guard freeze %s version=%d, want %d", path, freeze.Version, GuardFreezeVersion)
	}
	if err := ValidateGuardFreezeAcceptance(freeze.Acceptance); err != nil {
		return fmt.Errorf("guard freeze %s invalid acceptance: %w", path, err)
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
