package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const actionGrantInputMaximumBytes = 1 << 20

type actionGrantRuntime interface {
	Verify(context.Context, gatecontract.ActionGrant, actionGrantExpectation) error
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
		return protocolError("grant subcommand is required (verify, revoke, expire)")
	}
	switch args[0] {
	case "verify":
		return runGrantVerify(args[1:], stdout, connector)
	case "revoke", "expire":
		return runGrantTerminal(args[0], args[1:], stdout, connector)
	default:
		return protocolError("unknown grant subcommand %q", args[0])
	}
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
