package remoteci

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
	"golang.org/x/sync/errgroup"
)

// cleanup 在受限超时内并行回收本次 job 创建的 ECI 分组和同前缀 OSS 临时对象。
// 删除 ACK 不是权威完成证据；每个目标都必须额外通过 provider absence proof。
func (coordinator *Coordinator) cleanup(jobID string, groupIDs []string, objectKeys []string) error {
	ctx, cancel := gateprivate.WithTimeout(context.Background(), coordinator.config.CleanupTimeout)
	defer cancel()
	// 即使本次没有登记对象，也必须清理并证明整个 job 前缀为空，避免遗漏未知残留。
	objectCleanup := 1
	coordinator.progress.beginCleanup(len(groupIDs) + objectCleanup)
	failures := make([]error, len(groupIDs)+objectCleanup)
	var workers errgroup.Group
	for index, groupID := range groupIDs {
		workers.Go(func() error {
			failures[index] = coordinator.cleanupContainerGroup(ctx, groupID)
			coordinator.progress.markCleanup(failures[index] == nil)
			return nil
		})
	}
	prefix := coordinator.config.SourcePrefix + jobID + "/"
	workers.Go(func() error {
		failureIndex := len(groupIDs)
		failures[failureIndex] = coordinator.cleanupObjectPrefix(ctx, prefix, objectKeys)
		coordinator.progress.markCleanup(failures[failureIndex] == nil)
		return nil
	})
	_ = workers.Wait()
	cleanupErr := errors.Join(failures...)
	coordinator.progress.cleanupFinished(cleanupErr)
	return cleanupErr
}

// cleanupContainerGroup 删除单个 ECI 分组并取得 provider absence proof。
func (coordinator *Coordinator) cleanupContainerGroup(ctx context.Context, groupID string) error {
	deleteErr := coordinator.runtime.DeleteContainerGroup(ctx, groupID)
	confirmErr := waitForCleanupAbsence(ctx, coordinator.config.PollInterval,
		func(confirmCtx context.Context) (bool, error) {
			absent, err := coordinator.runtime.ConfirmContainerGroupAbsent(confirmCtx, groupID)
			if err == nil && !absent {
				_ = coordinator.runtime.DeleteContainerGroup(confirmCtx, groupID)
			}
			return absent, err
		}, fmt.Sprintf("ECI container group %s", groupID),
	)
	return errors.Join(
		wrapCleanupError(fmt.Sprintf("delete ECI container group %s", groupID), deleteErr),
		wrapCleanupError(fmt.Sprintf("confirm ECI container group %s absence", groupID), confirmErr),
	)
}

// cleanupObjectPrefix 校验临时对象归属后删除整个 OSS job 前缀并取得空前缀证明。
func (coordinator *Coordinator) cleanupObjectPrefix(ctx context.Context, prefix string, objectKeys []string) error {
	if err := validateRemoteTemporaryObjectKeys(prefix, objectKeys); err != nil {
		return err
	}
	deleteErr := coordinator.store.DeletePrefix(ctx, prefix)
	confirmErr := waitForCleanupAbsence(ctx, coordinator.config.PollInterval,
		func(confirmCtx context.Context) (bool, error) {
			return coordinator.store.ConfirmPrefixEmpty(confirmCtx, prefix)
		}, fmt.Sprintf("OSS job prefix %s", prefix),
	)
	return errors.Join(
		wrapCleanupError(fmt.Sprintf("delete OSS job prefix %s", prefix), deleteErr),
		wrapCleanupError(fmt.Sprintf("confirm OSS job prefix %s empty", prefix), confirmErr),
	)
}

// wrapCleanupError 只在 provider 操作失败时补充稳定 cleanup 上下文并保留原始错误。
func wrapCleanupError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}

// waitForCleanupAbsence 在共享清理超时内轮询一个 provider absence proof。
// provider error、未知状态和上下文超时都会 fail closed，并保留原始错误。
func waitForCleanupAbsence(ctx context.Context, pollInterval time.Duration, confirm func(context.Context) (bool, error), target string) error {
	if confirm == nil {
		return errors.New("cleanup absence confirmation is required")
	}
	if pollInterval <= 0 {
		return errors.New("cleanup absence confirmation poll interval must be positive")
	}
	for {
		absent, err := confirm(ctx)
		if err != nil {
			return fmt.Errorf("%s: %w", target, err)
		}
		if absent {
			return nil
		}
		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return fmt.Errorf("%s timed out waiting for absence: %w", target, ctx.Err())
		case <-timer.C:
		}
	}
}
