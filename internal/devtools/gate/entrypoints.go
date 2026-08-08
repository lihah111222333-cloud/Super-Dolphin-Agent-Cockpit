package gate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

// CIEntrypointID identifies one stable gate invocation boundary.
type CIEntrypointID string

const (
	CIEntrypointGitPreCommit CIEntrypointID = "git-pre-commit"
	CIEntrypointGitPrePush   CIEntrypointID = "git-pre-push"
	CIEntrypointManualCLI    CIEntrypointID = "manual-cli"
	CIEntrypointRelease      CIEntrypointID = "release"
)

// CIEntrypointOwner identifies the authority owner assigned to one adapter boundary.
type CIEntrypointOwner string

const (
	CIEntrypointOwnerManagedGitPreCommit CIEntrypointOwner = "managed-launcher/git-pre-commit"
	CIEntrypointOwnerManagedGitPrePush   CIEntrypointOwner = "managed-launcher/git-pre-push"
	CIEntrypointOwnerRepositoryGitHooks  CIEntrypointOwner = "repository-git-hooks"
	CIEntrypointOwnerManualCLI           CIEntrypointOwner = "gate-cli/manual"
	CIEntrypointOwnerRelease             CIEntrypointOwner = "external-release-authority"
)

// CIEntrypointAdapter identifies the only adapter assigned to an entrypoint.
type CIEntrypointAdapter string

const (
	CIEntrypointAdapterGitPreCommit CIEntrypointAdapter = "git-hook/pre-commit"
	CIEntrypointAdapterGitPrePush   CIEntrypointAdapter = "git-hook/pre-push"
	CIEntrypointAdapterManualCLI    CIEntrypointAdapter = "cmd/super-dolphin-gate/manual"
	CIEntrypointAdapterRelease      CIEntrypointAdapter = "release/pipeline"
)

// CIEntrypoint declares the canonical source, profile, authority, and adapter boundary.
type CIEntrypoint struct {
	ID              CIEntrypointID      `json:"id"`
	AllowedSources  []SourceKind        `json:"allowed_sources"`
	AllowedProfiles []Profile           `json:"allowed_profiles"`
	Authoritative   bool                `json:"authoritative"`
	Owner           CIEntrypointOwner   `json:"owner"`
	Adapter         CIEntrypointAdapter `json:"adapter"`
}

type ciEntrypointJSON struct {
	ID              *CIEntrypointID      `json:"id"`
	AllowedSources  *[]SourceKind        `json:"allowed_sources"`
	AllowedProfiles *[]Profile           `json:"allowed_profiles"`
	Authoritative   *bool                `json:"authoritative"`
	Owner           *CIEntrypointOwner   `json:"owner"`
	Adapter         *CIEntrypointAdapter `json:"adapter"`
}

// Validate 校验入口枚举、能力集合、authority 和唯一 owner/adapter 绑定。
func (e CIEntrypoint) Validate() error {
	if err := e.ID.Validate(); err != nil {
		return err
	}
	if err := e.validateCapabilities(); err != nil {
		return err
	}
	return e.validateIdentity()
}

// validateCapabilities 校验 source/profile 集合及其 canonical 绑定。
func (e CIEntrypoint) validateCapabilities() error {
	if err := validateSourceKindSet(e.AllowedSources); err != nil {
		return fmt.Errorf("CI entrypoint %q: %w", e.ID, err)
	}
	if err := validateProfileSet("allowed_profiles", e.AllowedProfiles); err != nil {
		return fmt.Errorf("CI entrypoint %q: %w", e.ID, err)
	}
	expected, ok := canonicalCIEntrypoint(e.ID)
	if !ok {
		return fmt.Errorf("canonical CI entrypoint %q is missing", e.ID)
	}
	if !slices.Equal(e.AllowedSources, expected.AllowedSources) || !slices.Equal(e.AllowedProfiles, expected.AllowedProfiles) {
		return fmt.Errorf("CI entrypoint %q capabilities do not match the canonical registry", e.ID)
	}
	return nil
}

// validateIdentity 校验 authority 信任根和唯一 owner/adapter 绑定。
func (e CIEntrypoint) validateIdentity() error {
	if e.Authoritative && !e.Owner.isTrusted() {
		return fmt.Errorf("authoritative CI entrypoint %q requires a trusted owner", e.ID)
	}
	if err := e.Owner.Validate(); err != nil {
		return fmt.Errorf("CI entrypoint %q: %w", e.ID, err)
	}
	if err := e.Adapter.Validate(); err != nil {
		return fmt.Errorf("CI entrypoint %q: %w", e.ID, err)
	}
	expected, ok := canonicalCIEntrypoint(e.ID)
	if !ok {
		return fmt.Errorf("canonical CI entrypoint %q is missing", e.ID)
	}
	if e.Authoritative != expected.Authoritative || e.Owner != expected.Owner || e.Adapter != expected.Adapter {
		return fmt.Errorf("CI entrypoint %q authority or owner/adapter identity does not match the canonical registry", e.ID)
	}
	return nil
}

// UnmarshalJSON 严格解码入口字段，并在校验零值前拒绝未知字段和缺字段。
func (e *CIEntrypoint) UnmarshalJSON(data []byte) error {
	if e == nil {
		return errors.New("CI entrypoint target is nil")
	}
	var wire ciEntrypointJSON
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return fmt.Errorf("decode CI entrypoint JSON: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return err
	}
	if err := wire.validatePresence(); err != nil {
		return err
	}
	decoded := CIEntrypoint{
		ID:              *wire.ID,
		AllowedSources:  append([]SourceKind(nil), (*wire.AllowedSources)...),
		AllowedProfiles: append([]Profile(nil), (*wire.AllowedProfiles)...),
		Authoritative:   *wire.Authoritative,
		Owner:           *wire.Owner,
		Adapter:         *wire.Adapter,
	}
	if err := decoded.Validate(); err != nil {
		return err
	}
	*e = decoded
	return nil
}

func (w ciEntrypointJSON) validatePresence() error {
	required := []struct {
		name    string
		present bool
	}{
		{name: "id", present: w.ID != nil},
		{name: "allowed_sources", present: w.AllowedSources != nil},
		{name: "allowed_profiles", present: w.AllowedProfiles != nil},
		{name: "authoritative", present: w.Authoritative != nil},
		{name: "owner", present: w.Owner != nil},
		{name: "adapter", present: w.Adapter != nil},
	}
	for _, field := range required {
		if !field.present {
			return fmt.Errorf("CI entrypoint JSON field %q is required", field.name)
		}
	}
	return nil
}

// Validate 校验 CI 入口 ID 是否属于固定公开集合。
func (id CIEntrypointID) Validate() error {
	switch id {
	case CIEntrypointGitPreCommit, CIEntrypointGitPrePush, CIEntrypointManualCLI, CIEntrypointRelease:
		return nil
	default:
		return fmt.Errorf("unsupported CI entrypoint id %q", id)
	}
}

// Validate 校验 owner 是否属于固定身份集合。
func (owner CIEntrypointOwner) Validate() error {
	switch owner {
	case CIEntrypointOwnerManagedGitPreCommit, CIEntrypointOwnerManagedGitPrePush,
		CIEntrypointOwnerRepositoryGitHooks, CIEntrypointOwnerManualCLI, CIEntrypointOwnerRelease:
		return nil
	default:
		return fmt.Errorf("unsupported CI entrypoint owner %q", owner)
	}
}

func (owner CIEntrypointOwner) isTrusted() bool {
	switch owner {
	case CIEntrypointOwnerManagedGitPreCommit, CIEntrypointOwnerManagedGitPrePush, CIEntrypointOwnerRelease:
		return true
	default:
		return false
	}
}

// Validate 校验 adapter 是否属于固定身份集合。
func (adapter CIEntrypointAdapter) Validate() error {
	switch adapter {
	case CIEntrypointAdapterGitPreCommit, CIEntrypointAdapterGitPrePush,
		CIEntrypointAdapterManualCLI, CIEntrypointAdapterRelease:
		return nil
	default:
		return fmt.Errorf("unsupported CI entrypoint adapter %q", adapter)
	}
}

// CIEntrypointRegistry 返回入口迁移使用的声明副本，不代表任何现有 adapter 已迁移。
func CIEntrypointRegistry() []CIEntrypoint {
	return cloneCIEntrypoints(canonicalCIEntrypoints())
}

// ResolveCIEntrypoint 返回与 source/profile 精确兼容的 canonical 入口声明。
func ResolveCIEntrypoint(
	id CIEntrypointID,
	source SourceKind,
	profile Profile,
) (CIEntrypoint, error) {
	entrypoint, ok := canonicalCIEntrypoint(id)
	if !ok {
		return CIEntrypoint{}, fmt.Errorf("canonical CI entrypoint %q is missing", id)
	}
	if err := entrypoint.Validate(); err != nil {
		return CIEntrypoint{}, err
	}
	if !slices.Contains(allSourceKinds(), source) {
		return CIEntrypoint{}, fmt.Errorf("unsupported source kind %q", source)
	}
	if err := profile.Validate(); err != nil {
		return CIEntrypoint{}, err
	}
	if !slices.Contains(entrypoint.AllowedSources, source) {
		return CIEntrypoint{}, fmt.Errorf(
			"CI entrypoint %q does not allow source kind %q",
			id,
			source,
		)
	}
	if !slices.Contains(entrypoint.AllowedProfiles, profile) {
		return CIEntrypoint{}, fmt.Errorf(
			"CI entrypoint %q does not allow profile %q",
			id,
			profile,
		)
	}
	return cloneCIEntrypoints([]CIEntrypoint{entrypoint})[0], nil
}

func canonicalCIEntrypoints() []CIEntrypoint {
	return []CIEntrypoint{
		newCIEntrypoint(CIEntrypointGitPreCommit, []SourceKind{SourceKindTree}, []Profile{ProfileLocalFast}, true, CIEntrypointOwnerManagedGitPreCommit, CIEntrypointAdapterGitPreCommit),
		newCIEntrypoint(CIEntrypointGitPrePush, []SourceKind{SourceKindRange}, []Profile{ProfilePush}, true, CIEntrypointOwnerManagedGitPrePush, CIEntrypointAdapterGitPrePush),
		newCIEntrypoint(CIEntrypointManualCLI, allSourceKinds(), allProfiles(), false, CIEntrypointOwnerManualCLI, CIEntrypointAdapterManualCLI),
		newCIEntrypoint(CIEntrypointRelease, []SourceKind{SourceKindCommit}, []Profile{ProfileRelease}, true, CIEntrypointOwnerRelease, CIEntrypointAdapterRelease),
	}
}

func newCIEntrypoint(id CIEntrypointID, sources []SourceKind, profiles []Profile, authoritative bool, owner CIEntrypointOwner, adapter CIEntrypointAdapter) CIEntrypoint {
	return CIEntrypoint{
		ID:              id,
		AllowedSources:  append([]SourceKind(nil), sources...),
		AllowedProfiles: append([]Profile(nil), profiles...),
		Authoritative:   authoritative,
		Owner:           owner,
		Adapter:         adapter,
	}
}

func canonicalCIEntrypoint(id CIEntrypointID) (CIEntrypoint, bool) {
	for _, entrypoint := range canonicalCIEntrypoints() {
		if entrypoint.ID == id {
			return entrypoint, true
		}
	}
	return CIEntrypoint{}, false
}

func allSourceKinds() []SourceKind {
	return []SourceKind{SourceKindCommit, SourceKindTree, SourceKindRange}
}

func validateSourceKindSet(sources []SourceKind) error {
	if len(sources) == 0 {
		return errors.New("allowed_sources is empty")
	}
	ordered := allSourceKinds()
	last := -1
	for _, source := range sources {
		index := slices.Index(ordered, source)
		if index < 0 {
			return fmt.Errorf("unsupported source kind %q", source)
		}
		if index <= last {
			return errors.New("allowed_sources must be unique and canonically ordered")
		}
		last = index
	}
	return nil
}

func cloneCIEntrypoints(entrypoints []CIEntrypoint) []CIEntrypoint {
	cloned := make([]CIEntrypoint, len(entrypoints))
	for index, entrypoint := range entrypoints {
		cloned[index] = entrypoint
		cloned[index].AllowedSources = append([]SourceKind(nil), entrypoint.AllowedSources...)
		cloned[index].AllowedProfiles = append([]Profile(nil), entrypoint.AllowedProfiles...)
	}
	return cloned
}
