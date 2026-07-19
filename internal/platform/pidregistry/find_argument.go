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
	return matchProcessByArgumentWithOps(
		ctx, pid, argument, expectedExecutable,
		CaptureStableProcessIdentity,
		processArgumentsForMatch,
	)
}

// matchProcessByArgumentWithOps 在 argv 读取前后捕获并比较完整 generation。
func matchProcessByArgumentWithOps(
	ctx context.Context,
	pid int,
	argument, expectedExecutable string,
	capture func(int) (StableProcessIdentity, error),
	readArguments func(int) ([]string, bool, error),
) (StableProcessIdentity, bool, error) {
	if capture == nil || readArguments == nil {
		return StableProcessIdentity{}, false, errors.New("pidregistry: process match operations are required")
	}
	before, found, err := captureArgumentMatchBoundary(ctx, pid, expectedExecutable, capture)
	if err != nil || !found {
		return StableProcessIdentity{}, false, err
	}
	matched, err := readExactProcessArgument(pid, argument, readArguments)
	if err != nil || !matched {
		return StableProcessIdentity{}, false, err
	}
	after, found, err := captureArgumentMatchBoundary(ctx, pid, expectedExecutable, capture)
	if err != nil || !found {
		return StableProcessIdentity{}, false, err
	}
	if !sameStableProcessGeneration(before, after) {
		return StableProcessIdentity{}, false, nil
	}
	return after, true, nil
}

func captureArgumentMatchBoundary(
	ctx context.Context,
	pid int,
	expectedExecutable string,
	capture func(int) (StableProcessIdentity, error),
) (StableProcessIdentity, bool, error) {
	if err := context.Cause(ctx); err != nil {
		return StableProcessIdentity{}, false, err
	}
	return captureExpectedArgumentIdentity(pid, expectedExecutable, capture)
}

func captureExpectedArgumentIdentity(
	pid int,
	expectedExecutable string,
	capture func(int) (StableProcessIdentity, error),
) (StableProcessIdentity, bool, error) {
	identity, found, err := captureIdentityForArgumentMatch(pid, capture)
	if err != nil || !found {
		return StableProcessIdentity{}, false, err
	}
	if identity.PID != pid || filepath.Clean(identity.ExecutableIdentity) != expectedExecutable {
		return StableProcessIdentity{}, false, nil
	}
	return identity, true, nil
}

func readExactProcessArgument(
	pid int,
	argument string,
	readArguments func(int) ([]string, bool, error),
) (bool, error) {
	arguments, gone, err := readArguments(pid)
	if err != nil {
		return false, err
	}
	if gone {
		return false, nil
	}
	return containsExactArgument(arguments, argument), nil
}

func captureIdentityForArgumentMatch(
	pid int,
	capture func(int) (StableProcessIdentity, error),
) (StableProcessIdentity, bool, error) {
	identity, err := capture(pid)
	if errors.Is(err, ErrStableProcessNotFound) || errors.Is(err, ErrStableProcessIdentityMismatch) {
		return StableProcessIdentity{}, false, nil
	}
	return identity, err == nil, err
}

func sameStableProcessGeneration(left, right StableProcessIdentity) bool {
	return left.PID == right.PID &&
		left.ProcessStartToken != "" && left.ProcessStartToken == right.ProcessStartToken &&
		left.ExecutableIdentity != "" && left.ExecutableIdentity == right.ExecutableIdentity
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
