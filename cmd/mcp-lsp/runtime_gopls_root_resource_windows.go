//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
)

const (
	runtimeServerWindowsGoplsRootResourceSchema         = 1
	runtimeServerWindowsGoplsRootResourceWithinLimit    = "within_limit"
	runtimeServerWindowsGoplsRootResourceDeferredLeases = "defer_active_leases"
	runtimeServerWindowsGoplsRootResourceReclaimed      = "reclaim_zero_leases"
)

// runtimeServerWindowsGoplsRootResourceReceipt 是 root owner 唯一发布的严格 Job RSS 决策记录。
type runtimeServerWindowsGoplsRootResourceReceipt struct {
	SchemaVersion       int    `json:"schema_version"`
	ConfigDigest        string `json:"config_digest"`
	CohortID            string `json:"cohort_id"`
	Generation          uint64 `json:"generation"`
	Source              string `json:"source"`
	DaemonPID           int    `json:"daemon_pid"`
	DaemonStartIdentity string `json:"daemon_start_identity"`
	MemberPIDs          []int  `json:"member_pids"`
	RSSBytes            uint64 `json:"rss_bytes"`
	RSSLimitBytes       uint64 `json:"rss_limit_bytes"`
	ActiveLeases        int    `json:"active_leases"`
	Decision            string `json:"decision"`
}

// runtimeServerAccountWindowsGoplsRootResource 查询 broker Job，并在 root 锁内唯一计账。
func runtimeServerAccountWindowsGoplsRootResource(controller *runtimeServerDurableGoplsRootCohortController, config multilsp.GoplsRootCohortConfig, daemon runtimeServerWindowsGoplsDaemonEndpoint) error {
	if err := runtimeServerValidateWindowsGoplsDaemonRecord(daemon); err != nil {
		return fmt.Errorf("validate Windows gopls resource owner: %w", err)
	}
	limit, err := multilsp.WindowsGoplsRootRSSLimitBytes()
	if err != nil {
		return fmt.Errorf("read Windows gopls root RSS limit: %w", err)
	}
	binding := runtimeServerWindowsGoplsObservationBinding{
		ConfigDigest: daemon.ConfigDigest, OwnerPID: daemon.OwnerPID, OwnerStartIdentity: daemon.OwnerStartIdentity,
		DaemonPID: daemon.DaemonPID, DaemonStartIdentity: daemon.DaemonStartIdentity,
	}
	observation, err := runtimeServerQueryWindowsGoplsObservationEndpoint(daemon.ObservationEndpoint, daemon.ObservationCapability, binding)
	if err != nil {
		return fmt.Errorf("query Windows gopls root Job RSS: %w", err)
	}
	_, err = runtimeServerDurableGoplsRootCohortWithStateLock(controller, config, func(dir string, state *runtimeServerDurableGoplsRootCohortState) (struct{}, error) {
		return struct{}{}, runtimeServerWriteWindowsGoplsRootResourceLocked(dir, state, config, daemon, observation, limit)
	})
	return err
}

// runtimeServerReclaimWindowsGoplsRootResourceAfterLease 在最后租约释放后复核并回收仍超限的唯一 Job。
func runtimeServerReclaimWindowsGoplsRootResourceAfterLease(controller *runtimeServerDurableGoplsRootCohortController, config multilsp.GoplsRootCohortConfig, fence multilsp.GoplsRootCohortFence) error {
	if err := config.Validate(); err != nil {
		return err
	}
	if controller == nil || !runtimeServerWindowsGoplsFenceComplete(fence) {
		return multilsp.ErrGoplsRootCohortFenceStale
	}
	_, err := runtimeServerDurableGoplsRootCohortWithStateLock(controller, config, func(dir string, state *runtimeServerDurableGoplsRootCohortState) (struct{}, error) {
		return struct{}{}, runtimeServerReclaimWindowsGoplsRootResourceLocked(dir, state, config, fence)
	})
	return err
}

// runtimeServerWindowsGoplsFenceComplete 拒绝缺少 durable release 身份的 fence。
func runtimeServerWindowsGoplsFenceComplete(fence multilsp.GoplsRootCohortFence) bool {
	return fence.Epoch > 0 && fence.JournalRevision > 0 && fence.MemberGeneration > 0 &&
		strings.TrimSpace(fence.MemberID) != "" && strings.TrimSpace(fence.LeaseID) != ""
}

// runtimeServerReclaimWindowsGoplsRootResourceLocked 在同一 root 锁内复核零租约、fresh RSS、确认回收并退役记录。
func runtimeServerReclaimWindowsGoplsRootResourceLocked(dir string, state *runtimeServerDurableGoplsRootCohortState, config multilsp.GoplsRootCohortConfig, fence multilsp.GoplsRootCohortFence) error {
	digest, zeroLeases, err := runtimeServerWindowsGoplsZeroLeaseDigest(dir, state, config, fence)
	if err != nil || !zeroLeases {
		return err
	}
	daemonPath := filepath.Join(dir, "daemon.json")
	daemon, err := runtimeServerReadWindowsGoplsDaemonRecord(daemonPath)
	if errors.Is(err, os.ErrNotExist) {
		return runtimeServerRequireWindowsGoplsReclaimReceipt(dir, state, config)
	}
	if err != nil {
		return err
	}
	return runtimeServerReclaimWindowsGoplsRootResourceAuthority(dir, state, config, daemonPath, daemon, digest)
}

// runtimeServerWindowsGoplsZeroLeaseDigest 在锁内验证 fence 并计算当前配置摘要与租约状态。
func runtimeServerWindowsGoplsZeroLeaseDigest(dir string, state *runtimeServerDurableGoplsRootCohortState, config multilsp.GoplsRootCohortConfig, fence multilsp.GoplsRootCohortFence) (string, bool, error) {
	if state == nil {
		return "", false, multilsp.ErrGoplsRootCohortFenceStale
	}
	active, err := runtimeServerCountGoplsRootCohortLeases(dir, state.ConfigDigest)
	if err != nil {
		return "", false, err
	}
	if active > 0 || state.Epoch != fence.Epoch {
		return state.ConfigDigest, false, nil
	}
	digest, err := runtimeServerWindowsGoplsDaemonStateDigest(state, config)
	return digest, err == nil, err
}

// runtimeServerReclaimWindowsGoplsRootResourceAuthority 复核 authority 和 fresh RSS 后执行已授权回收事务。
func runtimeServerReclaimWindowsGoplsRootResourceAuthority(dir string, state *runtimeServerDurableGoplsRootCohortState, config multilsp.GoplsRootCohortConfig, daemonPath string, daemon runtimeServerWindowsGoplsDaemonRecord, digest string) error {
	if daemon.ConfigDigest != digest {
		return errors.New("Windows gopls reclaim daemon config identity changed")
	}
	completed, err := runtimeServerWindowsGoplsReclaimReceiptMatchesDaemon(dir, state, config, daemon)
	if err != nil {
		return err
	}
	if completed {
		return runtimeServerRemoveWindowsGoplsDaemonRecord(dir, daemonPath)
	}
	limit, err := multilsp.WindowsGoplsRootRSSLimitBytes()
	if err != nil {
		return fmt.Errorf("read Windows gopls reclaim RSS limit: %w", err)
	}
	binding := runtimeServerWindowsGoplsObservationBinding{
		ConfigDigest: daemon.ConfigDigest, OwnerPID: daemon.OwnerPID, OwnerStartIdentity: daemon.OwnerStartIdentity,
		DaemonPID: daemon.DaemonPID, DaemonStartIdentity: daemon.DaemonStartIdentity,
	}
	observation, err := runtimeServerQueryWindowsGoplsObservationEndpoint(daemon.ObservationEndpoint, daemon.ObservationCapability, binding)
	if err != nil {
		return fmt.Errorf("query Windows gopls reclaim Job RSS: %w", err)
	}
	if observation.RSSBytes <= limit {
		return nil
	}
	receipt, err := runtimeServerNewWindowsGoplsRootResourceReclaimReceipt(state, config, observation, limit)
	if err != nil {
		return err
	}
	if err := runtimeServerReclaimWindowsGoplsObservationEndpoint(daemon.ObservationEndpoint, daemon.ReclaimCapability, binding); err != nil {
		return fmt.Errorf("reclaim Windows gopls Job: %w", err)
	}
	if err := runtimeServerWriteGoplsRootCohortJSON(filepath.Join(dir, "resource.json"), receipt); err != nil {
		return err
	}
	return runtimeServerRemoveWindowsGoplsDaemonRecord(dir, daemonPath)
}

// runtimeServerWriteWindowsGoplsRootResourceLocked 复核当前代际并原子覆盖唯一 receipt。
func runtimeServerWriteWindowsGoplsRootResourceLocked(dir string, state *runtimeServerDurableGoplsRootCohortState, config multilsp.GoplsRootCohortConfig, daemon runtimeServerWindowsGoplsDaemonEndpoint, observation runtimeServerWindowsGoplsJobRSSObservation, limit uint64) error {
	digest, err := runtimeServerWindowsGoplsDaemonStateDigest(state, config)
	if err != nil {
		return err
	}
	current, err := runtimeServerReadWindowsGoplsDaemonRecord(filepath.Join(dir, "daemon.json"))
	if err != nil {
		return err
	}
	if current != daemon || digest != observation.ConfigDigest {
		return errors.New("Windows gopls root resource authority changed before accounting")
	}
	active, err := runtimeServerCountGoplsRootCohortLeases(dir, digest)
	if err != nil {
		return err
	}
	receipt, err := runtimeServerNewWindowsGoplsRootResourceReceipt(state, config, observation, limit, active)
	if err != nil {
		return err
	}
	return runtimeServerWriteGoplsRootCohortJSON(filepath.Join(dir, "resource.json"), receipt)
}

// runtimeServerNewWindowsGoplsRootResourceReceipt 将单一 Job 样本映射为 schema1 receipt。
func runtimeServerNewWindowsGoplsRootResourceReceipt(state *runtimeServerDurableGoplsRootCohortState, config multilsp.GoplsRootCohortConfig, observation runtimeServerWindowsGoplsJobRSSObservation, limit uint64, active int) (runtimeServerWindowsGoplsRootResourceReceipt, error) {
	decision, err := runtimeServerWindowsGoplsRootResourceDecision(observation.RSSBytes, limit, active)
	if err != nil {
		return runtimeServerWindowsGoplsRootResourceReceipt{}, err
	}
	return runtimeServerBuildWindowsGoplsRootResourceReceipt(state, config, observation, limit, active, decision)
}

// runtimeServerNewWindowsGoplsRootResourceReclaimReceipt 构造已确认零租约超限回收的 durable 证据。
func runtimeServerNewWindowsGoplsRootResourceReclaimReceipt(state *runtimeServerDurableGoplsRootCohortState, config multilsp.GoplsRootCohortConfig, observation runtimeServerWindowsGoplsJobRSSObservation, limit uint64) (runtimeServerWindowsGoplsRootResourceReceipt, error) {
	if observation.RSSBytes <= limit || limit == 0 {
		return runtimeServerWindowsGoplsRootResourceReceipt{}, errors.New("Windows gopls reclaim sample is not over limit")
	}
	return runtimeServerBuildWindowsGoplsRootResourceReceipt(state, config, observation, limit, 0, runtimeServerWindowsGoplsRootResourceReclaimed)
}

// runtimeServerBuildWindowsGoplsRootResourceReceipt 统一映射 Job 样本和决策字段。
func runtimeServerBuildWindowsGoplsRootResourceReceipt(state *runtimeServerDurableGoplsRootCohortState, config multilsp.GoplsRootCohortConfig, observation runtimeServerWindowsGoplsJobRSSObservation, limit uint64, active int, decision string) (runtimeServerWindowsGoplsRootResourceReceipt, error) {
	members := append([]int(nil), observation.MemberPIDs...)
	slices.Sort(members)
	if err := runtimeServerValidateWindowsGoplsRootResourceMembers(members, observation.OwnerPID, observation.DaemonPID); err != nil {
		return runtimeServerWindowsGoplsRootResourceReceipt{}, err
	}
	return runtimeServerWindowsGoplsRootResourceReceipt{
		SchemaVersion: runtimeServerWindowsGoplsRootResourceSchema, ConfigDigest: state.ConfigDigest,
		CohortID: config.CohortID, Generation: state.Epoch, Source: observation.Source,
		DaemonPID: observation.DaemonPID, DaemonStartIdentity: observation.DaemonStartIdentity,
		MemberPIDs: members, RSSBytes: observation.RSSBytes, RSSLimitBytes: limit,
		ActiveLeases: active, Decision: decision,
	}, nil
}

// runtimeServerRequireWindowsGoplsReclaimReceipt 只把同代完整 receipt 视为并发回收已完成。
func runtimeServerRequireWindowsGoplsReclaimReceipt(dir string, state *runtimeServerDurableGoplsRootCohortState, config multilsp.GoplsRootCohortConfig) error {
	var receipt runtimeServerWindowsGoplsRootResourceReceipt
	if err := runtimeServerReadGoplsRootCohortJSON(filepath.Join(dir, "resource.json"), &receipt, 32*1024); err != nil {
		return errors.Join(errors.New("Windows gopls daemon record disappeared without reclaim evidence"), err)
	}
	if !runtimeServerWindowsGoplsReclaimReceiptIdentityValid(receipt, state, config) ||
		!runtimeServerWindowsGoplsReclaimReceiptDecisionValid(receipt) {
		return errors.New("Windows gopls reclaim evidence is invalid")
	}
	return runtimeServerValidateWindowsGoplsRootResourceMembers(receipt.MemberPIDs, 0, receipt.DaemonPID)
}

// runtimeServerWindowsGoplsReclaimReceiptMatchesDaemon 识别 ACK 后仅剩 record 退役的幂等重试。
func runtimeServerWindowsGoplsReclaimReceiptMatchesDaemon(dir string, state *runtimeServerDurableGoplsRootCohortState, config multilsp.GoplsRootCohortConfig, daemon runtimeServerWindowsGoplsDaemonRecord) (bool, error) {
	var receipt runtimeServerWindowsGoplsRootResourceReceipt
	err := runtimeServerReadGoplsRootCohortJSON(filepath.Join(dir, "resource.json"), &receipt, 32*1024)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !runtimeServerWindowsGoplsReclaimReceiptIdentityValid(receipt, state, config) {
		return false, errors.New("Windows gopls resource receipt identity is invalid")
	}
	if receipt.Decision != runtimeServerWindowsGoplsRootResourceReclaimed {
		return false, nil
	}
	if !runtimeServerWindowsGoplsReclaimReceiptDecisionValid(receipt) ||
		receipt.DaemonPID != daemon.DaemonPID || receipt.DaemonStartIdentity != daemon.DaemonStartIdentity {
		return false, errors.New("Windows gopls reclaim receipt daemon identity is invalid")
	}
	if err := runtimeServerValidateWindowsGoplsRootResourceMembers(receipt.MemberPIDs, daemon.OwnerPID, daemon.DaemonPID); err != nil {
		return false, err
	}
	return true, nil
}

// runtimeServerWindowsGoplsReclaimReceiptIdentityValid 核对 receipt 与当前代和 canonical cohort 一致。
func runtimeServerWindowsGoplsReclaimReceiptIdentityValid(receipt runtimeServerWindowsGoplsRootResourceReceipt, state *runtimeServerDurableGoplsRootCohortState, config multilsp.GoplsRootCohortConfig) bool {
	return receipt.SchemaVersion == runtimeServerWindowsGoplsRootResourceSchema &&
		receipt.ConfigDigest == state.ConfigDigest && receipt.CohortID == config.CohortID &&
		receipt.Generation == state.Epoch && receipt.Source == runtimeServerWindowsGoplsObservationSource
}

// runtimeServerWindowsGoplsReclaimReceiptDecisionValid 核对零租约、超限和 daemon 身份证据。
func runtimeServerWindowsGoplsReclaimReceiptDecisionValid(receipt runtimeServerWindowsGoplsRootResourceReceipt) bool {
	return receipt.ActiveLeases == 0 && receipt.Decision == runtimeServerWindowsGoplsRootResourceReclaimed &&
		receipt.RSSBytes > receipt.RSSLimitBytes && receipt.RSSLimitBytes > 0 &&
		receipt.DaemonPID > 1 && receipt.DaemonStartIdentity != ""
}

// runtimeServerWindowsGoplsRootResourceDecision 只记录限内或活跃租约延迟决策，不执行回收。
func runtimeServerWindowsGoplsRootResourceDecision(rssBytes, limit uint64, active int) (string, error) {
	if rssBytes == 0 || limit == 0 || active <= 0 {
		return "", errors.New("Windows gopls root resource sample is incomplete")
	}
	if rssBytes > limit {
		return runtimeServerWindowsGoplsRootResourceDeferredLeases, nil
	}
	return runtimeServerWindowsGoplsRootResourceWithinLimit, nil
}

// runtimeServerValidateWindowsGoplsRootResourceMembers 拒绝 broker、重复或缺少 daemon 的 Job 成员。
func runtimeServerValidateWindowsGoplsRootResourceMembers(members []int, ownerPID, daemonPID int) error {
	seen := make(map[int]struct{}, len(members))
	daemonFound := false
	for _, pid := range members {
		if pid <= 1 || pid == ownerPID {
			return errors.New("Windows gopls root resource contains a non-Job member")
		}
		if _, exists := seen[pid]; exists {
			return errors.New("Windows gopls root resource contains a duplicate member")
		}
		seen[pid] = struct{}{}
		daemonFound = daemonFound || pid == daemonPID
	}
	if !daemonFound {
		return errors.New("Windows gopls root resource omits the daemon")
	}
	return nil
}
