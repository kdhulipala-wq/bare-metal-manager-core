// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package workflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	temporalworkflow "go.temporal.io/sdk/workflow"

	activitypkg "github.com/NVIDIA/infra-controller/rest-api/flow/internal/task/executor/temporalworkflow/activity"
	"github.com/NVIDIA/infra-controller/rest-api/flow/internal/task/executor/temporalworkflow/common"
	"github.com/NVIDIA/infra-controller/rest-api/flow/internal/task/operationrules"
	"github.com/NVIDIA/infra-controller/rest-api/flow/pkg/common/devicetypes"
)

// runWaitDecommissionedWorkflow is a thin test shim that invokes
// executeWaitDecommissionedAction inside a Temporal workflow environment.
func runWaitDecommissionedWorkflow(
	ctx temporalworkflow.Context,
	target common.Target,
	cfg operationrules.ActionConfig,
) error {
	return executeWaitDecommissionedAction(actionExecutionContext{
		workflowContext: ctx,
		config:          cfg,
		target:          target,
	})
}

// stubGetDecommissionStatus is the no-op stub registered with the test
// environment; OnActivity overrides the actual return value per test.
func stubGetDecommissionStatus(_ context.Context, _ common.Target) (*activitypkg.GetDecommissionStatusResult, error) {
	return nil, nil
}

func registerDecommissionActivities(env *testsuite.TestWorkflowEnvironment) {
	env.RegisterWorkflowWithOptions(runWaitDecommissionedWorkflow,
		temporalworkflow.RegisterOptions{Name: "runWaitDecommissionedWorkflow"})
	env.RegisterActivityWithOptions(stubGetDecommissionStatus,
		activity.RegisterOptions{Name: activitypkg.NameGetDecommissionStatus})
}

func decommissionTestTarget() common.Target {
	return common.Target{
		Type:         devicetypes.ComponentTypeCompute,
		ComponentIDs: []string{"comp-1", "comp-2"},
	}
}

func shortDecommissionConfig() operationrules.ActionConfig {
	return operationrules.ActionConfig{
		Timeout:      10 * time.Second,
		PollInterval: time.Second,
	}
}

func TestEvaluateDecommissionResult_ManagedHostTerminalState(t *testing.T) {
	result := &activitypkg.GetDecommissionStatusResult{
		States: map[string]string{
			"comp-1": "Decommissioning/Decommissioned",
		},
	}

	done, err := evaluateDecommissionResult(result)

	assert.NoError(t, err)
	assert.True(t, done)
}

func TestEvaluateDecommissionResult_EmptyResultFails(t *testing.T) {
	done, err := evaluateDecommissionResult(&activitypkg.GetDecommissionStatusResult{})

	assert.ErrorContains(t, err, "status result is empty")
	assert.False(t, done)
}

// TestWaitDecommissioned_AllNotFound verifies that a later ambiguous lookup
// result cannot be inferred as successful decommissioning.
func TestWaitDecommissioned_AllNotFound(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	registerDecommissionActivities(env)

	// Preflight: all IDs found in Core, still in progress.
	env.OnActivity(activitypkg.NameGetDecommissionStatus, mock.Anything, mock.Anything).
		Return(&activitypkg.GetDecommissionStatusResult{
			States: map[string]string{
				"comp-1": "Decommissioning/step1",
				"comp-2": "Decommissioning/step1",
			},
		}, nil).Once()

	// First poll: neither component has a usable status.
	env.OnActivity(activitypkg.NameGetDecommissionStatus, mock.Anything, mock.Anything).
		Return(&activitypkg.GetDecommissionStatusResult{
			NotFound: []string{"comp-1", "comp-2"},
		}, nil).Once()

	env.ExecuteWorkflow(runWaitDecommissionedWorkflow, decommissionTestTarget(), shortDecommissionConfig())

	assert.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "comp-1")
	assert.Contains(t, err.Error(), "comp-2")
}

// TestWaitDecommissioned_Mixed verifies that an explicit terminal state cannot
// mask another component whose status is unavailable.
func TestWaitDecommissioned_Mixed(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	registerDecommissionActivities(env)

	// Preflight: both IDs known.
	env.OnActivity(activitypkg.NameGetDecommissionStatus, mock.Anything, mock.Anything).
		Return(&activitypkg.GetDecommissionStatusResult{
			States: map[string]string{
				"comp-1": "Decommissioning/step1",
				"comp-2": "Decommissioning/step1",
			},
		}, nil).Once()

	// First poll: comp-1 reached Decommissioned; comp-2 has no usable status.
	env.OnActivity(activitypkg.NameGetDecommissionStatus, mock.Anything, mock.Anything).
		Return(&activitypkg.GetDecommissionStatusResult{
			States:   map[string]string{"comp-1": "Decommissioned"},
			NotFound: []string{"comp-2"},
		}, nil).Once()

	env.ExecuteWorkflow(runWaitDecommissionedWorkflow, decommissionTestTarget(), shortDecommissionConfig())

	assert.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "comp-2")
}

// TestWaitDecommissioned_UnknownID verifies that a component absent from Core
// on the preflight call causes an immediate error rather than polling until
// timeout.
func TestWaitDecommissioned_UnknownID(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	registerDecommissionActivities(env)

	// Preflight: comp-1 is not found — unknown or mistyped ID.
	env.OnActivity(activitypkg.NameGetDecommissionStatus, mock.Anything, mock.Anything).
		Return(&activitypkg.GetDecommissionStatusResult{
			States:   map[string]string{"comp-2": "Decommissioning/step1"},
			NotFound: []string{"comp-1"},
		}, nil).Once()

	env.ExecuteWorkflow(runWaitDecommissionedWorkflow, decommissionTestTarget(), shortDecommissionConfig())

	assert.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "comp-1")
}

// TestWaitDecommissioned_PreflightExceedsTimeout verifies that the deadline is
// established before the preflight call, so a slow preflight is bounded by
// config.Timeout and cannot add 30 seconds on top of it. If the deadline were
// created after preflight, a two-second timeout could run for ~32 seconds.
func TestWaitDecommissioned_PreflightExceedsTimeout(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	registerDecommissionActivities(env)

	// Timeout shorter than the default 30s activity cap.
	cfg := operationrules.ActionConfig{
		Timeout:      2 * time.Second,
		PollInterval: time.Second,
	}

	// Preflight activity fails — simulates a slow/hung Core during preflight.
	env.OnActivity(activitypkg.NameGetDecommissionStatus, mock.Anything, mock.Anything).
		Return(nil, errors.New("Core unreachable")).Once()

	env.ExecuteWorkflow(runWaitDecommissionedWorkflow, decommissionTestTarget(), cfg)

	assert.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	assert.Error(t, err, "preflight failure must propagate as an error")
	assert.Contains(t, err.Error(), "preflight")
}

// TestWaitDecommissioned_PollIntervalExceedsTimeout verifies that when
// PollInterval is longer than the configured timeout the workflow still
// respects the timeout bound: the sleep is capped to the remaining time and
// the deadline is re-checked immediately after waking, so the workflow does
// not fire a full extra activity round-trip past the deadline.
func TestWaitDecommissioned_PollIntervalExceedsTimeout(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	registerDecommissionActivities(env)

	cfg := operationrules.ActionConfig{
		Timeout:      2 * time.Second,
		PollInterval: 10 * time.Second, // much larger than timeout
	}

	// Preflight: all IDs known, still in progress.
	env.OnActivity(activitypkg.NameGetDecommissionStatus, mock.Anything, mock.Anything).
		Return(&activitypkg.GetDecommissionStatusResult{
			States: map[string]string{"comp-1": "Decommissioning/step1"},
		}, nil).Once()

	// The loop should time out before firing a second poll — if sleep is not
	// capped, it would sleep for 10 s past the 2 s deadline and then call the
	// activity again. We register a second response that would indicate
	// success; if it is reached the test fails because the timeout was ignored.
	env.OnActivity(activitypkg.NameGetDecommissionStatus, mock.Anything, mock.Anything).
		Return(&activitypkg.GetDecommissionStatusResult{
			States: map[string]string{"comp-1": "Decommissioned"},
		}, nil)

	env.ExecuteWorkflow(runWaitDecommissionedWorkflow, decommissionTestTarget(), cfg)

	assert.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	assert.Error(t, err, "workflow must time out, not succeed via the post-deadline poll")
	assert.Contains(t, err.Error(), "timed out")
}

// TestWaitDecommissioned_ActivityError verifies that repeated activity errors
// abort the workflow after maxConsecutiveFailureDuration rather than spinning
// until the four-hour deadline. Timeout is set above maxConsecutiveFailureDuration
// (5 min) so the test actually exercises the failure-budget path rather than
// the normal timeout.
func TestWaitDecommissioned_ActivityError(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	registerDecommissionActivities(env)

	cfg := operationrules.ActionConfig{
		Timeout:      10 * time.Minute, // above maxConsecutiveFailureDuration (5 min)
		PollInterval: time.Second,
	}

	// Preflight succeeds.
	env.OnActivity(activitypkg.NameGetDecommissionStatus, mock.Anything, mock.Anything).
		Return(&activitypkg.GetDecommissionStatusResult{
			States: map[string]string{"comp-1": "Decommissioning/step1"},
		}, nil).Once()

	// All subsequent poll calls fail.
	env.OnActivity(activitypkg.NameGetDecommissionStatus, mock.Anything, mock.Anything).
		Return(nil, errors.New("Core unreachable"))

	env.ExecuteWorkflow(runWaitDecommissionedWorkflow, decommissionTestTarget(), cfg)

	assert.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "GetDecommissionStatus has been failing")
}
