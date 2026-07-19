package schema

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type filesystemSnapshotStagingState uint8

const (
	filesystemSnapshotStagingEmpty filesystemSnapshotStagingState = iota
	filesystemSnapshotStagingPublishing
	filesystemSnapshotStagingOwned
)

func writeFilesystemSnapshotMarker(directory string, identity filesystemSnapshotIdentity) error {
	encoded, err := json.Marshal(identity)
	if err != nil {
		return fmt.Errorf("encode schema snapshot owner marker: %w", err)
	}
	encoded = append(encoded, '\n')
	return writeExclusiveRegularFile(filepath.Join(directory, filesystemSnapshotMarker), encoded, 0o600)
}

// writeExclusiveRegularFile 以同目录临时文件和 fsync 原子发布严格独占文件。
func writeExclusiveRegularFile(path string, data []byte, mode os.FileMode) error {
	return writeDurableRegularFile(path, data, mode, true)
}

// writeAtomicRegularFile 以同目录临时文件和 fsync 原子替换常规文件。
func writeAtomicRegularFile(path string, data []byte, mode os.FileMode) error {
	return writeDurableRegularFile(path, data, mode, false)
}

// writeDurableRegularFile 写入、同步并原子发布文件，失败时保留可清扫的中间态。
func writeDurableRegularFile(path string, data []byte, mode os.FileMode, exclusive bool) (err error) {
	publishingPath := path + filesystemSnapshotPublishSuffix
	file, err := os.OpenFile(publishingPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, removeSnapshotPathIfPresent(publishingPath))
		}
	}()
	if err := writeAndSyncFilesystemSnapshotFile(file, data); err != nil {
		return err
	}
	if exclusive {
		if err := ensureFilesystemSnapshotFileAbsent(path); err != nil {
			return err
		}
	}
	if err := os.Rename(publishingPath, path); err != nil {
		return err
	}
	return syncFilesystemSnapshotDirectory(filepath.Dir(path))
}

func writeAndSyncFilesystemSnapshotFile(file *os.File, data []byte) error {
	written, err := file.Write(data)
	if err != nil {
		return errors.Join(err, file.Close())
	}
	if written != len(data) {
		return errors.Join(io.ErrShortWrite, file.Close())
	}
	if err := file.Sync(); err != nil {
		return errors.Join(err, file.Close())
	}
	return file.Close()
}

func ensureFilesystemSnapshotFileAbsent(path string) error {
	if _, err := os.Lstat(path); err == nil {
		return errors.New("schema snapshot file already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func syncFilesystemSnapshotDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open schema snapshot directory for fsync: %w", err)
	}
	if err := errors.Join(directory.Sync(), directory.Close()); err != nil {
		return fmt.Errorf("fsync schema snapshot directory: %w", err)
	}
	return nil
}

// classifyFilesystemSnapshotStagingState 只接受 empty、marker publishing 或有最终 marker 的状态。
func classifyFilesystemSnapshotStagingState(directory string, entries []os.DirEntry) (filesystemSnapshotStagingState, error) {
	if len(entries) == 0 {
		return filesystemSnapshotStagingEmpty, nil
	}
	if len(entries) > 2 {
		return 0, errors.New("schema snapshot staging contains an unexpected number of entries")
	}
	markerPublishing := filesystemSnapshotMarker + filesystemSnapshotPublishSuffix
	if len(entries) == 1 && entries[0].Name() == markerPublishing {
		if err := validateFilesystemSnapshotEntry(directory, "", entries[0]); err != nil {
			return 0, err
		}
		return filesystemSnapshotStagingPublishing, nil
	}
	for _, entry := range entries {
		if entry.Name() == filesystemSnapshotMarker {
			return filesystemSnapshotStagingOwned, nil
		}
	}
	return 0, errors.New("schema snapshot staging marker is missing")
}

// validateSweepStagingEntries 拒绝完整 marker 状态中的未知或重复发布条目。
func validateSweepStagingEntries(directory, helperName string, entries []os.DirEntry) error {
	for _, entry := range entries {
		name := entry.Name()
		if name != filesystemSnapshotMarker && name != helperName && name != helperName+filesystemSnapshotPublishSuffix {
			return fmt.Errorf("schema snapshot contains unexpected entry %q", name)
		}
		if err := validateFilesystemSnapshotEntry(directory, helperName, entry); err != nil {
			return err
		}
	}
	return nil
}
