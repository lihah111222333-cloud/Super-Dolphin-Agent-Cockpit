package securefs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func RedactPath(path string) string {
	base := filepath.Base(filepath.Clean(path))
	if strings.TrimSpace(base) == "" || base == "." || base == string(filepath.Separator) {
		return "<redacted-path>"
	}
	return "<redacted:" + base + ">"
}

func SafeError(err error) string {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		if pathErr.Err != nil {
			return pathErr.Op + ": " + pathErr.Err.Error()
		}
		return pathErr.Op
	}
	return err.Error()
}

func SafeErrorForPath(err error, path string) string {
	text := SafeError(err)
	redacted := RedactPath(path)
	for _, candidate := range []string{
		filepath.Clean(path),
		filepath.ToSlash(filepath.Clean(path)),
		path,
		filepath.ToSlash(path),
	} {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		text = strings.ReplaceAll(text, candidate, redacted)
	}
	return text
}

func ProbeWritableDir(dir string) error {
	file, err := os.CreateTemp(dir, ".super-dolphin-write-test-*")
	if err != nil {
		return fmt.Errorf("probe writable directory %s: %s", RedactPath(dir), SafeError(err))
	}
	name := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("close writable probe in %s: %s", RedactPath(dir), SafeError(err))
	}
	if err := os.Remove(name); err != nil {
		return fmt.Errorf("remove writable probe in %s: %s", RedactPath(dir), SafeError(err))
	}
	return nil
}
