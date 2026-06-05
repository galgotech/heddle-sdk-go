package execute

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/apache/arrow/go/v18/arrow"

	"github.com/galgotech/heddle-lang/pkg/logger"
	"github.com/galgotech/heddle-lang/pkg/plugin"
	"github.com/galgotech/heddle-lang/pkg/runtime/locality"

	"github.com/galgotech/heddle-sdk-go/internal/executor/history"
	"github.com/galgotech/heddle-sdk-go/internal/registry"
)

// workerExecutor coordinates the execution of steps in a worker process,
// accessing input and output column data through shared memory (SHM).
type workerExecutor struct {
	// registry contains step and resource definitions registered by the worker.
	registry registry.Registry
	// workerHistory tracks historical execution entries of completed steps.
	workerHistory history.WorkerHistory
}

// NewWorkerExecutor creates a new Executor instance configured for worker-based step execution.
func NewWorkerExecutor(registry registry.Registry, workerHistory history.WorkerHistory) Executor {
	return &workerExecutor{
		registry:      registry,
		workerHistory: workerHistory,
	}
}

// Execute runs the requested step inside the worker. It fetches input columns from SHM,
// initializes resources, triggers step logic, and writes output columns back to SHM.
func (e *workerExecutor) Execute(ctx context.Context, request plugin.ExecuteStepRequest) (plugin.ExecuteStepResponse, error) {
	// trackAndReturn is a helper that logs the execution result to workerHistory
	// and returns the response structure.
	trackAndReturn := func(resp plugin.ExecuteStepResponse) plugin.ExecuteStepResponse {
		entry := history.WorkerHistoryEntry{
			WorkflowID:    request.WorkflowID,
			TaskID:        request.TaskID,
			StepName:      request.StepName,
			Status:        string(resp.Status),
			ErrorMessage:  resp.ErrorMessage,
			OutputHandles: resp.OutputRef,
			Timestamp:     time.Now(),
		}
		e.workerHistory.Add(entry)

		return resp
	}

	// 1. Resolve and initialize required resources using the registered definitions.
	// If a resource reference is provided, each defined resource is instantiated
	// using the registry configuration and stored for step execution.
	resources := make(map[string]any)

	if request.ResourceRef != "" && len(request.Resources) > 0 {
		for resourceReference, resourceDefinition := range request.Resources {
			// Initialize the resourceInstance instance using its type and configuration.
			err := e.registry.InitResource(resourceReference, resourceDefinition.Type, resourceDefinition.Config)
			if err != nil {
				return trackAndReturn(plugin.ExecuteStepResponse{
					TaskID:       request.TaskID,
					Status:       plugin.StepResponseError,
					ErrorMessage: fmt.Sprintf("failed to initialize resource %s: %v", resourceReference, err),
				}), nil
			}

			resources[request.ResourceRef], err = e.registry.GetResource(resourceReference)
			if err != nil {
				return trackAndReturn(plugin.ExecuteStepResponse{
					TaskID:       request.TaskID,
					Status:       plugin.StepResponseError,
					ErrorMessage: fmt.Sprintf("failed to get resource %s: %v", resourceReference, err),
				}), nil
			}
		}
	}

	// 2. Read input columns from Shared Memory (SHM) using zero-copy access.
	// We read each input column array from the designated SHM path. Any loaded
	// arrays must be released at the end of the execution scope to prevent leaks.
	columns := make(map[string]arrow.Array)

	for fieldName, pathRef := range request.InputRef {
		arr, err := locality.ReadArrowArrayFromPath(pathRef)
		if err != nil {
			logger.L().Error("Failed to read input from SHM", logger.Error(err), logger.String("path", pathRef))
		} else {
			columns[fieldName] = arr
			defer arr.Release()
		}
	}

	// 3. Execute the step logic using the unified runner.
	// We delegate execution to UnifiedExecute, which handles configuration hydration,
	// dependency injection, dynamic invocation, and return value mapping.
	execRequest := ExecutionRequest{
		StepName:  request.StepName,
		Config:    request.ConfigJSON,
		Columns:   columns,
		Resources: resources,
	}

	result, err := unifiedExecute(ctx, e.registry, execRequest)
	if err != nil {
		logger.L().Error("Step execution failed", logger.String("step", request.StepName), logger.Error(err))

		return trackAndReturn(plugin.ExecuteStepResponse{
			TaskID:       request.TaskID,
			Status:       plugin.StepResponseError,
			ErrorMessage: err.Error(),
		}), nil
	}

	// 4. Write output column arrays back to Shared Memory (SHM).
	// We iterate through the result columns, retain and write each non-nil Arrow
	// array to SHM, and record the returned SHM paths as output handles.
	outputHandles := make(map[string]string)

	for name, arr := range result.Columns {
		if arr != nil && !reflect.ValueOf(arr).IsNil() {
			arr.Retain()
			defer arr.Release()

			path, err := locality.WriteArrowArrayOnlyToShm(arr)
			if err != nil {
				return trackAndReturn(plugin.ExecuteStepResponse{
					TaskID:       request.TaskID,
					Status:       plugin.StepResponseError,
					ErrorMessage: fmt.Sprintf("failed to write output frame: %v", err),
				}), nil
			}

			outputHandles[name] = path
		}
	}

	return trackAndReturn(plugin.ExecuteStepResponse{
		TaskID:    request.TaskID,
		Status:    plugin.StepResponseSuccess,
		OutputRef: outputHandles,
	}), nil
}
