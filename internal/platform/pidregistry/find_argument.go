package pidregistry

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
)

// FindStableProcessByArgument 查找唯一携带指定参数且 executable 身份精确匹配的进程。
func FindStableProcessByArgument(argument, expectedExecutable string) (StableProcessIdentity, bool, error) {
	if argument == "" || !filepath.IsAbs(expectedExecutable) || filepath.Clean(expectedExecutable) != expectedExecutable {
		return StableProcessIdentity{}, false, errors.New("pidregistry: exact argument and clean absolute executable are required")
	}
	pids, err := processIDs()
	if err != nil {
		return StableProcessIdentity{}, false, err
	}
	var match StableProcessIdentity
	for _, pid := range pids {
		identity, found, err := matchProcessByArgument(pid, argument, expectedExecutable)
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
func matchProcessByArgument(pid int, argument, expectedExecutable string) (StableProcessIdentity, bool, error) {
	arguments, err := processArguments(pid)
	if errors.Is(err, ErrStableProcessNotFound) {
		return StableProcessIdentity{}, false, nil
	}
	if err != nil {
		return StableProcessIdentity{}, false, err
	}
	if !containsExactArgument(arguments, argument) {
		return StableProcessIdentity{}, false, nil
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

func containsExactArgument(arguments []string, wanted string) bool {
	return slices.Contains(arguments, wanted)
}
