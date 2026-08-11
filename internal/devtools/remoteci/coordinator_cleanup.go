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
			deleteErr := coordinator.runtime.DeleteContainerGroup(ctx, groupID)
			confirmErr := waitForCleanupAbsence(ctx, coordinator.config.PollInterval,
				func(confirmCtx context.Context) (bool, error) {
					return coordinator.runtime.ConfirmContainerGroupAbsent(confirmCtx, groupID)
				}, fmt.Sprintf("ECI container group %s", groupID),
			)
			var failure error
			if deleteErr != nil {
				failure = fmt.Errorf("delete ECI container group %s: %w", groupID, deleteErr)
			}
			if confirmErr != nil {
				failure = errors.Join(failure, fmt.Errorf("confirm ECI container group %s absence: %w", groupID, confirmErr))
			}
			if failure != nil {
				failures[index] = failure
				coordinator.progress.markCleanup(false)
				return nil
			}
			coordinator.progress.markCleanup(true)
			return nil
		})
	}
	prefix := coordinator.config.SourcePrefix + jobID + "/"
	workers.Go(func() error {
		if err := validateRemoteTemporaryObjectKeys(prefix, objectKeys); err != nil {
			failures[len(groupIDs)] = err
			coordinator.progress.markCleanup(false)
			return nil
		}
		deleteErr := coordinator.store.DeletePrefix(ctx, prefix)
		confirmErr := waitForCleanupAbsence(ctx, coordinator.config.PollInterval,
			func(confirmCtx context.Context) (bool, error) {
				return coordinator.store.ConfirmPrefixEmpty(confirmCtx, prefix)
			}, fmt.Sprintf("OSS job prefix %s", prefix),
		)
		var failure error
		if deleteErr != nil {
			failure = fmt.Errorf("delete OSS job prefix %s: %w", prefix, deleteErr)
		}
		if confirmErr != nil {
			failure = errors.Join(failure, fmt.Errorf("confirm OSS job prefix %s empty: %w", prefix, confirmErr))
		}
		if failure != nil {
			failures[len(groupIDs)] = failure
			coordinator.progress.markCleanup(false)
			return nil
		}
		coordinator.progress.markCleanup(true)
		return nil
	})
	_ = workers.Wait()
	cleanupErr := errors.Join(failures...)
	coordinator.progress.cleanupFinished(cleanupErr)
	return cleanupErr
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
