// Package recoverycandidate provides a local, candidate-only recovery
// observation seam. Marker reads and resolution are owned by the
// wait-operation owner; this package never writes the product database.
package recoverycandidate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
)

const schemaVersion int64 = 1

var (
	ErrSchemaUnavailable   = errors.New("recovery candidate schema is unavailable")
	ErrRecoveryUnavailable = errors.New("cross-process recovery authority is unavailable")
	ErrProofMismatch       = errors.New("commit marker canonical post-state proof mismatches")
	ErrProofRequired       = errors.New("commit marker canonical post-state proof is required")
	ErrMarkerNotFound      = errors.New("commit marker was not found")
	ErrReceiptConflict     = errors.New("recovery receipt conflicts")
)

// Clock 提供恢复候选流程使用的显式非零时钟。
type Clock func() time.Time

// Marker 是从 owner 读取的不可变身份和持久后状态证明。
type Marker struct {
	MarkerKey, OperationKey, OperationDigest, TargetKey string
	MutationKind, RequestDigest, PreStateDigest         string
	PostStateDigest, State, CreatedAt, ResolvedAt       string
	SchemaVersion                                       int64
}

// MarkerReader 是 owner 签发的类型化读取能力；本包不接受 *sql.DB。
type MarkerReader interface {
	ReadMarker(context.Context, string) (Marker, error)
	ListPendingMarkers(context.Context) ([]Marker, error)
}

// MarkerResolver 是执行标记 CAS 和证明检查的 owner 类型化能力。
type MarkerResolver interface {
	ResolveMarker(context.Context, string, string) (Marker, error)
}

// CanonicalPostStateVerifier 根据规范后状态校验 owner 读取的标记。
type CanonicalPostStateVerifier interface {
	VerifyPostState(context.Context, Marker) (string, error)
}

// CanonicalPostStateVerifierFunc 将函数适配为规范后状态校验能力。
type CanonicalPostStateVerifierFunc func(context.Context, Marker) (string, error)

// VerifyPostState 调用底层函数完成规范后状态校验。
func (f CanonicalPostStateVerifierFunc) VerifyPostState(ctx context.Context, marker Marker) (string, error) {
	return f(ctx, marker)
}

// RecoveryReceipt 是候选恢复流程写入的不可变回执。
type RecoveryReceipt struct {
	ReceiptKey, MarkerKey, OperationKey, OperationDigest, TargetKey string
	MutationKind, PostStateDigest, ObservationDigest, RecoveredAt   string
	SchemaVersion                                                   int64
}

// Observation 描述一次候选恢复回执及其非权威赢家结果。
type Observation struct {
	Receipt RecoveryReceipt
	Winner  bool
	// Authoritative 永久为 false：此处不存在外部恢复权威。
	Authoritative bool
}

func markerKey(marker Marker) string {
	return digest("wait-operation/commit-marker/v1", strings.Join([]string{marker.MutationKind, marker.OperationKey, marker.OperationDigest, marker.TargetKey, marker.RequestDigest, marker.PreStateDigest}, "\x00"))
}
func receiptKey(marker Marker) string {
	return digest("wait-operation/recovery-receipt/v1", marker.MarkerKey)
}
func observationDigest(marker Marker, key, recoveredAt string) string {
	return digest("wait-operation/recovery-observation/v1", strings.Join([]string{key, marker.MarkerKey, marker.OperationKey, marker.OperationDigest, marker.TargetKey, marker.MutationKind, marker.PostStateDigest, recoveredAt}, "\x00"))
}
func digest(domain, value string) string {
	hash := sha256.Sum256(append([]byte(domain+"\x00"), []byte(value)...))
	return hex.EncodeToString(hash[:])
}

func validateMarker(marker Marker) error {
	if err := validateMarkerIdentity(marker); err != nil {
		return ErrReceiptConflict
	}
	if err := validateMarkerState(marker); err != nil {
		return ErrReceiptConflict
	}
	if err := validateMarkerMutation(marker); err != nil {
		return ErrReceiptConflict
	}
	if err := validateMarkerTimes(marker); err != nil {
		return ErrReceiptConflict
	}
	return nil
}

// validateMarkerIdentity 校验标记键、操作键和摘要字段的绑定关系。
func validateMarkerIdentity(marker Marker) error {
	if !validDigest(marker.MarkerKey) {
		return ErrReceiptConflict
	}
	if marker.MarkerKey != markerKey(marker) {
		return ErrReceiptConflict
	}
	if strings.TrimSpace(marker.OperationKey) != marker.OperationKey || marker.OperationKey == "" || len(marker.OperationKey) > 256 {
		return ErrReceiptConflict
	}
	if !validDigest(marker.OperationDigest) || !validDigest(marker.RequestDigest) {
		return ErrReceiptConflict
	}
	if !validDigestOptional(marker.PreStateDigest) || !validDigestOptional(marker.PostStateDigest) {
		return ErrReceiptConflict
	}
	return nil
}

// validateMarkerState 校验标记的版本和生命周期状态。
func validateMarkerState(marker Marker) error {
	if marker.SchemaVersion != schemaVersion {
		return ErrReceiptConflict
	}
	if marker.State != "pending" && marker.State != "resolved" {
		return ErrReceiptConflict
	}
	return nil
}

// validateMarkerMutation 校验变更类型与目标键的契约组合。
func validateMarkerMutation(marker Marker) error {
	switch marker.MutationKind {
	case "plan", "state_advance":
		if marker.TargetKey != "" {
			return ErrReceiptConflict
		}
	case "target_append", "target_provision", "target_cleanup", "target_resume":
		if marker.TargetKey == "" || strings.TrimSpace(marker.TargetKey) != marker.TargetKey || len(marker.TargetKey) > 128 {
			return ErrReceiptConflict
		}
	default:
		return ErrReceiptConflict
	}
	return nil
}

// validateMarkerTimes 校验创建、解决时间戳及其先后关系。
func validateMarkerTimes(marker Marker) error {
	createdAt, err := parseMarkerTime(marker.CreatedAt)
	if err != nil {
		return ErrReceiptConflict
	}
	if marker.State == "pending" {
		if marker.ResolvedAt != "" {
			return ErrReceiptConflict
		}
		return nil
	}
	if marker.PostStateDigest == "" || marker.ResolvedAt == "" {
		return ErrReceiptConflict
	}
	resolvedAt, err := parseMarkerTime(marker.ResolvedAt)
	if err != nil || resolvedAt.Before(createdAt) {
		return ErrReceiptConflict
	}
	return nil
}

// validDigest 判断值是否为小写十六进制 SHA-256 摘要。
func validDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return false
		}
	}
	return true
}

// validDigestOptional 判断可为空的摘要字段是否满足摘要格式。
func validDigestOptional(value string) bool { return value == "" || validDigest(value) }

// validateClock 拒绝缺失或返回零时间的隐式时钟。
func validateClock(clock Clock) error {
	if clock == nil {
		return errors.New("recovery candidate requires a non-zero explicit clock")
	}
	now := clock()
	if now.IsZero() || now.Location() != time.UTC {
		return errors.New("recovery candidate requires a non-zero UTC clock")
	}
	return nil
}

// isNilCapability 拒绝接口中携带的 typed-nil owner 能力，避免运行期解引用崩溃。
func isNilCapability(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

// formatTime 将时间标准化为严格 UTC RFC3339Nano 文本。
func formatTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

// parseMarkerTime 解析并严格校验 UTC RFC3339Nano 标记时间。
func parseMarkerTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.Location() != time.UTC || parsed.Format(time.RFC3339Nano) != value {
		return time.Time{}, fmt.Errorf("invalid strict UTC marker timestamp")
	}
	return parsed, nil
}
