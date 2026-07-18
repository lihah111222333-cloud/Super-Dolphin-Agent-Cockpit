//go:build unix

package schema

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSweepFilesystemSnapshotPublishingCrashWindows(t *testing.T) {
	setFilesystemSnapshotRoot(t)
	windows := []struct {
		name    string
		prepare func(*testing.T, filesystemSnapshotIdentity, string) string
	}{
		{name: "marker open", prepare: prepareMarkerPublishingOpen},
		{name: "marker write", prepare: prepareMarkerPublishingWrite},
		{name: "marker close", prepare: prepareMarkerPublishingClose},
		{name: "marker rename", prepare: prepareMarkerPublished},
		{name: "helper open", prepare: prepareHelperPublishingOpen},
		{name: "helper write", prepare: prepareHelperPublishingWrite},
		{name: "helper close", prepare: prepareHelperPublishingClose},
		{name: "helper rename", prepare: prepareHelperPublished},
		{name: "snapshot rename", prepare: prepareSnapshotPublished},
	}
	owners := []struct {
		name        string
		mutateOwner func(*filesystemSnapshotIdentity)
		wantExists  bool
	}{
		{name: "active", wantExists: true},
		{name: "stale", mutateOwner: makeStaleFilesystemSnapshotOwner},
		{name: "PID reuse", mutateOwner: makeReusedFilesystemSnapshotOwner},
	}
	for _, window := range windows {
		for _, owner := range owners {
			t.Run(window.name+"/"+owner.name, func(t *testing.T) {
				identity := newTestFilesystemSnapshotIdentity(t)
				if owner.mutateOwner != nil {
					owner.mutateOwner(&identity)
				}
				directory, err := createFilesystemSnapshotStagingDirectory(identity)
				if err != nil {
					t.Fatal(err)
				}
				candidate := window.prepare(t, identity, directory)
				t.Cleanup(func() { _ = removeOwnedFilesystemSnapshot(identity) })
				if err := sweepStaleFilesystemSnapshots(); err != nil {
					t.Fatalf("sweep %s for %s owner: %v", window.name, owner.name, err)
				}
				assertPathExistence(t, candidate, owner.wantExists)
			})
		}
	}
}

func TestSweepFilesystemSnapshotPreservesActiveOpenMarkerWrite(t *testing.T) {
	setFilesystemSnapshotRoot(t)
	identity := newTestFilesystemSnapshotIdentity(t)
	directory, err := createFilesystemSnapshotStagingDirectory(identity)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = removeOwnedFilesystemSnapshot(identity) })
	path := publishingFilesystemSnapshotPath(directory, filesystemSnapshotMarker)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	if _, err := file.Write([]byte("{")); err != nil {
		t.Fatal(err)
	}
	if err := sweepStaleFilesystemSnapshots(); err != nil {
		t.Fatalf("sweep active open marker write: %v", err)
	}
	assertPathExistence(t, directory, true)
}

func TestWriteExecutableSnapshotPublishesWithoutIntermediateFiles(t *testing.T) {
	setFilesystemSnapshotRoot(t)
	identity := newTestFilesystemSnapshotIdentity(t)
	path, err := writeExecutableSnapshot([]byte("durable-helper"), identity)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = removeOwnedFilesystemSnapshot(identity) })
	wantPath := filepath.Join(identity.Directory, HelperFileName(identity.HelperGOOS))
	if path != wantPath {
		t.Fatalf("snapshot path = %q, want %q", path, wantPath)
	}
	assertPublishedFilesystemSnapshot(t, identity, "durable-helper")
	assertPathExistence(t, filesystemSnapshotStagingDirectory(identity), false)
	assertPathExistence(t, filepath.Join(identity.Directory, filesystemSnapshotMarker)+filesystemSnapshotPublishSuffix, false)
	assertPathExistence(t, wantPath+filesystemSnapshotPublishSuffix, false)
}

func prepareMarkerPublishingOpen(t *testing.T, _ filesystemSnapshotIdentity, directory string) string {
	t.Helper()
	writeCrashWindowFile(t, publishingFilesystemSnapshotPath(directory, filesystemSnapshotMarker), nil, 0o600)
	return directory
}

func prepareMarkerPublishingWrite(t *testing.T, _ filesystemSnapshotIdentity, directory string) string {
	t.Helper()
	writeCrashWindowFile(t, publishingFilesystemSnapshotPath(directory, filesystemSnapshotMarker), []byte("{"), 0o600)
	return directory
}

func prepareMarkerPublishingClose(t *testing.T, identity filesystemSnapshotIdentity, directory string) string {
	t.Helper()
	writeCrashWindowFile(t, publishingFilesystemSnapshotPath(directory, filesystemSnapshotMarker), encodeCrashWindowIdentity(t, identity), 0o600)
	return directory
}

func prepareMarkerPublished(t *testing.T, identity filesystemSnapshotIdentity, directory string) string {
	t.Helper()
	writeCrashWindowFile(t, filepath.Join(directory, filesystemSnapshotMarker), encodeCrashWindowIdentity(t, identity), 0o600)
	return directory
}

func prepareHelperPublishingOpen(t *testing.T, identity filesystemSnapshotIdentity, directory string) string {
	t.Helper()
	prepareMarkerPublished(t, identity, directory)
	writeCrashWindowFile(t, publishingFilesystemSnapshotPath(directory, HelperFileName(identity.HelperGOOS)), nil, 0o700)
	return directory
}

func prepareHelperPublishingWrite(t *testing.T, identity filesystemSnapshotIdentity, directory string) string {
	t.Helper()
	prepareMarkerPublished(t, identity, directory)
	writeCrashWindowFile(t, publishingFilesystemSnapshotPath(directory, HelperFileName(identity.HelperGOOS)), []byte("partial"), 0o700)
	return directory
}

func prepareHelperPublishingClose(t *testing.T, identity filesystemSnapshotIdentity, directory string) string {
	t.Helper()
	prepareMarkerPublished(t, identity, directory)
	writeCrashWindowFile(t, publishingFilesystemSnapshotPath(directory, HelperFileName(identity.HelperGOOS)), []byte("helper"), 0o700)
	return directory
}

func prepareHelperPublished(t *testing.T, identity filesystemSnapshotIdentity, directory string) string {
	t.Helper()
	prepareMarkerPublished(t, identity, directory)
	writeCrashWindowFile(t, filepath.Join(directory, HelperFileName(identity.HelperGOOS)), []byte("helper"), 0o700)
	return directory
}

func prepareSnapshotPublished(t *testing.T, identity filesystemSnapshotIdentity, directory string) string {
	t.Helper()
	prepareHelperPublished(t, identity, directory)
	if err := os.Rename(directory, identity.Directory); err != nil {
		t.Fatal(err)
	}
	return identity.Directory
}

func publishingFilesystemSnapshotPath(directory, name string) string {
	return filepath.Join(directory, name+filesystemSnapshotPublishSuffix)
}

func writeCrashWindowFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}

func encodeCrashWindowIdentity(t *testing.T, identity filesystemSnapshotIdentity) []byte {
	t.Helper()
	encoded, err := json.Marshal(identity)
	if err != nil {
		t.Fatal(err)
	}
	return append(encoded, '\n')
}
