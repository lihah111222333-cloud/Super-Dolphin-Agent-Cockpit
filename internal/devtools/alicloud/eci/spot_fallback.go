package eci

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
)

const (
	defaultSpotSchedulingTimeout = 30 * time.Second
	defaultSpotCleanupTimeout    = 30 * time.Second
	defaultSpotPollInterval      = 2 * time.Second
)

var (
	errSpotSchedulingTimedOut = errors.New("spot ECI remained in Scheduling")
	errSpotSchedulingFailed   = errors.New("spot ECI scheduling failed")
	errSpotGroupDisappeared   = errors.New("spot ECI disappeared before admission")
)

type spotAdmissionDecision uint8

const (
	spotAdmissionObserve spotAdmissionDecision = iota
	spotAdmissionWait
	spotAdmissionAccepted
	spotAdmissionFallback
)

// createWithSpotFallback 统一处理分片与 Seed 的抢占准入、清理确认和按量回退。
func (c *Client) createWithSpotFallback(
	ctx context.Context,
	owner string,
	create func(string) (ContainerGroup, error),
) (ContainerGroup, error) {
	group, err := create(c.config.SpotStrategy)
	if err != nil {
		if !c.config.FallbackToPayAsYouGo || !isSpotCapacityUnavailable(err) {
			return group, err
		}
		return createPayAsYouGo(owner, create)
	}
	if !c.config.FallbackToPayAsYouGo || c.spotSchedulingTimeout <= 0 {
		return group, nil
	}

	observed, admissionErr := c.waitForSpotAdmission(ctx, group)
	if admissionErr == nil {
		return observed, nil
	}
	if !spotAdmissionAllowsFallback(admissionErr) {
		cleanupErr := c.retireCreatedSpot(ctx, group.ID)
		return ContainerGroup{}, errors.Join(
			fmt.Errorf("observe %s spot admission: %w", owner, admissionErr),
			cleanupErr,
		)
	}
	if !errors.Is(admissionErr, errSpotGroupDisappeared) {
		if err := c.retireCreatedSpot(ctx, group.ID); err != nil {
			return ContainerGroup{}, fmt.Errorf("retire %s spot instance before pay-as-you-go fallback: %w", owner, err)
		}
	}
	return createPayAsYouGo(owner, create)
}

func createPayAsYouGo(
	owner string,
	create func(string) (ContainerGroup, error),
) (ContainerGroup, error) {
	group, err := create(SpotStrategyNoSpot)
	if err != nil {
		return ContainerGroup{}, fmt.Errorf("create pay-as-you-go %s fallback: %w", owner, err)
	}
	return group, nil
}

func spotAdmissionAllowsFallback(err error) bool {
	return errors.Is(err, errSpotSchedulingTimedOut) ||
		errors.Is(err, errSpotSchedulingFailed) ||
		errors.Is(err, errSpotGroupDisappeared)
}

func (c *Client) retireCreatedSpot(ctx context.Context, groupID string) error {
	timeout := c.spotCleanupTimeout + 2*MaxControlPlaneRetryDuration()
	cleanup, cancel := gateprivate.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	return c.deleteContainerGroupAndWait(cleanup, groupID)
}

// waitForSpotAdmission 只对尚未取得资源的 Scheduling 状态等待 30 秒。
func (c *Client) waitForSpotAdmission(ctx context.Context, created ContainerGroup) (ContainerGroup, error) {
	if strings.TrimSpace(created.ID) == "" {
		return ContainerGroup{}, errors.New("created spot ECI is missing container group ID")
	}
	observation, cancel := gateprivate.WithTimeout(ctx, c.spotSchedulingTimeout)
	defer cancel()
	deadline := c.now().Add(c.spotSchedulingTimeout)
	current := created

	for {
		decision, err := classifySpotAdmission(current.Status)
		if err != nil {
			return current, err
		}
		switch decision {
		case spotAdmissionAccepted:
			return current, nil
		case spotAdmissionFallback:
			return current, fmt.Errorf("%w with status %s", errSpotSchedulingFailed, current.Status)
		case spotAdmissionWait:
			if err := c.waitForNextSpotObservation(ctx, observation, deadline); err != nil {
				return current, err
			}
		}

		current, err = c.observeSpotGroup(ctx, observation, current)
		if err != nil {
			return current, err
		}
	}
}

func classifySpotAdmission(status string) (spotAdmissionDecision, error) {
	switch status {
	case "":
		return spotAdmissionObserve, nil
	case "Scheduling":
		return spotAdmissionWait, nil
	case "ScheduleFailed", "Terminating":
		return spotAdmissionFallback, nil
	case "Pending", "Running", "Restarting", "Updating", "Succeeded", "Failed", "Expired":
		return spotAdmissionAccepted, nil
	default:
		return spotAdmissionObserve, fmt.Errorf("spot ECI returned unsupported admission status %q", status)
	}
}

func (c *Client) waitForNextSpotObservation(
	parent context.Context,
	observation context.Context,
	deadline time.Time,
) error {
	if !c.now().Before(deadline) {
		return fmt.Errorf("%w after %s", errSpotSchedulingTimedOut, c.spotSchedulingTimeout)
	}
	err := c.wait(observation, min(c.spotPollInterval, deadline.Sub(c.now())))
	return c.classifySpotObservationError(parent, observation, "wait to observe spot ECI scheduling", err)
}

func (c *Client) observeSpotGroup(
	parent context.Context,
	observation context.Context,
	current ContainerGroup,
) (ContainerGroup, error) {
	groups, err := c.describeContainerGroups(observation, true, current.ID)
	if err != nil {
		return current, c.classifySpotObservationError(parent, observation, "describe spot ECI admission", err)
	}
	if len(groups) == 0 {
		return current, errSpotGroupDisappeared
	}
	return groups[0], nil
}

func (c *Client) classifySpotObservationError(
	parent context.Context,
	observation context.Context,
	operation string,
	err error,
) error {
	if err == nil {
		return nil
	}
	if parent.Err() != nil {
		return parent.Err()
	}
	if errors.Is(observation.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%w after %s", errSpotSchedulingTimedOut, c.spotSchedulingTimeout)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

// deleteContainerGroupAndWait 阻止抢占与按量实例在同一分片上重叠运行。
func (c *Client) deleteContainerGroupAndWait(ctx context.Context, groupID string) error {
	if err := c.DeleteContainerGroup(ctx, groupID); err != nil {
		groups, describeErr := c.describeContainerGroups(ctx, true, groupID)
		if describeErr == nil && len(groups) == 0 {
			return nil
		}
		return errors.Join(err, describeErr)
	}

	observation, cancel := gateprivate.WithTimeout(ctx, c.spotCleanupTimeout)
	defer cancel()
	deadline := c.now().Add(c.spotCleanupTimeout)
	for {
		groups, err := c.describeContainerGroups(observation, true, groupID)
		if err != nil {
			return fmt.Errorf("confirm deleted spot ECI %s: %w", groupID, err)
		}
		if len(groups) == 0 {
			return nil
		}
		if !c.now().Before(deadline) {
			return fmt.Errorf("spot ECI %s still exists after %s", groupID, c.spotCleanupTimeout)
		}
		if err := c.wait(observation, min(c.spotPollInterval, deadline.Sub(c.now()))); err != nil {
			return fmt.Errorf("wait for spot ECI %s deletion: %w", groupID, err)
		}
	}
}
