//go:build darwin || linux || freebsd

package appupdaterecovery

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

const rollbackRestartShortTempRoot = "/tmp"

// rollbackRestartEndpointRoot 选择能容纳 Unix sockaddr 路径且具备安全权限的临时根。
func rollbackRestartEndpointRoot(basename string) (string, error) {
	return rollbackRestartEndpointRootFromCandidates(basename, []string{os.TempDir(), rollbackRestartShortTempRoot})
}

// rollbackRestartEndpointRootFromCandidates 在给定候选中选择唯一安全且满足 sockaddr 限制的根目录。
func rollbackRestartEndpointRootFromCandidates(basename string, candidates []string) (string, error) {
	pathLimit := len(syscall.RawSockaddrUnix{}.Path)
	if pathLimit <= 1 {
		return "", errors.New("rollback restart Unix socket path limit is invalid")
	}
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		if candidate == "." || !filepath.IsAbs(candidate) || !rollbackRestartEndpointRootIsSafe(candidate) {
			continue
		}
		endpoint := filepath.Join(candidate, basename)
		if len(endpoint) >= pathLimit {
			continue
		}
		return candidate, nil
	}
	return "", fmt.Errorf("rollback restart Unix socket path exceeds %d-byte sockaddr limit", pathLimit)
}

func rollbackRestartEndpointRootIsSafe(root string) bool {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return false
	}
	mode := info.Mode()
	if mode.Perm()&0o200 == 0 {
		return false
	}
	if mode.Perm()&0o022 != 0 && mode&os.ModeSticky == 0 {
		return false
	}
	probe, err := os.CreateTemp(root, ".sd-rr-root-probe-*")
	if err != nil {
		return false
	}
	probeName := probe.Name()
	if err := probe.Close(); err != nil {
		if removeErr := os.Remove(probeName); removeErr != nil {
			return false
		}
		return false
	}
	if err := os.Remove(probeName); err != nil {
		return false
	}
	return true
}
