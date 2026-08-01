package gate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate/testtiming"
)

var (
	// ErrDurationLedgerConflict 表示 CAS 使用了已过期的 generation。
	ErrDurationLedgerConflict = errors.New("duration ledger generation conflict")
	// ErrDurationLedgerBusy 表示另一个 writer 正持有账本存储锁。
	ErrDurationLedgerBusy = errors.New("duration ledger store busy")
	// ErrDurationLedgerMetadataMissing 表示 SQLite 权威文件存在但初始化元数据尚不可见。
	ErrDurationLedgerMetadataMissing = errors.New("duration ledger SQLite metadata is missing")
)

const (
	durationLedgerSuccessSamplesPerBucket = 8
	durationLedgerFailureSamplesPerBucket = 2
	durationLedgerExecutionsPerWorkload   = 3
)

// DurationLedgerSnapshot 将已校验账本与持久化 generation 绑定。
type DurationLedgerSnapshot struct {
	Generation  uint64
	Ledger      DurationLedger
	SampleIndex *DurationSampleIndex
}

// GoTestDurationWorkloadID 返回测试级样本在账本中的稳定目标身份。
func GoTestDurationWorkloadID(parentWorkloadID string, testName string) string {
	digest := sha256.Sum256([]byte(testName))
	return parentWorkloadID + "::go-test::" + hex.EncodeToString(digest[:])
}

// GoTestDurationCommandDigest 将父命令和精确测试名绑定为独立统计桶。
func GoTestDurationCommandDigest(parentCommandDigest string, testName string) string {
	digest := sha256.New()
	_, _ = io.WriteString(digest, parentCommandDigest)
	_, _ = digest.Write([]byte{0})
	_, _ = io.WriteString(digest, testName)
	return hex.EncodeToString(digest.Sum(nil))
}

// validateDurationSampleTarget 校验测试级样本和其父 workload 身份保持一致。
func validateDurationSampleTarget(sample DurationSample) error {
	if durationSampleTargetAbsent(sample) {
		return nil
	}
	if err := validateDurationSampleTargetIdentity(sample); err != nil {
		return err
	}
	timing := GoTestTiming{Name: sample.TargetName, Status: sample.TargetStatus, DurationMS: sample.DurationMS}
	if err := testtiming.Validate(timing); err != nil {
		return fmt.Errorf("test target timing: %w", err)
	}
	if sample.Succeeded != (sample.TargetStatus != GoTestStatusFail) {
		return errors.New("test target status and succeeded flag disagree")
	}
	if !durationSampleTargetBucketMatches(sample) {
		return errors.New("test target bucket does not match parent identity")
	}
	return nil
}

func durationSampleTargetAbsent(sample DurationSample) bool {
	return sample.TargetKind == "" &&
		sample.ParentWorkloadID == "" &&
		sample.ParentCommandDigest == "" &&
		sample.TargetName == "" &&
		sample.TargetStatus == ""
}

func validateDurationSampleTargetIdentity(sample DurationSample) error {
	if sample.TargetKind != WorkloadKindGoTest {
		return errors.New("test target kind is invalid")
	}
	if strings.TrimSpace(sample.ParentWorkloadID) == "" {
		return errors.New("test target parent workload is missing")
	}
	if !isSHA256Digest(sample.ParentCommandDigest) {
		return errors.New("test target parent command digest is invalid")
	}
	return nil
}

func durationSampleTargetBucketMatches(sample DurationSample) bool {
	return sample.Bucket.WorkloadID == GoTestDurationWorkloadID(sample.ParentWorkloadID, sample.TargetName) &&
		sample.Bucket.CommandDigest == GoTestDurationCommandDigest(sample.ParentCommandDigest, sample.TargetName)
}

// AppendSamples 追加并发 CI 观测并返回完整兼容快照。
// 协调器热路径应使用 AppendSamplesFast，避免追加后物化全部历史样本。
func (store *DurationLedgerStore) AppendSamples(samples []DurationSample) (DurationLedgerSnapshot, error) {
	if store == nil {
		return DurationLedgerSnapshot{}, errors.New("duration ledger store is nil")
	}
	generation, err := store.AppendSamplesFast(samples)
	if err != nil {
		return DurationLedgerSnapshot{}, err
	}
	snapshot, err := store.Load()
	if err != nil {
		return DurationLedgerSnapshot{}, err
	}
	if snapshot.Generation < generation {
		return DurationLedgerSnapshot{}, fmt.Errorf(
			"duration ledger generation regressed after append: wrote %d, loaded %d",
			generation,
			snapshot.Generation,
		)
	}
	return snapshot, nil
}

// AppendSamplesFast 只追加样本并返回新 generation，不扫描或物化历史样本。
func (store *DurationLedgerStore) AppendSamplesFast(samples []DurationSample) (uint64, error) {
	if store == nil {
		return 0, errors.New("duration ledger store is nil")
	}
	return store.appendSQLiteSamplesFast(samples)
}

// DurationLedgerStore 在 SQLite 权威文件中持久化 duration ledger。
type DurationLedgerStore struct {
	path       string
	legacyPath string
	nowFunc    func() time.Time
}

type durationLedgerFile struct {
	Generation uint64         `json:"generation"`
	Ledger     DurationLedger `json:"ledger"`
}

// NewDurationLedgerStore 构造存储，不隐式创建文件或目录。
// 旧 .json 路径自动映射到同名 .sqlite 权威文件，并仅在首次创建时导入 JSON。
func NewDurationLedgerStore(path string) (*DurationLedgerStore, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("duration ledger store path is required")
	}
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("duration ledger store path must be absolute: %q", path)
	}
	if filepath.Base(path) == "." {
		return nil, fmt.Errorf("duration ledger store path %q must name a file", path)
	}
	cleanedPath := filepath.Clean(path)
	parent, err := filepath.EvalSymlinks(filepath.Dir(cleanedPath))
	if err != nil {
		return nil, fmt.Errorf("resolve duration ledger store parent: %w", err)
	}
	canonicalPath := filepath.Join(parent, filepath.Base(cleanedPath))
	authorityPath, legacyPath := durationLedgerAuthorityPaths(canonicalPath)
	return &DurationLedgerStore{path: authorityPath, legacyPath: legacyPath, nowFunc: time.Now}, nil
}

// AuthorityPath 返回 SQLite 权威文件路径。
func (store *DurationLedgerStore) AuthorityPath() string {
	if store == nil {
		return ""
	}
	return store.path
}

// Load 读取并校验完整账本快照；正常 CI 热路径应使用 LoadPlanning。
func (store *DurationLedgerStore) Load() (DurationLedgerSnapshot, error) {
	if store == nil {
		return DurationLedgerSnapshot{}, errors.New("duration ledger store is nil")
	}
	return store.loadSQLiteSnapshot(true, PlanningContext{})
}

// LoadMetadata 只读取 generation、版本和校准元数据，不物化样本。
func (store *DurationLedgerStore) LoadMetadata() (DurationLedgerSnapshot, error) {
	if store == nil {
		return DurationLedgerSnapshot{}, errors.New("duration ledger store is nil")
	}
	return store.loadSQLiteSnapshot(false, PlanningContext{})
}

// LoadPlanning 使用 SQLite 覆盖索引聚合指定环境，不物化全部时长样本。
func (store *DurationLedgerStore) LoadPlanning(context PlanningContext) (DurationLedgerSnapshot, error) {
	if store == nil {
		return DurationLedgerSnapshot{}, errors.New("duration ledger store is nil")
	}
	if err := validatePlanningContext(context); err != nil {
		return DurationLedgerSnapshot{}, err
	}
	return store.loadSQLiteSnapshot(false, context)
}

// CompareAndSwap 仅在 expectedGeneration 仍为当前值时原子写入账本。
// generation 零明确表示文件不存在；首次成功创建后 generation 为一。
func (store *DurationLedgerStore) CompareAndSwap(
	expectedGeneration uint64,
	ledger DurationLedger,
) (DurationLedgerSnapshot, error) {
	if store == nil {
		return DurationLedgerSnapshot{}, errors.New("duration ledger store is nil")
	}
	if err := ValidateDurationLedger(ledger); err != nil {
		return DurationLedgerSnapshot{}, fmt.Errorf("validate duration ledger: %w", err)
	}
	return store.compareAndSwapSQLite(expectedGeneration, ledger)
}

// CompareAndSwapCalibration 只更新校准元数据和 generation，不物化或重写样本。
func (store *DurationLedgerStore) CompareAndSwapCalibration(
	expectedGeneration uint64,
	calibration *DurationCalibration,
) (DurationLedgerSnapshot, error) {
	if store == nil {
		return DurationLedgerSnapshot{}, errors.New("duration ledger store is nil")
	}
	if calibration != nil {
		if err := ValidateDurationCalibration(*calibration); err != nil {
			return DurationLedgerSnapshot{}, fmt.Errorf("validate duration calibration: %w", err)
		}
	}
	return store.compareAndSwapSQLiteCalibration(expectedGeneration, calibration)
}

// ExportLegacyJSON 按需导出兼容快照；导出文件不参与后续权威读取。
func (store *DurationLedgerStore) ExportLegacyJSON(writer io.Writer) error {
	if store == nil {
		return errors.New("duration ledger store is nil")
	}
	snapshot, err := store.Load()
	if err != nil {
		return err
	}
	return writeDurationLedgerFile(writer, snapshot)
}

// writeDurationLedgerFile 校验快照并在 writer 支持时同步文件内容。
func writeDurationLedgerFile(writer io.Writer, snapshot DurationLedgerSnapshot) error {
	if snapshot.Generation == 0 {
		return errors.New("duration ledger generation must be positive")
	}
	if err := ValidateDurationLedger(snapshot.Ledger); err != nil {
		return fmt.Errorf("validate duration ledger: %w", err)
	}
	stored := durationLedgerFile{Generation: snapshot.Generation, Ledger: snapshot.Ledger}
	if err := encodeDurationLedgerFile(writer, stored); err != nil {
		return err
	}
	if syncer, ok := writer.(interface{ Sync() error }); ok {
		if err := syncer.Sync(); err != nil {
			return fmt.Errorf("sync duration ledger temporary file: %w", err)
		}
	}
	return nil
}

func encodeDurationLedgerFile(writer io.Writer, stored durationLedgerFile) error {
	encoder := json.NewEncoder(writer)
	if err := encoder.Encode(stored); err != nil {
		return fmt.Errorf("encode duration ledger file: %w", err)
	}
	return nil
}
