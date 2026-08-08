package remoteci

import (
	"context"
	"errors"
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
	"golang.org/x/sync/errgroup"
)

// cleanup 在受限超时内并行回收本次 job 创建的 ECI 分组和同前缀 OSS 临时对象。
// 对象键先受 job 前缀约束；所有回收失败会汇总返回，不能因并发清理而丢失。
func (coordinator *Coordinator) cleanup(jobID string, groupIDs []string, objectKeys []string) error {
	ctx, cancel := gateprivate.WithTimeout(context.Background(), coordinator.config.CleanupTimeout)
	defer cancel()
	objectCleanup := 0
	if len(objectKeys) != 0 {
		objectCleanup = 1
	}
	coordinator.progress.beginCleanup(len(groupIDs) + objectCleanup)
	failures := make([]error, len(groupIDs)+objectCleanup)
	var workers errgroup.Group
	for index, groupID := range groupIDs {
		workers.Go(func() error {
			if err := coordinator.runtime.DeleteContainerGroup(ctx, groupID); err != nil {
				failures[index] = fmt.Errorf("delete ECI container group %s: %w", groupID, err)
			}
			return nil
		})
	}
	if len(objectKeys) != 0 {
		workers.Go(func() error {
			prefix := coordinator.config.SourcePrefix + jobID + "/"
			if err := validateRemoteTemporaryObjectKeys(prefix, objectKeys); err != nil {
				failures[len(groupIDs)] = err
				coordinator.progress.markCleanup(false)
				return nil
			}
			if err := coordinator.store.DeletePrefix(ctx, prefix); err != nil {
				failures[len(groupIDs)] = fmt.Errorf("delete OSS job prefix %s: %w", prefix, err)
				coordinator.progress.markCleanup(false)
				return nil
			}
			coordinator.progress.markCleanup(true)
			return nil
		})
	}
	_ = workers.Wait()
	cleanupErr := errors.Join(failures...)
	coordinator.progress.cleanupFinished(cleanupErr)
	return cleanupErr
}
