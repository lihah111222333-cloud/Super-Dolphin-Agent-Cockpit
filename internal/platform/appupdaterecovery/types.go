package appupdaterecovery

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

const transactionIDBytes = 16

// ErrIdentityMismatch 表示调用方提供的 exact identity 与持久 journal 不一致。
var ErrIdentityMismatch = errors.New("update transaction identity mismatch")

// TransactionID 唯一标识一次 release 更新事务。
type TransactionID string

// State 表示 release 事务的持久状态。
type State string

const (
	StatePrepared        State = "prepared"
	StateBackupPending   State = "backup_pending"
	StateBackupRetained  State = "backup_retained"
	StateInstallPending  State = "install_pending"
	StateProbation       State = "probation"
	StateCommitPending   State = "commit_pending"
	StateCommitted       State = "committed"
	StateRollbackPending State = "rollback_pending"
	StateRolledBack      State = "rolled_back"
)

// Trigger 表示 release 事务的外部或完成事件。
type Trigger string

const (
	TriggerRetainBackup       Trigger = "retain_backup"
	TriggerBackupRetained     Trigger = "backup_retained"
	TriggerInstallCandidate   Trigger = "install_candidate"
	TriggerCandidateInstalled Trigger = "candidate_installed"
	TriggerHealthy            Trigger = "healthy"
	TriggerCommitCompleted    Trigger = "commit_completed"
	TriggerRollbackRequested  Trigger = "rollback_requested"
	TriggerRollbackCompleted  Trigger = "rollback_completed"
)

// TrustState 表示候选信任代际的提交状态。
type TrustState string

const (
	TrustPending    TrustState = "pending"
	TrustCommitted  TrustState = "committed"
	TrustRolledBack TrustState = "rolled_back"
)

// ReleaseIdentity 以内容摘要和签名身份精确标识 release。
type ReleaseIdentity struct {
	SHA256         string `json:"sha256"`
	SignerIdentity string `json:"signer_identity"`
}

// HelperIdentity 绑定事务使用的 updater 和 Guard 内容摘要。
type HelperIdentity struct {
	UpdaterSHA256 string `json:"updater_sha256"`
	GuardSHA256   string `json:"guard_sha256"`
}

// Identity 绑定事务、尝试和新旧 release，不允许部分匹配。
type Identity struct {
	TransactionID    TransactionID   `json:"transaction_id"`
	AttemptID        string          `json:"attempt_id"`
	OldRelease       ReleaseIdentity `json:"old_release"`
	CandidateRelease ReleaseIdentity `json:"candidate_release"`
	OldHelpers       HelperIdentity  `json:"old_helpers"`
	CandidateHelpers HelperIdentity  `json:"candidate_helpers"`
	UpdaterProcess   ProcessIdentity `json:"updater_process"`
}

// Paths 绑定同一事务的 target、backup 和 staging。
type Paths struct {
	Target      string `json:"target"`
	Backup      string `json:"backup"`
	Staging     string `json:"staging"`
	RecoveryDir string `json:"recovery_dir"`
}

// TrustGeneration 描述候选包携带且待健康确认的信任代际。
type TrustGeneration struct {
	PreviousGeneration string     `json:"previous_generation"`
	Generation         string     `json:"generation"`
	PackageSigner      string     `json:"package_signer"`
	State              TrustState `json:"state"`
}

// ProcessIdentity 绑定候选进程的 PID、内核启动令牌、可执行身份和文件摘要。
type ProcessIdentity struct {
	PID                 int    `json:"pid"`
	StartToken          string `json:"start_token"`
	ExecutableIdentity  string `json:"executable_identity"`
	ExecutableSHA256    string `json:"executable_sha256"`
	TerminationEndpoint string `json:"termination_endpoint"`
	TerminationToken    string `json:"termination_token"`
}

// RollbackRestartProcess 绑定 rollback 后由 launch token 重发现的旧版本进程。
type RollbackRestartProcess struct {
	PID                int    `json:"pid"`
	StartToken         string `json:"start_token"`
	ExecutableIdentity string `json:"executable_identity"`
	ExecutableSHA256   string `json:"executable_sha256"`
}

// RollbackRestartACK 证明 exact launch token 已对应到已验证的旧版本进程。
type RollbackRestartACK struct {
	LaunchToken    string                 `json:"launch_token"`
	Process        RollbackRestartProcess `json:"process"`
	AcknowledgedAt string                 `json:"acknowledged_at"`
}

// RollbackRestartRecord 在文件恢复前持久化重启意图，并在重发现或启动后记录 ACK。
type RollbackRestartRecord struct {
	IntentPresent bool               `json:"intent_present"`
	LaunchToken   string             `json:"launch_token"`
	IntentAt      string             `json:"intent_at"`
	ACKPresent    bool               `json:"ack_present"`
	ACK           RollbackRestartACK `json:"ack"`
}

// GuardReadyReceipt 证明 exact Guard 已加载事务并完成进程、路径与摘要绑定。
type GuardReadyReceipt struct {
	TransactionID TransactionID   `json:"transaction_id"`
	AttemptID     string          `json:"attempt_id"`
	Phase         string          `json:"phase"`
	Process       ProcessIdentity `json:"process"`
	ReadyAt       string          `json:"ready_at"`
}

// ProbationLease 是 updater 或 Guard 对 exact probation 的有界所有权。
type ProbationLease struct {
	OwnerID    string          `json:"owner_id"`
	Generation uint64          `json:"generation"`
	Process    ProcessIdentity `json:"process"`
	AcquiredAt string          `json:"acquired_at"`
	ExpiresAt  string          `json:"expires_at"`
}

// HealthyACK 将健康确认绑定到 transaction、release 和候选进程身份。
type HealthyACK struct {
	TransactionID    TransactionID   `json:"transaction_id"`
	AttemptID        string          `json:"attempt_id"`
	CandidateRelease ReleaseIdentity `json:"candidate_release"`
	Process          ProcessIdentity `json:"process"`
	AcknowledgedAt   string          `json:"acknowledged_at"`
}

// ProbationRecord 保存 lease 和 ACK；presence 位避免 null/omitempty 绕过字段守卫。
type ProbationRecord struct {
	LeasePresent bool           `json:"lease_present"`
	Lease        ProbationLease `json:"lease"`
	ACKPresent   bool           `json:"ack_present"`
	ACK          HealthyACK     `json:"ack"`
}

// CreateRequest 是建立持久 release 事务的完整输入。
type CreateRequest struct {
	Identity Identity        `json:"identity"`
	Paths    Paths           `json:"paths"`
	Trust    TrustGeneration `json:"trust"`
}

// Transaction 是 journal 验证后暴露的事务快照。
type Transaction struct {
	Identity         Identity              `json:"identity"`
	Paths            Paths                 `json:"paths"`
	State            State                 `json:"state"`
	Trust            TrustGeneration       `json:"trust"`
	Probation        ProbationRecord       `json:"probation"`
	RollbackRestart  RollbackRestartRecord `json:"rollback_restart"`
	TargetGeneration uint64                `json:"target_generation"`
	Revision         uint64                `json:"revision"`
	CreatedAt        string                `json:"created_at"`
	UpdatedAt        string                `json:"updated_at"`
}

// NewTransactionID 生成不可预测的 128-bit 事务标识。
func NewTransactionID() (TransactionID, error) {
	raw := make([]byte, transactionIDBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate update transaction id: %w", err)
	}
	return TransactionID(hex.EncodeToString(raw)), nil
}

// PathsFor 为 exact transaction 派生同目录的 backup 与 staging 路径。
func PathsFor(target string, id TransactionID) (Paths, error) {
	if err := validateTransactionID(id); err != nil {
		return Paths{}, err
	}
	if target == "" || !filepath.IsAbs(target) || filepath.Clean(target) != target {
		return Paths{}, fmt.Errorf("update target path must be absolute and clean: %q", target)
	}
	parent := filepath.Dir(target)
	base := strings.TrimSuffix(filepath.Base(target), filepath.Ext(target))
	return Paths{
		Target:      target,
		Backup:      filepath.Join(parent, fmt.Sprintf(".%s.backup-%s.app", base, id)),
		Staging:     filepath.Join(parent, fmt.Sprintf(".%s.staging-%s.app", base, id)),
		RecoveryDir: filepath.Join(parent, TransactionRootDirName, string(id), "recovery"),
	}, nil
}

// validateCreateRequest 校验创建输入的 identity、路径绑定和 pending trust 初态。
func validateCreateRequest(req CreateRequest) error {
	if err := validateIdentity(req.Identity); err != nil {
		return err
	}
	expected, err := PathsFor(req.Paths.Target, req.Identity.TransactionID)
	if err != nil {
		return err
	}
	if req.Paths != expected {
		return fmt.Errorf("update transaction paths do not match exact transaction identity")
	}
	if req.Trust.PreviousGeneration == "" || req.Trust.Generation == "" || req.Trust.PackageSigner == "" {
		return errors.New("previous and pending trust generations plus package signer are required")
	}
	if req.Trust.State != TrustPending {
		return fmt.Errorf("new trust generation state = %q, want %q", req.Trust.State, TrustPending)
	}
	return nil
}

// validateIdentity 校验事务绑定的 release、helper 和 updater 进程身份。
func validateIdentity(identity Identity) error {
	if err := validateTransactionID(identity.TransactionID); err != nil {
		return err
	}
	if strings.TrimSpace(identity.AttemptID) == "" {
		return errors.New("update attempt id is required")
	}
	if err := validateReleaseIdentity("old", identity.OldRelease); err != nil {
		return err
	}
	if err := validateReleaseIdentity("candidate", identity.CandidateRelease); err != nil {
		return err
	}
	if err := validateHelperIdentity("old", identity.OldHelpers); err != nil {
		return err
	}
	if err := validateHelperIdentity("candidate", identity.CandidateHelpers); err != nil {
		return err
	}
	return validateUpdaterProcessIdentity(identity.UpdaterProcess)
}

func validateTransactionID(id TransactionID) error {
	raw, err := hex.DecodeString(string(id))
	if err != nil || len(raw) != transactionIDBytes {
		return fmt.Errorf("invalid update transaction id %q", id)
	}
	return nil
}

func validateReleaseIdentity(name string, identity ReleaseIdentity) error {
	raw, err := hex.DecodeString(identity.SHA256)
	if err != nil || len(raw) != sha256.Size {
		return fmt.Errorf("%s release sha256 is invalid", name)
	}
	if strings.TrimSpace(identity.SignerIdentity) == "" {
		return fmt.Errorf("%s release signer identity is required", name)
	}
	return nil
}

func validateHelperIdentity(name string, identity HelperIdentity) error {
	if err := validateDigest(name+" updater", identity.UpdaterSHA256); err != nil {
		return err
	}
	return validateDigest(name+" guard", identity.GuardSHA256)
}

func validateUpdaterProcessIdentity(identity ProcessIdentity) error {
	if identity.PID <= 0 || strings.TrimSpace(identity.StartToken) == "" ||
		strings.TrimSpace(identity.ExecutableIdentity) == "" {
		return errors.New("updater process identity is incomplete")
	}
	return validateDigest("updater executable", identity.ExecutableSHA256)
}

func validateDigest(name, value string) error {
	raw, err := hex.DecodeString(value)
	if err != nil || len(raw) != sha256.Size {
		return fmt.Errorf("%s sha256 is invalid", name)
	}
	return nil
}

func digestText(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
