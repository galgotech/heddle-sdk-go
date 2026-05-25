package execute

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/apache/arrow/go/v18/arrow"
	"go.uber.org/zap"

	"github.com/galgotech/heddle-lang/pkg/logger"
	baseplugin "github.com/galgotech/heddle-lang/pkg/plugin"
	"github.com/galgotech/heddle-lang/pkg/runtime/locality"

	"github.com/galgotech/heddle-sdk-go/internal/executor/history"
	"github.com/galgotech/heddle-sdk-go/internal/registry"
)

type workerExecutor struct {
	registry      registry.Registry
	workerHistory history.WorkerHistory
}

func NewWorkerExecutor(registry registry.Registry, workerHistory history.WorkerHistory) Executor {
	return &workerExecutor{
		registry:      registry,
		workerHistory: workerHistory,
	}
}

func (e *workerExecutor) Execute(ctx context.Context, input any) (any, error) {
	request, ok := input.(baseplugin.ExecuteStepRequest)
	if !ok {
		return nil, fmt.Errorf("invalid input type for worker executor")
	}

	// Helper to track execution and return response
	trackAndReturn := func(resp baseplugin.ExecuteStepResponse) baseplugin.ExecuteStepResponse {
		entry := history.WorkerHistoryEntry{
			TaskID:        request.TaskID,
			StepName:      request.StepName,
			Status:        string(resp.Status),
			ErrorMessage:  resp.ErrorMessage,
			OutputHandles: resp.OutputHandles,
			Timestamp:     time.Now(),
		}
		e.workerHistory.Add(entry)
		return resp
	}

	// 1. Resolve resources with their definitions from the worker
	resources := make(map[string]any)
	if request.ResourceId != "" && len(request.Resources) > 0 {
		for resourceReference, resourceDefinition := range request.Resources {
			// Verify resource type exists in registry
			_, ok := e.registry.GetResource(resourceDefinition.Type)
			if !ok {
				return trackAndReturn(baseplugin.ExecuteStepResponse{
					TaskID:       request.TaskID,
					Status:       baseplugin.StepResponseError,
					ErrorMessage: fmt.Sprintf("resource type %s not registered", resourceDefinition.Type),
				}), nil
			}

			// Initialize and retrieve the active resource instance
			initializedRes, err := e.registry.InitializeResource(request.ResourceId, resourceDefinition.Type, resourceDefinition.Config)
			if err != nil {
				return trackAndReturn(baseplugin.ExecuteStepResponse{
					TaskID:       request.TaskID,
					Status:       baseplugin.StepResponseError,
					ErrorMessage: fmt.Sprintf("failed to initialize resource %s: %v", resourceReference, err),
				}), nil
			}
			resources[resourceReference] = initializedRes
		}
	}

	// 2. Prepare the Input Frame using Zero-Copy SHM access.
	columns := make(map[string]arrow.Array)
	for fieldName, path := range request.InputReferences {
		arr, err := locality.ReadArrowArrayFromPath(path)
		if err != nil {
			logger.L().Error("Failed to read input from SHM", zap.Error(err), zap.String("path", path))
		} else {
			columns[fieldName] = arr
			defer arr.Release()
		}
	}

	// 3. Execute using UnifiedExecute
	execReq := ExecutionRequest{
		StepName:  request.StepName,
		Config:    request.ConfigJSON,
		Resources: resources,
		Columns:   columns,
	}

	result, err := UnifiedExecute(ctx, e.registry, execReq)
	if err != nil {
		logger.L().Error("Step execution failed", zap.String("step", request.StepName), zap.Error(err))
		return trackAndReturn(baseplugin.ExecuteStepResponse{
			TaskID:       request.TaskID,
			Status:       baseplugin.StepResponseError,
			ErrorMessage: err.Error(),
		}), nil
	}

	// 4. Formulate the response writing arrow arrays to SHM
	outputHandles := make(map[string]string)
	for name, arr := range result.Columns {
		if arr != nil && !reflect.ValueOf(arr).IsNil() {
			arr.Retain()
			defer arr.Release()
			path, err := locality.WriteArrowArrayOnlyToShm(arr)
			if err != nil {
				return trackAndReturn(baseplugin.ExecuteStepResponse{
					TaskID:       request.TaskID,
					Status:       baseplugin.StepResponseError,
					ErrorMessage: fmt.Sprintf("failed to write output frame: %v", err),
				}), nil
			}
			outputHandles[name] = path
		}
	}

	return trackAndReturn(baseplugin.ExecuteStepResponse{
		TaskID:        request.TaskID,
		Status:        baseplugin.StepResponseSuccess,
		OutputHandles: outputHandles,
	}), nil
}

