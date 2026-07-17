package pidregistry

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
)

// FindStableProcessByArgument 查找唯一携带指定参数且 executable 身份精确匹配的进程。
func FindStableProcessByArgument(argument, expectedExecutable string) (StableProcessIdentity, bool, error) {
	return FindStableProcessByArgumentContext(context.Background(), argument, expectedExecutable)
}

// FindStableProcessByArgumentContext 在可取消的进程枚举中查找唯一 exact match。
func FindStableProcessByArgumentContext(ctx context.Context, argument, expectedExecutable string) (StableProcessIdentity, bool, error) {
	if argument == "" || !filepath.IsAbs(expectedExecutable) || filepath.Clean(expectedExecutable) != expectedExecutable {
		return StableProcessIdentity{}, false, errors.New("pidregistry: exact argument and clean absolute executable are required")
	}
	if err := context.Cause(ctx); err != nil {
		return StableProcessIdentity{}, false, err
	}
	pids, err := processIDs()
	if err != nil {
		return StableProcessIdentity{}, false, err
	}
	return findStableProcessInIDs(ctx, pids, argument, expectedExecutable)
}

// findStableProcessInIDs 在枚举期间持续检查取消并拒绝多个 exact match。
func findStableProcessInIDs(ctx context.Context, pids []int, argument, expectedExecutable string) (StableProcessIdentity, bool, error) {
	var match StableProcessIdentity
	for _, pid := range pids {
		if err := context.Cause(ctx); err != nil {
			return StableProcessIdentity{}, false, err
		}
		identity, found, err := matchProcessByArgument(ctx, pid, argument, expectedExecutable)
		if err != nil {
			return StableProcessIdentity{}, false, err
		}
		if !found {
			continue
		}
		if match.PID != 0 {
			return StableProcessIdentity{}, false, fmt.Errorf("pidregistry: multiple exact argument matches for %q", expectedExecutable)
		}
		match = identity
	}
	return match, match.PID != 0, nil
}

// matchProcessByArgument 将参数匹配结果绑定到同一次稳定进程身份捕获。
func matchProcessByArgument(ctx context.Context, pid int, argument, expectedExecutable string) (StableProcessIdentity, bool, error) {
	if err := context.Cause(ctx); err != nil {
		return StableProcessIdentity{}, false, err
	}
	arguments, gone, err := processArgumentsForMatch(pid)
	if err != nil {
		return StableProcessIdentity{}, false, err
	}
	if gone {
		return StableProcessIdentity{}, false, nil
	}
	if !containsExactArgument(arguments, argument) {
		return StableProcessIdentity{}, false, nil
	}
	if err := context.Cause(ctx); err != nil {
		return StableProcessIdentity{}, false, err
	}
	identity, err := CaptureStableProcessIdentity(pid)
	if errors.Is(err, ErrStableProcessNotFound) {
		return StableProcessIdentity{}, false, nil
	}
	if err != nil {
		return StableProcessIdentity{}, false, err
	}
	if filepath.Clean(identity.ExecutableIdentity) != expectedExecutable {
		return StableProcessIdentity{}, false, nil
	}
	return identity, true, nil
}

func processArgumentsForMatch(pid int) ([]string, bool, error) {
	arguments, err := processArguments(pid)
	if errors.Is(err, ErrStableProcessNotFound) {
		return nil, true, nil
	}
	if err == nil {
		return arguments, false, nil
	}
	gone, classifyErr := processGoneAfterArgumentRead(pid, err)
	if classifyErr != nil {
		return nil, false, classifyErr
	}
	if gone {
		return nil, true, nil
	}
	return nil, false, err
}

// processGoneAfterArgumentRead 只在内核确认 PID 已消失时收敛 argv 读取竞态。
func processGoneAfterArgumentRead(pid int, readErr error) (bool, error) {
	exists, err := exactProcessExists(pid)
	if err != nil {
		return false, errors.Join(readErr, err)
	}
	return !exists, nil
}

func containsExactArgument(arguments []string, wanted string) bool {
	return slices.Contains(arguments, wanted)
}
