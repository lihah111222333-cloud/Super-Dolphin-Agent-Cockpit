package gatehook

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const maxPrePushLineBytes = 16 * 1024

type prePushUpdate struct {
	localRef  string
	localSHA  string
	remoteRef string
	remoteSHA string
}

// NormalizePreCommit 将活动 worktree 的 index 固定为显式 tree+parent submit 请求。
func NormalizePreCommit(ctx context.Context, cwd, hookInvocationID string) (Request, error) {
	repository, err := resolveGitRepository(ctx, cwd)
	if err != nil {
		return Request{}, err
	}
	treeSHA, err := repository.stableIndexTree(ctx)
	if err != nil {
		return Request{}, err
	}
	parentSHA, err := repository.headCommit(ctx)
	if err != nil {
		return Request{}, err
	}
	invocation, err := gitInvocationIdentity(
		gatecontract.CIEntrypointGitPreCommit,
		repository.identity,
		hookInvocationID,
		"index",
	)
	if err != nil {
		return Request{}, err
	}
	submit := SubmitRequest{
		Entrypoint: gatecontract.CIEntrypointGitPreCommit,
		Profile:    gatecontract.ProfileLocalFast,
		Repository: repository.identity,
		Invocation: invocation,
		Source:     treeSource(repository.identity.ObjectFormat, treeSHA, parentSHA),
	}
	request := Request{Kind: RequestKindSubmit, Submit: &submit}
	return request, request.Validate()
}

// NormalizePrePush 将每条 Git pre-push stdin 更新固定为 exact range submit 请求。
func NormalizePrePush(ctx context.Context, cwd, hookInvocationID string, input io.Reader) ([]Request, error) {
	repository, err := resolveGitRepository(ctx, cwd)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(hookInvocationID) == "" {
		return nil, errors.New("hook invocation id is required")
	}
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 1024), maxPrePushLineBytes)
	requests := make([]Request, 0, 1)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			return nil, fmt.Errorf("pre-push stdin line %d is empty", lineNumber)
		}
		request, normalizeErr := normalizePrePushLine(ctx, repository, hookInvocationID, lineNumber, line)
		if normalizeErr != nil {
			return nil, fmt.Errorf("pre-push stdin line %d: %w", lineNumber, normalizeErr)
		}
		requests = append(requests, request)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read pre-push stdin: %w", err)
	}
	if len(requests) == 0 {
		return nil, errors.New("pre-push stdin contains no ref updates")
	}
	return requests, nil
}

// normalizePrePushLine 将一条四字段更新绑定到活动 worktree HEAD。
func normalizePrePushLine(
	ctx context.Context,
	repository gitRepository,
	hookInvocationID string,
	lineNumber int,
	line string,
) (Request, error) {
	update, zeroOID, err := parsePrePushUpdate(ctx, repository, line)
	if err != nil {
		return Request{}, err
	}
	treeSHA, err := validateLocalPushHead(ctx, repository, update)
	if err != nil {
		return Request{}, err
	}
	rangeSource, err := buildRangeSource(
		ctx, repository, update.localRef, update.localSHA, update.remoteRef, update.remoteSHA, zeroOID,
	)
	if err != nil {
		return Request{}, err
	}
	source := gatecontract.SourceSpec{
		Kind:          gatecontract.SourceKindRange,
		ObjectFormat:  repository.identity.ObjectFormat,
		Range:         &rangeSource,
		SourceTreeSHA: treeSHA,
	}
	invocation, err := gitInvocationIdentity(
		gatecontract.CIEntrypointGitPrePush,
		repository.identity,
		hookInvocationID,
		fmt.Sprintf("line:%d:%s:%s", lineNumber, update.localRef, update.remoteRef),
	)
	if err != nil {
		return Request{}, err
	}
	submit := SubmitRequest{
		Entrypoint: gatecontract.CIEntrypointGitPrePush,
		Profile:    gatecontract.ProfilePush,
		Repository: repository.identity,
		Invocation: invocation,
		Source:     source,
	}
	request := Request{Kind: RequestKindSubmit, Submit: &submit}
	return request, request.Validate()
}

// parsePrePushUpdate 严格解析四字段 stdin 并拒绝 delete 策略外输入。
func parsePrePushUpdate(ctx context.Context, repository gitRepository, line string) (prePushUpdate, string, error) {
	fields := strings.Fields(line)
	if len(fields) != 4 {
		return prePushUpdate{}, "", fmt.Errorf(
			"expected local-ref local-sha remote-ref remote-sha, got %d fields", len(fields),
		)
	}
	update := prePushUpdate{localRef: fields[0], localSHA: fields[1], remoteRef: fields[2], remoteSHA: fields[3]}
	zeroOID, err := gatecontract.ZeroOID(repository.identity.ObjectFormat)
	if err != nil {
		return prePushUpdate{}, "", err
	}
	if err := validateCanonicalOID("local_sha", update.localSHA, zeroOID); err != nil {
		return prePushUpdate{}, "", err
	}
	if err := validateCanonicalOID("remote_sha", update.remoteSHA, zeroOID); err != nil {
		return prePushUpdate{}, "", err
	}
	if update.localSHA == zeroOID {
		return prePushUpdate{}, "", errors.New("delete updates require a separate Git policy and are not accepted")
	}
	if err := validateFullRef(ctx, repository, "local_ref", update.localRef); err != nil {
		return prePushUpdate{}, "", err
	}
	if err := validateFullRef(ctx, repository, "remote_ref", update.remoteRef); err != nil {
		return prePushUpdate{}, "", err
	}
	return update, zeroOID, nil
}

// validateCanonicalOID 拒绝参数样式、大小写漂移与 object-format 长度错配。
func validateCanonicalOID(name, oid, zeroOID string) error {
	if len(oid) != len(zeroOID) {
		return fmt.Errorf("%s must be a %d-character canonical Git OID", name, len(zeroOID))
	}
	for _, character := range oid {
		if !isLowerHex(character) {
			return fmt.Errorf("%s must contain only lowercase hexadecimal characters", name)
		}
	}
	return nil
}

// validateLocalPushHead 证明 stdin local ref、SHA 与活动 worktree HEAD 完全一致。
func validateLocalPushHead(ctx context.Context, repository gitRepository, update prePushUpdate) (string, error) {
	resolvedLocalSHA, err := repository.resolveCommit(ctx, update.localRef)
	if err != nil {
		return "", fmt.Errorf("resolve local ref %q: %w", update.localRef, err)
	}
	if resolvedLocalSHA != update.localSHA {
		return "", fmt.Errorf(
			"local ref %q resolves to %s, stdin supplied %s", update.localRef, resolvedLocalSHA, update.localSHA,
		)
	}
	headSHA, err := repository.headCommit(ctx)
	if err != nil {
		return "", err
	}
	if headSHA != update.localSHA {
		return "", fmt.Errorf("pre-push local sha %s does not equal active worktree HEAD %s", update.localSHA, headSHA)
	}
	treeSHA, err := repository.commitTree(ctx, update.localSHA)
	if err != nil {
		return "", err
	}
	return treeSHA, nil
}

// buildRangeSource 根据 observed remote commit 计算 create、fast-forward 或 force。
func buildRangeSource(
	ctx context.Context,
	repository gitRepository,
	localRef string,
	localSHA string,
	remoteRef string,
	remoteSHA string,
	zeroOID string,
) (gatecontract.RangeSource, error) {
	source := gatecontract.RangeSource{
		HeadSHA:           localSHA,
		LocalRef:          localRef,
		RemoteRef:         remoteRef,
		ObservedRemoteSHA: remoteSHA,
	}
	if remoteSHA == zeroOID {
		source.BaseKind = gatecontract.BaseKindEmptyTree
		source.UpdateKind = gatecontract.UpdateKindCreate
		return source, nil
	}
	if err := repository.verifyObject(ctx, remoteSHA, "commit"); err != nil {
		return gatecontract.RangeSource{}, fmt.Errorf("verify observed remote commit: %w", err)
	}
	source.BaseKind = gatecontract.BaseKindCommit
	source.BaseSHA = remoteSHA
	fastForward, err := repository.isAncestor(ctx, remoteSHA, localSHA)
	if err != nil {
		return gatecontract.RangeSource{}, err
	}
	if fastForward {
		source.UpdateKind = gatecontract.UpdateKindFastForward
	} else {
		source.UpdateKind = gatecontract.UpdateKindForce
	}
	return source, nil
}

func validateFullRef(ctx context.Context, repository gitRepository, name, ref string) error {
	if !strings.HasPrefix(ref, "refs/") {
		return fmt.Errorf("%s must be a full ref", name)
	}
	if _, err := runGit(ctx, repository.identity.WorktreeRoot, nil, "check-ref-format", ref); err != nil {
		return fmt.Errorf("%s %q is invalid: %w", name, ref, err)
	}
	return nil
}

func gitInvocationIdentity(
	entrypoint gatecontract.CIEntrypointID,
	repository RepositoryIdentity,
	hookInvocationID string,
	suffix string,
) (InvocationIdentity, error) {
	if strings.TrimSpace(hookInvocationID) == "" {
		return InvocationIdentity{}, errors.New("hook invocation id is required")
	}
	if strings.TrimSpace(suffix) == "" {
		return InvocationIdentity{}, errors.New("hook invocation suffix is required")
	}
	identity := InvocationIdentity{
		Owner: sha256Identity("gatehook/git-owner/v1", string(entrypoint), repository.CommonDir, repository.WorktreeRoot),
		Key:   sha256Identity("gatehook/git-invocation/v1", string(entrypoint), hookInvocationID, suffix),
	}
	return identity, identity.Validate()
}
