package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sourceexport"
)

const (
	remoteBaselineRefreshResultSchemaVersion uint32 = 2
	remoteBaselineRefreshDeadline                   = 90 * time.Minute
	remoteBaselineRefreshLeaseDuration              = 5 * time.Minute
	remoteBaselineRefreshTokenEnv                   = "SUPER_DOLPHIN_REMOTE_BASELINE_REFRESH_TOKEN"
	remoteBaselineRefreshLogLimit                   = 1 << 20
)

type remoteBaselineRefreshOptions struct{ ConfigPath, LedgerPath, RepositoryRoot, Remote, Ref, Platform string }
type remoteBaselineRefreshInput struct {
	Identity                                                                                                                             remoteci.BaselineIdentity
	GateSourceDigest, RuntimeDependencyDigest, RuntimeDependencySchemaVersion, GoToolchain, SqruffURL, SqruffSHA256, AcceptedStateSHA256 string
	RepositoryRoot                                                                                                                       string
	SourceEntries                                                                                                                        []sourceexport.TreeEntry
}
type remoteBaselineRefreshResultOutcome string

const (
	remoteBaselineRefreshResultAuthority = "refresh_non_normal_test"

	remoteBaselineRefreshResultOutcomeUnchanged        remoteBaselineRefreshResultOutcome = "unchanged"
	remoteBaselineRefreshResultOutcomeCleanupCompleted remoteBaselineRefreshResultOutcome = "cleanup_completed"
	remoteBaselineRefreshResultOutcomePromoted         remoteBaselineRefreshResultOutcome = "promoted"
)

// remoteBaselineRefreshResult is a refresh-only operation report. It is never
// evidence that any normal CI workload or test executed.
type remoteBaselineRefreshResult struct {
	SchemaVersion uint32                             `json:"schema_version"`
	Authority     string                             `json:"authority"`
	Outcome       remoteBaselineRefreshResultOutcome `json:"outcome"`
	Phase         remoteBaselineRefreshResultOutcome `json:"phase"`
	State         remoteci.BaselineState             `json:"state"`
}

func newRemoteBaselineRefreshResult(outcome remoteBaselineRefreshResultOutcome, state remoteci.BaselineState) remoteBaselineRefreshResult {
	return remoteBaselineRefreshResult{
		SchemaVersion: remoteBaselineRefreshResultSchemaVersion,
		Authority:     remoteBaselineRefreshResultAuthority,
		Outcome:       outcome,
		Phase:         outcome,
		State:         state,
	}
}

func (result remoteBaselineRefreshResult) Validate() error {
	if result.SchemaVersion != remoteBaselineRefreshResultSchemaVersion {
		return fmt.Errorf("remote baseline refresh result schema version %d is unsupported", result.SchemaVersion)
	}
	if result.Authority != remoteBaselineRefreshResultAuthority {
		return fmt.Errorf("remote baseline refresh result authority %q is invalid", result.Authority)
	}
	if result.Outcome != result.Phase {
		return fmt.Errorf("remote baseline refresh result outcome %q and phase %q must match", result.Outcome, result.Phase)
	}
	switch result.Outcome {
	case remoteBaselineRefreshResultOutcomeUnchanged, remoteBaselineRefreshResultOutcomeCleanupCompleted, remoteBaselineRefreshResultOutcomePromoted:
		return nil
	default:
		return fmt.Errorf("remote baseline refresh result outcome %q is invalid", result.Outcome)
	}
}

type remoteOCIBaselineBuild struct {
	Cache   *remoteci.BaselineOCIProjectCache
	Request remoteci.OCIBaselineBuilderRequest
	Result  remoteci.OCIBaselineBuilderResult
}

type remoteOCIBaselineBuilder func(context.Context, remoteRunConfig, remoteci.BaselineState, remoteBaselineRefreshInput, remoteci.OCIBaselineBuilderRequest, []byte) (*remoteOCIBaselineBuild, error)
type remoteBaselineRefreshLeaseMutation func(gatecontract.RemoteBaselineRefreshLease) (gatecontract.RemoteBaselineRefreshLease, error)

type remoteBaselineRefreshLeaseRequest struct {
	mutation remoteBaselineRefreshLeaseMutation
	result   chan error
}

// remoteBaselineRefreshLeaseOwner is the sole mutable lease owner. The worker
// and the refresh flow communicate only through serialized mutations.
type remoteBaselineRefreshLeaseOwner struct {
	ctx      context.Context
	cancel   context.CancelCauseFunc
	requests chan remoteBaselineRefreshLeaseRequest
	stop     chan chan error
}

func newRemoteBaselineRefreshLeaseOwner(parent context.Context, initial gatecontract.RemoteBaselineRefreshLease, heartbeatInterval time.Duration, heartbeat remoteBaselineRefreshLeaseMutation, fail func(gatecontract.RemoteBaselineRefreshLease, error) error) (context.Context, *remoteBaselineRefreshLeaseOwner) {
	ctx, cancel := context.WithCancelCause(parent)
	owner := &remoteBaselineRefreshLeaseOwner{ctx: ctx, cancel: cancel, requests: make(chan remoteBaselineRefreshLeaseRequest), stop: make(chan chan error)}
	go owner.run(initial, heartbeatInterval, heartbeat, fail)
	return ctx, owner
}

func (owner *remoteBaselineRefreshLeaseOwner) run(lease gatecontract.RemoteBaselineRefreshLease, heartbeatInterval time.Duration, heartbeat remoteBaselineRefreshLeaseMutation, fail func(gatecontract.RemoteBaselineRefreshLease, error) error) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	var terminal error
	for {
		select {
		case request := <-owner.requests:
			if terminal != nil {
				request.result <- terminal
				continue
			}
			updated, err := request.mutation(lease)
			if err == nil {
				lease = updated
			}
			request.result <- err
		case <-ticker.C:
			if terminal != nil {
				continue
			}
			updated, err := heartbeat(lease)
			if err == nil {
				lease = updated
				continue
			}
			terminal = fmt.Errorf("heartbeat remote baseline refresh lease: %w", err)
			if failErr := fail(lease, terminal); failErr != nil {
				terminal = errors.Join(terminal, fmt.Errorf("record remote baseline refresh heartbeat failure: %w", failErr))
			}
			owner.cancel(terminal)
		case result := <-owner.stop:
			result <- terminal
			return
		}
	}
}

func (owner *remoteBaselineRefreshLeaseOwner) apply(mutation remoteBaselineRefreshLeaseMutation) error {
	result := make(chan error, 1)
	request := remoteBaselineRefreshLeaseRequest{mutation: mutation, result: result}
	select {
	case <-owner.ctx.Done():
		return context.Cause(owner.ctx)
	case owner.requests <- request:
	}
	select {
	case <-owner.ctx.Done():
		return context.Cause(owner.ctx)
	case err := <-result:
		return err
	}
}

func (owner *remoteBaselineRefreshLeaseOwner) close() error {
	result := make(chan error, 1)
	owner.stop <- result
	return <-result
}

// triggerRemoteBaselineRefresh performs the short SQLite claim only. A busy or
// throttled refresh is an observable background state, never a CI failure.
func triggerRemoteBaselineRefresh(options remoteBaselineRefreshOptions, stderr io.Writer) {
	stored, err := loadStoredRemoteBaselineState(options.LedgerPath)
	if err != nil {
		fmt.Fprintf(stderr, "remote baseline refresh: skipped: load accepted state: %v\n", err)
		return
	}
	store, err := baselineLedger(options.LedgerPath)
	if err != nil {
		fmt.Fprintf(stderr, "remote baseline refresh: skipped: open authority: %v\n", err)
		return
	}
	record, err := store.LoadRemoteBaselineState()
	if err != nil {
		fmt.Fprintf(stderr, "remote baseline refresh: skipped: read authority: %v\n", err)
		return
	}
	if cleanup, cleanupErr := store.ClaimRemoteBaselineRefreshCleanup(remoteBaselineRefreshLeaseDuration); cleanupErr == nil {
		if err := startRemoteBaselineCleanupWorker(options, cleanup.Token); err != nil {
			_ = store.CompleteRemoteBaselineRefreshCleanup(cleanup, fmt.Errorf("detach cleanup worker: %w", err))
			fmt.Fprintf(stderr, "remote baseline refresh: cleanup worker launch failed: %v; CI continues\n", err)
		} else {
			fmt.Fprintln(stderr, "remote baseline refresh: cleanup recovery detached; CI continues")
		}
	} else if !errors.Is(cleanupErr, gatecontract.ErrRemoteBaselineRefreshBusy) {
		fmt.Fprintf(stderr, "remote baseline refresh: cleanup recovery skipped: %v; CI continues\n", cleanupErr)
	}
	lease, err := store.AcquireRemoteBaselineRefreshLease(gatecontract.RemoteBaselineRefreshLeaseRequest{AcceptedGeneration: stored.state.Generation, AcceptedStateSHA256: record.StateSHA256, LeaseDuration: remoteBaselineRefreshLeaseDuration})
	if errors.Is(err, gatecontract.ErrRemoteBaselineRefreshBusy) || errors.Is(err, gatecontract.ErrRemoteBaselineRefreshThrottled) {
		fmt.Fprintf(stderr, "remote baseline refresh: %s; CI continues\n", strings.TrimPrefix(err.Error(), "remote baseline refresh "))
		return
	}
	if err != nil {
		fmt.Fprintf(stderr, "remote baseline refresh: claim failed: %v; CI continues\n", err)
		return
	}
	if err := startRemoteBaselineRefreshWorker(options, lease.Token); err != nil {
		_ = store.FailRemoteBaselineRefreshLease(lease, "", "", "detach background worker: "+err.Error())
		fmt.Fprintf(stderr, "remote baseline refresh: worker launch failed: %v; CI continues\n", err)
		return
	}
	fmt.Fprintln(stderr, "remote baseline refresh: claimed and detached; CI continues")
}

func startRemoteBaselineRefreshWorker(options remoteBaselineRefreshOptions, token string) error {
	return startRemoteBaselineDetachedWorker(options, token, "_remote-baseline-refresh-worker")
}

func startRemoteBaselineCleanupWorker(options remoteBaselineRefreshOptions, token string) error {
	return startRemoteBaselineDetachedWorker(options, token, "_remote-baseline-cleanup-worker")
}

func startRemoteBaselineDetachedWorker(options remoteBaselineRefreshOptions, token, worker string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	log, err := openRemoteBaselineRefreshLog(options.LedgerPath)
	if err != nil {
		return err
	}
	args := []string{worker, "--config", options.ConfigPath, "--ledger", options.LedgerPath, "--repository", options.RepositoryRoot, "--remote", options.Remote, "--ref", options.Ref, "--platform", options.Platform}
	command, err := newDetachedRemoteBaselineRefreshCommand(executable, args...)
	if err != nil {
		_ = log.Close()
		return err
	}
	command.Stdout, command.Stderr = log, log
	command.Env = remoteBaselineRefreshWorkerEnv(os.Environ(), token)
	if err := command.Start(); err != nil {
		_ = log.Close()
		return err
	}
	if err := command.Process.Release(); err != nil {
		_ = log.Close()
		return err
	}
	return log.Close()
}

func openRemoteBaselineRefreshLog(ledger string) (*os.File, error) {
	path := ledger + ".refresh.log"
	if info, err := os.Stat(path); err == nil && info.Size() >= remoteBaselineRefreshLogLimit {
		return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}

// runRemoteBaselineRefresh is deliberately only the child worker path; public
// refresh initiation comes from normal remote runs and cannot be synchronous.
func runRemoteBaselineRefresh(args []string, stdout io.Writer) error {
	return runRemoteBaselineRefreshWithBuilder(args, stdout, buildRemoteOCIBaseline)
}
func runRemoteBaselineRefreshWithBuilder(args []string, stdout io.Writer, builder remoteOCIBaselineBuilder) error {
	options, err := parseRemoteBaselineRefreshOptions(args)
	if err != nil {
		return err
	}
	token := os.Getenv(remoteBaselineRefreshTokenEnv)
	if strings.TrimSpace(token) == "" {
		return protocolError("remote baseline refresh worker token is required")
	}
	store, err := baselineLedger(options.LedgerPath)
	if err != nil {
		return infrastructureError("open refresh SQLite authority: %v", err)
	}
	lease, err := store.ResumeRemoteBaselineRefreshLease(token)
	if err != nil {
		return infrastructureError("resume refresh lease: %v", err)
	}
	return runClaimedRemoteBaselineRefresh(options, store, lease, builder, stdout)
}

func runRemoteBaselineCleanup(args []string, stdout io.Writer) error {
	options, err := parseRemoteBaselineRefreshOptions(args)
	if err != nil {
		return err
	}
	token := os.Getenv(remoteBaselineRefreshTokenEnv)
	if strings.TrimSpace(token) == "" {
		return protocolError("remote baseline cleanup worker token is required")
	}
	store, err := baselineLedger(options.LedgerPath)
	if err != nil {
		return infrastructureError("open cleanup SQLite authority: %v", err)
	}
	lease, err := store.ResumeRemoteBaselineRefreshCleanup(token)
	if err != nil {
		return infrastructureError("resume cleanup lease: %v", err)
	}
	config, err := loadRemoteRunConfig(options.ConfigPath)
	if err != nil {
		return protocolError("load remote CI config: %v", err)
	}
	authority, err := newRemoteBaselineImageCacheAuthority(config)
	if err != nil {
		return infrastructureError("create cleanup ImageCache authority: %v", err)
	}
	deleteErr := deleteRemoteBaselineImageCache(lease.RetiringImageCacheID, authority)
	if err := store.CompleteRemoteBaselineRefreshCleanup(lease, deleteErr); err != nil {
		return infrastructureError("persist cleanup outcome: %v", err)
	}
	if deleteErr != nil {
		return infrastructureError("promotion succeeded; old ImageCache cleanup pending: %v", deleteErr)
	}
	return encodeRemoteBaselineRefreshResult(stdout, newRemoteBaselineRefreshResult(remoteBaselineRefreshResultOutcomeCleanupCompleted, remoteci.BaselineState{}))
}

func runClaimedRemoteBaselineRefresh(options remoteBaselineRefreshOptions, store *gatecontract.DurationLedgerStore, lease gatecontract.RemoteBaselineRefreshLease, builder remoteOCIBaselineBuilder, stdout io.Writer) (returnErr error) {
	ctx, cancel := gateprivate.WithTimeout(context.Background(), remoteBaselineRefreshDeadline)
	defer cancel()
	ctx, heartbeat := newRemoteBaselineRefreshLeaseOwner(ctx, lease, remoteBaselineRefreshLeaseDuration/3,
		func(current gatecontract.RemoteBaselineRefreshLease) (gatecontract.RemoteBaselineRefreshLease, error) {
			return store.HeartbeatRemoteBaselineRefreshLease(current, remoteBaselineRefreshLeaseDuration, current.ImageCacheName, current.ImageCacheID)
		},
		func(current gatecontract.RemoteBaselineRefreshLease, failure error) error {
			return store.FailRemoteBaselineRefreshLease(current, current.ImageCacheName, current.ImageCacheID, failure.Error())
		},
	)
	heartbeatStopped := false
	defer func() {
		if !heartbeatStopped {
			returnErr = errors.Join(returnErr, heartbeat.close())
		}
	}()
	acceptedRecord, err := store.LoadRemoteBaselineState()
	if err != nil {
		return err
	}
	var accepted remoteci.BaselineState
	if err := gatecontract.DecodeStrictJSON(acceptedRecord.StateJSON, &accepted); err != nil {
		return err
	}
	if err := accepted.Validate(); err != nil || accepted.SchemaVersion == 0 {
		return protocolError("refresh requires accepted immutable Ready ImageCache")
	}
	inputStateDigest := "sha256:" + acceptedRecord.StateSHA256
	config, err := loadRemoteRefreshConfig(options.ConfigPath)
	if err != nil {
		return failRemoteBaselineRefresh(heartbeat, store, err)
	}
	input, err := resolveRemoteBaselineRefreshInput(ctx, options)
	if err != nil {
		return sourceError("resolve remote baseline input: %v", err)
	}
	input.AcceptedStateSHA256 = inputStateDigest
	input.Identity.RuntimeImage = accepted.RuntimeImage
	if accepted.Matches(input.Identity) {
		if err = heartbeat.apply(func(current gatecontract.RemoteBaselineRefreshLease) (gatecontract.RemoteBaselineRefreshLease, error) {
			return store.AdvanceRemoteBaselineRefreshLease(current, cicontract.RefreshUnchanged)
		}); err != nil {
			return err
		}
		return encodeRemoteBaselineRefreshResult(stdout, newRemoteBaselineRefreshResult(remoteBaselineRefreshResultOutcomeUnchanged, accepted))
	}
	request, deltaArchive, err := prepareRemoteOCIBuildRequest(ctx, config, accepted, input)
	if err != nil {
		return failRemoteBaselineRefresh(heartbeat, store, err)
	}
	var boundLease gatecontract.RemoteBaselineRefreshLease
	if err = heartbeat.apply(func(current gatecontract.RemoteBaselineRefreshLease) (gatecontract.RemoteBaselineRefreshLease, error) {
		updated, bindErr := store.BindRemoteBaselineRefreshLeaseBuilder(current, request.JobID, request.TargetTree)
		if bindErr == nil {
			boundLease = updated
		}
		return updated, bindErr
	}); err != nil {
		return err
	}
	lease = boundLease
	if err = heartbeat.apply(func(current gatecontract.RemoteBaselineRefreshLease) (gatecontract.RemoteBaselineRefreshLease, error) {
		return store.AdvanceRemoteBaselineRefreshLease(current, cicontract.RefreshBuilding)
	}); err != nil {
		return err
	}
	build, err := builder(ctx, config, accepted, input, request, deltaArchive)
	if err != nil {
		return failRemoteBaselineRefresh(heartbeat, store, err)
	}
	if err := validateRemoteOCIBaselineBuild(build); err != nil {
		return failRemoteBaselineRefresh(heartbeat, store, err)
	}
	if err := persistRemoteRefreshDelta(store, lease, accepted, build); err != nil {
		return failRemoteBaselineRefresh(heartbeat, store, err)
	}
	if err = heartbeat.apply(func(current gatecontract.RemoteBaselineRefreshLease) (gatecontract.RemoteBaselineRefreshLease, error) {
		return store.AdvanceRemoteBaselineRefreshLease(current, cicontract.RefreshCachePreparing)
	}); err != nil {
		return err
	}
	authority, err := newRemoteBaselineImageCacheAuthority(config)
	if err != nil {
		return failRemoteBaselineRefresh(heartbeat, store, err)
	}
	name, err := remoteBaselineImageCacheName(lease.TargetGeneration, input.Identity.MainTree)
	if err != nil {
		return failRemoteBaselineRefresh(heartbeat, store, err)
	}
	if err = heartbeat.apply(func(current gatecontract.RemoteBaselineRefreshLease) (gatecontract.RemoteBaselineRefreshLease, error) {
		return store.HeartbeatRemoteBaselineRefreshLease(current, remoteBaselineRefreshLeaseDuration, name, "")
	}); err != nil {
		return err
	}
	created, err := authority.CreateImageCache(ctx, eci.ImageCacheCreateRequest{ImageCacheName: name, Images: []string{build.Cache.Image}, ImageCacheSize: remoteBaselineImageCacheSizeGiB, Tags: map[string]string{"super-dolphin-baseline-generation": fmt.Sprintf("%d", lease.TargetGeneration)}})
	if err != nil {
		return failRemoteBaselineRefresh(heartbeat, store, err)
	}
	if err = heartbeat.apply(func(current gatecontract.RemoteBaselineRefreshLease) (gatecontract.RemoteBaselineRefreshLease, error) {
		return store.HeartbeatRemoteBaselineRefreshLease(current, remoteBaselineRefreshLeaseDuration, name, created.ID)
	}); err != nil {
		return err
	}
	ready, err := authority.WaitImageCacheReady(ctx, created.ID)
	if err != nil {
		return failRemoteBaselineRefresh(heartbeat, store, err)
	}
	if err := validateRemoteBaselineReadyImageCache(created, ready, build.Cache.Image); err != nil {
		return failRemoteBaselineRefresh(heartbeat, store, err)
	}
	if err = heartbeat.apply(func(current gatecontract.RemoteBaselineRefreshLease) (gatecontract.RemoteBaselineRefreshLease, error) {
		return store.AdvanceRemoteBaselineRefreshLease(current, cicontract.RefreshReadyValidated)
	}); err != nil {
		return err
	}
	successor, err := newRemoteOCIBaselineState(accepted, input, build, ready)
	if err != nil {
		return failRemoteBaselineRefresh(heartbeat, store, err)
	}
	payload, err := json.Marshal(successor)
	if err != nil {
		return failRemoteBaselineRefresh(heartbeat, store, err)
	}
	digest := sha256.Sum256(payload)
	if err = heartbeat.apply(func(current gatecontract.RemoteBaselineRefreshLease) (gatecontract.RemoteBaselineRefreshLease, error) {
		if err := store.PromoteRemoteBaselineStateWithRefreshLease(current, gatecontract.RemoteBaselineStateRecord{Generation: successor.Generation, StateJSON: payload, StateSHA256: hex.EncodeToString(digest[:])}, name, created.ID); err != nil {
			return current, err
		}
		return current, nil
	}); err != nil {
		return failRemoteBaselineRefresh(heartbeat, store, err)
	}
	if err = heartbeat.close(); err != nil {
		heartbeatStopped = true
		return err
	}
	heartbeatStopped = true
	cleanup, err := store.ClaimRemoteBaselineRefreshCleanup(remoteBaselineRefreshLeaseDuration)
	if err != nil {
		return fmt.Errorf("promotion succeeded; claim old ImageCache cleanup: %w", err)
	}
	deleteErr := deleteRemoteBaselineImageCache(cleanup.RetiringImageCacheID, authority)
	if err := store.CompleteRemoteBaselineRefreshCleanup(cleanup, deleteErr); err != nil {
		return fmt.Errorf("promotion succeeded; persist old ImageCache cleanup: %w", err)
	}
	if deleteErr != nil {
		return fmt.Errorf("promotion succeeded; old ImageCache cleanup pending: %w", deleteErr)
	}
	return encodeRemoteBaselineRefreshResult(stdout, newRemoteBaselineRefreshResult(remoteBaselineRefreshResultOutcomePromoted, successor))
}

func failRemoteBaselineRefresh(owner *remoteBaselineRefreshLeaseOwner, store *gatecontract.DurationLedgerStore, failure error) error {
	if err := owner.apply(func(current gatecontract.RemoteBaselineRefreshLease) (gatecontract.RemoteBaselineRefreshLease, error) {
		return current, store.FailRemoteBaselineRefreshLease(current, current.ImageCacheName, current.ImageCacheID, failure.Error())
	}); err != nil {
		return errors.Join(infrastructureError("refresh successor: %v", failure), err)
	}
	return infrastructureError("refresh successor: %v", failure)
}
func persistRemoteRefreshDelta(store *gatecontract.DurationLedgerStore, lease gatecontract.RemoteBaselineRefreshLease, accepted remoteci.BaselineState, build *remoteOCIBaselineBuild) error {
	if store == nil {
		return errors.New("remote refresh delta authority and builder result are required")
	}
	if err := validateRemoteOCIBaselineBuild(build); err != nil {
		return err
	}
	if err := cicontract.ValidateIncrementalRefreshTransfer(build.Result.TransferMode, build.Result.ParentGeneration, build.Result.ParentImageSnapshotID, build.Result.DeltaArchiveSHA256); err != nil {
		return fmt.Errorf("validate remote refresh delta transfer: %w", err)
	}
	if build.Result.ParentGeneration != lease.AcceptedGeneration || build.Result.ParentImageSnapshotID != accepted.ImageCacheSnapshotID || build.Result.ParentStateSHA256 != lease.AcceptedStateSHA256 {
		return errors.New("remote refresh delta result does not match active accepted lease")
	}
	record := gatecontract.RemoteRefreshDeltaRecord{
		JobID:               build.Result.JobID,
		AttemptGeneration:   lease.AttemptGeneration,
		AcceptedGeneration:  lease.AcceptedGeneration,
		AcceptedStateSHA256: lease.AcceptedStateSHA256,
		AcceptedSnapshotID:  accepted.ImageCacheSnapshotID,
		DeltaIdentity:       build.Result.DeltaArchiveSHA256,
		DeltaSHA256:         build.Result.DeltaArchiveSHA256,
		DeltaSizeBytes:      build.Result.DeltaArchiveSize,
		TargetTreeSHA:       build.Result.TargetTree,
		TargetClosureSHA256: build.Result.TargetSourceClosure,
		TransferMode:        build.Result.TransferMode,
		RecordedAt:          time.Now().UTC(),
	}
	if err := store.AppendRemoteRefreshDelta(record); err != nil {
		return fmt.Errorf("persist remote refresh delta: %w", err)
	}
	return nil
}

func validateRemoteOCIBaselineBuild(build *remoteOCIBaselineBuild) error {
	if build == nil || build.Cache == nil {
		return errors.New("remote OCI builder request, result, and cache are required")
	}
	if err := build.Request.Validate(); err != nil {
		return fmt.Errorf("validate remote OCI builder request: %w", err)
	}
	if err := build.Result.ValidateAgainst(build.Request); err != nil {
		return fmt.Errorf("validate remote OCI builder result: %w", err)
	}
	if build.Cache.Image != build.Result.Image || build.Cache.ContentManifestSHA256 != build.Result.ImageInputDigest || build.Cache.MainTree != build.Result.TargetTree || build.Cache.ToolchainDigest != build.Result.ToolchainDigest || build.Cache.Platform != build.Result.Platform {
		return errors.New("remote OCI cache identity does not match builder receipt")
	}
	return nil
}

func newRemoteOCIBaselineState(accepted remoteci.BaselineState, input remoteBaselineRefreshInput, build *remoteOCIBaselineBuild, imageCaches ...eci.ImageCache) (remoteci.BaselineState, error) {
	if accepted.Generation == ^uint64(0) {
		return remoteci.BaselineState{}, errors.New("remote baseline generation is exhausted")
	}
	if err := validateRemoteOCIBaselineBuild(build); err != nil {
		return remoteci.BaselineState{}, err
	}
	if len(imageCaches) != 1 {
		return remoteci.BaselineState{}, errors.New("exactly one ready ECI ImageCache is required")
	}
	ready := imageCaches[0]
	if err := cicontract.ValidateIncrementalRefreshTransfer(build.Result.TransferMode, build.Result.ParentGeneration, build.Result.ParentImageSnapshotID, build.Result.DeltaArchiveSHA256); err != nil {
		return remoteci.BaselineState{}, fmt.Errorf("validate successor incremental transfer: %w", err)
	}
	if build.Result.TargetTree != input.Identity.MainTree || build.Result.TargetCommit != input.Identity.MainCommit || build.Result.ParentGeneration != accepted.Generation || build.Result.ParentImageSnapshotID != accepted.ImageCacheSnapshotID {
		return remoteci.BaselineState{}, errors.New("successor builder receipt does not match accepted state or target identity")
	}
	if err := cicontract.ValidateDeltaRebuild(build.Result.TransferMode, build.Result.ParentGeneration, build.Result.ParentImageSnapshotID, build.Result.DeltaArchiveSHA256, build.Result.TargetTree, build.Result.TargetSourceClosure); err != nil {
		return remoteci.BaselineState{}, fmt.Errorf("validate successor delta rebuild: %w", err)
	}
	now := time.Now().UTC()
	state := remoteci.BaselineState{SchemaVersion: remoteci.BaselineStateSchemaVersion, Generation: accepted.Generation + 1, MainCommit: input.Identity.MainCommit, MainTree: input.Identity.MainTree, Platform: input.Identity.Platform, PolicyDigest: input.Identity.PolicyDigest, ToolchainDigest: input.Identity.ToolchainDigest, RuntimeImage: build.Result.Image, OCIProjectCache: build.Cache, ImageCacheID: ready.ID, ImageCacheSnapshotID: ready.SnapshotID, ImageCacheReady: ready.Status == "Ready", ImageDigest: strings.TrimPrefix(build.Result.Image, strings.Split(build.Result.Image, "@")[0]+"@"), GateBinarySHA256: input.GateSourceDigest, RuntimeSeedSHA256: input.RuntimeDependencyDigest, BaselineManifestDigest: build.Result.ImageInputDigest, SourceSnapshotManifestDigest: build.Result.TargetSourceManifest, SourceSnapshotImagePath: cicontract.SourceSnapshotManifestPath, SourceSnapshotClosureDigest: build.Result.TargetSourceClosure, CreatedAt: now, AcceptedAt: now, RenewedAt: now}
	return state, state.Validate()
}
func encodeRemoteBaselineRefreshResult(stdout io.Writer, result remoteBaselineRefreshResult) error {
	if err := result.Validate(); err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(result)
}
