package remoteci

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"regexp"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sourceexport"
)

var gitObjectPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

const (
	maxReadOnlyGitTreeEntries = 100_000
	maxReadOnlyGitTreeBytes   = 512 << 20
)

func interfaceValueIsNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func parseReadOnlyTreeEntry(record []byte) (sourceexport.TreeEntry, error) {
	metadata, entryPath, found := bytes.Cut(record, []byte{'\t'})
	if !found || len(entryPath) == 0 {
		return sourceexport.TreeEntry{}, fmt.Errorf("read-only Git tree record is missing its path")
	}
	fields := bytes.Fields(metadata)
	if len(fields) != 3 || string(fields[1]) != "blob" {
		return sourceexport.TreeEntry{}, fmt.Errorf("read-only Git tree entry %q is not a blob", entryPath)
	}
	return sourceexport.TreeEntry{Path: string(entryPath), Mode: string(fields[0]), Hash: string(fields[2])}, nil
}

func bytesDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
