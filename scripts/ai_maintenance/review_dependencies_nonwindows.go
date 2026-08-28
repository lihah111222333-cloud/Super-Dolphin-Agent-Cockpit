//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os"
)

// normalizeReviewFrontendDependencySeed 建立符号链接视图，避免复制或遍历完整依赖树。
func normalizeReviewFrontendDependencySeed(sourceRoot, worktreeRoot string) (string, func() error, error) {
	seedRoot, err := os.MkdirTemp(worktreeRoot, ".ai-maintenance-frontend-seed-*")
	if err != nil {
		return "", nil, fmt.Errorf("reserve normalized frontend dependency seed: %w", err)
	}
	cleanup := func() error { return removeReviewDependencySeed(worktreeRoot, seedRoot) }
	if err := populateNormalizedReviewDependencySeed(sourceRoot, seedRoot); err != nil {
		return "", nil, errors.Join(err, cleanup())
	}
	return seedRoot, cleanup, nil
}
