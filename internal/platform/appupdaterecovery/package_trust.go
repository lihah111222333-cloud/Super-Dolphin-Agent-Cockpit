package appupdaterecovery

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"
)

const (
	PackageTrustFilename      = "update-trust.json"
	PackageTrustSchemaVersion = 1
	TransactionRootDirName    = ".super-dolphin-update-transactions"
	GuardReceiptPhaseArmed    = "armed"

	UpdateSourceGitHub   = "github"
	UpdateSourceManifest = "manifest"

	PackageSignerPolicyDisabled   = "disabled"
	PackageSignerPolicyExact      = "exact"
	PackageSignerPolicySameSigner = "same-signer"
)

// UpdateSource 是 package owner 冻结的更新源，不读取运行时 source override。
type UpdateSource struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// PackageTrust 是包内 update trust 的唯一生产真值。
type PackageTrust struct {
	SchemaVersion     int          `json:"schema_version"`
	Enabled           bool         `json:"enabled"`
	Production        bool         `json:"production"`
	Platform          string       `json:"platform"`
	Source            UpdateSource `json:"source"`
	ManifestPublicKey string       `json:"manifest_public_key"`
	Channel           string       `json:"channel"`
	SignerPolicy      string       `json:"signer_policy"`
	SignerIdentity    string       `json:"signer_identity"`
	UpdaterSHA256     string       `json:"updater_sha256"`
	GuardSHA256       string       `json:"guard_sha256"`
}

// UpdateCapability 描述一个受审平台是否开放 check/install/publish。
type UpdateCapability struct {
	Platform string
	Check    bool
	Install  bool
	Publish  bool
	Reason   string
}

var packageTrustOverrideNames = map[string]struct{}{
	"SUPER_DOLPHIN_UPDATE_ENABLED":            {},
	"SUPER_DOLPHIN_UPDATE_MANIFEST_URL":       {},
	"SUPER_DOLPHIN_UPDATE_GITHUB_REPO":        {},
	"SUPER_DOLPHIN_UPDATE_PUBLIC_KEY":         {},
	"SUPER_DOLPHIN_UPDATE_CHANNEL":            {},
	"SUPER_DOLPHIN_UPDATE_STAGE_DIR":          {},
	"SUPER_DOLPHIN_UPDATE_HELPER_PATH":        {},
	"SUPER_DOLPHIN_UPDATE_TARGET_APP_PATH":    {},
	"SUPER_DOLPHIN_UPDATE_PLATFORM":           {},
	"SUPER_DOLPHIN_UPDATE_VERSION":            {},
	"SUPER_DOLPHIN_UPDATE_ALLOW_UNSIGNED":     {},
	"SUPER_DOLPHIN_UPDATE_WINDOWS_PUBLISHER":  {},
	"SUPER_DOLPHIN_UPDATE_WINDOWS_THUMBPRINT": {},
}

// PackageTrustOverrideNames 返回生产 trust 拒绝的完整、稳定排序运行时变量名。
func PackageTrustOverrideNames() []string {
	names := make([]string, 0, len(packageTrustOverrideNames))
	for name := range packageTrustOverrideNames {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// UpdateCapabilityFor 返回完整六目标矩阵；未知目标直接报错。
func UpdateCapabilityFor(platform string) (UpdateCapability, error) {
	switch strings.TrimSpace(platform) {
	case "darwin-arm64":
		return UpdateCapability{Platform: platform, Check: true, Install: true, Publish: true, Reason: "signed macOS transaction and Guard E2E"}, nil
	case "darwin-amd64":
		return disabledUpdateCapability(platform, "packaging has no independently verified amd64 artifact E2E"), nil
	case "linux-amd64", "linux-arm64":
		return disabledUpdateCapability(platform, "Linux package has no transactional desktop installer"), nil
	case "windows-amd64", "windows-arm64":
		return disabledUpdateCapability(platform, "Windows package has no release transaction and Guard recovery path"), nil
	default:
		return UpdateCapability{}, fmt.Errorf("unknown update capability platform %q", platform)
	}
}

func disabledUpdateCapability(platform, reason string) UpdateCapability {
	return UpdateCapability{Platform: platform, Reason: reason}
}

// LoadPackageTrust 严格读取 package-owned trust，并返回 exact bytes digest。
func LoadPackageTrust(resources, platform string) (PackageTrust, string, error) {
	if err := RequireCanonicalExistingPath(resources); err != nil {
		return PackageTrust{}, "", fmt.Errorf("reject package trust resources alias: %w", err)
	}
	path := filepath.Join(resources, PackageTrustFilename)
	if err := RequireCanonicalExistingPath(path); err != nil {
		return PackageTrust{}, "", fmt.Errorf("reject package trust file alias: %w", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return PackageTrust{}, "", fmt.Errorf("read package-owned update trust: %w", err)
	}
	if err := validateRequiredJSONFieldsForChain(raw, reflect.TypeFor[PackageTrust](), "package_update_trust"); err != nil {
		return PackageTrust{}, "", fmt.Errorf("validate package-owned update trust fields: %w", err)
	}
	var trust PackageTrust
	if err := decodeStrict(raw, &trust); err != nil {
		return PackageTrust{}, "", fmt.Errorf("decode package-owned update trust: %w", err)
	}
	if err := validatePackageTrust(trust, platform); err != nil {
		return PackageTrust{}, "", err
	}
	sum := sha256.Sum256(raw)
	return trust, hex.EncodeToString(sum[:]), nil
}

// EncodePackageTrust 生成脚本和测试共享的 canonical package trust bytes。
func EncodePackageTrust(trust PackageTrust) ([]byte, error) {
	if err := validatePackageTrust(trust, trust.Platform); err != nil {
		return nil, err
	}
	raw, err := json.MarshalIndent(trust, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode package-owned update trust: %w", err)
	}
	return append(raw, '\n'), nil
}

// CanonicalExistingPath 返回已存在文件经绝对化和符号链接解析后的 exact 路径。
func CanonicalExistingPath(path string) (string, error) {
	return canonicalExistingPath(path)
}

// CanonicalExistingPathContext 在可终止 helper 中解析现存路径，deadline 会 kill 并同步回收 helper。
func CanonicalExistingPathContext(ctx context.Context, path string) (string, error) {
	return canonicalExistingPathInHelper(ctx, path)
}

func canonicalExistingPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve absolute executable path: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve canonical executable path: %w", err)
	}
	return filepath.Clean(canonical), nil
}

// RequireCanonicalExistingPath 拒绝相对、非 clean、symlink 或 alias 的现存路径。
func RequireCanonicalExistingPath(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("path is not clean absolute: %q", path)
	}
	canonical, err := CanonicalExistingPath(path)
	if err != nil {
		return err
	}
	if canonical != path {
		return fmt.Errorf("path is an alias: got %q canonical %q", path, canonical)
	}
	return nil
}

// RequireCanonicalPath 仅在最深现存祖先 canonical 时允许末端路径尚不存在。
func RequireCanonicalPath(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("path is not clean absolute: %q", path)
	}
	current := path
	for {
		if _, err := os.Lstat(current); err == nil {
			return RequireCanonicalExistingPath(current)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect path %q: %w", current, err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return fmt.Errorf("path has no existing canonical ancestor: %q", path)
		}
		current = parent
	}
}

// BuildGuardReadyReceipt 生成绑定 exact transaction 与 Guard 进程的就绪凭据。
func BuildGuardReadyReceipt(transaction Transaction, process ProcessIdentity, readyAt time.Time) GuardReadyReceipt {
	return GuardReadyReceipt{
		TransactionID: transaction.Identity.TransactionID,
		AttemptID:     transaction.Identity.AttemptID,
		Phase:         GuardReceiptPhaseArmed,
		Process:       process,
		ReadyAt:       readyAt.UTC().Format(time.RFC3339Nano),
	}
}

// EncodeGuardReadyReceipt 编码经完整校验的单条 Guard 就绪凭据。
func EncodeGuardReadyReceipt(receipt GuardReadyReceipt) ([]byte, error) {
	if err := validateGuardReadyReceipt(receipt); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return nil, fmt.Errorf("encode Guard readiness receipt: %w", err)
	}
	return append(raw, '\n'), nil
}

// DecodeGuardReadyReceipt 严格解码并动态守卫全部 Guard 就绪字段。
func DecodeGuardReadyReceipt(raw []byte) (GuardReadyReceipt, error) {
	if err := validateRequiredJSONFieldsForChain(raw, reflect.TypeFor[GuardReadyReceipt](), "guard_ready_receipt"); err != nil {
		return GuardReadyReceipt{}, fmt.Errorf("validate Guard readiness receipt fields: %w", err)
	}
	var receipt GuardReadyReceipt
	if err := decodeStrict(raw, &receipt); err != nil {
		return GuardReadyReceipt{}, fmt.Errorf("decode Guard readiness receipt: %w", err)
	}
	if err := validateGuardReadyReceipt(receipt); err != nil {
		return GuardReadyReceipt{}, err
	}
	return receipt, nil
}

// ValidateGuardReadyReceipt 校验凭据与 exact transaction 和预期子进程完全一致。
func ValidateGuardReadyReceipt(receipt GuardReadyReceipt, transaction Transaction, expected ProcessIdentity) error {
	if err := validateGuardReadyReceipt(receipt); err != nil {
		return err
	}
	if receipt.TransactionID != transaction.Identity.TransactionID || receipt.AttemptID != transaction.Identity.AttemptID {
		return errors.New("Guard readiness receipt transaction identity mismatch")
	}
	if receipt.Process != expected {
		return errors.New("Guard readiness receipt process identity mismatch")
	}
	return nil
}

// validateGuardReadyReceipt 校验 receipt 字段、armed phase、时间戳与进程身份。
func validateGuardReadyReceipt(receipt GuardReadyReceipt) error {
	if err := validateTransactionID(receipt.TransactionID); err != nil {
		return err
	}
	if strings.TrimSpace(receipt.AttemptID) == "" {
		return errors.New("Guard readiness receipt attempt id is required")
	}
	if receipt.Phase != GuardReceiptPhaseArmed {
		return fmt.Errorf("Guard readiness receipt phase = %q, want %q", receipt.Phase, GuardReceiptPhaseArmed)
	}
	if err := validateUpdaterProcessIdentity(receipt.Process); err != nil {
		return fmt.Errorf("validate Guard readiness process: %w", err)
	}
	readyAt, err := time.Parse(time.RFC3339Nano, receipt.ReadyAt)
	if err != nil || readyAt.IsZero() {
		return errors.New("Guard readiness receipt ready_at must be RFC3339Nano")
	}
	return nil
}

// validatePackageTrust 校验 package trust 的公共字段和启停分支。
func validatePackageTrust(trust PackageTrust, platform string) error {
	if trust.SchemaVersion != PackageTrustSchemaVersion {
		return fmt.Errorf("package-owned update trust schema_version = %d, want %d", trust.SchemaVersion, PackageTrustSchemaVersion)
	}
	if trust.Platform != platform {
		return fmt.Errorf("package-owned update trust platform = %q, want %q", trust.Platform, platform)
	}
	if err := validateHelperDigest("updater_sha256", trust.UpdaterSHA256); err != nil {
		return err
	}
	if err := validateHelperDigest("guard_sha256", trust.GuardSHA256); err != nil {
		return err
	}
	if !trust.Enabled {
		if trust.Production || trust.SignerPolicy != PackageSignerPolicyDisabled {
			return errors.New("disabled package-owned update trust must be non-production with disabled signer policy")
		}
		return nil
	}
	return validateEnabledPackageTrust(trust, platform)
}

// validateEnabledPackageTrust 校验开启更新时的平台、来源、密钥和签名策略。
func validateEnabledPackageTrust(trust PackageTrust, platform string) error {
	capability, err := UpdateCapabilityFor(platform)
	if err != nil {
		return err
	}
	if !capability.Check || !capability.Install || !capability.Publish {
		return fmt.Errorf("package-owned update trust enables unsupported platform %q: %s", platform, capability.Reason)
	}
	if err := validateUpdateSource(trust.Source); err != nil {
		return err
	}
	key, err := base64.StdEncoding.DecodeString(trust.ManifestPublicKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return fmt.Errorf("package-owned update trust manifest_public_key must decode to %d bytes", ed25519.PublicKeySize)
	}
	if strings.TrimSpace(trust.Channel) == "" {
		return errors.New("package-owned update trust channel is required")
	}
	return validatePackageSignerPolicy(trust)
}

// validatePackageSignerPolicy 校验生产包只能使用 exact signer。
func validatePackageSignerPolicy(trust PackageTrust) error {
	if trust.Production && trust.SignerPolicy != PackageSignerPolicyExact {
		return errors.New("production package-owned update trust requires exact signer policy")
	}
	switch trust.SignerPolicy {
	case PackageSignerPolicyExact:
		if strings.TrimSpace(trust.SignerIdentity) == "" {
			return errors.New("package-owned update trust exact signer_identity is required")
		}
	case PackageSignerPolicySameSigner:
		if strings.TrimSpace(trust.SignerIdentity) != "" {
			return errors.New("same-signer package policy must not carry signer_identity")
		}
	default:
		return fmt.Errorf("unsupported package signer policy %q", trust.SignerPolicy)
	}
	return nil
}

// validateUpdateSource 校验 package owner 固化的更新源格式。
func validateUpdateSource(source UpdateSource) error {
	value := strings.TrimSpace(source.Value)
	switch source.Kind {
	case UpdateSourceGitHub:
		parts := strings.Split(value, "/")
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" || strings.ContainsAny(value, " \t\r\n") {
			return fmt.Errorf("package-owned GitHub update source must be owner/repo: %q", source.Value)
		}
	case UpdateSourceManifest:
		parsed, err := url.Parse(value)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			return fmt.Errorf("package-owned manifest update source must be HTTPS with host: %q", source.Value)
		}
	default:
		return fmt.Errorf("unsupported package-owned update source kind %q", source.Kind)
	}
	return nil
}

func validateHelperDigest(field, value string) error {
	raw, err := hex.DecodeString(value)
	if err != nil || len(raw) != sha256.Size {
		return fmt.Errorf("package-owned update trust %s must be a SHA-256 digest", field)
	}
	return nil
}

// RejectPackageTrustOverrides 拒绝所有运行时 update trust 环境变量。
func RejectPackageTrustOverrides(environ []string) error {
	for _, entry := range environ {
		name, _, _ := strings.Cut(entry, "=")
		if _, blocked := packageTrustOverrideNames[name]; blocked {
			return fmt.Errorf("package-owned update trust rejects runtime override %s", name)
		}
	}
	return nil
}

// TransactionRootForTarget 返回 target 同级的标准 transaction root。
func TransactionRootForTarget(target string) string {
	return filepath.Join(filepath.Dir(target), TransactionRootDirName)
}

// ResolveTransactionBoundPackageTrust 在 active transaction 中只返回旧包信任。
func ResolveTransactionBoundPackageTrust(ctx context.Context, target, platform string) (PackageTrust, string, error) {
	if err := RequireCanonicalPath(target); err != nil {
		return PackageTrust{}, "", fmt.Errorf("reject package target alias: %w", err)
	}
	root := TransactionRootForTarget(target)
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		return LoadPackageTrust(filepath.Join(target, "Contents", "Resources"), platform)
	} else if err != nil {
		return PackageTrust{}, "", fmt.Errorf("inspect update transaction root: %w", err)
	}
	store := &Store{root: root, now: time.Now}
	transaction, found, err := store.SelectForTarget(ctx, target)
	if err != nil {
		return PackageTrust{}, "", err
	}
	if !found {
		return LoadPackageTrust(filepath.Join(target, "Contents", "Resources"), platform)
	}
	if transaction.State == StatePrepared {
		if _, err := os.Stat(transaction.Paths.RecoveryDir); errors.Is(err, os.ErrNotExist) {
			return recoverPreparedWithoutCapsule(ctx, store, target, platform, transaction)
		} else if err != nil {
			return PackageTrust{}, "", fmt.Errorf("inspect prepared recovery capsule: %w", err)
		}
	}
	return ResolvePackageTrustForTransaction(ctx, target, platform, transaction)
}

// recoverPreparedWithoutCapsule 验证 journal 两代真值后安全终止 journal-before-capsule 崩溃窗。
func recoverPreparedWithoutCapsule(ctx context.Context, store *Store, target, platform string, transaction Transaction) (PackageTrust, string, error) {
	oldTrust, oldGeneration, err := loadExactTransactionTrust(
		ctx, target, platform, transaction.Identity.OldRelease, transaction.Identity.OldHelpers,
		transaction.Trust.PreviousGeneration, transaction.Trust.PackageSigner,
	)
	if err != nil {
		return PackageTrust{}, "", err
	}
	if err := verifyCandidateForState(ctx, platform, transaction); err != nil {
		return PackageTrust{}, "", err
	}
	rolledBack, err := store.Rollback(ctx, transaction.Identity)
	if err != nil {
		return PackageTrust{}, "", fmt.Errorf("rollback prepared transaction without recovery capsule: %w", err)
	}
	if err := cleanupIncompleteRecoveryCapsule(transaction.Paths.RecoveryDir); err != nil {
		return PackageTrust{}, "", err
	}
	if rolledBack.State != StateRolledBack {
		return PackageTrust{}, "", fmt.Errorf("prepared transaction recovery state = %q, want %q", rolledBack.State, StateRolledBack)
	}
	return oldTrust, oldGeneration, nil
}

func cleanupIncompleteRecoveryCapsule(recoveryDir string) error {
	return cleanupTransactionCapsule(Paths{RecoveryDir: recoveryDir})
}

// cleanupTransactionCapsule 删除所有已治理的 capsule 入口并同步 transaction 目录。
func cleanupTransactionCapsule(paths Paths) error {
	for _, path := range []string{paths.RecoveryDir + ".pending", paths.RecoveryDir} {
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("remove incomplete recovery capsule %s: %w", path, err)
		}
	}
	parent := filepath.Dir(paths.RecoveryDir)
	if _, err := os.Stat(parent); err == nil {
		return syncDirectory(parent)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect recovery capsule parent: %w", err)
	}
	return nil
}

// ResolvePackageTrustForTransaction 按 exact state/intent 校验双代 release、helper 和 trust。
func ResolvePackageTrustForTransaction(ctx context.Context, target, platform string, transaction Transaction) (PackageTrust, string, error) {
	if transaction.Paths.Target != target {
		return PackageTrust{}, "", ErrIdentityMismatch
	}
	trust, generation, terminal, err := resolveTerminalPackageTrust(ctx, target, platform, transaction)
	if terminal || err != nil {
		return trust, generation, err
	}
	if err := verifyRecoveryCapsule(ctx, platform, transaction); err != nil {
		return PackageTrust{}, "", err
	}
	oldPath, err := oldReleasePathForState(transaction)
	if err != nil {
		return PackageTrust{}, "", err
	}
	if oldPath != "" {
		if _, _, err := loadExactTransactionTrust(ctx, oldPath, platform, transaction.Identity.OldRelease, transaction.Identity.OldHelpers, transaction.Trust.PreviousGeneration, transaction.Trust.PackageSigner); err != nil {
			return PackageTrust{}, "", err
		}
	}
	if err := verifyCandidateForState(ctx, platform, transaction); err != nil {
		return PackageTrust{}, "", err
	}
	return loadCapsuleTrust(platform, transaction)
}

// resolveTerminalPackageTrust 清理 capsule 后仅从 exact target 解析终态 trust。
func resolveTerminalPackageTrust(ctx context.Context, target, platform string, transaction Transaction) (PackageTrust, string, bool, error) {
	if transaction.State != StateCommitted && transaction.State != StateRolledBack {
		return PackageTrust{}, "", false, nil
	}
	if err := cleanupTransactionCapsule(transaction.Paths); err != nil {
		return PackageTrust{}, "", true, err
	}
	if transaction.State == StateCommitted {
		trust, generation, err := loadExactTransactionTrust(ctx, target, platform, transaction.Identity.CandidateRelease, transaction.Identity.CandidateHelpers, transaction.Trust.Generation, transaction.Trust.PackageSigner)
		return trust, generation, true, err
	}
	trust, generation, err := loadExactTransactionTrust(ctx, target, platform, transaction.Identity.OldRelease, transaction.Identity.OldHelpers, transaction.Trust.PreviousGeneration, transaction.Trust.PackageSigner)
	return trust, generation, true, err
}

// oldReleasePathForState 按 journal intent 选择旧 release 的唯一合法位置。
func oldReleasePathForState(transaction Transaction) (string, error) {
	switch transaction.State {
	case StatePrepared:
		return transaction.Paths.Target, nil
	case StateBackupPending:
		return exactExistingPath(transaction.Paths.Target, transaction.Paths.Backup)
	case StateBackupRetained, StateInstallPending, StateProbation:
		return transaction.Paths.Backup, nil
	case StateCommitPending:
		return optionalExistingPath(transaction.Paths.Backup)
	case StateRollbackPending:
		return firstExistingPath(transaction.Paths.Backup, transaction.Paths.Target)
	case StateRolledBack:
		return transaction.Paths.Target, nil
	default:
		return "", fmt.Errorf("resolve old trust from unsupported transaction state %q", transaction.State)
	}
}

// verifyCandidateForState 验证当前 state 仍应存在的 exact candidate。
func verifyCandidateForState(ctx context.Context, platform string, transaction Transaction) error {
	var path string
	var err error
	switch transaction.State {
	case StatePrepared, StateBackupPending, StateBackupRetained:
		path = transaction.Paths.Staging
	case StateInstallPending:
		path, err = exactExistingPath(transaction.Paths.Staging, transaction.Paths.Target)
	case StateProbation, StateCommitPending:
		path = transaction.Paths.Target
	case StateRollbackPending, StateRolledBack:
		return nil
	default:
		return fmt.Errorf("verify candidate from unsupported transaction state %q", transaction.State)
	}
	if err != nil {
		return err
	}
	_, _, err = loadExactTransactionTrust(ctx, path, platform, transaction.Identity.CandidateRelease, transaction.Identity.CandidateHelpers, transaction.Trust.Generation, transaction.Trust.PackageSigner)
	return err
}

// loadExactTransactionTrust 联合校验 release、helper、generation 和 signer。
func loadExactTransactionTrust(ctx context.Context, path, platform string, release ReleaseIdentity, helpers HelperIdentity, generation, signer string) (PackageTrust, string, error) {
	if err := RequireCanonicalExistingPath(path); err != nil {
		return PackageTrust{}, "", fmt.Errorf("reject transaction release alias: %w", err)
	}
	if err := verifyRelease(ctx, path, release); err != nil {
		return PackageTrust{}, "", fmt.Errorf("verify transaction release at %s: %w", path, err)
	}
	trust, actualGeneration, err := LoadPackageTrust(filepath.Join(path, "Contents", "Resources"), platform)
	if err != nil {
		return PackageTrust{}, "", err
	}
	if actualGeneration != generation {
		return PackageTrust{}, "", fmt.Errorf("stale transaction trust generation = %s, want %s", actualGeneration, generation)
	}
	if trust.SignerIdentity != signer {
		return PackageTrust{}, "", fmt.Errorf("transaction trust signer = %q, want %q", trust.SignerIdentity, signer)
	}
	if err := verifyReleaseHelpers(ctx, path, trust, helpers); err != nil {
		return PackageTrust{}, "", err
	}
	return trust, actualGeneration, nil
}

// verifyReleaseHelpers 校验包内 helper 同时匹配 trust 与 journal identity。
func verifyReleaseHelpers(ctx context.Context, release string, trust PackageTrust, helpers HelperIdentity) error {
	updaterPath := filepath.Join(release, "Contents", "Resources", "bin", "super-dolphin-updater")
	if err := RequireCanonicalExistingPath(updaterPath); err != nil {
		return fmt.Errorf("reject transaction updater alias: %w", err)
	}
	updater, err := ComputeReleaseDigestContext(ctx, updaterPath)
	if err != nil {
		return fmt.Errorf("digest transaction updater helper: %w", err)
	}
	guardPath := filepath.Join(release, "Contents", "Resources", "bin", "super-dolphin-guard")
	if err := RequireCanonicalExistingPath(guardPath); err != nil {
		return fmt.Errorf("reject transaction Guard alias: %w", err)
	}
	guard, err := ComputeReleaseDigestContext(ctx, guardPath)
	if err != nil {
		return fmt.Errorf("digest transaction Guard helper: %w", err)
	}
	if updater != trust.UpdaterSHA256 || updater != helpers.UpdaterSHA256 || guard != trust.GuardSHA256 || guard != helpers.GuardSHA256 {
		return errors.New("transaction release helper identity mismatch")
	}
	return nil
}

func verifyRecoveryCapsule(ctx context.Context, platform string, transaction Transaction) error {
	updater := filepath.Join(transaction.Paths.RecoveryDir, "super-dolphin-updater")
	guard := filepath.Join(transaction.Paths.RecoveryDir, "super-dolphin-guard")
	updaterDigest, err := ComputeReleaseDigestContext(ctx, updater)
	if err != nil {
		return fmt.Errorf("verify recovery capsule updater: %w", err)
	}
	guardDigest, err := ComputeReleaseDigestContext(ctx, guard)
	if err != nil {
		return fmt.Errorf("verify recovery capsule Guard: %w", err)
	}
	if updaterDigest != transaction.Identity.OldHelpers.UpdaterSHA256 || guardDigest != transaction.Identity.OldHelpers.GuardSHA256 {
		return errors.New("recovery capsule helper identity mismatch")
	}
	_, _, err = loadCapsuleTrust(platform, transaction)
	return err
}

// loadCapsuleTrust 从 transaction capsule 返回已绑定的旧 trust 快照。
func loadCapsuleTrust(platform string, transaction Transaction) (PackageTrust, string, error) {
	trust, generation, err := LoadPackageTrust(transaction.Paths.RecoveryDir, platform)
	if err != nil {
		return PackageTrust{}, "", fmt.Errorf("load recovery capsule trust: %w", err)
	}
	if generation != transaction.Trust.PreviousGeneration {
		return PackageTrust{}, "", fmt.Errorf("stale recovery trust generation = %s, want %s", generation, transaction.Trust.PreviousGeneration)
	}
	if trust.SignerIdentity != transaction.Trust.PackageSigner ||
		trust.UpdaterSHA256 != transaction.Identity.OldHelpers.UpdaterSHA256 ||
		trust.GuardSHA256 != transaction.Identity.OldHelpers.GuardSHA256 {
		return PackageTrust{}, "", errors.New("recovery capsule trust identity mismatch")
	}
	return trust, generation, nil
}

func exactExistingPath(first, second string) (string, error) {
	firstExists, err := pathExists(first)
	if err != nil {
		return "", err
	}
	secondExists, err := pathExists(second)
	if err != nil {
		return "", err
	}
	if firstExists == secondExists {
		return "", fmt.Errorf("transaction intent requires exactly one release path: first=%v second=%v", firstExists, secondExists)
	}
	if firstExists {
		return first, nil
	}
	return second, nil
}

func optionalExistingPath(path string) (string, error) {
	exists, err := pathExists(path)
	if err != nil || !exists {
		return "", err
	}
	return path, nil
}

func firstExistingPath(first, second string) (string, error) {
	firstExists, err := pathExists(first)
	if err != nil {
		return "", err
	}
	if firstExists {
		return first, nil
	}
	secondExists, err := pathExists(second)
	if err != nil {
		return "", err
	}
	if !secondExists {
		return "", errors.New("transaction intent has no old release path")
	}
	return second, nil
}
