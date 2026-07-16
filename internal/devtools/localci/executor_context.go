package localci

import (
	"archive/tar"
	"bytes"
	"crypto/sha1" // #nosec G505 -- Git SHA-1 object IDs require SHA-1 compatibility verification.
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sourceexport"
)

type canonicalContext struct {
	Tar           []byte
	ContextDigest string
	InputDigest   string
}

// buildCanonicalContext 将已验证 Git blob 规范化为稳定 tar 和输入摘要。
func buildCanonicalContext(sourceEntries []sourceexport.TreeEntry) (canonicalContext, error) {
	if len(sourceEntries) == 0 {
		return canonicalContext{}, errors.New("canonical context requires at least one source entry")
	}
	entries := append([]sourceexport.TreeEntry(nil), sourceEntries...)
	sort.Slice(entries, func(left int, right int) bool { return entries[left].Path < entries[right].Path })

	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	var manifest []byte
	seenPaths := make(map[string]string, len(entries))
	for _, entry := range entries {
		if err := validateContextEntry(entry, seenPaths); err != nil {
			return canonicalContext{}, err
		}
		mode := int64(0o644)
		if entry.Mode == "100755" {
			mode = 0o755
		}
		header := &tar.Header{
			Name: entry.Path, Mode: mode, Size: int64(len(entry.Data)), Typeflag: tar.TypeReg,
			ModTime: time.Unix(0, 0), Uid: 0, Gid: 0, Format: tar.FormatUSTAR,
		}
		if err := writer.WriteHeader(header); err != nil {
			return canonicalContext{}, fmt.Errorf("write canonical header %q: %w", entry.Path, err)
		}
		if _, err := writer.Write(entry.Data); err != nil {
			return canonicalContext{}, fmt.Errorf("write canonical content %q: %w", entry.Path, err)
		}
		contentHash := sha256.Sum256(entry.Data)
		manifest = appendManifestField(manifest, entry.Path)
		manifest = appendManifestField(manifest, entry.Mode)
		manifest = appendManifestField(manifest, entry.Hash)
		manifest = appendManifestField(manifest, hex.EncodeToString(contentHash[:]))
	}
	if err := writer.Close(); err != nil {
		return canonicalContext{}, fmt.Errorf("close canonical context: %w", err)
	}
	contextHash := sha256.Sum256(archive.Bytes())
	inputHash := sha256.Sum256(manifest)
	return canonicalContext{
		Tar:           archive.Bytes(),
		ContextDigest: "sha256:" + hex.EncodeToString(contextHash[:]),
		InputDigest:   "sha256:" + hex.EncodeToString(inputHash[:]),
	}, nil
}

func validateContextEntry(entry sourceexport.TreeEntry, seenPaths map[string]string) error {
	if err := validateContextPath(entry.Path, seenPaths); err != nil {
		return err
	}
	if entry.Mode != "100644" && entry.Mode != "100755" {
		return fmt.Errorf("source entry %q has unsupported mode %q", entry.Path, entry.Mode)
	}
	return validateContextBlob(entry)
}

// validateContextPath 拒绝非规范路径和大小写折叠冲突。
func validateContextPath(entryPath string, seenPaths map[string]string) error {
	if entryPath == "" || entryPath == "." || path.Clean(entryPath) != entryPath || path.IsAbs(entryPath) {
		return fmt.Errorf("source entry path %q is not canonical", entryPath)
	}
	if strings.HasPrefix(entryPath, "../") || strings.ContainsAny(entryPath, "\\\x00") {
		return fmt.Errorf("source entry path %q is not canonical", entryPath)
	}
	foldedPath := strings.ToLower(entryPath)
	if previousPath, exists := seenPaths[foldedPath]; exists {
		return fmt.Errorf("source entry path %q collides with %q", entryPath, previousPath)
	}
	seenPaths[foldedPath] = entryPath
	return nil
}

func validateContextBlob(entry sourceexport.TreeEntry) error {
	if entry.Hash == "" {
		return fmt.Errorf("source entry %q is missing verified Git object hash", entry.Path)
	}
	calculatedHash, err := gitBlobHash(entry.Hash, entry.Data)
	if err != nil {
		return fmt.Errorf("source entry %q: %w", entry.Path, err)
	}
	if calculatedHash != entry.Hash {
		return fmt.Errorf("source entry %q data does not match Git blob %s", entry.Path, entry.Hash)
	}
	return nil
}

func gitBlobHash(objectID string, data []byte) (string, error) {
	payload := fmt.Appendf(nil, "blob %d\x00", len(data))
	payload = append(payload, data...)
	switch len(objectID) {
	case sha1.Size * 2:
		sum := sha1.Sum(payload) // #nosec G401 -- this verifies Git's object format, not a security signature.
		return hex.EncodeToString(sum[:]), nil
	case sha256.Size * 2:
		sum := sha256.Sum256(payload)
		return hex.EncodeToString(sum[:]), nil
	default:
		return "", fmt.Errorf("Git blob object ID length %d is unsupported", len(objectID))
	}
}

func appendManifestField(manifest []byte, value string) []byte {
	manifest = append(manifest, value...)
	return append(manifest, 0)
}
