//go:build !windows

package installer

import "fmt"

// ensureWindowsCatalogProductionBranch prevents the Windows native catalog
// from becoming a non-Windows installer or PATH/emulation fallback.
func ensureWindowsCatalogProductionBranch() error {
	return fmt.Errorf("Windows native catalog is not registered on non-Windows production branch: %w", ErrUnsupportedWindowsHostPlatform)
}
