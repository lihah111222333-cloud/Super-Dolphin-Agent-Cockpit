package remoteci

import (
	"archive/tar"
	"bytes"
	"crypto/sha1" // #nosec G505 -- Git SHA-1 object verification is required for SHA-1 repositories.
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

const (
	// SourceSnapshotDeltaSchemaVersion identifies the strict source-delta wire format.
	SourceSnapshotDeltaSchemaVersion uint32 = 1
	// SourceSnapshotContentManifestSchemaVersion identifies the sole on-image
	// source content manifest format.
	SourceSnapshotContentManifestSchemaVersion uint32 = 1

	sourceSnapshotDeltaManifestName = ".source-snapshot-delta.json"
	maxSourceSnapshotDeltaEntries   = 100_000
	maxSourceSnapshotDeltaPathBytes = 4 << 10
	maxSourceSnapshotDeltaFileBytes = 64 << 20
	maxSourceSnapshotDeltaBytes     = 512 << 20
)

// SourceSnapshotFile is one canonical accepted snapshot file. BlobDigest is
// the SHA-256 of its bytes; BlobOID is the exact Git blob object ID.
type SourceSnapshotFile struct {
	Path       string `json:"path"`
	Mode       string `json:"mode"`
	BlobOID    string `json:"blob_oid"`
	Size       int64  `json:"size"`
	BlobDigest string `json:"blob_digest"`
}

// SourceSnapshotContentManifest is the only public on-image source content
// manifest. Its strict JSON form has no compatibility aliases or old keys.
type SourceSnapshotContentManifest struct {
	SchemaVersion    uint32               `json:"schema_version"`
	SourceTree       string               `json:"source_tree"`
	ClosureDigest    string               `json:"closure_digest"`
	ImageInputDigest string               `json:"image_input_digest"`
	PolicyDigest     string               `json:"policy_digest"`
	ToolchainDigest  string               `json:"toolchain_digest"`
	Platform         string               `json:"platform"`
	ObjectFormat     string               `json:"object_format"`
	Files            []SourceSnapshotFile `json:"files"`
}

// SourceSnapshotAuthorityBinding is the SQLite-authoritative identity that
// binds content to one accepted generation and snapshot.
type SourceSnapshotAuthorityBinding struct {
	Generation   uint64 `json:"generation"`
	StateDigest  string `json:"state_digest"`
	SnapshotID   string `json:"snapshot_id"`
	SourceDigest string `json:"source_digest"`
}

// AcceptedSourceSnapshotManifest is the accepted SQLite-derived snapshot
// identity and its complete file manifest. It deliberately has no fallback
// representation: a missing or malformed accepted manifest is fatal.
type AcceptedSourceSnapshotManifest struct {
	SchemaVersion uint32                         `json:"schema_version"`
	Authority     SourceSnapshotAuthorityBinding `json:"authority"`
	Content       SourceSnapshotContentManifest  `json:"content"`
}

// SourceSnapshotBlob is a target build-closure file read from an exact Git
// tree. Data is never serialized in the manifest; it is emitted only for new
// or changed blobs in the tar payload.
type SourceSnapshotBlob struct {
	SourceSnapshotFile
	Data []byte `json:"-"`
}

// TargetSourceBuildClosure is the complete target closure, not a filesystem
// scan. ClosureDigest must equal SourceSnapshotClosureDigest(Entries).
type TargetSourceBuildClosure struct {
	SourceDigest    string               `json:"source_digest"`
	TreeOID         string               `json:"tree_oid"`
	ClosureDigest   string               `json:"closure_digest"`
	InputDigest     string               `json:"input_digest"`
	ToolchainDigest string               `json:"toolchain_digest"`
	PolicyDigest    string               `json:"policy_digest"`
	Platform        string               `json:"platform"`
	ObjectFormat    string               `json:"object_format"`
	Entries         []SourceSnapshotBlob `json:"entries"`
}

// SourceSnapshotDeltaManifest is the strictly decoded first tar entry. It
// binds every delta to both the accepted snapshot and complete target closure.
type SourceSnapshotDeltaManifest struct {
	SchemaVersion uint32                         `json:"schema_version"`
	TransferMode  cicontract.RefreshTransferMode `json:"transfer_mode"`
	Accepted      AcceptedSourceSnapshotManifest `json:"accepted"`
	Target        sourceSnapshotDeltaTarget      `json:"target"`
	Changes       []sourceSnapshotDeltaChange    `json:"changes"`
}

type sourceSnapshotDeltaTarget struct {
	SourceDigest    string               `json:"source_digest"`
	TreeOID         string               `json:"tree_oid"`
	ClosureDigest   string               `json:"closure_digest"`
	InputDigest     string               `json:"input_digest"`
	ToolchainDigest string               `json:"toolchain_digest"`
	PolicyDigest    string               `json:"policy_digest"`
	Platform        string               `json:"platform"`
	ObjectFormat    string               `json:"object_format"`
	Entries         []SourceSnapshotFile `json:"entries"`
}

type sourceSnapshotDeltaChange struct {
	Operation string             `json:"operation"`
	File      SourceSnapshotFile `json:"file"`
}

// SourceSnapshotDelta is a deterministic, uncompressed tar payload and its
// decoded manifest.
type SourceSnapshotDelta struct {
	Manifest SourceSnapshotDeltaManifest
	Archive  []byte
}

// SourceSnapshotClosureDigest returns the canonical digest for a complete
// manifest. Callers use it to bind their target closure before building a delta.
func SourceSnapshotClosureDigest(entries []SourceSnapshotFile) (string, error) {
	canonical, err := canonicalSourceSnapshotFiles(entries)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("encode canonical source snapshot closure: %w", err)
	}
	return sourceSnapshotSHA256(data), nil
}

// SourceSnapshotContentDigest returns the deterministic digest of the exact
// strict on-image content manifest.
func SourceSnapshotContentDigest(content SourceSnapshotContentManifest) (string, error) {
	encoded, err := EncodeSourceSnapshotContentManifest(content)
	if err != nil {
		return "", err
	}
	return sourceSnapshotSHA256(encoded), nil
}

// SourceSnapshotSourceDigest returns the canonical source digest bound by an
// accepted authority record. It is deliberately identical to content digest.
func SourceSnapshotSourceDigest(content SourceSnapshotContentManifest) (string, error) {
	return SourceSnapshotContentDigest(content)
}

// EncodeSourceSnapshotContentManifest validates and encodes the unique
// deterministic content-manifest wire format.
func EncodeSourceSnapshotContentManifest(content SourceSnapshotContentManifest) ([]byte, error) {
	if err := ValidateSourceSnapshotContentManifest(content); err != nil {
		return nil, err
	}
	canonical, _ := canonicalSourceSnapshotFiles(content.Files)
	content.Files = canonical
	data, err := json.Marshal(content)
	if err != nil {
		return nil, fmt.Errorf("encode source snapshot content manifest: %w", err)
	}
	return data, nil
}

// DecodeSourceSnapshotContentManifest strictly rejects unknown, old, and
// multiple JSON values before returning a validated content manifest.
func DecodeSourceSnapshotContentManifest(data []byte) (SourceSnapshotContentManifest, error) {
	var content SourceSnapshotContentManifest
	if err := decodeStrictJSON(data, &content); err != nil {
		return SourceSnapshotContentManifest{}, fmt.Errorf("decode source snapshot content manifest: %w", err)
	}
	if err := ValidateSourceSnapshotContentManifest(content); err != nil {
		return SourceSnapshotContentManifest{}, err
	}
	canonical, _ := canonicalSourceSnapshotFiles(content.Files)
	content.Files = canonical
	return content, nil
}

// ValidateSourceSnapshotContentManifest validates the exact source content
// format shared by image embedding and incremental transfer.
func ValidateSourceSnapshotContentManifest(content SourceSnapshotContentManifest) error {
	if content.SchemaVersion != SourceSnapshotContentManifestSchemaVersion {
		return errors.New("source snapshot content manifest schema is unsupported")
	}
	if err := validateSourceSnapshotContentIdentity(content); err != nil {
		return err
	}
	files, err := canonicalSourceSnapshotFiles(content.Files)
	if err != nil {
		return err
	}
	if err := validateSourceSnapshotObjectFormat(content.ObjectFormat, files); err != nil {
		return err
	}
	digest, err := SourceSnapshotClosureDigest(files)
	if err != nil || digest != content.ClosureDigest {
		return errors.New("source snapshot content closure digest does not match files")
	}
	return nil
}

// NewAcceptedSourceSnapshotManifest constructs the only accepted-manifest
// representation from authority and validated content; it has no legacy read.
func NewAcceptedSourceSnapshotManifest(authority SourceSnapshotAuthorityBinding, content SourceSnapshotContentManifest) (AcceptedSourceSnapshotManifest, error) {
	if authority.Generation == 0 || authority.SnapshotID == "" || !sha256DigestPattern.MatchString(authority.StateDigest) {
		return AcceptedSourceSnapshotManifest{}, errors.New("accepted source snapshot authority is required")
	}
	sourceDigest, err := SourceSnapshotSourceDigest(content)
	if err != nil {
		return AcceptedSourceSnapshotManifest{}, err
	}
	if authority.SourceDigest != sourceDigest {
		return AcceptedSourceSnapshotManifest{}, errors.New("accepted source snapshot authority source digest does not match content")
	}
	return AcceptedSourceSnapshotManifest{SchemaVersion: SourceSnapshotDeltaSchemaVersion, Authority: authority, Content: content}, nil
}

// BuildSourceSnapshotDelta creates a deterministic uncompressed tar delta.
// It rejects absent accepted state and never produces a full-snapshot fallback.
func BuildSourceSnapshotDelta(accepted AcceptedSourceSnapshotManifest, target TargetSourceBuildClosure) (SourceSnapshotDelta, error) {
	if err := validateAcceptedSourceSnapshotManifest(accepted); err != nil {
		return SourceSnapshotDelta{}, fmt.Errorf("validate accepted source snapshot manifest: %w", err)
	}
	if err := validateTargetSourceBuildClosure(target); err != nil {
		return SourceSnapshotDelta{}, fmt.Errorf("validate target source build closure: %w", err)
	}
	acceptedFiles, err := canonicalSourceSnapshotFiles(accepted.Content.Files)
	if err != nil {
		return SourceSnapshotDelta{}, err
	}
	targetEntries := append([]SourceSnapshotBlob(nil), target.Entries...)
	sort.Slice(targetEntries, func(left, right int) bool { return targetEntries[left].Path < targetEntries[right].Path })
	targetFiles := make([]SourceSnapshotFile, len(targetEntries))
	for index, entry := range targetEntries {
		targetFiles[index] = entry.SourceSnapshotFile
	}
	if err := validateSourceSnapshotCrossStatePaths(acceptedFiles, targetFiles); err != nil {
		return SourceSnapshotDelta{}, err
	}
	accepted.Content.Files = acceptedFiles
	if err := cicontract.ValidateDeltaRebuild(
		cicontract.RefreshTransferAcceptedSnapshotDelta,
		accepted.Authority.Generation,
		accepted.Authority.SnapshotID,
		target.ClosureDigest,
		target.TreeOID,
		target.ClosureDigest,
	); err != nil {
		return SourceSnapshotDelta{}, fmt.Errorf("validate source snapshot delta contract: %w", err)
	}
	byTargetPath := make(map[string]SourceSnapshotBlob, len(targetEntries))
	for _, entry := range targetEntries {
		byTargetPath[entry.Path] = entry
	}
	acceptedByPath := make(map[string]SourceSnapshotFile, len(acceptedFiles))
	for _, entry := range acceptedFiles {
		acceptedByPath[entry.Path] = entry
	}
	changes := make([]sourceSnapshotDeltaChange, 0)
	for _, entry := range targetFiles {
		previous, exists := acceptedByPath[entry.Path]
		if !exists || previous != entry {
			changes = append(changes, sourceSnapshotDeltaChange{Operation: "upsert", File: entry})
		}
	}
	for _, entry := range acceptedFiles {
		if _, exists := byTargetPath[entry.Path]; !exists {
			changes = append(changes, sourceSnapshotDeltaChange{Operation: "delete", File: entry})
		}
	}
	sort.Slice(changes, func(left, right int) bool { return changes[left].File.Path < changes[right].File.Path })
	manifest := SourceSnapshotDeltaManifest{
		SchemaVersion: SourceSnapshotDeltaSchemaVersion,
		TransferMode:  cicontract.RefreshTransferAcceptedSnapshotDelta,
		Accepted:      accepted,
		Target:        sourceSnapshotDeltaTarget{SourceDigest: target.SourceDigest, TreeOID: target.TreeOID, ClosureDigest: target.ClosureDigest, InputDigest: target.InputDigest, ToolchainDigest: target.ToolchainDigest, PolicyDigest: target.PolicyDigest, Platform: target.Platform, ObjectFormat: target.ObjectFormat, Entries: targetFiles},
		Changes:       changes,
	}
	archive, err := writeSourceSnapshotDeltaTar(manifest, byTargetPath)
	if err != nil {
		return SourceSnapshotDelta{}, err
	}
	return SourceSnapshotDelta{Manifest: manifest, Archive: archive}, nil
}

func validateAcceptedSourceSnapshotManifest(manifest AcceptedSourceSnapshotManifest) error {
	_, err := NewAcceptedSourceSnapshotManifest(manifest.Authority, manifest.Content)
	return err
}

func validateSourceSnapshotContentIdentity(content SourceSnapshotContentManifest) error {
	for name, digest := range map[string]string{"closure": content.ClosureDigest, "image input": content.ImageInputDigest, "toolchain": content.ToolchainDigest, "policy": content.PolicyDigest} {
		if !sha256DigestPattern.MatchString(digest) {
			return fmt.Errorf("%s digest must be an immutable sha256 digest", name)
		}
	}
	if !gitObjectPattern.MatchString(content.SourceTree) || (content.ObjectFormat != "sha1" && content.ObjectFormat != "sha256") || (content.ObjectFormat == "sha1" && len(content.SourceTree) != 40) || (content.ObjectFormat == "sha256" && len(content.SourceTree) != 64) {
		return errors.New("source tree and object format must be canonical Git identities")
	}
	if content.Platform != "linux/amd64" {
		return errors.New("source snapshot platform must be linux/amd64")
	}
	return nil
}

func validateTargetSourceBuildClosure(target TargetSourceBuildClosure) error {
	if err := validateSourceSnapshotIdentity("", target.SourceDigest, target.TreeOID, target.ClosureDigest, target.InputDigest, target.ToolchainDigest, target.PolicyDigest, target.Platform, target.ObjectFormat); err != nil {
		return err
	}
	files := make([]SourceSnapshotFile, len(target.Entries))
	var total int64
	for index, entry := range target.Entries {
		if int64(len(entry.Data)) != entry.Size || sourceSnapshotSHA256(entry.Data) != entry.BlobDigest {
			return fmt.Errorf("target blob %q bytes do not match metadata", entry.Path)
		}
		if err := verifySourceSnapshotGitBlob(entry); err != nil {
			return err
		}
		total += entry.Size
		if total > maxSourceSnapshotDeltaBytes {
			return errors.New("target build closure exceeds total size limit")
		}
		files[index] = entry.SourceSnapshotFile
	}
	if err := validateSourceSnapshotObjectFormat(target.ObjectFormat, files); err != nil {
		return err
	}
	digest, err := SourceSnapshotClosureDigest(files)
	if err != nil {
		return err
	}
	if digest != target.ClosureDigest {
		return errors.New("target build closure digest does not match complete target manifest")
	}
	return nil
}

func validateSourceSnapshotObjectFormat(objectFormat string, entries []SourceSnapshotFile) error {
	wantLength := 40
	if objectFormat == "sha256" {
		wantLength = 64
	}
	for _, entry := range entries {
		if len(entry.BlobOID) != wantLength {
			return fmt.Errorf("source snapshot file %q object ID does not match object format", entry.Path)
		}
	}
	return nil
}

func validateSourceSnapshotIdentity(stateDigest, sourceDigest, treeOID, closureDigest, inputDigest, toolchainDigest, policyDigest, platform, objectFormat string) error {
	for name, digest := range map[string]string{"state": stateDigest, "source": sourceDigest, "closure": closureDigest, "input": inputDigest, "toolchain": toolchainDigest, "policy": policyDigest} {
		// Target-only identities do not have an accepted-state digest yet. When a
		// caller supplies one, it must still be immutable and exact.
		if (name == "state" && digest != "" && !sha256DigestPattern.MatchString(digest)) || (name != "state" && !sha256DigestPattern.MatchString(digest)) {
			return fmt.Errorf("%s digest must be an immutable sha256 digest", name)
		}
	}
	if !gitObjectPattern.MatchString(treeOID) || (objectFormat != "sha1" && objectFormat != "sha256") || (objectFormat == "sha1" && len(treeOID) != 40) || (objectFormat == "sha256" && len(treeOID) != 64) {
		return errors.New("tree object and object format must be canonical Git identities")
	}
	if platform != "linux/amd64" {
		return errors.New("source snapshot platform must be linux/amd64")
	}
	return nil
}

func canonicalSourceSnapshotFiles(entries []SourceSnapshotFile) ([]SourceSnapshotFile, error) {
	if len(entries) == 0 || len(entries) > maxSourceSnapshotDeltaEntries {
		return nil, errors.New("source snapshot entry count is invalid")
	}
	canonical := append([]SourceSnapshotFile(nil), entries...)
	sort.Slice(canonical, func(left, right int) bool { return canonical[left].Path < canonical[right].Path })
	seen := make(map[string]string, len(canonical))
	var total int64
	for _, entry := range canonical {
		if err := validateSourceSnapshotFile(entry); err != nil {
			return nil, err
		}
		folded := strings.ToLower(entry.Path)
		if previous, exists := seen[folded]; exists {
			return nil, fmt.Errorf("source snapshot path %q collides with %q", entry.Path, previous)
		}
		seen[folded] = entry.Path
		total += entry.Size
		if total > maxSourceSnapshotDeltaBytes {
			return nil, errors.New("source snapshot exceeds total size limit")
		}
	}
	return canonical, nil
}

func sourceSnapshotFilesAreCanonical(entries []SourceSnapshotFile) bool {
	canonical, err := canonicalSourceSnapshotFiles(entries)
	if err != nil || len(canonical) != len(entries) {
		return false
	}
	for index := range canonical {
		if canonical[index] != entries[index] {
			return false
		}
	}
	return true
}

func validateSourceSnapshotCrossStatePaths(accepted, target []SourceSnapshotFile) error {
	acceptedByFoldedPath := make(map[string]string, len(accepted))
	for _, entry := range accepted {
		acceptedByFoldedPath[strings.ToLower(entry.Path)] = entry.Path
	}
	for _, entry := range target {
		if previous, exists := acceptedByFoldedPath[strings.ToLower(entry.Path)]; exists && previous != entry.Path {
			return fmt.Errorf("source snapshot path %q case-collides with accepted path %q", entry.Path, previous)
		}
	}
	return nil
}

func validateSourceSnapshotFile(entry SourceSnapshotFile) error {
	if err := validateSourceSnapshotPath(entry.Path); err != nil {
		return err
	}
	if entry.Mode != "100644" && entry.Mode != "100755" {
		return fmt.Errorf("source snapshot file %q has unsupported Git mode %q", entry.Path, entry.Mode)
	}
	if !gitObjectPattern.MatchString(entry.BlobOID) || !sha256DigestPattern.MatchString(entry.BlobDigest) || entry.Size < 0 || entry.Size > maxSourceSnapshotDeltaFileBytes {
		return fmt.Errorf("source snapshot file %q metadata is invalid", entry.Path)
	}
	return nil
}

func validateSourceSnapshotPath(filePath string) error {
	if filePath == "" || len(filePath) > maxSourceSnapshotDeltaPathBytes || !utf8.ValidString(filePath) || strings.ContainsRune(filePath, 0) || strings.Contains(filePath, "\\") || strings.HasPrefix(filePath, "/") || path.Clean(filePath) != filePath || filePath == "." || strings.HasPrefix(filePath, "../") || strings.Contains(filePath, "/../") {
		return fmt.Errorf("source snapshot path %q is unsafe", filePath)
	}
	return nil
}

func verifySourceSnapshotGitBlob(entry SourceSnapshotBlob) error {
	if len(entry.BlobOID) == 40 {
		hash := sha1.Sum(append([]byte(fmt.Sprintf("blob %d\x00", len(entry.Data))), entry.Data...))
		if entry.BlobOID != hex.EncodeToString(hash[:]) {
			return fmt.Errorf("target blob %q is not its declared Git object", entry.Path)
		}
		return nil
	}
	hash := sha256.Sum256(append([]byte(fmt.Sprintf("blob %d\x00", len(entry.Data))), entry.Data...))
	if entry.BlobOID != hex.EncodeToString(hash[:]) {
		return fmt.Errorf("target blob %q is not its declared Git object", entry.Path)
	}
	return nil
}

func writeSourceSnapshotDeltaTar(manifest SourceSnapshotDeltaManifest, target map[string]SourceSnapshotBlob) ([]byte, error) {
	manifestBytes, err := json.Marshal(manifest)
	if err != nil || len(manifestBytes) > maxSourceManifestLength {
		return nil, fmt.Errorf("encode source snapshot delta manifest: %w", err)
	}
	var output bytes.Buffer
	writer := tar.NewWriter(&output)
	if err := writeSourceSnapshotTarFile(writer, sourceSnapshotDeltaManifestName, 0o600, manifestBytes); err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	for _, change := range manifest.Changes {
		if change.Operation != "upsert" {
			continue
		}
		if _, exists := seen[change.File.BlobOID]; exists {
			continue
		}
		seen[change.File.BlobOID] = struct{}{}
		entry := target[change.File.Path]
		if err := writeSourceSnapshotTarFile(writer, "blobs/"+change.File.BlobOID, 0o600, entry.Data); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close source snapshot delta tar: %w", err)
	}
	if output.Len() > maxSourceSnapshotDeltaBytes+maxSourceManifestLength+1<<20 {
		return nil, errors.New("source snapshot delta archive exceeds total size limit")
	}
	return output.Bytes(), nil
}

func writeSourceSnapshotTarFile(writer *tar.Writer, name string, mode int64, data []byte) error {
	header := &tar.Header{Name: name, Mode: mode, Size: int64(len(data)), Format: tar.FormatPAX}
	if err := writer.WriteHeader(header); err != nil {
		return fmt.Errorf("write source snapshot delta tar header %q: %w", name, err)
	}
	if _, err := writer.Write(data); err != nil {
		return fmt.Errorf("write source snapshot delta tar data %q: %w", name, err)
	}
	return nil
}

func sourceSnapshotSHA256(data []byte) string {
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func readSourceSnapshotDeltaTar(archive []byte) (SourceSnapshotDeltaManifest, map[string][]byte, error) {
	if len(archive) == 0 || len(archive) > maxSourceSnapshotDeltaBytes+maxSourceManifestLength+1<<20 {
		return SourceSnapshotDeltaManifest{}, nil, errors.New("source snapshot delta archive size is invalid")
	}
	reader := tar.NewReader(bytes.NewReader(archive))
	header, err := reader.Next()
	if err != nil || header.Name != sourceSnapshotDeltaManifestName || header.Typeflag != tar.TypeReg || header.Linkname != "" || header.Size < 1 || header.Size > maxSourceManifestLength {
		return SourceSnapshotDeltaManifest{}, nil, errors.New("source snapshot delta tar must start with a strict manifest")
	}
	manifestData, err := io.ReadAll(io.LimitReader(reader, maxSourceManifestLength+1))
	if err != nil || int64(len(manifestData)) != header.Size {
		return SourceSnapshotDeltaManifest{}, nil, errors.New("read source snapshot delta manifest")
	}
	var manifest SourceSnapshotDeltaManifest
	if err := decodeStrictJSON(manifestData, &manifest); err != nil {
		return SourceSnapshotDeltaManifest{}, nil, fmt.Errorf("decode source snapshot delta manifest: %w", err)
	}
	if err := validateSourceSnapshotDeltaManifest(manifest); err != nil {
		return SourceSnapshotDeltaManifest{}, nil, err
	}
	blobs := make(map[string][]byte)
	var payloadBytes int64
	for {
		header, err = reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || header.Typeflag != tar.TypeReg || header.Linkname != "" || !strings.HasPrefix(header.Name, "blobs/") || header.Name != "blobs/"+strings.TrimPrefix(header.Name, "blobs/") || header.Size < 0 || header.Size > maxSourceSnapshotDeltaFileBytes {
			return SourceSnapshotDeltaManifest{}, nil, errors.New("source snapshot delta tar contains an unsafe payload")
		}
		if len(blobs) >= maxSourceSnapshotDeltaEntries || payloadBytes > maxSourceSnapshotDeltaBytes-header.Size {
			return SourceSnapshotDeltaManifest{}, nil, errors.New("source snapshot delta tar exceeds payload limits")
		}
		oid := strings.TrimPrefix(header.Name, "blobs/")
		if !gitObjectPattern.MatchString(oid) {
			return SourceSnapshotDeltaManifest{}, nil, errors.New("source snapshot delta tar payload name is not a Git object ID")
		}
		if _, exists := blobs[oid]; exists {
			return SourceSnapshotDeltaManifest{}, nil, errors.New("source snapshot delta tar duplicates a blob")
		}
		data, readErr := io.ReadAll(io.LimitReader(reader, maxSourceSnapshotDeltaFileBytes+1))
		if readErr != nil || int64(len(data)) != header.Size {
			return SourceSnapshotDeltaManifest{}, nil, errors.New("read source snapshot delta blob")
		}
		blobs[oid] = data
		payloadBytes += header.Size
	}
	return manifest, blobs, nil
}

func validateSourceSnapshotDeltaManifest(manifest SourceSnapshotDeltaManifest) error {
	if manifest.SchemaVersion != SourceSnapshotDeltaSchemaVersion {
		return errors.New("source snapshot delta manifest schema is unsupported")
	}
	if err := cicontract.ValidateDeltaRebuild(
		manifest.TransferMode,
		manifest.Accepted.Authority.Generation,
		manifest.Accepted.Authority.SnapshotID,
		manifest.Target.ClosureDigest,
		manifest.Target.TreeOID,
		manifest.Target.ClosureDigest,
	); err != nil {
		return fmt.Errorf("validate source snapshot delta contract: %w", err)
	}
	if err := validateAcceptedSourceSnapshotManifest(manifest.Accepted); err != nil {
		return err
	}
	if err := validateSourceSnapshotIdentity("", manifest.Target.SourceDigest, manifest.Target.TreeOID, manifest.Target.ClosureDigest, manifest.Target.InputDigest, manifest.Target.ToolchainDigest, manifest.Target.PolicyDigest, manifest.Target.Platform, manifest.Target.ObjectFormat); err != nil {
		return err
	}
	if !sourceSnapshotFilesAreCanonical(manifest.Accepted.Content.Files) || !sourceSnapshotFilesAreCanonical(manifest.Target.Entries) {
		return errors.New("source snapshot delta manifest file entries are not canonical")
	}
	if err := validateSourceSnapshotCrossStatePaths(manifest.Accepted.Content.Files, manifest.Target.Entries); err != nil {
		return err
	}
	digest, err := SourceSnapshotClosureDigest(manifest.Target.Entries)
	if err != nil || digest != manifest.Target.ClosureDigest {
		return errors.New("source snapshot target closure digest does not match manifest")
	}
	if err := validateSourceSnapshotObjectFormat(manifest.Target.ObjectFormat, manifest.Target.Entries); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(manifest.Changes))
	for index, change := range manifest.Changes {
		if (change.Operation != "upsert" && change.Operation != "delete") || validateSourceSnapshotFile(change.File) != nil {
			return errors.New("source snapshot delta change is invalid")
		}
		if _, exists := seen[change.File.Path]; exists {
			return errors.New("source snapshot delta repeats a path")
		}
		if index > 0 && manifest.Changes[index-1].File.Path >= change.File.Path {
			return errors.New("source snapshot delta changes are not in canonical order")
		}
		seen[change.File.Path] = struct{}{}
	}
	return nil
}
