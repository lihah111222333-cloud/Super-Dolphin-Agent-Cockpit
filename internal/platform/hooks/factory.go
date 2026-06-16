package hooks

import (
	"context"
	"errors"
	"fmt"
	"strings"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	shared "github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
)

const lostSubscriberFailureThreshold = 3

type leaseValidationSpec struct {
	instanceError   string
	generationError string
}

var (
	hookSubscriptionLeaseValidation = leaseValidationSpec{
		instanceError:   "hook subscription requires lease instance_id",
		generationError: "hook subscription requires lease generation",
	}
	hookSubscriberLeaseValidation = leaseValidationSpec{
		instanceError:   "hook escalate requires subscriber lease instance_id",
		generationError: "hook escalate requires subscriber lease generation",
	}
)

type phaseDecisionConfig struct {
	defaultDecision string
	ranks           map[string]int
}

var (
	beforeDecisionConfig = phaseDecisionConfig{
		defaultDecision: mcp.HookDecisionDeny,
		ranks: map[string]int{
			mcp.HookDecisionAllow:  0,
			mcp.HookDecisionModify: 1,
			mcp.HookDecisionWait:   2,
			mcp.HookDecisionDeny:   3,
		},
	}
	checkDecisionConfig = phaseDecisionConfig{
		defaultDecision: mcp.HookDecisionContinue,
		ranks: map[string]int{
			mcp.HookDecisionContinue: 0,
			mcp.HookDecisionWarn:     1,
			mcp.HookDecisionAbort:    2,
		},
	}
	afterDecisionConfig = phaseDecisionConfig{
		defaultDecision: mcp.HookDecisionReject,
		ranks: map[string]int{
			mcp.HookDecisionApprove:  0,
			mcp.HookDecisionEscalate: 1,
			mcp.HookDecisionReject:   2,
		},
	}
)

type phaseDispatchResult[T any] struct {
	decisions []peerDecision[T]
	payload   mcp.HookPayload
}

type phaseSpec[T any] struct {
	defaultDecision T
	dispatch        func(context.Context, string, mcp.HookPayload) (phaseDispatchResult[T], error)
	merge           func([]peerDecision[T]) MergeResult[T]
	finalize        func(context.Context, phaseDispatchResult[T], MergeResult[T]) (T, error)
}

type dispatchRecoverSpec struct {
	sentinel error
	message  string
}

func runPhase[T any](ctx context.Context, manager *Manager, topic string, payload mcp.HookPayload, spec phaseSpec[T]) (T, error) {
	if err := manager.validate(); err != nil {
		return spec.defaultDecision, err
	}
	if manager.depthExceeded(payload) {
		return spec.defaultDecision, nil
	}

	dispatched, err := spec.dispatch(ctx, topic, payload)
	if err != nil {
		return spec.defaultDecision, err
	}

	result := spec.merge(dispatched.decisions)
	manager.handleLostLeases(ctx, result.LostLeases)
	if spec.finalize != nil {
		return spec.finalize(ctx, dispatched, result)
	}
	return result.Decision, nil
}

func dispatchBySelector[T any](
	dispatcher *HookDispatcher,
	ctx context.Context,
	selector mcp.Selector,
	payload mcp.HookPayload,
	peerFn func(context.Context, mcp.LeaseKey, mcp.HookPayload) (T, error),
) ([]peerDecision[T], error) {
	if err := dispatcher.validate(); err != nil {
		return nil, err
	}
	leases, prepared := dispatcher.prepareDispatchBySelector(selector, payload)
	return dispatchPrepared(dispatcher, ctx, leases, prepared, peerFn)
}

func dispatchPrepared[T any](
	dispatcher *HookDispatcher,
	ctx context.Context,
	leases []mcp.LeaseKey,
	payload mcp.HookPayload,
	peerFn func(context.Context, mcp.LeaseKey, mcp.HookPayload) (T, error),
) ([]peerDecision[T], error) {
	if err := dispatcher.validate(); err != nil {
		return nil, err
	}
	if len(leases) == 0 {
		return nil, nil
	}
	return dispatchDecisions(dispatcher, ctx, leases, payload, peerFn), nil
}

func buildDispatchSelector(topic string, payload mcp.HookPayload) mcp.Selector {
	selector := mcp.Selector{Subscription: topic}
	scope := shared.NormalizeSelectorScope(&mcp.SelectorScope{
		AgentID:  payload.AgentID,
		ThreadID: payload.ThreadID,
	})
	if scope != (mcp.SelectorScope{}) {
		selector.Scope = &scope
	}
	return selector
}

func scopeMatches(requested, subscribed mcp.SelectorScope) bool {
	return scopeFieldMatches(subscribed.AgentID, requested.AgentID) &&
		scopeFieldMatches(subscribed.ThreadID, requested.ThreadID) &&
		scopeFieldMatches(subscribed.ClientKind, requested.ClientKind) &&
		scopeFieldMatches(subscribed.InstanceID, requested.InstanceID)
}

func scopeFieldMatches(subscriptionField, requestedField string) bool {
	return subscriptionField == "" || requestedField == subscriptionField
}

// normalizePeerDecisions 规范化peerdecisions。
func normalizePeerDecisions[In any, Out any](
	decisions []peerDecision[In],
	normalize func(In) Out,
) ([]Out, []mcp.LeaseKey, []mcp.LeaseKey) {
	normalized := make([]Out, 0, len(decisions))
	failed := make([]mcp.LeaseKey, 0)
	lost := make([]mcp.LeaseKey, 0)
	failedSeen := make(map[mcp.LeaseKey]struct{}, len(decisions))
	lostSeen := make(map[mcp.LeaseKey]struct{}, len(decisions))
	for _, item := range decisions {
		if item.Err != nil {
			failed = appendUniqueLease(failed, failedSeen, item.Lease)
			if item.ConsecutiveFailures >= lostSubscriberFailureThreshold {
				lost = appendUniqueLease(lost, lostSeen, item.Lease)
			}
			continue
		}
		normalized = append(normalized, normalize(item.Decision))
	}
	return normalized, failed, lost
}

func normalizeDecision(decision string, config phaseDecisionConfig) string {
	decision = strings.ToLower(strings.TrimSpace(decision))
	if _, ok := config.ranks[decision]; ok {
		return decision
	}
	return config.defaultDecision
}

func chooseDecision[T any](items []T, decisionOf func(T) string, config phaseDecisionConfig) string {
	if len(items) == 0 {
		return config.defaultDecision
	}
	best := config.defaultDecision
	bestRank := -1
	for _, item := range items {
		decision := normalizeDecision(decisionOf(item), config)
		if rank := config.ranks[decision]; rank > bestRank {
			best = decision
			bestRank = rank
		}
	}
	return best
}

func firstMatching[T any](items []T, pred func(T) bool) (T, bool) {
	for _, item := range items {
		if pred(item) {
			return item, true
		}
	}
	var zero T
	return zero, false
}

func cleanupSubscriberLease(ctx context.Context, manager *Manager, lease mcp.LeaseKey) error {
	manager.registry.Unsubscribe(lease)
	manager.dispatcher.ForgetLease(lease)
	_, err := manager.resolver.CancelByLease(ctx, lease)
	return err
}

func recoverDispatchWorker[T any](
	dispatcher *HookDispatcher,
	job dispatchJob,
	rec any,
	spec dispatchRecoverSpec,
) peerDecision[T] {
	err := fmt.Errorf("hooks dispatch panic for %s/%d: %v", job.lease.InstanceID, job.lease.Generation, rec)
	if spec.sentinel != nil {
		err = fmt.Errorf("%w for %s/%d: %v", spec.sentinel, job.lease.InstanceID, job.lease.Generation, rec)
	}
	failures := 0
	if dispatcher != nil {
		failures = dispatcher.recordPeerResult(job.lease, err)
	}
	result := peerDecision[T]{
		Lease:               job.lease,
		Err:                 err,
		ConsecutiveFailures: failures,
	}
	if spec.message != "" {
		pkglogger.Error(spec.message,
			"lease_key", job.lease,
			"job_index", job.index,
			"panic", rec,
			"error", err,
		)
	}
	return result
}

func executeDispatchJob[T any](
	dispatcher *HookDispatcher,
	ctx context.Context,
	payload mcp.HookPayload,
	job dispatchJob,
	invoke func(context.Context, mcp.LeaseKey, mcp.HookPayload) (T, error),
) (result peerDecision[T]) {
	result.Lease = job.lease
	defer func() {
		if rec := recover(); rec != nil {
			result = recoverDispatchWorker[T](dispatcher, job, rec, dispatchRecoverSpec{
				message: "hooks dispatch worker panic",
			})
		}
	}()

	callCtx, cancel := config.WithPeerTimeout(ctx, dispatcher.peerTimeoutOrDefault())
	defer cancel()

	decision, err := invoke(callCtx, job.lease, shared.CloneHookPayload(payload))
	result.Decision = decision
	result.Err = err
	result.ConsecutiveFailures = dispatcher.recordPeerResult(job.lease, err)
	return result
}

func markDispatchWorkerPanicResult[T any](d *HookDispatcher, results []peerDecision[T], job dispatchJob, hasCurrent bool, rec any) {
	if !hasCurrent {
		pkglogger.Error("hooks dispatch worker goroutine panic", "panic", rec)
		return
	}
	if job.index < 0 || job.index >= len(results) {
		pkglogger.Error("hooks dispatch worker goroutine panic",
			"lease_key", job.lease,
			"job_index", job.index,
			"results_len", len(results),
			"panic", rec,
		)
		return
	}
	results[job.index] = recoverDispatchWorker[T](d, job, rec, dispatchRecoverSpec{
		sentinel: errDispatchWorkerPanic,
		message:  "hooks dispatch worker goroutine panic",
	})
}

func runDispatchWorker[T any](
	d *HookDispatcher,
	ctx context.Context,
	jobs <-chan dispatchJob,
	payload mcp.HookPayload,
	results []peerDecision[T],
	invoke func(context.Context, mcp.LeaseKey, mcp.HookPayload) (T, error),
	state *dispatchWorkerState,
) {
	for job := range jobs {
		if state != nil {
			state.current = job
			state.hasCurrent = true
		}
		results[job.index] = executeDispatchJob(d, ctx, payload, job, invoke)
		if state != nil {
			state.hasCurrent = false
		}
	}
}

func validateLease(lease mcp.LeaseKey, spec leaseValidationSpec) (mcp.LeaseKey, error) {
	lease = trimLease(lease)
	if lease.InstanceID == "" {
		return mcp.LeaseKey{}, errors.New(spec.instanceError)
	}
	if lease.Generation == 0 {
		return mcp.LeaseKey{}, errors.New(spec.generationError)
	}
	return lease, nil
}

func formatLease(lease mcp.LeaseKey, spec leaseValidationSpec) (string, error) {
	lease, err := validateLease(lease, spec)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/%d", lease.InstanceID, lease.Generation), nil
}
