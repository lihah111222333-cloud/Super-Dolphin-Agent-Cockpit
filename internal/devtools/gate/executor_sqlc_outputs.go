package gate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sqlcconfig"
)

// prepareTrustedSQLCOutputs removes every non-manual output entry so the fixed generator owns the actual file set.
func prepareTrustedSQLCOutputs(ctx context.Context, sourceCopy string, units []sqlcconfig.Unit) error {
	manual := make(map[string]bool)
	for _, relative := range sqlcconfig.ManualExtensionPaths(units) {
		manual[path.Clean(relative)] = true
	}
	for _, unit := range units {
		if err := pruneTrustedSQLCOutput(ctx, filepath.Join(sourceCopy, filepath.FromSlash(unit.Output)), unit.Output, manual); err != nil {
			return fmt.Errorf("prepare trusted SQLC output %s: %w", unit.Output, err)
		}
	}
	return nil
}

func pruneTrustedSQLCOutput(ctx context.Context, absolute, relative string, manual map[string]bool) error {
	if err := trustedOutputContextError(ctx); err != nil {
		return err
	}
	entries, err := os.ReadDir(absolute)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := trustedOutputContextError(ctx); err != nil {
			return err
		}
		if err := handleTrustedSQLCOutputEntry(ctx, absolute, relative, entry, manual); err != nil {
			return err
		}
	}
	return nil
}

func handleTrustedSQLCOutputEntry(ctx context.Context, absolute, relative string, entry os.DirEntry, manual map[string]bool) error {
	childRelative := path.Join(relative, entry.Name())
	childAbsolute := filepath.Join(absolute, entry.Name())
	if manual[childRelative] {
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return fmt.Errorf("manual extension is not a regular file: %s", childRelative)
		}
		return nil
	}
	if !manualPathBelow(childRelative, manual) {
		return os.RemoveAll(childAbsolute)
	}
	if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
		return fmt.Errorf("manual extension parent is not a regular directory: %s", childRelative)
	}
	return pruneTrustedSQLCOutput(ctx, childAbsolute, childRelative, manual)
}

func manualPathBelow(relative string, manual map[string]bool) bool {
	prefix := relative + "/"
	for candidate := range manual {
		if strings.HasPrefix(candidate, prefix) {
			return true
		}
	}
	return false
}

func trustedOutputContextError(ctx context.Context) error {
	if ctx == nil {
		return errors.New("trusted SQLC output context is required")
	}
	if ctx.Err() == nil {
		return nil
	}
	return errors.Join(ctx.Err(), context.Cause(ctx))
}
