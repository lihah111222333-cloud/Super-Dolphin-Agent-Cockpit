package gate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// RequiredGoToolchain 定义生产门禁唯一接受的 Go 发行版身份。
const RequiredGoToolchain = cicontract.GoToolchainVersion

// PlainTextLog 是 JSON 中保持普通 UTF-8 文本语义的有界门禁日志。
type PlainTextLog []byte

// MarshalJSON 将日志编码为普通 JSON 字符串，拒绝二进制或 NUL 数据。
func (log PlainTextLog) MarshalJSON() ([]byte, error) {
	if !utf8.Valid(log) || bytes.IndexByte(log, 0) >= 0 {
		return nil, errors.New("gate log must be NUL-free UTF-8 text")
	}
	encoded, err := json.Marshal(string(log))
	if err != nil {
		return nil, fmt.Errorf("marshal gate log text: %w", err)
	}
	return encoded, nil
}

// UnmarshalJSON 从普通 JSON 字符串恢复日志原始 UTF-8 字节。
func (log *PlainTextLog) UnmarshalJSON(data []byte) error {
	if log == nil {
		return errors.New("gate log destination is nil")
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return fmt.Errorf("unmarshal gate log text: %w", err)
	}
	if !utf8.ValidString(text) || bytes.IndexByte([]byte(text), 0) >= 0 {
		return errors.New("gate log must be NUL-free UTF-8 text")
	}
	*log = append((*log)[:0], text...)
	return nil
}

// SourceKind identifies the single Git object form carried by a SourceSpec.
type SourceKind string

const (
	SourceKindCommit SourceKind = "commit"
	SourceKindTree   SourceKind = "tree"
	SourceKindRange  SourceKind = "range"
)

// BaseKind identifies the base semantics of a range source.
type BaseKind string

const (
	BaseKindCommit    BaseKind = "commit"
	BaseKindEmptyTree BaseKind = "empty_tree"
)

// UpdateKind identifies how a Git ref moves.
type UpdateKind string

const (
	UpdateKindCreate      UpdateKind = "create"
	UpdateKindFastForward UpdateKind = "fast_forward"
	UpdateKindForce       UpdateKind = "force"
)

// GitObjectFormat identifies the hash algorithm and OID width of a Git repository.
type GitObjectFormat string

const (
	GitObjectFormatSHA1   GitObjectFormat = "sha1"
	GitObjectFormatSHA256 GitObjectFormat = "sha256"
)

// CommitSource identifies a committed Git source.
type CommitSource struct {
	SHA string `json:"sha"`
}

// TreeSource identifies an explicit Git tree and its optional parent commit.
type TreeSource struct {
	SHA             string `json:"sha"`
	ParentCommitSHA string `json:"parent_commit_sha,omitempty"`
}

// RangeSource identifies one non-delete Git ref update.
type RangeSource struct {
	BaseKind          BaseKind   `json:"base_kind"`
	BaseSHA           string     `json:"base_sha,omitempty"`
	HeadSHA           string     `json:"head_sha"`
	LocalRef          string     `json:"local_ref"`
	RemoteRef         string     `json:"remote_ref"`
	ObservedRemoteSHA string     `json:"observed_remote_sha"`
	UpdateKind        UpdateKind `json:"update_kind"`
}

// SourceSpec is the canonical tagged union for commit, tree, or range input.
type SourceSpec struct {
	Kind          SourceKind      `json:"kind"`
	ObjectFormat  GitObjectFormat `json:"object_format"`
	Commit        *CommitSource   `json:"commit,omitempty"`
	Tree          *TreeSource     `json:"tree,omitempty"`
	Range         *RangeSource    `json:"range,omitempty"`
	SourceTreeSHA string          `json:"source_tree_sha"`
}

// ResultStatus identifies the terminal state of a remote or local execution result.
type ResultStatus string

const (
	ResultStatusPassed            ResultStatus = "passed"
	ResultStatusFailed            ResultStatus = "failed"
	ResultStatusCancelled         ResultStatus = "cancelled"
	ResultStatusTimeout           ResultStatus = "timeout"
	ResultStatusInfraFailed       ResultStatus = "infra_failed"
	ResultStatusPassedStalePolicy ResultStatus = "passed_stale_policy"
)
