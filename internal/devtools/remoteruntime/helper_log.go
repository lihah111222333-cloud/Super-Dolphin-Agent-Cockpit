package remoteruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

// HelperLogSchemaVersion 标识 runtime helper stderr 的内部 evidence 形状。
// 它不是 outer remote event schema，也不进入 stdout 或 PostgreSQL event wire。
const HelperLogSchemaVersion = "remote-ci-helper-log/v1"

// HelperLogEvidence 只保留 bounded helper stderr 的安全摘要，不保存 raw 日志。
type HelperLogEvidence struct {
	SchemaVersion string
	Bytes         int
	Digest        string
}

// Validate 校验 helper-local evidence 的 schema、大小和摘要形状。
func (evidence HelperLogEvidence) Validate() error {
	if evidence.SchemaVersion != HelperLogSchemaVersion || evidence.Bytes < 0 || evidence.Bytes > MaxNodeStderrBytes || !validHelperLogDigest(evidence.Digest) {
		return errors.New("runtime helper log evidence is invalid")
	}
	return nil
}

// helperLogEvidence 从 bounded stderr 生成不含日志正文的内部 evidence。
func helperLogEvidence(raw []byte) HelperLogEvidence {
	sum := sha256.Sum256(raw)
	evidence := HelperLogEvidence{SchemaVersion: HelperLogSchemaVersion, Bytes: len(raw), Digest: "sha256:" + hex.EncodeToString(sum[:])}
	if err := evidence.Validate(); err != nil {
		return HelperLogEvidence{SchemaVersion: HelperLogSchemaVersion}
	}
	return evidence
}

func validHelperLogDigest(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || value[:len("sha256:")] != "sha256:" {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

type helperLogError struct {
	cause    error
	evidence HelperLogEvidence
}

// Error 保持 transport 原有稳定错误文本，不渲染 helper stderr。
func (err helperLogError) Error() string { return err.cause.Error() }

// Unwrap 保留既有 errors.Is 分类。
func (err helperLogError) Unwrap() error { return err.cause }

// HelperLogEvidence 返回安全的 helper-local evidence 摘要。
func (err helperLogError) HelperLogEvidence() HelperLogEvidence { return err.evidence }

func wrapHelperLogError(cause error, evidence HelperLogEvidence) error {
	if cause == nil || evidence.Bytes == 0 {
		return cause
	}
	return helperLogError{cause: cause, evidence: evidence}
}
