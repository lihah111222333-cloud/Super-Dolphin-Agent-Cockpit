// Package main provides the scheduler-only Super Dolphin gate CLI boundary.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

var errSchedulerNotWired = gatecontract.WithExitCode(gatecontract.ExitInfrastructure, errors.New("scheduler client not wired"))

func main() {
	if productionBootstrapRunnerProgram(os.Args[0]) {
		if err := runProductionBootstrapRunnerCLI(os.Args[1:], os.Stdout); err != nil {
			_ = writeCLIError(os.Stderr, err)
			os.Exit(int(gatecontract.ExitCodeOf(err)))
		}
		return
	}
	os.Exit(runCLI(os.Args[1:], os.Stdout, os.Stderr))
}

func runCLI(args []string, stdout, stderr io.Writer) int {
	err := dispatchCLI(args, stdout)
	if err == nil {
		return int(gatecontract.ExitOK)
	}
	exitCode := gatecontract.ExitCodeOf(err)
	if writeErr := writeCLIError(stderr, err); writeErr != nil {
		return int(gatecontract.ExitInfrastructure)
	}
	return int(exitCode)
}

func writeCLIError(stderr io.Writer, commandErr error) error {
	if _, err := fmt.Fprintf(stderr, "super-dolphin-gate: %v\n", commandErr); err != nil {
		return fmt.Errorf("write CLI error: %w", err)
	}
	return nil
}

// dispatchCLI 将固定命令面分派到 plan 或未接线 scheduler 边界。
func dispatchCLI(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return protocolError("subcommand is required (plan, submit, run, status, wait, receipt verify, grant, provision)")
	}
	switch args[0] {
	case "plan":
		return runPlan(args[1:], stdout)
	case "hook":
		return runHook(args[1:], os.Stdin, stdout)
	case "bootstrap":
		return runProductionBootstrapControllerCLI(args[1:], os.Stdin, stdout)
	case "provision":
		return runProductionProvisionCLI(args[1:], stdout)
	default:
		return dispatchCoordinatorCLI(args, stdout)
	}
}

// dispatchCoordinatorCLI 分派既有 coordinator 与 receipt 命令。
func dispatchCoordinatorCLI(args []string, stdout io.Writer) error {
	switch args[0] {
	case "submit":
		return runSubmit(args[1:], stdout)
	case "run":
		if _, err := parseRequiredFlag("run", "job-token", args[1:]); err != nil {
			return err
		}
		return errSchedulerNotWired
	case "status":
		return runStatus(args[1:], stdout)
	case "wait":
		return runWait(args[1:], stdout)
	case "_owner":
		return runOwnerProcess(args[1:], stdout)
	case "receipt":
		return runReceipt(args[1:])
	case "grant":
		return runGrant(args[1:], stdout)
	default:
		return protocolError("unknown subcommand %q", args[0])
	}
}

func runPlan(args []string, stdout io.Writer) error {
	plan, err := parsePlan(args)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(plan); err != nil {
		return gatecontract.WithExitCode(gatecontract.ExitInfrastructure, fmt.Errorf("encode plan JSON: %w", err))
	}
	return nil
}

func runReceipt(args []string) error {
	if len(args) == 0 || args[0] != "verify" {
		return protocolError("receipt subcommand must be verify")
	}
	if _, err := parseRequiredFlag("receipt verify", "input", args[1:]); err != nil {
		return err
	}
	return errSchedulerNotWired
}

type planFlags struct {
	profile        string
	objectFormat   string
	sourceTree     string
	commit         string
	tree           string
	parent         string
	baseKind       string
	base           string
	head           string
	localRef       string
	remoteRef      string
	observedRemote string
	updateKind     string
}

// parsePlan 严格解析 profile 与唯一 SourceSpec，并生成 canonical plan。
func parsePlan(args []string) (gatecontract.GatePlan, error) {
	options := planFlags{}
	flags := flag.NewFlagSet("plan", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	registerPlanFlags(flags, &options)
	if err := flags.Parse(args); err != nil {
		return gatecontract.GatePlan{}, protocolError("parse plan flags: %v", err)
	}
	if flags.NArg() != 0 {
		return gatecontract.GatePlan{}, protocolError("unexpected positional arguments: %v", flags.Args())
	}
	profile := gatecontract.Profile(options.profile)
	if err := profile.Validate(); err != nil {
		return gatecontract.GatePlan{}, protocolError("%v", err)
	}
	source, err := options.sourceSpec()
	if err != nil {
		return gatecontract.GatePlan{}, err
	}
	plan, err := gatecontract.BuildGatePlan(profile, source)
	if err != nil {
		return gatecontract.GatePlan{}, gatecontract.WithExitCode(gatecontract.ExitRegistryInvariant, err)
	}
	return plan, nil
}

func registerPlanFlags(flags *flag.FlagSet, options *planFlags) {
	flags.StringVar(&options.profile, "profile", "", "gate profile")
	flags.StringVar(&options.objectFormat, "object-format", "", "Git object format")
	flags.StringVar(&options.sourceTree, "source-tree", "", "normalized source tree OID")
	flags.StringVar(&options.commit, "commit", "", "commit OID")
	flags.StringVar(&options.tree, "tree", "", "tree OID")
	flags.StringVar(&options.parent, "parent", "", "optional tree parent commit OID")
	flags.StringVar(&options.baseKind, "base-kind", "", "range base kind")
	flags.StringVar(&options.base, "base", "", "range base commit OID")
	flags.StringVar(&options.head, "head", "", "range head commit OID")
	flags.StringVar(&options.localRef, "local-ref", "", "range local ref")
	flags.StringVar(&options.remoteRef, "remote-ref", "", "range remote ref")
	flags.StringVar(&options.observedRemote, "observed-remote", "", "observed remote OID")
	flags.StringVar(&options.updateKind, "update-kind", "", "range update kind")
}

// sourceSpec 将互斥 CLI flags 转为严格 SourceSpec。
func (o planFlags) sourceSpec() (gatecontract.SourceSpec, error) {
	commitSet := o.commit != ""
	treeSet := o.tree != "" || o.parent != ""
	rangeSet := o.rangeRequested()
	if boolCount(commitSet, treeSet, rangeSet) != 1 {
		return gatecontract.SourceSpec{}, sourceError("exactly one commit, tree, or range source is required")
	}
	spec := gatecontract.SourceSpec{ObjectFormat: gatecontract.GitObjectFormat(o.objectFormat), SourceTreeSHA: o.sourceTree}
	switch {
	case commitSet:
		spec.Kind = gatecontract.SourceKindCommit
		spec.Commit = &gatecontract.CommitSource{SHA: o.commit}
	case treeSet:
		spec.Kind = gatecontract.SourceKindTree
		spec.Tree = &gatecontract.TreeSource{SHA: o.tree, ParentCommitSHA: o.parent}
	case rangeSet:
		spec.Kind = gatecontract.SourceKindRange
		spec.Range = &gatecontract.RangeSource{
			BaseKind: gatecontract.BaseKind(o.baseKind), BaseSHA: o.base, HeadSHA: o.head,
			LocalRef: o.localRef, RemoteRef: o.remoteRef, ObservedRemoteSHA: o.observedRemote,
			UpdateKind: gatecontract.UpdateKind(o.updateKind),
		}
	}
	if err := spec.Validate(); err != nil {
		return gatecontract.SourceSpec{}, sourceError("%v", err)
	}
	return spec, nil
}

func (o planFlags) rangeRequested() bool {
	values := []string{o.baseKind, o.base, o.head, o.localRef, o.remoteRef, o.observedRemote, o.updateKind}
	for _, value := range values {
		if value != "" {
			return true
		}
	}
	return false
}

func parseRequiredFlag(command, name string, args []string) (string, error) {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	value := flags.String(name, "", name)
	if err := flags.Parse(args); err != nil {
		return "", protocolError("parse %s flags: %v", command, err)
	}
	if flags.NArg() != 0 {
		return "", protocolError("unexpected positional arguments: %v", flags.Args())
	}
	if strings.TrimSpace(*value) == "" {
		return "", protocolError("--%s is required", name)
	}
	return *value, nil
}

func boolCount(values ...bool) int {
	count := 0
	for _, value := range values {
		if value {
			count++
		}
	}
	return count
}

func protocolError(format string, args ...any) error {
	return gatecontract.WithExitCode(gatecontract.ExitProtocol, fmt.Errorf(format, args...))
}

func sourceError(format string, args ...any) error {
	return gatecontract.WithExitCode(gatecontract.ExitSourceMismatch, fmt.Errorf(format, args...))
}
