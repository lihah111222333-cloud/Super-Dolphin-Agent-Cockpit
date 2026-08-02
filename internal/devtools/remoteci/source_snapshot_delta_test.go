package remoteci

import (
	"archive/tar"
	"bytes"
	"crypto/sha1" // #nosec G505 -- test fixture constructs Git SHA-1 object IDs.
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sourceexport"
)

func TestCanonicalContextDigestsAreDeterministic(t *testing.T) {
	blobs := []SourceSnapshotBlob{
		testSourceSnapshotBlob(t, "bin/run", "100755", []byte("run\n")),
		testSourceSnapshotBlob(t, "input.txt", "100644", []byte("input\n")),
	}
	entries := make([]sourceexport.TreeEntry, len(blobs))
	for index, blob := range blobs {
		entries[index] = sourceexport.TreeEntry{Path: blob.Path, Mode: blob.Mode, Hash: blob.BlobOID, Data: blob.Data}
	}
	first, err := canonicalContextDigests(entries)
	if err != nil {
		t.Fatalf("canonicalContextDigests() error = %v", err)
	}
	second, err := canonicalContextDigests([]sourceexport.TreeEntry{entries[1], entries[0]})
	if err != nil {
		t.Fatalf("canonicalContextDigests() reordered error = %v", err)
	}
	if first != second {
		t.Fatalf("canonical context identity depends on source order: first=%#v second=%#v", first, second)
	}
}

func TestSourceSnapshotDeltaBuildAndApplyDeterministically(t *testing.T) {
	acceptedEntries := []SourceSnapshotFile{
		testSourceSnapshotFile(t, "bin/run", "100755", []byte("old executable\n")),
		testSourceSnapshotFile(t, "removed.txt", "100644", []byte("remove me\n")),
		testSourceSnapshotFile(t, "same.txt", "100644", []byte("unchanged\n")),
	}
	accepted := testAcceptedSourceSnapshotManifest(t, acceptedEntries)
	targetEntries := []SourceSnapshotBlob{
		testSourceSnapshotBlob(t, "bin/run", "100755", []byte("new executable\n")),
		testSourceSnapshotBlob(t, "new.txt", "100644", []byte("new file\n")),
		testSourceSnapshotBlob(t, "same.txt", "100644", []byte("unchanged\n")),
	}
	target := testTargetSourceSnapshotClosure(t, targetEntries)
	first, err := BuildSourceSnapshotDelta(accepted, target)
	if err != nil {
		t.Fatalf("BuildSourceSnapshotDelta() error = %v", err)
	}
	second, err := BuildSourceSnapshotDelta(accepted, target)
	if err != nil {
		t.Fatalf("BuildSourceSnapshotDelta() second error = %v", err)
	}
	if !bytes.Equal(first.Archive, second.Archive) {
		t.Fatal("BuildSourceSnapshotDelta() is not deterministic")
	}
	if got, want := len(first.Manifest.Changes), 3; got != want {
		t.Fatalf("delta changes = %d, want %d", got, want)
	}
	reader := tar.NewReader(bytes.NewReader(first.Archive))
	header, err := reader.Next()
	if err != nil || header.Name != sourceSnapshotDeltaManifestName || header.Format == tar.FormatGNU {
		t.Fatalf("first tar entry = %#v, %v", header, err)
	}

	acceptedRoot := t.TempDir()
	for _, entry := range acceptedEntries {
		writeTestSourceSnapshotFile(t, acceptedRoot, entry, testSourceSnapshotData(entry.Path))
	}
	outputRoot := t.TempDir()
	manifest, err := ApplySourceSnapshotDelta(acceptedRoot, outputRoot, accepted, first.Archive)
	if err != nil {
		t.Fatalf("ApplySourceSnapshotDelta() error = %v", err)
	}
	if manifest.Target.ClosureDigest != target.ClosureDigest {
		t.Fatalf("applied closure = %q, want %q", manifest.Target.ClosureDigest, target.ClosureDigest)
	}
	data, err := os.ReadFile(filepath.Join(outputRoot, "bin", "run"))
	if err != nil || string(data) != "new executable\n" {
		t.Fatalf("applied executable = %q, %v", data, err)
	}
	info, err := os.Stat(filepath.Join(outputRoot, "bin", "run"))
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("applied executable mode = %v, %v", info.Mode(), err)
	}
	if _, err := os.Stat(filepath.Join(outputRoot, "removed.txt")); !os.IsNotExist(err) {
		t.Fatalf("deleted path stat error = %v", err)
	}
}

func TestSourceSnapshotDeltaCanonicalizesEquivalentInputOrder(t *testing.T) {
	acceptedEntries := []SourceSnapshotFile{
		testSourceSnapshotFile(t, "bin/run", "100755", []byte("old executable\n")),
		testSourceSnapshotFile(t, "same.txt", "100644", []byte("unchanged\n")),
	}
	targetEntries := []SourceSnapshotBlob{
		testSourceSnapshotBlob(t, "bin/run", "100755", []byte("new executable\n")),
		testSourceSnapshotBlob(t, "new.txt", "100644", []byte("new file\n")),
		testSourceSnapshotBlob(t, "same.txt", "100644", []byte("unchanged\n")),
	}
	first, err := BuildSourceSnapshotDelta(testAcceptedSourceSnapshotManifest(t, acceptedEntries), testTargetSourceSnapshotClosure(t, targetEntries))
	if err != nil {
		t.Fatalf("BuildSourceSnapshotDelta() first error = %v", err)
	}
	reversedAccepted := []SourceSnapshotFile{acceptedEntries[1], acceptedEntries[0]}
	reversedTarget := []SourceSnapshotBlob{targetEntries[2], targetEntries[1], targetEntries[0]}
	second, err := BuildSourceSnapshotDelta(testAcceptedSourceSnapshotManifest(t, reversedAccepted), testTargetSourceSnapshotClosure(t, reversedTarget))
	if err != nil {
		t.Fatalf("BuildSourceSnapshotDelta() reordered error = %v", err)
	}
	if !bytes.Equal(first.Archive, second.Archive) {
		t.Fatal("BuildSourceSnapshotDelta() output depends on input ordering")
	}
}

func TestSourceSnapshotDeltaRejectsCaseOnlyRename(t *testing.T) {
	accepted := testAcceptedSourceSnapshotManifest(t, []SourceSnapshotFile{testSourceSnapshotFile(t, "README", "100644", []byte("old\n"))})
	target := testTargetSourceSnapshotClosure(t, []SourceSnapshotBlob{testSourceSnapshotBlob(t, "readme", "100644", []byte("new\n"))})
	if _, err := BuildSourceSnapshotDelta(accepted, target); err == nil {
		t.Fatal("BuildSourceSnapshotDelta() accepted a case-only path rename")
	}
}

func TestApplySourceSnapshotDeltaRejectsLinkPayloads(t *testing.T) {
	acceptedEntry := testSourceSnapshotFile(t, "safe.txt", "100644", []byte("old\n"))
	accepted := testAcceptedSourceSnapshotManifest(t, []SourceSnapshotFile{acceptedEntry})
	targetEntry := testSourceSnapshotBlob(t, "safe.txt", "100644", []byte("new\n"))
	delta, err := BuildSourceSnapshotDelta(accepted, testTargetSourceSnapshotClosure(t, []SourceSnapshotBlob{targetEntry}))
	if err != nil {
		t.Fatalf("BuildSourceSnapshotDelta() error = %v", err)
	}
	acceptedRoot := t.TempDir()
	writeTestSourceSnapshotFile(t, acceptedRoot, acceptedEntry, []byte("old\n"))
	for name, typeflag := range map[string]byte{"symlink": tar.TypeSymlink, "hardlink": tar.TypeLink} {
		t.Run(name, func(t *testing.T) {
			archive := testSourceSnapshotLinkArchive(t, delta.Manifest, targetEntry.BlobOID, typeflag)
			if _, err := ApplySourceSnapshotDelta(acceptedRoot, t.TempDir(), accepted, archive); err == nil || !strings.Contains(err.Error(), "source snapshot delta tar contains an unsafe payload") {
				t.Fatalf("ApplySourceSnapshotDelta() %s payload error = %v, want unsafe tar payload rejection", name, err)
			}
		})
	}
}

func TestApplySourceSnapshotDeltaRejectsAcceptedSnapshotSymlinkParent(t *testing.T) {
	acceptedEntry := testSourceSnapshotFile(t, "bin/run", "100755", []byte("old executable\n"))
	accepted := testAcceptedSourceSnapshotManifest(t, []SourceSnapshotFile{acceptedEntry})
	target := testTargetSourceSnapshotClosure(t, []SourceSnapshotBlob{testSourceSnapshotBlob(t, "bin/run", "100755", []byte("old executable\n"))})
	delta, err := BuildSourceSnapshotDelta(accepted, target)
	if err != nil {
		t.Fatalf("BuildSourceSnapshotDelta() error = %v", err)
	}
	acceptedRoot := t.TempDir()
	externalRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(externalRoot, "run"), []byte("old executable\n"), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.Symlink(externalRoot, filepath.Join(acceptedRoot, "bin")); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	if _, err := ApplySourceSnapshotDelta(acceptedRoot, t.TempDir(), accepted, delta.Archive); err == nil || !strings.Contains(err.Error(), "source snapshot file has a symlink parent") {
		t.Fatalf("ApplySourceSnapshotDelta() error = %v, want symlink-parent rejection", err)
	}
}

func TestApplySourceSnapshotDeltaRejectsAcceptedSnapshotHardLink(t *testing.T) {
	acceptedEntry := testSourceSnapshotFile(t, "safe.txt", "100644", []byte("old\n"))
	accepted := testAcceptedSourceSnapshotManifest(t, []SourceSnapshotFile{acceptedEntry})
	target := testTargetSourceSnapshotClosure(t, []SourceSnapshotBlob{testSourceSnapshotBlob(t, "safe.txt", "100644", []byte("old\n"))})
	delta, err := BuildSourceSnapshotDelta(accepted, target)
	if err != nil {
		t.Fatalf("BuildSourceSnapshotDelta() error = %v", err)
	}
	acceptedRoot := t.TempDir()
	externalPath := filepath.Join(t.TempDir(), "source.txt")
	if err := os.WriteFile(externalPath, []byte("old\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.Link(externalPath, filepath.Join(acceptedRoot, acceptedEntry.Path)); err != nil {
		t.Skipf("hard links are unavailable: %v", err)
	}
	if _, err := ApplySourceSnapshotDelta(acceptedRoot, t.TempDir(), accepted, delta.Archive); err == nil || !strings.Contains(err.Error(), "source snapshot file must not be a hard link") {
		t.Fatalf("ApplySourceSnapshotDelta() error = %v, want hard-link rejection", err)
	}
}

func TestApplySourceSnapshotDeltaRejectsAcceptedBlobOIDMismatch(t *testing.T) {
	acceptedEntry := testSourceSnapshotFile(t, "safe.txt", "100644", []byte("old\n"))
	acceptedEntry.BlobOID = strings.Repeat("a", 40)
	accepted := testAcceptedSourceSnapshotManifest(t, []SourceSnapshotFile{acceptedEntry})
	targetEntry := testSourceSnapshotBlob(t, "new.txt", "100644", []byte("new\n"))
	delta, err := BuildSourceSnapshotDelta(accepted, testTargetSourceSnapshotClosure(t, []SourceSnapshotBlob{targetEntry}))
	if err != nil {
		t.Fatalf("BuildSourceSnapshotDelta() error = %v", err)
	}
	acceptedRoot := t.TempDir()
	writeTestSourceSnapshotFile(t, acceptedRoot, acceptedEntry, []byte("old\n"))
	if _, err := ApplySourceSnapshotDelta(acceptedRoot, t.TempDir(), accepted, delta.Archive); err == nil {
		t.Fatal("ApplySourceSnapshotDelta() accepted an accepted-source Git blob OID mismatch")
	}
}

func TestSourceSnapshotDeltaRejectsUnsafeAndUnacceptedInputs(t *testing.T) {
	entry := testSourceSnapshotBlob(t, "safe.txt", "100644", []byte("safe\n"))
	target := testTargetSourceSnapshotClosure(t, []SourceSnapshotBlob{entry})
	if _, err := BuildSourceSnapshotDelta(AcceptedSourceSnapshotManifest{}, target); err == nil {
		t.Fatal("BuildSourceSnapshotDelta() accepted missing accepted manifest")
	}
	unsafe := entry
	unsafe.Path = "../escape"
	target.Entries = []SourceSnapshotBlob{unsafe}
	if _, err := BuildSourceSnapshotDelta(testAcceptedSourceSnapshotManifest(t, []SourceSnapshotFile{entry.SourceSnapshotFile}), target); err == nil || !strings.Contains(err.Error(), "source snapshot path \"../escape\" is unsafe") {
		t.Fatalf("BuildSourceSnapshotDelta() traversal path error = %v, want target-closure path rejection", err)
	}
	badOID := entry
	badOID.BlobOID = strings.Repeat("0", 40)
	target = testTargetSourceSnapshotClosure(t, []SourceSnapshotBlob{badOID})
	if _, err := BuildSourceSnapshotDelta(testAcceptedSourceSnapshotManifest(t, []SourceSnapshotFile{entry.SourceSnapshotFile}), target); err == nil {
		t.Fatal("BuildSourceSnapshotDelta() accepted non-Git blob object")
	}
}

func TestSourceSnapshotContentManifestStrictSchemaAndFields(t *testing.T) {
	accepted := testAcceptedSourceSnapshotManifest(t, []SourceSnapshotFile{testSourceSnapshotFile(t, "safe.txt", "100644", []byte("safe\n"))})
	encoded, err := EncodeSourceSnapshotContentManifest(accepted.Content)
	if err != nil {
		t.Fatalf("EncodeSourceSnapshotContentManifest() error = %v", err)
	}
	decoded, err := DecodeSourceSnapshotContentManifest(encoded)
	if err != nil {
		t.Fatalf("DecodeSourceSnapshotContentManifest() error = %v", err)
	}
	decodedDigest, err := SourceSnapshotContentDigest(decoded)
	if err != nil || decodedDigest != accepted.Authority.SourceDigest {
		t.Fatalf("content manifest digest = %q, %v", decodedDigest, err)
	}
	for name, wire := range map[string][]byte{
		"old entries key": []byte(strings.Replace(string(encoded), `"files"`, `"entries"`, 1)),
		"old tree key":    []byte(strings.Replace(string(encoded), `"source_tree"`, `"tree_oid"`, 1)),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeSourceSnapshotContentManifest(wire); err == nil {
				t.Fatalf("DecodeSourceSnapshotContentManifest() accepted %s", name)
			}
		})
	}
	for name, mutate := range map[string]func(*SourceSnapshotContentManifest){
		"size": func(content *SourceSnapshotContentManifest) { content.Files[0].Size++ },
		"mode": func(content *SourceSnapshotContentManifest) { content.Files[0].Mode = "120000" },
		"oid":  func(content *SourceSnapshotContentManifest) { content.Files[0].BlobOID = strings.Repeat("g", 40) },
	} {
		t.Run(name, func(t *testing.T) {
			content := accepted.Content
			content.Files = append([]SourceSnapshotFile(nil), content.Files...)
			mutate(&content)
			if err := ValidateSourceSnapshotContentManifest(content); err == nil {
				t.Fatalf("ValidateSourceSnapshotContentManifest() accepted invalid %s", name)
			}
		})
	}
}

func testAcceptedSourceSnapshotManifest(t *testing.T, entries []SourceSnapshotFile) AcceptedSourceSnapshotManifest {
	t.Helper()
	closure, err := SourceSnapshotClosureDigest(entries)
	if err != nil {
		t.Fatalf("SourceSnapshotClosureDigest() error = %v", err)
	}
	content := SourceSnapshotContentManifest{SchemaVersion: SourceSnapshotContentManifestSchemaVersion, SourceTree: strings.Repeat("a", 40), ClosureDigest: closure, ImageInputDigest: testSourceSnapshotDigest("accepted-input"), ToolchainDigest: testSourceSnapshotDigest("toolchain"), PolicyDigest: testSourceSnapshotDigest("policy"), Platform: "linux/amd64", ObjectFormat: "sha1", Files: entries}
	sourceDigest, err := SourceSnapshotSourceDigest(content)
	if err != nil {
		t.Fatalf("SourceSnapshotSourceDigest() error = %v", err)
	}
	manifest, err := NewAcceptedSourceSnapshotManifest(SourceSnapshotAuthorityBinding{Generation: 7, StateDigest: testSourceSnapshotDigest("state"), SnapshotID: "accepted-snapshot", SourceDigest: sourceDigest}, content)
	if err != nil {
		t.Fatalf("NewAcceptedSourceSnapshotManifest() error = %v", err)
	}
	return manifest
}

func testTargetSourceSnapshotClosure(t *testing.T, entries []SourceSnapshotBlob) TargetSourceBuildClosure {
	t.Helper()
	files := make([]SourceSnapshotFile, len(entries))
	for index, entry := range entries {
		files[index] = entry.SourceSnapshotFile
	}
	closure, err := SourceSnapshotClosureDigest(files)
	if err != nil {
		t.Fatalf("SourceSnapshotClosureDigest() error = %v", err)
	}
	return TargetSourceBuildClosure{SourceDigest: testSourceSnapshotDigest("target-source"), TreeOID: strings.Repeat("b", 40), ClosureDigest: closure, InputDigest: testSourceSnapshotDigest("target-input"), ToolchainDigest: testSourceSnapshotDigest("toolchain"), PolicyDigest: testSourceSnapshotDigest("policy"), Platform: "linux/amd64", ObjectFormat: "sha1", Entries: entries}
}

func testSourceSnapshotBlob(t *testing.T, filePath, mode string, data []byte) SourceSnapshotBlob {
	t.Helper()
	return SourceSnapshotBlob{SourceSnapshotFile: testSourceSnapshotFile(t, filePath, mode, data), Data: append([]byte(nil), data...)}
}

func testSourceSnapshotFile(t *testing.T, filePath, mode string, data []byte) SourceSnapshotFile {
	t.Helper()
	header := []byte("blob " + strconv.Itoa(len(data)) + "\x00")
	object := sha1.Sum(append(header, data...))
	return SourceSnapshotFile{Path: filePath, Mode: mode, BlobOID: hex.EncodeToString(object[:]), Size: int64(len(data)), BlobDigest: sourceSnapshotSHA256(data)}
}

func testSourceSnapshotDigest(value string) string { return sourceSnapshotSHA256([]byte(value)) }

func testSourceSnapshotLinkArchive(t *testing.T, manifest SourceSnapshotDeltaManifest, oid string, typeflag byte) []byte {
	t.Helper()
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	if err := writer.WriteHeader(&tar.Header{Name: sourceSnapshotDeltaManifestName, Mode: 0o600, Size: int64(len(manifestData)), Format: tar.FormatPAX}); err != nil {
		t.Fatalf("WriteHeader() manifest error = %v", err)
	}
	if _, err := writer.Write(manifestData); err != nil {
		t.Fatalf("Write() manifest error = %v", err)
	}
	if err := writer.WriteHeader(&tar.Header{Name: "blobs/" + oid, Typeflag: typeflag, Linkname: "../outside", Format: tar.FormatPAX}); err != nil {
		t.Fatalf("WriteHeader() link error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return archive.Bytes()
}

func writeTestSourceSnapshotFile(t *testing.T, root string, entry SourceSnapshotFile, data []byte) {
	t.Helper()
	filePath := filepath.Join(root, filepath.FromSlash(entry.Path))
	if err := os.MkdirAll(filepath.Dir(filePath), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	permissions := os.FileMode(0o644)
	if entry.Mode == "100755" {
		permissions = 0o755
	}
	if err := os.WriteFile(filePath, data, permissions); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func testSourceSnapshotData(filePath string) []byte {
	switch filePath {
	case "bin/run":
		return []byte("old executable\n")
	case "removed.txt":
		return []byte("remove me\n")
	default:
		return []byte("unchanged\n")
	}
}
