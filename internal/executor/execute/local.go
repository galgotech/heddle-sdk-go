package execute

import (
	"context"
	"maps"

	"github.com/apache/arrow/go/v18/arrow"
	"github.com/galgotech/heddle-lang/pkg/logger"
	baseplugin "github.com/galgotech/heddle-lang/pkg/plugin"
	"go.uber.org/zap"

	"github.com/galgotech/heddle-sdk-go/internal/executor/history"
	"github.com/galgotech/heddle-sdk-go/registry"
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

func (e *LocalExecutor) Execute(ctx context.Context, input baseplugin.ExecuteStepRequest) (baseplugin.ExecuteStepResponse, error) {
	stepName := input.StepName
	configJSON := input.ConfigJSON

	// 1. Resources are injected by the generated Invoke closure using reg.GetResource
	resources := make(map[string]any)

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

		return baseplugin.ExecuteStepResponse{
			TaskID:       input.TaskID,
			Status:       baseplugin.StepResponseError,
			ErrorMessage: err.Error(),
		}, nil
	}

	// 4. Save to simulated SHM/history
	if len(result.Columns) > 0 {
		e.localHistory.Add(stepName, result.Columns)
	}

	return baseplugin.ExecuteStepResponse{
		TaskID: input.TaskID,
		Status: baseplugin.StepResponseSuccess,
	}, nil
}
