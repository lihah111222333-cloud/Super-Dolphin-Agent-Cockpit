package remoteci

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ApplySourceSnapshotDelta copies the exact accepted snapshot into outputRoot,
// applies the delta, and verifies the complete target manifest and closure.
// Both roots must be canonical directories; outputRoot must exist and be empty.
func ApplySourceSnapshotDelta(
	acceptedRoot string,
	outputRoot string,
	accepted AcceptedSourceSnapshotManifest,
	archive []byte,
) (SourceSnapshotDeltaManifest, error) {
	if err := validateAcceptedSourceSnapshotManifest(accepted); err != nil {
		return SourceSnapshotDeltaManifest{}, fmt.Errorf("validate accepted source snapshot manifest: %w", err)
	}
	if err := validateEmptySourceSnapshotDirectory(acceptedRoot, false); err != nil {
		return SourceSnapshotDeltaManifest{}, fmt.Errorf("validate accepted source snapshot root: %w", err)
	}
	if err := validateEmptySourceSnapshotDirectory(outputRoot, true); err != nil {
		return SourceSnapshotDeltaManifest{}, fmt.Errorf("validate output source snapshot root: %w", err)
	}
	manifest, blobs, err := readSourceSnapshotDeltaTar(archive)
	if err != nil {
		return SourceSnapshotDeltaManifest{}, err
	}
	if !sourceSnapshotManifestsEqual(accepted, manifest.Accepted) {
		return SourceSnapshotDeltaManifest{}, errors.New("source snapshot delta is not bound to the accepted manifest")
	}
	if err := validateSourceSnapshotDeltaChanges(manifest); err != nil {
		return SourceSnapshotDeltaManifest{}, err
	}
	if err := copyAcceptedSourceSnapshot(acceptedRoot, outputRoot, accepted.Content.Files); err != nil {
		return SourceSnapshotDeltaManifest{}, err
	}
	if err := applySourceSnapshotChanges(outputRoot, manifest, blobs); err != nil {
		return SourceSnapshotDeltaManifest{}, err
	}
	if err := verifyAppliedSourceSnapshot(outputRoot, manifest.Target); err != nil {
		return SourceSnapshotDeltaManifest{}, err
	}
	return manifest, nil
}

func validateEmptySourceSnapshotDirectory(root string, requireEmpty bool) error {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return errors.New("source snapshot root must be an absolute canonical directory")
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("source snapshot root must be an existing directory")
	}
	if !requireEmpty {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("read output source snapshot root: %w", err)
	}
	if len(entries) != 0 {
		return errors.New("output source snapshot root must be empty")
	}
	return nil
}

func sourceSnapshotManifestsEqual(left, right AcceptedSourceSnapshotManifest) bool {
	leftBytes, leftErr := jsonMarshalSourceSnapshotManifest(left)
	rightBytes, rightErr := jsonMarshalSourceSnapshotManifest(right)
	return leftErr == nil && rightErr == nil && string(leftBytes) == string(rightBytes)
}

func jsonMarshalSourceSnapshotManifest(manifest AcceptedSourceSnapshotManifest) ([]byte, error) {
	canonical, err := canonicalSourceSnapshotFiles(manifest.Content.Files)
	if err != nil {
		return nil, err
	}
	manifest.Content.Files = canonical
	return json.Marshal(manifest)
}

func validateSourceSnapshotDeltaChanges(manifest SourceSnapshotDeltaManifest) error {
	expected := make(map[string]sourceSnapshotDeltaChange)
	accepted := make(map[string]SourceSnapshotFile, len(manifest.Accepted.Content.Files))
	for _, entry := range manifest.Accepted.Content.Files {
		accepted[entry.Path] = entry
	}
	target := make(map[string]SourceSnapshotFile, len(manifest.Target.Entries))
	for _, entry := range manifest.Target.Entries {
		target[entry.Path] = entry
		if previous, exists := accepted[entry.Path]; !exists || previous != entry {
			expected[entry.Path] = sourceSnapshotDeltaChange{Operation: "upsert", File: entry}
		}
	}
	for _, entry := range manifest.Accepted.Content.Files {
		if _, exists := target[entry.Path]; !exists {
			expected[entry.Path] = sourceSnapshotDeltaChange{Operation: "delete", File: entry}
		}
	}
	if len(expected) != len(manifest.Changes) {
		return errors.New("source snapshot delta changes do not equal the accepted-to-target diff")
	}
	for _, change := range manifest.Changes {
		if expectedChange, exists := expected[change.File.Path]; !exists || expectedChange != change {
			return errors.New("source snapshot delta contains an unexpected change")
		}
	}
	return nil
}

func copyAcceptedSourceSnapshot(acceptedRoot, outputRoot string, entries []SourceSnapshotFile) error {
	for _, entry := range entries {
		from, err := sourceSnapshotJoin(acceptedRoot, entry.Path)
		if err != nil {
			return err
		}
		to, err := sourceSnapshotJoin(outputRoot, entry.Path)
		if err != nil {
			return err
		}
		data, err := readVerifiedSourceSnapshotFile(acceptedRoot, from, entry)
		if err != nil {
			return fmt.Errorf("verify accepted source snapshot %q: %w", entry.Path, err)
		}
		if err := writeSourceSnapshotFile(to, entry.Mode, data); err != nil {
			return err
		}
	}
	return nil
}

func applySourceSnapshotChanges(outputRoot string, manifest SourceSnapshotDeltaManifest, blobs map[string][]byte) error {
	required := make(map[string]SourceSnapshotFile)
	for _, change := range manifest.Changes {
		if change.Operation == "upsert" {
			required[change.File.BlobOID] = change.File
		}
	}
	if len(required) != len(blobs) {
		return errors.New("source snapshot delta payload does not contain exactly the changed blobs")
	}
	for oid, entry := range required {
		data, exists := blobs[oid]
		if !exists || int64(len(data)) != entry.Size || sourceSnapshotSHA256(data) != entry.BlobDigest {
			return fmt.Errorf("source snapshot delta blob %s does not match manifest", oid)
		}
		if err := verifySourceSnapshotGitBlob(SourceSnapshotBlob{SourceSnapshotFile: entry, Data: data}); err != nil {
			return err
		}
	}
	for _, change := range manifest.Changes {
		filePath, err := sourceSnapshotJoin(outputRoot, change.File.Path)
		if err != nil {
			return err
		}
		switch change.Operation {
		case "upsert":
			if err := writeSourceSnapshotFile(filePath, change.File.Mode, blobs[change.File.BlobOID]); err != nil {
				return err
			}
		case "delete":
			if err := os.Remove(filePath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("delete source snapshot file %q: %w", change.File.Path, err)
			}
		}
	}
	return nil
}

func verifyAppliedSourceSnapshot(root string, target sourceSnapshotDeltaTarget) error {
	entries, err := canonicalSourceSnapshotFiles(target.Entries)
	if err != nil {
		return err
	}
	actualEntries := make([]SourceSnapshotFile, len(entries))
	for index, entry := range entries {
		filePath, err := sourceSnapshotJoin(root, entry.Path)
		if err != nil {
			return err
		}
		if _, err := readVerifiedSourceSnapshotFile(root, filePath, entry); err != nil {
			return fmt.Errorf("verify applied target source snapshot %q: %w", entry.Path, err)
		}
		actualEntries[index] = entry
	}
	actual, err := listAppliedSourceSnapshotPaths(root)
	if err != nil {
		return err
	}
	expected := make([]string, len(entries))
	for index, entry := range entries {
		expected[index] = entry.Path
	}
	if len(actual) != len(expected) {
		return errors.New("applied target source snapshot contains unexpected files")
	}
	for index := range actual {
		if actual[index] != expected[index] {
			return errors.New("applied target source snapshot path set does not match manifest")
		}
	}
	digest, err := SourceSnapshotClosureDigest(actualEntries)
	if err != nil || digest != target.ClosureDigest {
		return errors.New("applied target source snapshot closure digest does not match")
	}
	return nil
}

func sourceSnapshotJoin(root, relative string) (string, error) {
	if err := validateSourceSnapshotPath(relative); err != nil {
		return "", err
	}
	result := filepath.Join(root, filepath.FromSlash(relative))
	if filepath.Dir(result) == "" || !strings.HasPrefix(result, root+string(filepath.Separator)) {
		return "", errors.New("source snapshot path escapes root")
	}
	return result, nil
}

func readVerifiedSourceSnapshotFile(root, filePath string, entry SourceSnapshotFile) ([]byte, error) {
	if err := ensureSourceSnapshotNoLinkParents(root, filePath); err != nil {
		return nil, err
	}
	info, err := os.Lstat(filePath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeType != 0 || info.Size() != entry.Size {
		return nil, errors.New("source snapshot file is missing or not a regular file")
	}
	singleLink, err := sourceSnapshotFileHasSingleLink(filePath, info)
	if err != nil {
		return nil, fmt.Errorf("verify source snapshot file link count: %w", err)
	}
	if !singleLink {
		return nil, errors.New("source snapshot file must not be a hard link")
	}
	wantMode := os.FileMode(0o644)
	if entry.Mode == "100755" {
		wantMode = 0o755
	}
	if info.Mode().Perm() != wantMode {
		return nil, errors.New("source snapshot file mode does not match manifest")
	}
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxSourceSnapshotDeltaFileBytes+1))
	if err != nil || int64(len(data)) != entry.Size || sourceSnapshotSHA256(data) != entry.BlobDigest {
		return nil, errors.New("source snapshot file digest does not match manifest")
	}
	if err := verifySourceSnapshotGitBlob(SourceSnapshotBlob{SourceSnapshotFile: entry, Data: data}); err != nil {
		return nil, fmt.Errorf("source snapshot file Git blob does not match manifest: %w", err)
	}
	return data, nil
}

func ensureSourceSnapshotNoLinkParents(root, filePath string) error {
	for directory := filepath.Dir(filePath); directory != root; directory = filepath.Dir(directory) {
		if directory == filepath.Dir(directory) || !strings.HasPrefix(directory, root+string(filepath.Separator)) {
			return errors.New("source snapshot file escapes root")
		}
		info, err := os.Lstat(directory)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("source snapshot file has a symlink parent")
		}
	}
	return nil
}

func writeSourceSnapshotFile(filePath, mode string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(filePath), 0o700); err != nil {
		return fmt.Errorf("create source snapshot parent: %w", err)
	}
	permissions := os.FileMode(0o644)
	if mode == "100755" {
		permissions = 0o755
	}
	file, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, permissions)
	if err != nil {
		return fmt.Errorf("open source snapshot file: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("write source snapshot file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close source snapshot file: %w", err)
	}
	return os.Chmod(filePath, permissions)
}

func listAppliedSourceSnapshotPaths(root string) ([]string, error) {
	paths := make([]string, 0)
	err := filepath.WalkDir(root, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if filePath == root {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeType != 0 {
			return errors.New("source snapshot output contains a non-regular entry")
		}
		relative, err := filepath.Rel(root, filePath)
		if err != nil {
			return err
		}
		canonical := filepath.ToSlash(relative)
		if err := validateSourceSnapshotPath(canonical); err != nil {
			return err
		}
		paths = append(paths, canonical)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	return paths, nil
}
