//go:build windows

package processobserve

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// readDurableRecords 读取并校验持久化根中的全部事件记录。
func (r *secureRoot) readDurableRecords() (map[string]loadedDurableRecord, error) {
	entries, err := os.ReadDir(r.path)
	if err != nil {
		return nil, fmt.Errorf("read durable observation root: %w", err)
	}
	loaded := make(map[string]loadedDurableRecord, len(entries))
	for _, entry := range entries {
		if entry.Name() == durableLockName {
			continue
		}
		record, key, size, err := r.loadWindowsDurableRecord(entry.Name())
		if err != nil {
			return nil, err
		}
		if _, exists := loaded[key]; exists {
			return nil, errors.New("durable observation contains duplicate incident key")
		}
		loaded[key] = loadedDurableRecord{record: record, size: size}
	}
	return loaded, nil
}

// loadWindowsDurableRecord 安全打开、解码并核对单个 Windows 事件记录。
func (r *secureRoot) loadWindowsDurableRecord(name string) (durableRecord, string, uint64, error) {
	eventID, err := durableIncidentEventID(name)
	if err != nil {
		return durableRecord{}, "", 0, err
	}
	raw, size, err := r.readWindowsDurableFile(name)
	if err != nil {
		return durableRecord{}, "", 0, err
	}
	record, err := decodeDurableRecord(raw)
	if err != nil {
		return durableRecord{}, "", 0, err
	}
	if record.EventID != eventID {
		return durableRecord{}, "", 0, errors.New("durable observation incident filename does not match event")
	}
	key := record.BucketKey
	if key == "" {
		key = record.DedupKey
	}
	if key == "" {
		return durableRecord{}, "", 0, errors.New("durable observation incident has no deduplication key")
	}
	return record, key, size, nil
}

func durableIncidentEventID(name string) (string, error) {
	if !strings.HasSuffix(name, ".incident") {
		return "", errors.New("durable observation root contains an unknown entry")
	}
	eventID := strings.TrimSuffix(name, ".incident")
	if !validID(eventID) {
		return "", errors.New("durable observation incident filename is invalid")
	}
	return eventID, nil
}

// readWindowsDurableFile 在验证文件身份、安全描述符和大小后读取记录内容。
func (r *secureRoot) readWindowsDurableFile(name string) ([]byte, uint64, error) {
	path := filepath.Join(r.path, name)
	handle, err := openWindowsDurableFile(
		path,
		windows.GENERIC_READ|windows.READ_CONTROL,
		windows.OPEN_EXISTING,
		windowsOpenReparsePoint,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("open durable observation incident: %w", err)
	}
	info, err := requireWindowsPrivateRegularFile(handle, maxDurableRecordSize)
	if err != nil {
		_ = windows.CloseHandle(handle)
		return nil, 0, err
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, 0, errors.New("create durable observation incident reader")
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, int64(maxDurableRecordSize)+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, 0, fmt.Errorf("read durable observation incident: %w", readErr)
	}
	if closeErr != nil {
		return nil, 0, fmt.Errorf("close durable observation incident: %w", closeErr)
	}
	if uint64(len(raw)) > maxDurableRecordSize {
		return nil, 0, errors.New("durable observation incident exceeds size limit")
	}
	size := uint64(info.FileSizeHigh)<<32 | uint64(info.FileSizeLow)
	return raw, size, nil
}

// deleteDurableRecord 在复核目标仍是私有常规文件后删除指定事件记录。
func (r *secureRoot) deleteDurableRecord(eventID string) error {
	if r == nil || r.handle == windows.InvalidHandle || !validID(eventID) {
		return errors.New("delete durable record: invalid event ID or root")
	}
	name := eventID + ".incident"
	if err := verifyWindowsDurableTarget(r, name, true); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(r.path, name)); err != nil {
		return fmt.Errorf("delete durable observation record: %w", err)
	}
	return nil
}

// publishDurableRecord 通过私有临时文件和原子替换发布事件记录。
func (r *secureRoot) publishDurableRecord(eventID string, raw []byte) (retErr error) {
	if !validDurablePayload(eventID, raw) {
		return errors.New("durable observation record is invalid")
	}
	tempName, tempPath, tempFile, err := createWindowsDurableTemp(r)
	if err != nil {
		return err
	}
	published := false
	defer func() {
		if !published {
			retErr = errors.Join(retErr, removeWindowsDurableTemp(tempPath))
		}
	}()
	if err := writeAndSyncWindowsDurable(tempFile, raw); err != nil {
		_ = tempFile.Close()
		return err
	}
	if _, err := requireWindowsPrivateRegularFile(windows.Handle(tempFile.Fd()), maxDurableRecordSize); err != nil {
		_ = tempFile.Close()
		return err
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("close durable observation temporary record: %w", err)
	}
	name := eventID + ".incident"
	if err := verifyWindowsDurableTarget(r, name, false); err != nil {
		return err
	}
	if err := moveWindowsDurableTemp(tempPath, filepath.Join(r.path, name)); err != nil {
		return err
	}
	published = true
	_ = tempName
	return verifyWindowsDurableTarget(r, name, true)
}

func removeWindowsDurableTemp(path string) error {
	err := os.Remove(path)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("remove durable observation temporary record: %w", err)
}

func validDurablePayload(eventID string, raw []byte) bool {
	return validID(eventID) && len(raw) > 0 && uint64(len(raw)) <= maxDurableRecordSize
}

func createWindowsDurableTemp(root *secureRoot) (string, string, *os.File, error) {
	name, err := durableTempName()
	if err != nil {
		return "", "", nil, err
	}
	path := filepath.Join(root.path, name)
	handle, err := openWindowsDurableFile(
		path,
		windows.GENERIC_READ|windows.GENERIC_WRITE|windows.READ_CONTROL,
		windows.CREATE_NEW,
		windowsOpenReparsePoint|windows.FILE_FLAG_WRITE_THROUGH,
	)
	if err != nil {
		return "", "", nil, fmt.Errorf("create durable observation temporary record: %w", err)
	}
	if _, err := requireWindowsPrivateRegularFile(handle, maxDurableRecordSize); err != nil {
		return "", "", nil, discardWindowsDurableTemp(handle, path, err)
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		err := errors.New("create durable observation temporary writer")
		return "", "", nil, discardWindowsDurableTemp(handle, path, err)
	}
	return name, path, file, nil
}

func discardWindowsDurableTemp(handle windows.Handle, path string, primary error) error {
	closeErr := windows.CloseHandle(handle)
	removeErr := removeWindowsDurableTemp(path)
	return errors.Join(primary, closeErr, removeErr)
}

func writeAndSyncWindowsDurable(file *os.File, raw []byte) error {
	for len(raw) > 0 {
		written, err := file.Write(raw)
		if err != nil {
			return fmt.Errorf("write durable observation record: %w", err)
		}
		if written == 0 {
			return errors.New("write durable observation record made no progress")
		}
		raw = raw[written:]
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync durable observation record: %w", err)
	}
	return nil
}

func verifyWindowsDurableTarget(root *secureRoot, name string, required bool) error {
	path := filepath.Join(root.path, name)
	handle, err := openWindowsDurableFile(
		path,
		windows.GENERIC_READ|windows.READ_CONTROL,
		windows.OPEN_EXISTING,
		windowsOpenReparsePoint,
	)
	if windowsPathNotExist(err) && !required {
		return nil
	}
	if err != nil {
		return fmt.Errorf("verify durable observation target: %w", err)
	}
	checkErr := func() error {
		_, err := requireWindowsPrivateRegularFile(handle, maxDurableRecordSize)
		return err
	}()
	closeErr := windows.CloseHandle(handle)
	return errors.Join(checkErr, closeErr)
}

func moveWindowsDurableTemp(source, destination string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	if err := windows.MoveFileEx(
		from,
		to,
		windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH,
	); err != nil {
		return fmt.Errorf("publish durable observation record: %w", err)
	}
	return nil
}

func durableTempName() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate durable observation temporary name: %w", err)
	}
	return ".incident-" + hex.EncodeToString(value[:]) + ".tmp", nil
}
