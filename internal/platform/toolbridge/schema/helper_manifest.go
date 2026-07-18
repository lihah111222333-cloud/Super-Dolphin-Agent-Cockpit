package schema

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/pidregistry"
)

// buildAppCommit 由桌面应用与 schema helper 的构建命令以同一 Git commit 注入。
// archguard:ignore global_vars -- 该值仅允许 go link -X 在构建期设置，运行期只读。
var buildAppCommit string

const (
	HelperBaseName                  = "mcp-schema-compiler-helper"
	HelperManifestSuffix            = ".manifest.json"
	maxHelperBytes                  = 64 << 20
	maxManifestBytes                = 32 << 10
	filesystemSnapshotVersion       = 1
	filesystemSnapshotPrefix        = "reasonix-schema-helper."
	filesystemSnapshotMarker        = ".reasonix-schema-owner.json"
	filesystemSnapshotStagingOwner  = ".owner-"
	filesystemSnapshotStagingSuffix = ".staging"
	filesystemSnapshotTokenBytes    = 16
)

type filesystemSnapshotIdentity struct {
	Version         int    `json:"version"`
	Directory       string `json:"directory"`
	Token           string `json:"token"`
	HelperGOOS      string `json:"helper_goos"`
	OwnerPID        int    `json:"owner_pid"`
	OwnerStartToken string `json:"owner_start_token"`
	OwnerExecutable string `json:"owner_executable"`
}

// HelperManifest binds the package-owned helper to the application build identity.
type HelperManifest struct {
	Protocol         string `json:"protocol"`
	Helper           string `json:"helper"`
	SHA256           string `json:"sha256"`
	ExecutablePolicy string `json:"executable_policy"`
	AppCommit        string `json:"app_commit"`
	GoVersion        string `json:"go_version"`
	GOOS             string `json:"goos"`
	GOARCH           string `json:"goarch"`
}

// HelperIdentity is the application identity that a helper package must match.
type HelperIdentity struct {
	AppCommit string
	GoVersion string
	GOOS      string
	GOARCH    string
}

// HelperFileName 返回目标平台唯一允许的 helper 文件名。
func HelperFileName(goos string) string {
	if goos == "windows" {
		return HelperBaseName + ".exe"
	}
	return HelperBaseName
}

// HelperManifestFileName 返回与目标 helper 同目录绑定的 manifest 文件名。
func HelperManifestFileName(goos string) string {
	return HelperFileName(goos) + HelperManifestSuffix
}

func executablePolicy(goos string) string {
	if goos == "windows" {
		return "windows_pe"
	}
	return "owner_execute"
}

// WriteHelperManifest 为已构建 helper 写入完整且精确的 package identity。
func WriteHelperManifest(helperPath, manifestPath string, identity HelperIdentity) error {
	image, err := readRegularNoSymlink(helperPath, maxHelperBytes)
	if err != nil {
		return fmt.Errorf("read helper: %w", err)
	}
	if err := validateIdentity(identity); err != nil {
		return err
	}
	if err := verifyExecutable(helperPath, identity.GOOS, image); err != nil {
		return err
	}
	digest := sha256.Sum256(image)
	manifest := HelperManifest{
		Protocol: ProtocolID, Helper: filepath.Base(helperPath), SHA256: hex.EncodeToString(digest[:]),
		ExecutablePolicy: executablePolicy(identity.GOOS), AppCommit: identity.AppCommit,
		GoVersion: identity.GoVersion, GOOS: identity.GOOS, GOARCH: identity.GOARCH,
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal helper manifest: %w", err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(manifestPath, encoded, 0o644); err != nil {
		return fmt.Errorf("write helper manifest: %w", err)
	}
	return nil
}

func verifyHelperPackage(helperPath, manifestPath string, expected HelperIdentity) ([]byte, error) {
	manifest, err := readHelperManifest(manifestPath)
	if err != nil {
		return nil, err
	}
	if err := validateHelperManifest(manifest, helperPath, manifestPath, expected); err != nil {
		return nil, StableInitializationError(err)
	}
	return readVerifiedHelperImage(helperPath, manifest, expected.GOOS)
}

func readHelperManifest(manifestPath string) (HelperManifest, error) {
	manifestBytes, err := readRegularNoSymlink(manifestPath, maxManifestBytes)
	if err != nil {
		return HelperManifest{}, classifyFilesystemReadInitializationError(
			fmt.Errorf("read package-owned helper manifest: %w", err),
		)
	}
	decoder := json.NewDecoder(bytes.NewReader(manifestBytes))
	decoder.DisallowUnknownFields()
	var manifest HelperManifest
	if err := decoder.Decode(&manifest); err != nil {
		return HelperManifest{}, StableInitializationError(
			fmt.Errorf("decode package-owned helper manifest: %w", err),
		)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return HelperManifest{}, StableInitializationError(
			fmt.Errorf("decode package-owned helper manifest: %w", err),
		)
	}
	return manifest, nil
}

// validateHelperManifest 校验 manifest identity 与 helper 的同目录 canonical layout。
func validateHelperManifest(manifest HelperManifest, helperPath, manifestPath string, expected HelperIdentity) error {
	if err := validateIdentity(expected); err != nil {
		return fmt.Errorf("expected helper identity: %w", err)
	}
	wantName := HelperFileName(expected.GOOS)
	actual := HelperIdentity{AppCommit: manifest.AppCommit, GoVersion: manifest.GoVersion, GOOS: manifest.GOOS, GOARCH: manifest.GOARCH}
	if manifest.Protocol != ProtocolID || manifest.Helper != wantName || manifest.ExecutablePolicy != executablePolicy(expected.GOOS) || actual != expected {
		return fmt.Errorf("package-owned helper manifest identity mismatch")
	}
	if filepath.Base(helperPath) != manifest.Helper || filepath.Dir(helperPath) != filepath.Dir(manifestPath) {
		return fmt.Errorf("package-owned helper and manifest layout mismatch")
	}
	return nil
}

func readVerifiedHelperImage(helperPath string, manifest HelperManifest, goos string) ([]byte, error) {
	image, err := readRegularNoSymlink(helperPath, maxHelperBytes)
	if err != nil {
		return nil, classifyFilesystemReadInitializationError(
			fmt.Errorf("read package-owned helper: %w", err),
		)
	}
	if err := verifyExecutable(helperPath, goos, image); err != nil {
		return nil, StableInitializationError(err)
	}
	digest := sha256.Sum256(image)
	if !strings.EqualFold(manifest.SHA256, hex.EncodeToString(digest[:])) {
		return nil, StableInitializationError(fmt.Errorf("package-owned helper SHA-256 mismatch"))
	}
	return image, nil
}

func classifyFilesystemReadInitializationError(err error) error {
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
		return StableInitializationError(err)
	}
	return TransientInitializationError(err)
}

// VerifyHelperPackage 校验 package identity，并在 helper bytes 已固定后返回。
func VerifyHelperPackage(helperPath, manifestPath string, expected HelperIdentity) error {
	_, err := verifyHelperPackage(helperPath, manifestPath, expected)
	return err
}

func validateIdentity(identity HelperIdentity) error {
	if strings.TrimSpace(identity.AppCommit) == "" || strings.TrimSpace(identity.GoVersion) == "" ||
		strings.TrimSpace(identity.GOOS) == "" || strings.TrimSpace(identity.GOARCH) == "" {
		return errors.New("helper identity fields are required")
	}
	return nil
}

func readRegularNoSymlink(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s must be a regular non-symlink file", path)
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("%s exceeds %d bytes", path, limit)
	}
	return os.ReadFile(path)
}

// verifyExecutable 按目标平台执行 owner-execute 或 Windows PE 策略校验。
func verifyExecutable(path, goos string, image []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if goos == "windows" {
		if filepath.Ext(path) != ".exe" || len(image) < 2 || image[0] != 'M' || image[1] != 'Z' {
			return fmt.Errorf("package-owned helper violates windows executable policy")
		}
		return nil
	}
	if info.Mode().Perm()&0o100 == 0 {
		return fmt.Errorf("package-owned helper violates owner-execute policy")
	}
	return nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func newFilesystemSnapshotIdentity(
	helperGOOS string,
	owner pidregistry.StableProcessIdentity,
) (filesystemSnapshotIdentity, error) {
	tokenBytes := make([]byte, filesystemSnapshotTokenBytes)
	if _, err := rand.Read(tokenBytes); err != nil {
		return filesystemSnapshotIdentity{}, TransientInitializationError(
			fmt.Errorf("generate schema snapshot token: %w", err),
		)
	}
	token := hex.EncodeToString(tokenBytes)
	identity := filesystemSnapshotIdentity{
		Version:         filesystemSnapshotVersion,
		Directory:       filepath.Join(filepath.Clean(os.TempDir()), filesystemSnapshotPrefix+token),
		Token:           token,
		HelperGOOS:      helperGOOS,
		OwnerPID:        owner.PID,
		OwnerStartToken: owner.ProcessStartToken,
		OwnerExecutable: owner.ExecutableIdentity,
	}
	if err := validateFilesystemSnapshotIdentity(identity); err != nil {
		return filesystemSnapshotIdentity{}, StableInitializationError(err)
	}
	return identity, nil
}

// validateFilesystemSnapshotIdentity 绑定版本、路径、token、helper 与 owner。
func validateFilesystemSnapshotIdentity(identity filesystemSnapshotIdentity) error {
	if identity.Version != filesystemSnapshotVersion {
		return errors.New("schema snapshot identity version mismatch")
	}
	if err := validateFilesystemSnapshotToken(identity.Token); err != nil {
		return err
	}
	if err := validateFilesystemSnapshotPath(identity); err != nil {
		return err
	}
	return validateFilesystemSnapshotOwner(identity)
}

func validateFilesystemSnapshotToken(token string) error {
	if len(token) != filesystemSnapshotTokenBytes*2 || strings.ToLower(token) != token {
		return errors.New("schema snapshot token is invalid")
	}
	if _, err := hex.DecodeString(token); err != nil {
		return fmt.Errorf("decode schema snapshot token: %w", err)
	}
	return nil
}

// validateFilesystemSnapshotPath 只接受临时根目录下由 token 精确派生的路径。
func validateFilesystemSnapshotPath(identity filesystemSnapshotIdentity) error {
	root := filepath.Clean(os.TempDir())
	expected := filepath.Join(root, filesystemSnapshotPrefix+identity.Token)
	if !filepath.IsAbs(root) || identity.Directory != expected || filepath.Clean(identity.Directory) != identity.Directory {
		return errors.New("schema snapshot directory is not the exact temporary-root child")
	}
	if strings.TrimSpace(identity.HelperGOOS) == "" ||
		filepath.Base(HelperFileName(identity.HelperGOOS)) != HelperFileName(identity.HelperGOOS) {
		return errors.New("schema snapshot helper GOOS is invalid")
	}
	return nil
}

func validateFilesystemSnapshotOwner(identity filesystemSnapshotIdentity) error {
	if identity.OwnerPID <= 0 || strings.TrimSpace(identity.OwnerStartToken) == "" ||
		strings.TrimSpace(identity.OwnerExecutable) == "" {
		return errors.New("schema snapshot owner identity is incomplete")
	}
	return nil
}

func writeExecutableSnapshot(image []byte, identity filesystemSnapshotIdentity) (string, error) {
	if err := validateFilesystemSnapshotIdentity(identity); err != nil {
		return "", err
	}
	directory, err := createFilesystemSnapshotStagingDirectory(identity)
	if err != nil {
		return "", err
	}
	return publishExecutableSnapshot(image, identity, directory)
}

// createFilesystemSnapshotStagingDirectory 建立快照写入目录，发布前由调用方独占。
func createFilesystemSnapshotStagingDirectory(identity filesystemSnapshotIdentity) (string, error) {
	if err := validateFilesystemSnapshotIdentity(identity); err != nil {
		return "", err
	}
	directory := filesystemSnapshotStagingDirectory(identity)
	if err := os.Mkdir(directory, 0o700); err != nil {
		return "", err
	}
	return directory, nil
}

// filesystemSnapshotStagingDirectory 在目录名中绑定 owner，使 empty staging 也可安全判活。
func filesystemSnapshotStagingDirectory(identity filesystemSnapshotIdentity) string {
	digest := filesystemSnapshotOwnerDigest(identity)
	return identity.Directory + filesystemSnapshotStagingOwner +
		strconv.Itoa(identity.OwnerPID) + "-" + digest + filesystemSnapshotStagingSuffix
}

func filesystemSnapshotOwnerDigest(identity filesystemSnapshotIdentity) string {
	owner := fmt.Sprintf("%d\n%d:%s%d:%s", identity.OwnerPID, len(identity.OwnerStartToken), identity.OwnerStartToken, len(identity.OwnerExecutable), identity.OwnerExecutable)
	digest := sha256.Sum256([]byte(owner))
	return hex.EncodeToString(digest[:])
}

// publishExecutableSnapshot 完整写入 staging 后原子发布最终快照目录。
func publishExecutableSnapshot(
	image []byte,
	identity filesystemSnapshotIdentity,
	directory string,
) (string, error) {
	if err := validateFilesystemSnapshotIdentity(identity); err != nil {
		return "", errors.Join(err, removeStagedFilesystemSnapshot(identity, directory))
	}
	expectedDirectory := filesystemSnapshotStagingDirectory(identity)
	if directory != expectedDirectory {
		return "", errors.New("schema snapshot staging directory is invalid")
	}
	if err := writeFilesystemSnapshotMarker(directory, identity); err != nil {
		return "", errors.Join(err, removeStagedFilesystemSnapshot(identity, directory))
	}
	path := filepath.Join(directory, HelperFileName(identity.HelperGOOS))
	if err := writeExclusiveRegularFile(path, image, 0o700); err != nil {
		return "", errors.Join(err, removeStagedFilesystemSnapshot(identity, directory))
	}
	if _, err := os.Lstat(identity.Directory); err == nil {
		return "", errors.Join(
			errors.New("schema snapshot final directory already exists"),
			removeStagedFilesystemSnapshot(identity, directory),
		)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", errors.Join(err, removeStagedFilesystemSnapshot(identity, directory))
	}
	if err := os.Rename(directory, identity.Directory); err != nil {
		return "", errors.Join(err, removeStagedFilesystemSnapshot(identity, directory))
	}
	return filepath.Join(identity.Directory, filepath.Base(path)), nil
}

// removeStagedFilesystemSnapshot 只清理本次 identity 的严格 staging 内容。
func removeStagedFilesystemSnapshot(identity filesystemSnapshotIdentity, directory string) error {
	if err := validateFilesystemSnapshotIdentity(identity); err != nil {
		return err
	}
	expectedDirectory := filesystemSnapshotStagingDirectory(identity)
	if directory != expectedDirectory {
		return errors.New("schema snapshot staging directory is invalid")
	}
	entries, exists, err := ownedFilesystemSnapshotEntries(directory)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if len(entries) == 0 {
		return os.Remove(directory)
	}
	helperName := HelperFileName(identity.HelperGOOS)
	for _, entry := range entries {
		if err := validateFilesystemSnapshotEntry(directory, helperName, entry); err != nil {
			return err
		}
	}
	return removeFilesystemSnapshotFiles(directory, helperName)
}

func writeFilesystemSnapshotMarker(directory string, identity filesystemSnapshotIdentity) error {
	encoded, err := json.Marshal(identity)
	if err != nil {
		return fmt.Errorf("encode schema snapshot owner marker: %w", err)
	}
	encoded = append(encoded, '\n')
	return writeExclusiveRegularFile(filepath.Join(directory, filesystemSnapshotMarker), encoded, 0o600)
}

func writeExclusiveRegularFile(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return errors.Join(err, file.Close())
	}
	return file.Close()
}

// removeOwnedFilesystemSnapshot 清理 identity 精确派生的 staging 与最终目录。
func removeOwnedFilesystemSnapshot(identity filesystemSnapshotIdentity) error {
	if err := validateFilesystemSnapshotIdentity(identity); err != nil {
		return err
	}
	stagingDirectory := filesystemSnapshotStagingDirectory(identity)
	stagingErr := removeStagedFilesystemSnapshot(
		identity,
		stagingDirectory,
	)
	entries, exists, err := ownedFilesystemSnapshotEntries(identity.Directory)
	if err != nil {
		return errors.Join(stagingErr, err)
	}
	if !exists {
		return stagingErr
	}
	if len(entries) == 0 {
		return errors.Join(stagingErr, os.Remove(identity.Directory))
	}
	if err := verifyFilesystemSnapshotMarker(identity); err != nil {
		return errors.Join(stagingErr, err)
	}
	helperName := HelperFileName(identity.HelperGOOS)
	for _, entry := range entries {
		if err := validateFilesystemSnapshotEntry(identity.Directory, helperName, entry); err != nil {
			return errors.Join(stagingErr, err)
		}
	}
	return errors.Join(
		stagingErr,
		removeFilesystemSnapshotFiles(identity.Directory, helperName),
	)
}

func ownedFilesystemSnapshotEntries(directory string) ([]os.DirEntry, bool, error) {
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, false, errors.New("schema snapshot path is not a non-symlink directory")
	}
	entries, err := os.ReadDir(directory)
	return entries, true, err
}

func removeFilesystemSnapshotFiles(directory, helperName string) error {
	if err := removeSnapshotEntryIfPresent(directory, helperName); err != nil {
		return err
	}
	if err := removeSnapshotEntryIfPresent(directory, filesystemSnapshotMarker); err != nil {
		return err
	}
	return os.Remove(directory)
}

// validateFilesystemSnapshotEntry 严格拒绝未知、symlink 或非 regular 条目。
func validateFilesystemSnapshotEntry(directory, helperName string, entry os.DirEntry) error {
	if entry.Name() != filesystemSnapshotMarker && entry.Name() != helperName {
		return fmt.Errorf("schema snapshot contains unexpected entry %q", entry.Name())
	}
	info, err := os.Lstat(filepath.Join(directory, entry.Name()))
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("schema snapshot entry %q is not a regular non-symlink file", entry.Name())
	}
	return nil
}

func verifyFilesystemSnapshotMarker(identity filesystemSnapshotIdentity) error {
	raw, err := readRegularNoSymlink(filepath.Join(identity.Directory, filesystemSnapshotMarker), maxManifestBytes)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var recorded filesystemSnapshotIdentity
	if err := decoder.Decode(&recorded); err != nil {
		return fmt.Errorf("decode schema snapshot owner marker: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return fmt.Errorf("decode schema snapshot owner marker: %w", err)
	}
	if recorded != identity {
		return errors.New("schema snapshot owner identity mismatch")
	}
	return nil
}

func removeSnapshotEntryIfPresent(directory, name string) error {
	err := os.Remove(filepath.Join(directory, name))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// sweepStaleFilesystemSnapshots 清扫 owner 已失效的严格新格式目录。
func sweepStaleFilesystemSnapshots() error {
	root := filepath.Clean(os.TempDir())
	if !filepath.IsAbs(root) {
		return errors.New("schema snapshot temporary root must be absolute")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := sweepFilesystemSnapshotCandidate(root, entry.Name()); err != nil {
			return err
		}
	}
	return nil
}

// sweepFilesystemSnapshotCandidate 分派严格 final 与 staging 候选。
func sweepFilesystemSnapshotCandidate(root, name string) error {
	token, ok := filesystemSnapshotTokenFromName(name)
	if ok {
		return sweepPublishedFilesystemSnapshotCandidate(root, name, token)
	}
	token, ownerPID, ownerDigest, matched, err := parseFilesystemSnapshotStagingName(name)
	if err != nil {
		return err
	}
	if !matched {
		return nil
	}
	return sweepFilesystemSnapshotStagingCandidate(root, name, token, ownerPID, ownerDigest)
}

// sweepPublishedFilesystemSnapshotCandidate 清理 owner 已失效的 final 快照。
func sweepPublishedFilesystemSnapshotCandidate(root, name, token string) error {
	directory := filepath.Join(root, name)
	identity, empty, err := readSweepSnapshotIdentity(directory, token)
	if err != nil {
		return err
	}
	if empty {
		return os.Remove(directory)
	}
	active, err := filesystemSnapshotOwnerActive(identity)
	if err != nil || active {
		return err
	}
	return removeOwnedFilesystemSnapshot(identity)
}

// sweepFilesystemSnapshotStagingCandidate 清理 owner 已失效的严格 staging 状态。
func sweepFilesystemSnapshotStagingCandidate(root, name, token string, ownerPID int, ownerDigest string) error {
	directory := filepath.Join(root, name)
	identity, marked, err := readSweepStagingIdentity(directory, token)
	if err != nil {
		return err
	}
	active, err := filesystemSnapshotStagingOwnerActive(ownerPID, ownerDigest)
	if err != nil || active {
		return err
	}
	if !marked {
		return os.Remove(directory)
	}
	return removeStagedFilesystemSnapshot(identity, directory)
}

func filesystemSnapshotTokenFromName(name string) (string, bool) {
	if !strings.HasPrefix(name, filesystemSnapshotPrefix) {
		return "", false
	}
	token := strings.TrimPrefix(name, filesystemSnapshotPrefix)
	if len(token) != filesystemSnapshotTokenBytes*2 || strings.ToLower(token) != token {
		return "", false
	}
	if _, err := hex.DecodeString(token); err != nil {
		return "", false
	}
	return token, true
}

// parseFilesystemSnapshotStagingName 严格解析 token 与可判活 owner 绑定。
func parseFilesystemSnapshotStagingName(name string) (string, int, string, bool, error) {
	if !strings.HasPrefix(name, filesystemSnapshotPrefix) ||
		!strings.HasSuffix(name, filesystemSnapshotStagingSuffix) {
		return "", 0, "", false, nil
	}
	stem := strings.TrimSuffix(name, filesystemSnapshotStagingSuffix)
	finalName, owner, ok := strings.Cut(stem, filesystemSnapshotStagingOwner)
	if !ok {
		return "", 0, "", true, errors.New("schema snapshot staging owner binding is missing")
	}
	token, ok := filesystemSnapshotTokenFromName(finalName)
	if !ok {
		return "", 0, "", true, errors.New("schema snapshot staging token is invalid")
	}
	pidText, digest, ok := strings.Cut(owner, "-")
	ownerPID, err := strconv.Atoi(pidText)
	if !ok || err != nil || ownerPID <= 0 {
		return "", 0, "", true, errors.New("schema snapshot staging owner PID is invalid")
	}
	if !validFilesystemSnapshotOwnerDigest(digest) {
		return "", 0, "", true, errors.New("schema snapshot staging owner digest is invalid")
	}
	return token, ownerPID, digest, true, nil
}

func validFilesystemSnapshotOwnerDigest(digest string) bool {
	if len(digest) != sha256.Size*2 || strings.ToLower(digest) != digest {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}

// readSweepSnapshotIdentity 读取并复验启动清扫候选的 marker identity。
func readSweepSnapshotIdentity(directory, token string) (filesystemSnapshotIdentity, bool, error) {
	info, err := os.Lstat(directory)
	if err != nil {
		return filesystemSnapshotIdentity{}, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return filesystemSnapshotIdentity{}, false, errors.New(
			"schema snapshot sweep candidate is not a non-symlink directory",
		)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return filesystemSnapshotIdentity{}, false, err
	}
	if len(entries) == 0 {
		return filesystemSnapshotIdentity{}, true, nil
	}
	identity, err := readFilesystemSnapshotIdentity(directory)
	if err != nil {
		return filesystemSnapshotIdentity{}, false, err
	}
	if identity.Directory != directory || identity.Token != token {
		return filesystemSnapshotIdentity{}, false, errors.New("schema snapshot sweep identity mismatch")
	}
	if err := validateFilesystemSnapshotIdentity(identity); err != nil {
		return filesystemSnapshotIdentity{}, false, err
	}
	return identity, false, nil
}

// readSweepStagingIdentity 读取 marker 并复验其与 staging 名称的 owner 绑定。
func readSweepStagingIdentity(directory, token string) (filesystemSnapshotIdentity, bool, error) {
	entries, _, err := ownedFilesystemSnapshotEntries(directory)
	if err != nil {
		return filesystemSnapshotIdentity{}, false, err
	}
	if len(entries) == 0 {
		return filesystemSnapshotIdentity{}, false, nil
	}
	if len(entries) > 2 {
		return filesystemSnapshotIdentity{}, false, errors.New(
			"schema snapshot staging contains an unexpected number of entries",
		)
	}
	identity, err := readFilesystemSnapshotIdentity(directory)
	if err != nil {
		return filesystemSnapshotIdentity{}, false, err
	}
	if err := validateFilesystemSnapshotIdentity(identity); err != nil {
		return filesystemSnapshotIdentity{}, false, err
	}
	expectedDirectory := filesystemSnapshotStagingDirectory(identity)
	if [2]string{identity.Token, directory} != [2]string{token, expectedDirectory} {
		return filesystemSnapshotIdentity{}, false, errors.New(
			"schema snapshot staging identity mismatch",
		)
	}
	helperName := ""
	if len(entries) == 2 {
		helperName = HelperFileName(identity.HelperGOOS)
	}
	for _, entry := range entries {
		if err := validateFilesystemSnapshotEntry(directory, helperName, entry); err != nil {
			return filesystemSnapshotIdentity{}, false, err
		}
	}
	return identity, true, nil
}

func readFilesystemSnapshotIdentity(directory string) (filesystemSnapshotIdentity, error) {
	raw, err := readRegularNoSymlink(filepath.Join(directory, filesystemSnapshotMarker), maxManifestBytes)
	if err != nil {
		return filesystemSnapshotIdentity{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var identity filesystemSnapshotIdentity
	if err := decoder.Decode(&identity); err != nil {
		return filesystemSnapshotIdentity{}, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return filesystemSnapshotIdentity{}, err
	}
	return identity, nil
}

func filesystemSnapshotStagingOwnerActive(ownerPID int, ownerDigest string) (bool, error) {
	current, err := pidregistry.CaptureStableProcessIdentity(ownerPID)
	if errors.Is(err, pidregistry.ErrStableProcessNotFound) ||
		errors.Is(err, pidregistry.ErrStableProcessIdentityMismatch) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	digest := filesystemSnapshotOwnerDigest(filesystemSnapshotIdentity{
		OwnerPID: current.PID, OwnerStartToken: current.ProcessStartToken, OwnerExecutable: current.ExecutableIdentity,
	})
	return digest == ownerDigest, nil
}

func filesystemSnapshotOwnerActive(identity filesystemSnapshotIdentity) (bool, error) {
	return filesystemSnapshotStagingOwnerActive(
		identity.OwnerPID,
		filesystemSnapshotOwnerDigest(identity),
	)
}

func currentRuntimeIdentity(appCommit, goVersion string) HelperIdentity {
	return HelperIdentity{AppCommit: appCommit, GoVersion: goVersion, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
}

// CurrentBuildIdentity 返回当前 Go binary 内嵌的 VCS 与 toolchain identity。
func CurrentBuildIdentity() (HelperIdentity, error) {
	info, ok := debug.ReadBuildInfo()
	if !ok || strings.TrimSpace(info.GoVersion) == "" {
		return HelperIdentity{}, errors.New("running Go build identity is unavailable")
	}
	commit := strings.TrimSpace(buildAppCommit)
	if commit == "" {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				commit = strings.TrimSpace(setting.Value)
				break
			}
		}
	}
	if commit == "" {
		return HelperIdentity{}, errors.New("running application commit identity is unavailable")
	}
	return currentRuntimeIdentity(commit, info.GoVersion), nil
}
