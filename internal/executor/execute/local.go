package execute

import (
	"context"
	"fmt"
	"maps"

	"github.com/apache/arrow/go/v18/arrow"
	"github.com/galgotech/heddle-lang/pkg/logger"
	"github.com/galgotech/heddle-lang/pkg/plugin"
	"go.uber.org/zap"

	"github.com/galgotech/heddle-sdk-go/internal/executor/history"
	"github.com/galgotech/heddle-sdk-go/internal/registry"
	"github.com/galgotech/heddle-sdk-go/internal/schema"
)

type LocalExecutor struct {
	registry     registry.Registry
	localHistory history.LocalHistory
}

func NewLocalExecutor(registry registry.Registry, localHistory history.LocalHistory) *LocalExecutor {
	return &LocalExecutor{
		registry:     registry,
		localHistory: localHistory,
	}
}

func (e *LocalExecutor) Execute(ctx context.Context, input plugin.ExecuteStepRequest) (plugin.ExecuteStepResponse, error) {
	stepName := input.StepName
	configJSON := input.ConfigJSON

	step, ok := e.registry.GetStep(stepName)
	if !ok {
		logger.L().Fatal("step not found", zap.String("stepName", stepName))

		return plugin.ExecuteStepResponse{
			TaskID:       input.TaskID,
			Status:       plugin.StepResponseError,
			ErrorMessage: fmt.Sprintf("step not found: %s", stepName),
		}, nil
	}

	// 1. Resolve resources from local bindings
	resources := make(map[string]any)

	t := step.StructType
	for fieldType := range t.Fields() {
		if schema.IsResource(fieldType.Type) {
			fieldName := fieldType.Name

			inst, err := e.registry.GetResource(fieldName)
			if err != nil {
				return plugin.ExecuteStepResponse{
					TaskID:       input.TaskID,
					Status:       plugin.StepResponseError,
					ErrorMessage: fmt.Sprintf("failed to get resource %s: %v", fieldName, err),
				}, nil
			}

			resources[fieldName] = inst
		}
	}

	// 2. Resolve columns or raw input
	// Auto chaining from simulated SHM history
	shm := e.localHistory.GetSimulatedSHM()
	columns := make(map[string]arrow.Array)
	maps.Copy(columns, shm)

	// 3. Execute using UnifiedExecute
	execReq := ExecutionRequest{
		StepName:  stepName,
		Config:    configJSON,
		Resources: resources,
		Columns:   columns,
	}

	result, err := unifiedExecute(ctx, e.registry, execReq)
	if err != nil {
		logger.L().Fatal("step execution failed", zap.String("stepName", stepName), zap.Error(err))

		return plugin.ExecuteStepResponse{
			TaskID:       input.TaskID,
			Status:       plugin.StepResponseError,
			ErrorMessage: err.Error(),
		}, nil
	}

	// 4. Save to simulated SHM/history
	if len(result.Columns) > 0 {
		e.localHistory.Add(stepName, result.Columns)
	}

	return plugin.ExecuteStepResponse{
		TaskID: input.TaskID,
		Status: plugin.StepResponseSuccess,
	}, nil
}
