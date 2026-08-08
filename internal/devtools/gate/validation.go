package gate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"strings"
)

var (
	gitOIDPattern = regexp.MustCompile(`^[0-9a-f]+$`)
	digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// Validatable is implemented by every canonical contract with fail-fast validation.
type Validatable interface {
	Validate() error
}

// DecodeStrictJSON 严格解码协议 JSON，并拒绝未知字段、畸形输入和尾随值。
func DecodeStrictJSON(data []byte, target Validatable) error {
	if target == nil || reflect.ValueOf(target).Kind() != reflect.Pointer || reflect.ValueOf(target).IsNil() {
		return errors.New("strict JSON target must be a non-nil pointer")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode strict JSON: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return err
	}
	return target.Validate()
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("decode trailing JSON: %w", err)
	}
	return errors.New("trailing JSON value is not allowed")
}

// Validate 校验 SourceSpec tagged union 及其 Git 对象类型不变量。
func (s SourceSpec) Validate() error {
	if sourceVariantCount(s) > 1 {
		return errors.New("exactly one source variant is required")
	}
	if _, err := objectFormatOIDLength(s.ObjectFormat); err != nil {
		return err
	}
	if err := validateOID("source_tree_sha", s.SourceTreeSHA, s.ObjectFormat, false); err != nil {
		return err
	}
	switch s.Kind {
	case SourceKindCommit:
		return validateCommitSource(s.Commit, s.ObjectFormat, s.SourceTreeSHA)
	case SourceKindTree:
		return validateTreeSource(s.Tree, s.ObjectFormat, s.SourceTreeSHA)
	case SourceKindRange:
		return validateRangeSource(s.Range, s.ObjectFormat, s.SourceTreeSHA)
	default:
		return fmt.Errorf("unsupported source kind %q", s.Kind)
	}
}

func sourceVariantCount(spec SourceSpec) int {
	count := 0
	for _, present := range []bool{spec.Commit != nil, spec.Tree != nil, spec.Range != nil} {
		if present {
			count++
		}
	}
	return count
}

func validateCommitSource(source *CommitSource, objectFormat GitObjectFormat, sourceTreeSHA string) error {
	if source == nil {
		return errors.New("commit source is required for kind commit")
	}
	if err := validateOID("commit.sha", source.SHA, objectFormat, false); err != nil {
		return err
	}
	if source.SHA == sourceTreeSHA {
		return errors.New("commit sha and source_tree_sha must identify different Git object types")
	}
	return nil
}

func validateTreeSource(source *TreeSource, objectFormat GitObjectFormat, sourceTreeSHA string) error {
	if source == nil {
		return errors.New("tree source is required for kind tree")
	}
	if err := validateOID("tree.sha", source.SHA, objectFormat, false); err != nil {
		return err
	}
	if source.SHA != sourceTreeSHA {
		return errors.New("tree sha must equal source_tree_sha")
	}
	if source.ParentCommitSHA != "" {
		return validateOID("tree.parent_commit_sha", source.ParentCommitSHA, objectFormat, false)
	}
	return nil
}

func validateRangeSource(source *RangeSource, objectFormat GitObjectFormat, sourceTreeSHA string) error {
	if source == nil {
		return errors.New("range source is required for kind range")
	}
	if source.HeadSHA == sourceTreeSHA {
		return errors.New("range head_sha and source_tree_sha must identify different Git object types")
	}
	return source.validate(objectFormat)
}

// validate 校验 range 的 ref、对象类型与更新类型组合。
func (r RangeSource) validate(objectFormat GitObjectFormat) error {
	if err := validateOID("range.head_sha", r.HeadSHA, objectFormat, false); err != nil {
		return err
	}
	if !strings.HasPrefix(r.LocalRef, "refs/") || !strings.HasPrefix(r.RemoteRef, "refs/") {
		return errors.New("range local_ref and remote_ref must be full refs")
	}
	switch r.UpdateKind {
	case UpdateKindCreate:
		return r.validateCreate(objectFormat)
	case UpdateKindFastForward, UpdateKindForce:
		return r.validateExistingRef(objectFormat)
	default:
		return fmt.Errorf("unsupported update kind %q", r.UpdateKind)
	}
}

func (r RangeSource) validateCreate(objectFormat GitObjectFormat) error {
	if r.BaseKind != BaseKindEmptyTree {
		return errors.New("create update requires empty_tree base")
	}
	if r.BaseSHA != "" {
		return errors.New("empty_tree base must not include base_sha")
	}
	zeroOID, err := ZeroOID(objectFormat)
	if err != nil {
		return err
	}
	if r.ObservedRemoteSHA != zeroOID {
		return fmt.Errorf("create update requires %s zero observed_remote_sha", objectFormat)
	}
	return nil
}

func (r RangeSource) validateExistingRef(objectFormat GitObjectFormat) error {
	if r.BaseKind != BaseKindCommit {
		return fmt.Errorf("%s update requires commit base", r.UpdateKind)
	}
	if err := validateOID("range.base_sha", r.BaseSHA, objectFormat, false); err != nil {
		return err
	}
	return validateOID("range.observed_remote_sha", r.ObservedRemoteSHA, objectFormat, false)
}

// ZeroOID 返回指定 Git object format 的 canonical zero OID。
func ZeroOID(objectFormat GitObjectFormat) (string, error) {
	length, err := objectFormatOIDLength(objectFormat)
	if err != nil {
		return "", err
	}
	return strings.Repeat("0", length), nil
}

func objectFormatOIDLength(objectFormat GitObjectFormat) (int, error) {
	switch objectFormat {
	case GitObjectFormatSHA1:
		return 40, nil
	case GitObjectFormatSHA256:
		return 64, nil
	default:
		return 0, fmt.Errorf("unsupported git object format %q", objectFormat)
	}
}

// validateOID 按仓库 object format 校验 OID 字符集、长度和 zero 约束。
func validateOID(name, value string, objectFormat GitObjectFormat, allowZero bool) error {
	length, err := objectFormatOIDLength(objectFormat)
	if err != nil {
		return err
	}
	if len(value) != length || !gitOIDPattern.MatchString(value) {
		return fmt.Errorf("%s must be a lowercase %d-character %s Git OID", name, length, objectFormat)
	}
	zeroOID, err := ZeroOID(objectFormat)
	if err != nil {
		return err
	}
	if !allowZero && value == zeroOID {
		return fmt.Errorf("%s must not be the zero OID", name)
	}
	return nil
}

func validateDigest(name, value string) error {
	if !digestPattern.MatchString(value) {
		return fmt.Errorf("%s must be a canonical sha256 digest", name)
	}
	return nil
}
