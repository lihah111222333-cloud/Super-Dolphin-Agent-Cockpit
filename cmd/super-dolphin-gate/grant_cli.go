package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const (
	actionGrantInputMaximumBytes = 1 << 20
	releaseActionPolicy          = "github.release.create"
)

type releaseGrantOptions struct {
	Repository string
	Tag        string
	Assets     []gatecontract.ReleaseAsset
	Output     string
}

type actionGrantRuntime interface {
	Verify(context.Context, gatecontract.ActionGrant, actionGrantExpectation) error
	Consume(context.Context, gatecontract.ActionGrant, actionGrantExpectation) (gatecontract.ActionGrant, error)
	Revoke(context.Context, string) (gatecontract.ActionGrant, error)
	Expire(context.Context, string) (gatecontract.ActionGrant, error)
	Close() error
}

type productionActionGrantRuntime struct {
	service *actionGrantService
	client  coordinatorClient
}

type actionGrantRuntimeConnector func(context.Context) (actionGrantRuntime, error)

// connectProductionActionGrantRuntime 装配只读验签与 owner 管理终态的 CLI runtime。
func connectProductionActionGrantRuntime(ctx context.Context) (actionGrantRuntime, error) {
	client, err := connectProductionCoordinator(ctx)
	if err != nil {
		return nil, err
	}
	transport, ok := client.(*coordinatorTransportClient)
	if !ok {
		return nil, errors.Join(errors.New("production coordinator lacks action grant store"), client.Close())
	}
	config, err := loadProductionCoordinatorConfig()
	if err != nil {
		return nil, errors.Join(err, client.Close())
	}
	receiptAuthority, err := newProductionHookResultReceiptAuthority(ctx, config)
	if err != nil {
		return nil, errors.Join(err, client.Close())
	}
	service, err := newProductionActionGrantService(config, transport.store, receiptAuthority)
	if err != nil {
		return nil, errors.Join(err, client.Close())
	}
	return &productionActionGrantRuntime{service: service, client: client}, nil
}

// Verify 委托 authority 复验 durable issued grant。
func (runtime *productionActionGrantRuntime) Verify(
	ctx context.Context,
	grant gatecontract.ActionGrant,
	expected actionGrantExpectation,
) error {
	return runtime.service.Verify(ctx, grant, expected)
}

// Consume 委托 authority 原子消费已验签的 issued grant。
func (runtime *productionActionGrantRuntime) Consume(
	ctx context.Context,
	grant gatecontract.ActionGrant,
	expected actionGrantExpectation,
) (gatecontract.ActionGrant, error) {
	return runtime.service.Consume(ctx, grant, expected)
}

// Revoke 委托 authority 持久撤销 grant。
func (runtime *productionActionGrantRuntime) Revoke(
	ctx context.Context,
	grantID string,
) (gatecontract.ActionGrant, error) {
	return runtime.service.Revoke(ctx, grantID)
}

// Expire 委托 authority 持久标记到期 grant。
func (runtime *productionActionGrantRuntime) Expire(
	ctx context.Context,
	grantID string,
) (gatecontract.ActionGrant, error) {
	return runtime.service.Expire(ctx, grantID)
}

// Close 关闭 ActionGrant runtime 持有的 coordinator client。
func (runtime *productionActionGrantRuntime) Close() error {
	if runtime == nil || runtime.client == nil {
		return nil
	}
	return runtime.client.Close()
}

// runGrant 通过生产 connector 执行受限 grant 管理命令。
func runGrant(args []string, stdout io.Writer) error {
	return runGrantWithConnector(args, stdout, connectProductionActionGrantRuntime)
}

// runGrantWithConnector 严格分派验签、撤销与到期管理，不暴露签发入口。
func runGrantWithConnector(
	args []string,
	stdout io.Writer,
	connector actionGrantRuntimeConnector,
) error {
	if len(args) == 0 {
		return protocolError("grant subcommand is required (verify, consume-release, revoke, expire)")
	}
	switch args[0] {
	case "verify":
		return runGrantVerify(args[1:], stdout, connector)
	case "consume-release":
		return runGrantConsumeRelease(args[1:], stdout, connector)
	case "revoke", "expire":
		return runGrantTerminal(args[0], args[1:], stdout, connector)
	default:
		return protocolError("unknown grant subcommand %q", args[0])
	}
}

// runGrantConsumeRelease 以宿主重新观测的发布参数原子消费授权，参数漂移或重复消费均阻断发布。
func runGrantConsumeRelease(
	args []string,
	stdout io.Writer,
	connector actionGrantRuntimeConnector,
) error {
	remaining, options, err := extractReleaseGrantOptions(args, false)
	if err != nil {
		return err
	}
	if options == nil {
		return protocolError("consume-release requires release repository, tag, and assets")
	}
	flags := flag.NewFlagSet("grant consume-release", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	input := flags.String("input", "", "input")
	commit := flags.String("commit", "", "commit")
	tree := flags.String("tree", "", "tree")
	if err := flags.Parse(remaining); err != nil || flags.NArg() != 0 {
		return protocolError("parse grant consume-release flags")
	}
	if *input == "" || *commit == "" || *tree == "" {
		return protocolError("consume-release requires --input, --commit, and --tree")
	}
	grant, err := readActionGrant(*input)
	if err != nil {
		return protocolError("read action grant: %v", err)
	}
	expected := actionGrantExpectation{
		Audience: gatecontract.ActionAudienceRelease, RepoID: grant.Request.RepoID, InvocationID: grant.Request.InvocationID,
		SourceTreeSHA: *tree, Generation: grant.Request.Generation, ReleaseRepository: options.Repository,
		ReleaseTag: options.Tag, ReleaseCommitSHA: *commit, ReleaseAssets: append([]gatecontract.ReleaseAsset(nil), options.Assets...),
		ActionAttemptID: grant.Request.ActionAttemptID,
	}
	return withActionGrantRuntime(context.Background(), connector, func(ctx context.Context, runtime actionGrantRuntime) error {
		consumed, err := runtime.Consume(ctx, grant, expected)
		if err != nil {
			return infrastructureError("consume release action grant: %v", err)
		}
		return encodeActionGrant(stdout, consumed)
	})
}

// runGrantVerify 严格读取 signed grant 并执行当前状态复验。
func runGrantVerify(
	args []string,
	stdout io.Writer,
	connector actionGrantRuntimeConnector,
) error {
	input, err := parseRequiredFlag("grant verify", "input", args)
	if err != nil {
		return err
	}
	grant, err := readActionGrant(input)
	if err != nil {
		return protocolError("read action grant: %v", err)
	}
	expected := actionGrantExpectationFromRequest(grant.Request)
	return withActionGrantRuntime(context.Background(), connector, func(ctx context.Context, runtime actionGrantRuntime) error {
		if err := runtime.Verify(ctx, grant, expected); err != nil {
			return infrastructureError("verify action grant: %v", err)
		}
		return encodeActionGrant(stdout, grant)
	})
}

// runGrantTerminal 分派 revoke 或 expire 的 owner 管理终态。
func runGrantTerminal(
	command string,
	args []string,
	stdout io.Writer,
	connector actionGrantRuntimeConnector,
) error {
	grantID, err := parseRequiredFlag("grant "+command, "id", args)
	if err != nil {
		return err
	}
	return withActionGrantRuntime(context.Background(), connector, func(ctx context.Context, runtime actionGrantRuntime) error {
		var grant gatecontract.ActionGrant
		var actionErr error
		if command == "revoke" {
			grant, actionErr = runtime.Revoke(ctx, grantID)
		} else {
			grant, actionErr = runtime.Expire(ctx, grantID)
		}
		if actionErr != nil {
			return infrastructureError("%s action grant: %v", command, actionErr)
		}
		return encodeActionGrant(stdout, grant)
	})
}

// withActionGrantRuntime 保证 CLI 动作与 coordinator 关闭错误同时保留。
func withActionGrantRuntime(
	ctx context.Context,
	connector actionGrantRuntimeConnector,
	action func(context.Context, actionGrantRuntime) error,
) error {
	runtime, err := connector(ctx)
	if err != nil {
		return infrastructureError("connect action grant runtime: %v", err)
	}
	return errors.Join(action(ctx, runtime), runtime.Close())
}

// readActionGrant 有界读取并严格解码 signed grant 文件。
func readActionGrant(path string) (gatecontract.ActionGrant, error) {
	file, err := os.Open(path)
	if err != nil {
		return gatecontract.ActionGrant{}, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, actionGrantInputMaximumBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return gatecontract.ActionGrant{}, errors.Join(readErr, closeErr)
	}
	if len(data) > actionGrantInputMaximumBytes {
		return gatecontract.ActionGrant{}, errors.New("action grant input exceeds size limit")
	}
	var grant gatecontract.ActionGrant
	if err := gatecontract.DecodeStrictJSON(data, &grant); err != nil {
		return gatecontract.ActionGrant{}, err
	}
	return grant, nil
}

// actionGrantExpectationFromRequest 为审计验签重建全部签名动作字段。
func actionGrantExpectationFromRequest(request gatecontract.GrantRequest) actionGrantExpectation {
	return actionGrantExpectation{
		Audience: request.Audience, RepoID: request.RepoID, InvocationID: request.InvocationID,
		SourceTreeSHA: request.SourceTreeSHA, Generation: request.Generation, RemoteURL: request.RemoteURL,
		Ref: request.Ref, OldSHA: request.OldSHA, NewSHA: request.NewSHA,
		ReleaseRepository: request.ReleaseRepository, ReleaseTag: request.ReleaseTag,
		ReleaseCommitSHA: request.ReleaseCommitSHA, ReleaseAssets: append([]gatecontract.ReleaseAsset(nil), request.ReleaseAssets...),
		ActionAttemptID: request.ActionAttemptID,
	}
}

// encodeActionGrant 输出规范缩进的授权状态 JSON。
func encodeActionGrant(stdout io.Writer, grant gatecontract.ActionGrant) error {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(grant); err != nil {
		return fmt.Errorf("encode action grant: %w", err)
	}
	return nil
}

// extractReleaseGrantOptions 从共享命令行中提取完整发布授权参数，缺项或重复项立即失败。
func extractReleaseGrantOptions(args []string, requireOutput bool) ([]string, *releaseGrantOptions, error) {
	remaining := make([]string, 0, len(args))
	options := &releaseGrantOptions{}
	sawReleaseOption := false
	for index := 0; index < len(args); index++ {
		name := args[index]
		if !isReleaseGrantOption(name) {
			remaining = append(remaining, name)
			continue
		}
		if index+1 == len(args) {
			return nil, nil, protocolError("%s requires a value", name)
		}
		index++
		sawReleaseOption = true
		if err := applyReleaseGrantOption(options, name, args[index]); err != nil {
			return nil, nil, err
		}
	}
	if !sawReleaseOption {
		return remaining, nil, nil
	}
	if err := options.normalize(requireOutput); err != nil {
		return nil, nil, protocolError("release grant options: %v", err)
	}
	return remaining, options, nil
}

func isReleaseGrantOption(name string) bool {
	switch name {
	case "--release-repository", "--release-tag", "--release-asset", "--release-grant-output":
		return true
	default:
		return false
	}
}

// applyReleaseGrantOption 写入单个 release grant 参数，并拒绝唯一参数的重复声明。
func applyReleaseGrantOption(options *releaseGrantOptions, name, value string) error {
	switch name {
	case "--release-repository":
		if options.Repository != "" {
			return protocolError("--release-repository must appear once")
		}
		options.Repository = value
	case "--release-tag":
		if options.Tag != "" {
			return protocolError("--release-tag must appear once")
		}
		options.Tag = value
	case "--release-asset":
		asset, err := parseReleaseAsset(value)
		if err != nil {
			return protocolError("parse --release-asset: %v", err)
		}
		options.Assets = append(options.Assets, asset)
	case "--release-grant-output":
		if options.Output != "" {
			return protocolError("--release-grant-output must appear once")
		}
		options.Output = value
	}
	return nil
}

func parseReleaseAsset(value string) (gatecontract.ReleaseAsset, error) {
	parts := strings.Split(value, "|")
	if len(parts) != 3 {
		return gatecontract.ReleaseAsset{}, errors.New("must be name|sha256:<digest>|size")
	}
	size, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return gatecontract.ReleaseAsset{}, fmt.Errorf("size: %w", err)
	}
	asset := gatecontract.ReleaseAsset{Name: parts[0], SHA256: parts[1], Size: size}
	if err := asset.Validate(); err != nil {
		return gatecontract.ReleaseAsset{}, err
	}
	return asset, nil
}

// normalize 规范化资产顺序并拒绝不完整、重复或消费端越权携带的输出参数。
func (options *releaseGrantOptions) normalize(requireOutput bool) error {
	if options == nil {
		return errors.New("options are required")
	}
	if err := options.validateRequiredFields(requireOutput); err != nil {
		return err
	}
	sort.Slice(options.Assets, func(left, right int) bool { return options.Assets[left].Name < options.Assets[right].Name })
	for index, asset := range options.Assets {
		if err := asset.Validate(); err != nil {
			return err
		}
		if index > 0 && options.Assets[index-1].Name == asset.Name {
			return fmt.Errorf("duplicate asset name %q", asset.Name)
		}
	}
	return nil
}

// validateRequiredFields 校验签发和消费场景各自必需且允许的 release grant 字段。
func (options *releaseGrantOptions) validateRequiredFields(requireOutput bool) error {
	if options.Repository == "" || options.Tag == "" || len(options.Assets) == 0 {
		return errors.New("repository, tag, at least one asset, and grant output are required")
	}
	if requireOutput && options.Output == "" {
		return errors.New("repository, tag, at least one asset, and grant output are required")
	}
	if !requireOutput && options.Output != "" {
		return errors.New("grant output is not accepted here")
	}
	return nil
}

func (options releaseGrantOptions) expected(plan gatecontract.GatePlan) (actionGrantExpectation, error) {
	if plan.Source.Kind != gatecontract.SourceKindCommit || plan.Source.Commit == nil {
		return actionGrantExpectation{}, errors.New("release grant requires a commit plan source")
	}
	return actionGrantExpectation{
		Audience: gatecontract.ActionAudienceRelease, SourceTreeSHA: plan.Source.SourceTreeSHA,
		ReleaseRepository: options.Repository, ReleaseTag: options.Tag, ReleaseCommitSHA: plan.Source.Commit.SHA,
		ReleaseAssets: append([]gatecontract.ReleaseAsset(nil), options.Assets...),
	}, nil
}

// issueReleaseActionGrant 只依据通过的 release receipt 签发短期单次授权，receipt 与当前 commit/tree 不一致时拒绝签发。
func issueReleaseActionGrant(
	ctx context.Context,
	service *actionGrantService,
	receipt gatecontract.ResultReceipt,
	plan gatecontract.GatePlan,
	options releaseGrantOptions,
) (gatecontract.ActionGrant, error) {
	expected, err := options.expected(plan)
	if err != nil {
		return gatecontract.ActionGrant{}, err
	}
	if releaseReceiptDrifted(receipt, expected) {
		return gatecontract.ActionGrant{}, errors.New("passed receipt does not match the release grant source")
	}
	attemptID, err := newReleaseActionAttemptID()
	if err != nil {
		return gatecontract.ActionGrant{}, err
	}
	return service.Issue(ctx, actionGrantIntent{
		Receipt: receipt, InvocationOwner: string(gatecontract.CIEntrypointOwnerRelease), Audience: gatecontract.ActionAudienceRelease,
		ActionPolicy: releaseActionPolicy, ReleaseRepository: options.Repository, ReleaseTag: options.Tag,
		ReleaseCommitSHA: expected.ReleaseCommitSHA, ReleaseAssets: expected.ReleaseAssets,
		ActionAttemptID: attemptID, RequestNonce: releaseGrantNonce(receipt, expected, attemptID),
	})
}

// waitAndIssueProductionReleaseGrant 只在权威终态通过后读取 receipt 并落盘授权，任一生产依赖缺失即失败。
func waitAndIssueProductionReleaseGrant(
	ctx context.Context,
	client coordinatorClient,
	stdout io.Writer,
	config productionCoordinatorConfig,
	jobID string,
	plan gatecontract.GatePlan,
	options releaseGrantOptions,
) error {
	terminal, err := client.Wait(ctx, jobID)
	if err != nil {
		return infrastructureError("wait authoritative release gate job: %v", err)
	}
	if terminal.State != jobStatePassed {
		return encodeTerminalStatus(stdout, terminal)
	}
	transport, ok := client.(*coordinatorTransportClient)
	if !ok {
		return infrastructureError("release grant requires the production coordinator transport")
	}
	receipt, err := transport.ResultReceipt(ctx, terminal.JobID)
	if err != nil {
		return infrastructureError("load authoritative release receipt: %v", err)
	}
	authority, err := newProductionHookResultReceiptAuthority(ctx, config)
	if err != nil {
		return infrastructureError("load release grant receipt authority: %v", err)
	}
	grants, err := newProductionActionGrantService(config, transport.store, authority)
	if err != nil {
		return infrastructureError("load release action grant authority: %v", err)
	}
	grant, err := issueReleaseActionGrant(ctx, grants, receipt, plan, options)
	if err != nil {
		return infrastructureError("issue release action grant: %v", err)
	}
	if err := writeActionGrantExclusive(options.Output, grant); err != nil {
		return infrastructureError("write release action grant: %v", err)
	}
	return encodeTerminalStatus(stdout, terminal)
}

func releaseReceiptDrifted(receipt gatecontract.ResultReceipt, expected actionGrantExpectation) bool {
	return receipt.Entrypoint != gatecontract.CIEntrypointRelease ||
		receipt.Source.Kind != gatecontract.SourceKindCommit || receipt.Source.Commit == nil ||
		receipt.Source.Commit.SHA != expected.ReleaseCommitSHA || receipt.Source.SourceTreeSHA != expected.SourceTreeSHA
}

func newReleaseActionAttemptID() (string, error) {
	randomBytes := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, randomBytes); err != nil {
		return "", fmt.Errorf("read release action attempt entropy: %w", err)
	}
	return "attempt:v1" + hex.EncodeToString(randomBytes), nil
}

func releaseGrantNonce(receipt gatecontract.ResultReceipt, expected actionGrantExpectation, attemptID string) string {
	parts := []string{receipt.ReceiptID, receipt.InvocationID, expected.ReleaseRepository, expected.ReleaseTag,
		expected.ReleaseCommitSHA, expected.SourceTreeSHA, attemptID}
	for _, asset := range expected.ReleaseAssets {
		parts = append(parts, asset.Name, asset.SHA256, strconv.FormatInt(asset.Size, 10))
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return fmt.Sprintf("sha256:%x", sum)
}

func writeActionGrantExclusive(path string, grant gatecontract.ActionGrant) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	writeErr := encoder.Encode(grant)
	closeErr := file.Close()
	return errors.Join(writeErr, closeErr)
}
