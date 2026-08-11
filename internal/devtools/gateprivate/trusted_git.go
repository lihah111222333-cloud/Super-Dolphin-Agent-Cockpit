package gateprivate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

const defaultTrustedGitBinary = "/usr/bin/git"

// CandidateObjectAuthority is the single, explicitly captured object-database
// routing proof for a private staged candidate. It is captured before the
// trusted Git environment is cleaned and never re-read from ambient variables.
type CandidateObjectAuthority struct {
	objectDirectory string
	alternates      []string
	digest          string
}

// CaptureCandidateObjectAuthority reads the only ambient Git object-routing
// variables allowed at the outer candidate boundary and converts them into a
// validated value. A partial or malformed routing configuration is rejected.
func CaptureCandidateObjectAuthority() (CandidateObjectAuthority, error) {
	objectDirectory, hasObjectDirectory := os.LookupEnv("GIT_OBJECT_DIRECTORY")
	alternates, hasAlternates := os.LookupEnv("GIT_ALTERNATE_OBJECT_DIRECTORIES")
	if !hasObjectDirectory && !hasAlternates {
		return CandidateObjectAuthority{}, nil
	}
	if !hasObjectDirectory {
		return CandidateObjectAuthority{}, errors.New("candidate Git alternate object directories require GIT_OBJECT_DIRECTORY")
	}
	primary, err := canonicalCandidateObjectDirectory(objectDirectory)
	if err != nil {
		return CandidateObjectAuthority{}, err
	}
	resolvedAlternates, err := canonicalCandidateObjectAlternates(alternates, hasAlternates, primary)
	if err != nil {
		return CandidateObjectAuthority{}, err
	}
	digest, err := candidateObjectAuthorityDigest(primary, resolvedAlternates)
	if err != nil {
		return CandidateObjectAuthority{}, err
	}
	return CandidateObjectAuthority{objectDirectory: primary, alternates: resolvedAlternates, digest: digest}, nil
}

// IsZero reports whether the candidate is wholly available from the canonical
// repository object database and therefore needs no private routing proof.
func (authority CandidateObjectAuthority) IsZero() bool {
	return authority.objectDirectory == "" && len(authority.alternates) == 0 && authority.digest == ""
}

// Digest returns the receipt-bound, path-redacted authority proof.
func (authority CandidateObjectAuthority) Digest() (string, error) {
	if authority.IsZero() {
		return "", nil
	}
	if err := authority.reverify(); err != nil {
		return "", err
	}
	digest, err := candidateObjectAuthorityDigest(authority.objectDirectory, authority.alternates)
	if err != nil {
		return "", err
	}
	if digest != authority.digest {
		return "", errors.New("candidate Git object authority digest drifted")
	}
	return digest, nil
}

func (authority CandidateObjectAuthority) environment() ([]string, error) {
	if authority.IsZero() {
		return nil, nil
	}
	if _, err := authority.Digest(); err != nil {
		return nil, err
	}
	environment := []string{"GIT_OBJECT_DIRECTORY=" + authority.objectDirectory}
	if len(authority.alternates) != 0 {
		environment = append(environment, "GIT_ALTERNATE_OBJECT_DIRECTORIES="+strings.Join(authority.alternates, string(filepath.ListSeparator)))
	}
	return environment, nil
}

func (authority CandidateObjectAuthority) reverify() error {
	if authority.IsZero() {
		return nil
	}
	primary, err := canonicalCandidateObjectDirectory(authority.objectDirectory)
	if err != nil || primary != authority.objectDirectory {
		return errors.New("candidate Git object directory drifted")
	}
	_, err = canonicalCandidateObjectAlternates(strings.Join(authority.alternates, string(filepath.ListSeparator)), true, authority.objectDirectory)
	return err
}

func canonicalCandidateObjectAlternates(value string, present bool, primary string) ([]string, error) {
	if !present {
		return nil, nil
	}
	if value == "" {
		return nil, errors.New("candidate Git alternate object directories are empty")
	}
	seen := map[string]struct{}{primary: {}}
	entries := strings.Split(value, string(filepath.ListSeparator))
	resolved := make([]string, 0, len(entries))
	for _, entry := range entries {
		canonical, err := canonicalCandidateObjectDirectory(entry)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[canonical]; duplicate {
			return nil, errors.New("candidate Git object directories contain a duplicate")
		}
		seen[canonical] = struct{}{}
		resolved = append(resolved, canonical)
	}
	slices.Sort(resolved)
	return resolved, nil
}

func canonicalCandidateObjectDirectory(value string) (string, error) {
	if value == "" || strings.ContainsAny(value, "\x00\r\n") || !filepath.IsAbs(value) {
		return "", errors.New("candidate Git object directory must be an absolute clean path")
	}
	canonical, err := filepath.EvalSymlinks(value)
	if err != nil {
		return "", errors.New("candidate Git object directory is not resolvable")
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return "", errors.New("candidate Git object directory is not a directory")
	}
	return canonical, nil
}

func candidateObjectAuthorityDigest(objectDirectory string, alternates []string) (string, error) {
	payload, err := json.Marshal(struct {
		Domain     string   `json:"domain"`
		ObjectDir  string   `json:"object_dir"`
		Alternates []string `json:"alternates"`
	}{Domain: "local-candidate-git-object-authority/v1", ObjectDir: objectDirectory, Alternates: alternates})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// TrustedGitCommand returns the gate-owned Git invocation for exact-object
// reads. It does not inherit the caller environment: repository/object routing,
// replacement refs, configuration, HOME, and PATH must not change an exact OID.
func TrustedGitCommand(ctx context.Context, repository string, args ...string) (*exec.Cmd, error) {
	return TrustedGitCommandWithCandidateObjectAuthority(ctx, repository, CandidateObjectAuthority{}, args...)
}

// TrustedGitCommandWithCandidateObjectAuthority returns a trusted exact-object
// invocation with only a previously validated candidate ODB proof appended to
// the clean environment.
func TrustedGitCommandWithCandidateObjectAuthority(ctx context.Context, repository string, authority CandidateObjectAuthority, args ...string) (*exec.Cmd, error) {
	git, err := TrustedGitBinary()
	if err != nil {
		return nil, err
	}
	commandArgs := make([]string, 0, len(args)+3)
	commandArgs = append(commandArgs, "--no-replace-objects", "-C", repository)
	commandArgs = append(commandArgs, args...)
	command := exec.CommandContext(ctx, git, commandArgs...)
	command.Env = TrustedGitEnvironment()
	authorityEnvironment, err := authority.environment()
	if err != nil {
		return nil, err
	}
	command.Env = append(command.Env, authorityEnvironment...)
	return command, nil
}

// TrustedGitBinary resolves only the explicitly configured gate binary or the
// fixed system binary. It never resolves through ambient PATH.
func TrustedGitBinary() (string, error) {
	git := os.Getenv("SUPER_DOLPHIN_GATE_GIT")
	if git == "" {
		git = defaultTrustedGitBinary
	}
	return CanonicalRootExecutable("trusted gate Git", git)
}

// TrustedGitEnvironment is the sole environment for exact Git object reads.
// Do not append os.Environ: all repository/object redirectors must stay absent.
func TrustedGitEnvironment() []string {
	return []string{
		"GIT_NO_REPLACE_OBJECTS=1",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=/bin/false",
		"GIT_OPTIONAL_LOCKS=0",
		"LC_ALL=C",
	}
}
