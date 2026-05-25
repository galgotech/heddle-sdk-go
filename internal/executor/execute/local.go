package execute

import (
	"context"
	"fmt"
	"maps"

	"github.com/apache/arrow/go/v18/arrow"
	"github.com/galgotech/heddle-lang/pkg/logger"
	"go.uber.org/zap"

	"github.com/galgotech/heddle-sdk-go/internal/executor/history"
	"github.com/galgotech/heddle-sdk-go/internal/registry"
	internalschema "github.com/galgotech/heddle-sdk-go/internal/schema"
)

type localExecutor struct {
	registry     registry.Registry
	localHistory history.LocalHistory
}

func NewLocalExecutor(registry registry.Registry, localHistory history.LocalHistory) Executor {
	return &localExecutor{
		registry:     registry,
		localHistory: localHistory,
	}
}

func (e *localExecutor) Execute(ctx context.Context, input any) (any, error) {
	localInput, ok := input.(LocalInput)
	if !ok {
		return nil, fmt.Errorf("invalid input type for local executor")
	}

	stepName := localInput.StepName
	configJSON := localInput.ConfigJSON
	innerInput := localInput.Input

	step, ok := e.registry.GetStep(stepName)
	if !ok {
		logger.L().Fatal("step not found", zap.String("stepName", stepName))
		return nil, fmt.Errorf("step not found: %s", stepName)
	}

	// 1. Resolve resources from local bindings
	resources := make(map[string]any)
	t := step.StructVal.Type()
	for i := 0; i < t.NumField(); i++ {
		fieldType := t.Field(i)
		if internalschema.IsResource(fieldType.Type) {
			fieldName := fieldType.Name
			if instID, bound := e.registry.GetResourceBinding(fieldName); bound {
				if inst, exists := e.registry.GetResourceInstance(instID); exists {
					resources[fieldName] = inst
				}
			}
		}
	}

	// 2. Resolve columns or raw input
	var columns map[string]arrow.Array
	var rawInput any
	var isRef bool

	if innerInput != nil {
		if refObj, ok := innerInput.(*StepReference); ok {
			isRef = true
			columns = make(map[string]arrow.Array)
			maps.Copy(columns, refObj.Columns)
		} else {
			rawInput = innerInput
		}
	} else {
		// Auto chaining from simulated SHM history
		shm := e.localHistory.GetSimulatedSHM()
		columns = make(map[string]arrow.Array)
		maps.Copy(columns, shm)
	}

	// 3. Execute using UnifiedExecute
	execReq := ExecutionRequest{
		StepName:  stepName,
		Config:    configJSON,
		Resources: resources,
		Columns:   columns,
		RawInput:  rawInput,
	}

	result, err := UnifiedExecute(ctx, e.registry, execReq)
	if err != nil {
		logger.L().Fatal("step execution failed", zap.String("stepName", stepName), zap.Error(err))
		return nil, err
	}

	// 4. Save to simulated SHM/history
	if len(result.Columns) > 0 {
		e.localHistory.Add(stepName, result.Columns)
	}

	if isRef {
		return &StepReference{
			Data:    result.Data,
			Columns: result.Columns,
		}, nil
	}

	return result.Data, nil
}
