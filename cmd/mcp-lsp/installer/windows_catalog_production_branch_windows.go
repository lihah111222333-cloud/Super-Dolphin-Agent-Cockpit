//go:build windows

package installer

// ensureWindowsCatalogProductionBranch keeps the native catalog entry point
// registered only by the Windows production build.
func ensureWindowsCatalogProductionBranch() error {
	return nil
}
