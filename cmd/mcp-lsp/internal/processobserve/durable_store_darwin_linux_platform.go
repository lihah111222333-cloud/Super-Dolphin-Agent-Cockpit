//go:build darwin || linux

package processobserve

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const durableLockName = ".store.lock"

type secureRoot struct {
	fd  int
	dev uint64
	ino uint64
}

func openDurableRoot(path string) (*secureRoot, error) {
	absolute, err := validatedDurableRootPath(path)
	if err != nil {
		return nil, err
	}
	fd, err := openDurableAnchor()
	if err != nil {
		return nil, fmt.Errorf("open trusted filesystem anchor: %w", err)
	}
	closeFD := true
	defer func() {
		if closeFD {
			_ = unix.Close(fd)
		}
	}()
	components := strings.Split(strings.TrimPrefix(absolute, string(filepath.Separator)), string(filepath.Separator))
	for _, component := range components {
		next, openErr := openDurableComponent(fd, component)
		if openErr != nil {
			return nil, openErr
		}
		if closeErr := unix.Close(fd); closeErr != nil {
			_ = unix.Close(next)
			return nil, fmt.Errorf("close durable observation root component: %w", closeErr)
		}
		fd = next
	}
	root := &secureRoot{fd: fd}
	closeFD = false
	if err := requirePrivateDirectory(root.fd); err != nil {
		_ = root.close()
		return nil, err
	}
	root.dev, root.ino, err = descriptorIdentity(root.fd)
	if err != nil {
		_ = root.close()
		return nil, err
	}
	return root, nil
}

func validatedDurableRootPath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("durable observation root must be absolute")
	}
	absolute := filepath.Clean(path)
	if absolute == string(filepath.Separator) || strings.TrimSpace(absolute) == "" {
		return "", errors.New("durable observation root must be a private directory")
	}
	return absolute, nil
}

func openDurableAnchor() (int, error) {
	return unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
}

func openDurableComponent(parentFD int, component string) (int, error) {
	if component == "" || component == "." || component == ".." {
		return -1, errors.New("durable observation root contains an unsafe component")
	}
	flags := unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC | unix.O_NOFOLLOW
	fd, err := unix.Openat(parentFD, component, flags, 0)
	if !errors.Is(err, unix.ENOENT) {
		if err != nil {
			return -1, fmt.Errorf("open durable observation root component: %w", err)
		}
		return fd, nil
	}
	if mkdirErr := unix.Mkdirat(parentFD, component, 0o700); mkdirErr != nil && !errors.Is(mkdirErr, unix.EEXIST) {
		return -1, fmt.Errorf("create durable observation root component: %w", mkdirErr)
	}
	fd, err = unix.Openat(parentFD, component, flags, 0)
	if err != nil {
		return -1, fmt.Errorf("open durable observation root component: %w", err)
	}
	return fd, nil
}

func (r *secureRoot) identity() (uint64, uint64) {
	if r == nil {
		return 0, 0
	}
	return r.dev, r.ino
}

func (r *secureRoot) close() error {
	if r == nil || r.fd < 0 {
		return nil
	}
	err := unix.Close(r.fd)
	r.fd = -1
	return err
}

func (r *secureRoot) withStoreLock(ctx context.Context, action func(*secureRoot) error) error {
	if r == nil || r.fd < 0 {
		return ErrDurableStoreClosed
	}
	lockFD, err := unix.Openat(r.fd, durableLockName, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("open durable observation lock: %w", err)
	}
	defer func() { _ = unix.Close(lockFD) }()
	if err := requirePrivateRegularFile(lockFD, 0); err != nil {
		return err
	}
	if err := acquireDurableLock(ctx, lockFD); err != nil {
		return err
	}
	defer func() { _ = unix.Flock(lockFD, unix.LOCK_UN) }()
	return action(r)
}

func acquireDurableLock(ctx context.Context, fd int) error {
	for {
		err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return nil
		}
		if !retryableDurableLockError(err) {
			return fmt.Errorf("lock durable observation store: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func retryableDurableLockError(err error) bool {
	return errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN)
}

func (r *secureRoot) readDurableRecords() (map[string]loadedDurableRecord, error) {
	names, err := r.recordNames()
	if err != nil {
		return nil, err
	}
	loaded := make(map[string]loadedDurableRecord, len(names))
	for _, name := range names {
		if name == durableLockName {
			continue
		}
		record, key, size, err := r.loadDurableRecord(name)
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

func (r *secureRoot) loadDurableRecord(name string) (durableRecord, string, uint64, error) {
	eventID, err := durableIncidentEventID(name)
	if err != nil {
		return durableRecord{}, "", 0, err
	}
	raw, size, err := r.readDurableFile(name)
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

func (r *secureRoot) recordNames() ([]string, error) {
	dup, err := unix.Dup(r.fd)
	if err != nil {
		return nil, fmt.Errorf("duplicate durable observation root handle: %w", err)
	}
	file := os.NewFile(uintptr(dup), "durable-observation-root")
	if file == nil {
		_ = unix.Close(dup)
		return nil, errors.New("create durable observation root reader")
	}
	names, readErr := file.Readdirnames(-1)
	closeErr := file.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return nil, fmt.Errorf("read durable observation root: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close durable observation root reader: %w", closeErr)
	}
	return names, nil
}

func (r *secureRoot) readDurableFile(name string) ([]byte, uint64, error) {
	fd, err := unix.Openat(r.fd, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, 0, fmt.Errorf("open durable observation incident: %w", err)
	}
	defer func() { _ = unix.Close(fd) }()
	if err := requirePrivateRegularFile(fd, int64(maxDurableRecordSize)); err != nil {
		return nil, 0, err
	}
	size, err := durableFileSize(fd)
	if err != nil {
		return nil, 0, err
	}
	buf := make([]byte, 0, int(size))
	return readDurableChunks(fd, buf)
}

func durableFileSize(fd int) (int64, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return 0, fmt.Errorf("inspect durable observation incident: %w", err)
	}
	if stat.Size < 0 {
		return 0, errors.New("durable observation incident has negative size")
	}
	if stat.Size > int64(maxDurableRecordSize) {
		return 0, errors.New("durable observation incident exceeds size limit")
	}
	return stat.Size, nil
}

func readDurableChunks(fd int, buf []byte) ([]byte, uint64, error) {
	chunk := make([]byte, 32*1024)
	for {
		count, readErr := unix.Read(fd, chunk)
		if count > 0 {
			buf = append(buf, chunk[:count]...)
			if uint64(len(buf)) > maxDurableRecordSize {
				return nil, 0, errors.New("durable observation incident exceeds size limit")
			}
		}
		if readErr != nil {
			return nil, 0, fmt.Errorf("read durable observation incident: %w", readErr)
		}
		if count == 0 {
			break
		}
	}
	return buf, uint64(len(buf)), nil
}

func (r *secureRoot) deleteDurableRecord(eventID string) error {
	if r == nil || r.fd < 0 || !validID(eventID) {
		return errors.New("delete durable record: invalid event ID or root")
	}
	name := eventID + ".incident"
	return unix.Unlinkat(r.fd, name, 0)
}

func (r *secureRoot) publishDurableRecord(eventID string, raw []byte) error {
	if !validDurablePayload(eventID, raw) {
		return errors.New("durable observation record is invalid")
	}
	tempName, tempFD, err := createDurableTemp(r)
	if err != nil {
		return err
	}
	published := false
	defer func() {
		if !published {
			_ = unix.Unlinkat(r.fd, tempName, 0)
		}
	}()
	name := eventID + ".incident"
	if err := publishDurableTemp(r, tempName, tempFD, name, raw); err != nil {
		return err
	}
	published = true
	return nil
}

func validDurablePayload(eventID string, raw []byte) bool {
	return validID(eventID) && len(raw) > 0 && uint64(len(raw)) <= maxDurableRecordSize
}

func createDurableTemp(root *secureRoot) (string, int, error) {
	name, err := durableTempName()
	if err != nil {
		return "", -1, err
	}
	fd, err := unix.Openat(root.fd, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return "", -1, fmt.Errorf("create durable observation temporary record: %w", err)
	}
	return name, fd, nil
}

func publishDurableTemp(root *secureRoot, tempName string, tempFD int, name string, raw []byte) error {
	if err := writeAndSyncDurable(tempFD, raw); err != nil {
		_ = unix.Close(tempFD)
		return err
	}
	if err := requirePrivateRegularFile(tempFD, int64(maxDurableRecordSize)); err != nil {
		_ = unix.Close(tempFD)
		return err
	}
	if err := unix.Close(tempFD); err != nil {
		return fmt.Errorf("close durable observation temporary record: %w", err)
	}
	if err := verifyDurableTarget(root, name); err != nil {
		return err
	}
	if err := unix.Renameat(root.fd, tempName, root.fd, name); err != nil {
		return fmt.Errorf("publish durable observation record: %w", err)
	}
	if err := unix.Fsync(root.fd); err != nil {
		return fmt.Errorf("sync durable observation directory: %w", err)
	}
	return revalidateDurableTarget(root, name)
}

func verifyDurableTarget(root *secureRoot, name string) error {
	fd, err := unix.Openat(root.fd, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("verify durable observation target: %w", err)
	}
	checkErr := requirePrivateRegularFile(fd, int64(maxDurableRecordSize))
	_ = unix.Close(fd)
	return checkErr
}

func revalidateDurableTarget(root *secureRoot, name string) error {
	// Re-open through the same dirfd and re-validate type, owner, mode and link
	// count before acknowledging the projection.
	fd, err := unix.Openat(root.fd, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("reopen durable observation record: %w", err)
	}
	checkErr := requirePrivateRegularFile(fd, int64(maxDurableRecordSize))
	_ = unix.Close(fd)
	return checkErr
}

func requirePrivateDirectory(fd int) error {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return fmt.Errorf("inspect durable observation directory: %w", err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR || uint32(stat.Uid) != uint32(os.Geteuid()) || stat.Mode&0o7777 != 0o700 || stat.Nlink < 2 {
		return fmt.Errorf("durable observation directory must be owned by current user with mode 0700 (mode=%#o uid=%d nlink=%d)", stat.Mode&0o7777, stat.Uid, stat.Nlink)
	}
	return nil
}

func requirePrivateRegularFile(fd int, maximumSize int64) error {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return fmt.Errorf("inspect durable observation file: %w", err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || uint32(stat.Uid) != uint32(os.Geteuid()) || stat.Mode&0o7777 != 0o600 || stat.Nlink != 1 {
		return errors.New("durable observation file must be owned by current user with mode 0600 and one link")
	}
	if maximumSize > 0 && stat.Size > maximumSize {
		return errors.New("durable observation file exceeds size limit")
	}
	return nil
}

func descriptorIdentity(fd int) (uint64, uint64, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return 0, 0, fmt.Errorf("inspect durable observation identity: %w", err)
	}
	return uint64(stat.Dev), uint64(stat.Ino), nil
}

func writeAndSyncDurable(fd int, raw []byte) error {
	for len(raw) > 0 {
		written, err := unix.Write(fd, raw)
		if err != nil {
			return fmt.Errorf("write durable observation record: %w", err)
		}
		if written == 0 {
			return errors.New("write durable observation record made no progress")
		}
		raw = raw[written:]
	}
	if err := unix.Fsync(fd); err != nil {
		return fmt.Errorf("sync durable observation record: %w", err)
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
