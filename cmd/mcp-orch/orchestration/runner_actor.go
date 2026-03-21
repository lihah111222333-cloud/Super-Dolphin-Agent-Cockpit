package orchestration

import (
	"context"
	"log/slog"
	"time"

	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
)

type runnerActor struct {
	logger  *slog.Logger
	service *service
}

type waitResult struct {
	agentID   string
	launchSeq uint64
	err       error
}

func NewRunnerActor(logger *slog.Logger, service *service) platformrunner.Runner {
	return &runnerActor{logger: logger, service: service}
}

func (a *runnerActor) Run(ctx context.Context) error {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	stallDetector := &StallDetector{threshold: 30 * time.Second, logger: a.logger}
	results := make(chan waitResult, 32)
	for {
		a.startWaiters(ctx, results)
		a.processTurnQueues(ctx)

		select {
		case <-ctx.Done():
			a.stopAll()
			return ctx.Err()
		case result := <-results:
			a.service.handleProcessExit(ctx, result.agentID, result.launchSeq, result.err)
		case <-ticker.C:
			a.recoverStalledAgents(ctx, stallDetector)
		}
	}
}

func (a *runnerActor) startWaiters(ctx context.Context, results chan<- waitResult) {
	for _, target := range a.service.claimMonitorTargetsLocked() {
		go a.waitForExit(ctx, target, results)
	}
}

func (a *runnerActor) waitForExit(ctx context.Context, target monitorTarget, results chan<- waitResult) {
	err := target.cmd.Wait()
	select {
	case <-ctx.Done():
	case results <- waitResult{agentID: target.agentID, launchSeq: target.launchSeq, err: err}:
	}
}

func (a *runnerActor) processTurnQueues(ctx context.Context) {
	for _, work := range a.service.claimTurnWork(ctx) {
		a.service.startTurnExecution(ctx, work)
	}
}

func (a *runnerActor) recoverStalledAgents(ctx context.Context, stallDetector *StallDetector) {
	for _, agent := range a.service.listAgents() {
		if !stallDetector.CheckStall(&agent) {
			continue
		}
		if err := a.service.Recover(ctx, agent.id); err != nil {
			a.logger.Warn("orchestration: stalled agent recovery failed", "agent_id", agent.id, "error", err)
		}
	}
}

func (a *runnerActor) stopAll() {
	a.service.StopAllAgents()
}
