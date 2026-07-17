package gate

import (
	"bufio"
	"errors"
	"io"
	"slices"
	"strings"
)

// validateMountInfo 要求 source 自身是 mountinfo 中明确标记为只读的挂载点。
func validateMountInfo(reader io.Reader, path string) error {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 6 || decodeMountPath(fields[4]) != path {
			continue
		}
		if slices.Contains(strings.Split(fields[5], ","), "ro") {
			return nil
		}
		return errors.New("source mount is not read-only")
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return errors.New("source path is not an explicit mount point")
}

func decodeMountPath(path string) string {
	replacer := strings.NewReplacer("\\040", " ", "\\011", "\t", "\\012", "\n", "\\134", "\\")
	return replacer.Replace(path)
}
