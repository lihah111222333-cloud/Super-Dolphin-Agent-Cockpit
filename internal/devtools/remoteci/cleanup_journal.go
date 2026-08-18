package remoteci

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
)

// CleanupJournalStore 只暴露 independent operational journal；cleanup-only 绝不读取或写入 ci_runs。
type CleanupJournalStore interface {
	LoadRemoteCleanupJournal(string) (gate.RemoteCleanupJournal, error)
}

// CleanupJournalObjectStore 提供 cleanup-only 所需的 OSS 删除与空前缀证明。
type CleanupJournalObjectStore interface {
	DeletePrefix(context.Context, string) error
	ConfirmPrefixEmpty(context.Context, string) (bool, error)
}

// CleanupJournalRuntime 提供 cleanup-only 所需的 ECI 查询、删除与缺失证明。
type CleanupJournalRuntime interface {
	DeleteContainerGroup(context.Context, string) error
	ConfirmContainerGroupAbsent(context.Context, string) (bool, error)
	FindContainerGroupsByNameAndJobTag(context.Context, string, string) ([]eci.ContainerGroup, error)
}

// CleanupJournalConfig 只包含 cleanup-only 的受限 provider 操作参数。
type CleanupJournalConfig struct {
	PollInterval time.Duration
	Timeout      time.Duration
}

// CleanupOrphanedRemoteJob 只根据 durable journal 删除并取得 absence proof。
// 它不执行 workload、不创建资源、不写 ci_runs，也不触发 authority finalization。
func CleanupOrphanedRemoteJob(ctx context.Context, jobID string, config CleanupJournalConfig, journalStore CleanupJournalStore, objectStore CleanupJournalObjectStore, runtime CleanupJournalRuntime) error {
	if err := validateCleanupJournalInvocation(ctx, jobID, config, journalStore, objectStore, runtime); err != nil {
		return err
	}
	journal, err := journalStore.LoadRemoteCleanupJournal(jobID)
	if err != nil {
		return fmt.Errorf("load remote cleanup journal: %w", err)
	}
	if journal.JobID != jobID {
		return errors.New("remote cleanup journal drifted")
	}
	if len(journal.Targets) == 0 {
		return errors.New("remote cleanup journal has no targets")
	}
	cleanupCtx, cancel := gateprivate.WithTimeout(ctx, config.Timeout)
	defer cancel()
	for _, target := range journal.Targets {
		if err := cleanupJournalTarget(cleanupCtx, config.PollInterval, jobID, target, objectStore, runtime); err != nil {
			return err
		}
	}
	return nil
}

// validateCleanupJournalInvocation 拒绝缺少上下文、依赖或有界轮询参数的回收请求。
func validateCleanupJournalInvocation(ctx context.Context, jobID string, config CleanupJournalConfig, journalStore CleanupJournalStore, objectStore CleanupJournalObjectStore, runtime CleanupJournalRuntime) error {
	if ctx == nil {
		return errors.New("remote cleanup-only context is required")
	}
	if journalStore == nil || objectStore == nil || runtime == nil {
		return errors.New("remote cleanup-only dependencies are required")
	}
	if strings.TrimSpace(jobID) == "" {
		return errors.New("remote cleanup-only job ID is required")
	}
	if config.PollInterval <= 0 || config.Timeout <= 0 {
		return errors.New("remote cleanup-only poll interval and timeout are required")
	}
	return nil
}

func cleanupJournalTarget(ctx context.Context, pollInterval time.Duration, jobID string, target gate.RemoteCleanupTarget, objectStore CleanupJournalObjectStore, runtime CleanupJournalRuntime) error {
	switch target.Kind {
	case gate.RemoteCleanupTargetOSSPrefix:
		return cleanupJournalOSSPrefix(ctx, pollInterval, target, objectStore)
	case gate.RemoteCleanupTargetECIContainerGroup:
		return cleanupJournalECIContainerGroup(ctx, pollInterval, jobID, target, runtime)
	default:
		return errors.New("remote cleanup journal target kind is unsupported")
	}
}

// cleanupJournalOSSPrefix 删除唯一 journal 前缀并把 absence proof 作为成功条件。
func cleanupJournalOSSPrefix(ctx context.Context, pollInterval time.Duration, target gate.RemoteCleanupTarget, objectStore CleanupJournalObjectStore) error {
	if target.ProviderID != "" || target.DeterministicName != "" || target.JobTag != "" || !strings.HasSuffix(target.Key, "/") {
		return errors.New("remote cleanup OSS journal target drifted")
	}
	deleteErr := objectStore.DeletePrefix(ctx, target.Key)
	confirmErr := waitForCleanupAbsence(ctx, pollInterval, func(confirmCtx context.Context) (bool, error) {
		return objectStore.ConfirmPrefixEmpty(confirmCtx, target.Key)
	}, fmt.Sprintf("OSS job prefix %s", target.Key))
	if confirmErr == nil {
		return nil
	}
	return errors.Join(wrapCleanupError(fmt.Sprintf("delete OSS job prefix %s", target.Key), deleteErr), confirmErr)
}

// cleanupJournalECIContainerGroup 解析精确 provider 实例后删除并等待 absence proof。
func cleanupJournalECIContainerGroup(ctx context.Context, pollInterval time.Duration, jobID string, target gate.RemoteCleanupTarget, runtime CleanupJournalRuntime) error {
	if target.Key != target.DeterministicName || target.JobTag != jobID {
		return errors.New("remote cleanup ECI journal target drifted")
	}
	providerID, err := resolveCleanupContainerGroupID(ctx, target, runtime)
	if err != nil || providerID == "" {
		return err
	}
	deleteErr := runtime.DeleteContainerGroup(ctx, providerID)
	confirmErr := waitForCleanupAbsence(ctx, pollInterval, func(confirmCtx context.Context) (bool, error) {
		return runtime.ConfirmContainerGroupAbsent(confirmCtx, providerID)
	}, fmt.Sprintf("ECI container group %s", providerID))
	if confirmErr == nil {
		return nil
	}
	return errors.Join(wrapCleanupError(fmt.Sprintf("delete ECI container group %s", providerID), deleteErr), confirmErr)
}

func resolveCleanupContainerGroupID(ctx context.Context, target gate.RemoteCleanupTarget, runtime CleanupJournalRuntime) (string, error) {
	if target.ProviderID != "" {
		return target.ProviderID, nil
	}
	groups, err := runtime.FindContainerGroupsByNameAndJobTag(ctx, target.DeterministicName, target.JobTag)
	if err != nil {
		return "", fmt.Errorf("find ECI container group by deterministic name and job tag: %w", err)
	}
	if len(groups) == 0 {
		return "", nil
	}
	if err := validateCleanupDiscoveredGroup(groups, target); err != nil {
		return "", err
	}
	return groups[0].ID, nil
}

func validateCleanupDiscoveredGroup(groups []eci.ContainerGroup, target gate.RemoteCleanupTarget) error {
	if len(groups) != 1 {
		return errors.New("ECI cleanup discovery is ambiguous or drifted")
	}
	group := groups[0]
	if group.ID == "" || group.Name != target.DeterministicName || !hasExactJobTag(group.Tags, target.JobTag) {
		return errors.New("ECI cleanup discovery is ambiguous or drifted")
	}
	return nil
}

func hasExactJobTag(tags []eci.ContainerGroupTag, jobID string) bool {
	for _, tag := range tags {
		if tag.Key == "super-dolphin-job" && tag.Value == jobID {
			return true
		}
	}
	return false
}
