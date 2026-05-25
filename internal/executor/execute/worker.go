package execute

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/apache/arrow/go/v18/arrow"
	"go.uber.org/zap"

	"github.com/galgotech/heddle-lang/pkg/logger"
	baseplugin "github.com/galgotech/heddle-lang/pkg/plugin"
	"github.com/galgotech/heddle-lang/pkg/runtime/locality"

	"github.com/galgotech/heddle-sdk-go/internal/accessor"
	internalarrow "github.com/galgotech/heddle-sdk-go/internal/arrow"
	"github.com/galgotech/heddle-sdk-go/internal/executor/history"
	"github.com/galgotech/heddle-sdk-go/internal/registry"
	"github.com/galgotech/heddle-sdk-go/schema"
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

	// 1. Resolve the requested step.
	targetStep, ok := e.registry.GetStep(request.StepName)
	if !ok {
		return trackAndReturn(baseplugin.ExecuteStepResponse{
			TaskID:       request.TaskID,
			Status:       baseplugin.StepResponseError,
			ErrorMessage: fmt.Sprintf("step %s not found", request.StepName),
		}), nil
	}

	// 2. Hydrate the step configuration from the provided JSON.
	configType := targetStep.ConfigType
	configVal := reflect.Zero(configType)
	if request.ConfigJSON != "" {
		if err := json.Unmarshal([]byte(request.ConfigJSON), configVal.Addr().Interface()); err != nil {
			return trackAndReturn(baseplugin.ExecuteStepResponse{
				TaskID:       request.TaskID,
				Status:       baseplugin.StepResponseError,
				ErrorMessage: fmt.Errorf("failed to unmarshal config: %w", err).Error(),
			}), nil
		}
	}

	// Create a new instance of the step group struct to act as the receiver,
	// copying the registered structVal fields. This avoids concurrent execution conflicts.
	receiverVal := reflect.New(targetStep.StructVal.Type())
	receiverVal.Elem().Set(targetStep.StructVal)

	// 2.1 Configure resource fields with their definitions from the worker
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

			// Determine resource instance ID
			resourceID := request.ResourceId

			// Initialize and retrieve the active resource instance
			initializedRes, err := e.registry.InitializeResource(resourceID, resourceDefinition.Type, resourceDefinition.Config)
			if err != nil {
				return trackAndReturn(baseplugin.ExecuteStepResponse{
					TaskID:       request.TaskID,
					Status:       baseplugin.StepResponseError,
					ErrorMessage: fmt.Sprintf("failed to initialize resource %s: %v", resourceReference, err),
				}), nil
			}

			// Inject the initialized resource into the receiver's field
			field := receiverVal.Elem().FieldByName(resourceReference)
			if !field.IsValid() {
				// Fallback to case-insensitive lookup
				for i := 0; i < receiverVal.Elem().NumField(); i++ {
					f := receiverVal.Elem().Type().Field(i)
					if strings.EqualFold(f.Name, resourceReference) {
						field = receiverVal.Elem().Field(i)
						break
					}
				}
			}

			if field.IsValid() {
				if field.Addr().CanInterface() {
					if setter, ok := field.Addr().Interface().(schema.ResourceSetter); ok {
						setter.SetResource(initializedRes)
					} else {
						return trackAndReturn(baseplugin.ExecuteStepResponse{
							TaskID:       request.TaskID,
							Status:       baseplugin.StepResponseError,
							ErrorMessage: fmt.Sprintf("resource field %s does not implement ResourceSetter", resourceReference),
						}), nil
					}
				}
			}
		}
	}

	// 3. Prepare the Input Frame using Zero-Copy SHM access.
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

	var inputVal reflect.Value
	if len(columns) > 0 {
		if targetStep.InputType == reflect.TypeFor[*schema.Any]() {
			inputVal = reflect.ValueOf(schema.NewAnyAccessor(accessor.Token{}, columns))

		} else {
			inputVal = reflect.New(targetStep.InputType.Elem())
			if err := Bind(inputVal, targetStep.InputFieldsIndex, columns); err != nil {
				return trackAndReturn(baseplugin.ExecuteStepResponse{
					TaskID:       request.TaskID,
					Status:       baseplugin.StepResponseError,
					ErrorMessage: fmt.Sprintf("failed to bind input frame: %v", err),
				}), nil
			}
		}
	}

	// 4. Implement Timeout and Panic Recovery for Step Execution.
	execCtx := ctx
	var cancel context.CancelFunc
	if _, ok := execCtx.Deadline(); !ok {
		execCtx, cancel = context.WithTimeout(ctx, 15*time.Minute)
		defer cancel()
	}

	var results []reflect.Value
	var stepPanicked bool
	var panicVal any

	func() {
		defer func() {
			if r := recover(); r != nil {
				stepPanicked = true
				panicVal = r
			}
		}()

		args := []reflect.Value{
			receiverVal,
			reflect.ValueOf(execCtx),
			configVal,
			inputVal,
		}
		results = targetStep.Func.Call(args)
	}()

	if stepPanicked {
		logger.L().Error("Step execution panicked", zap.String("step", request.StepName), zap.Any("panic", panicVal))
		return trackAndReturn(baseplugin.ExecuteStepResponse{
			TaskID:       request.TaskID,
			Status:       baseplugin.StepResponseError,
			ErrorMessage: fmt.Sprintf("panic: %v", panicVal),
		}), nil
	}

	vVal := results[0]
	if vVal.Kind() == reflect.Pointer {
		if vVal.IsNil() {
			return trackAndReturn(baseplugin.ExecuteStepResponse{
				TaskID: request.TaskID,
				Status: baseplugin.StepResponseError,
			}), nil
		}
		vVal = vVal.Elem()
	}

	// Check if the output is a VoidFrame (explicitly no-data).
	outT := targetStep.OutputType
	if outT == reflect.TypeFor[*schema.Void]() {
		return trackAndReturn(baseplugin.ExecuteStepResponse{
			TaskID: request.TaskID,
			Status: baseplugin.StepResponseSuccess,
		}), nil
	}

	outputHandles := make(map[string]string)
	if targetStep.OutputType == reflect.TypeFor[*schema.Any]() {
		var df *schema.Any
		if !results[0].IsNil() {
			df = results[0].Interface().(*schema.Any)
		}
		if df != nil {
			for _, name := range df.Columns() {
				colData, _ := df.Get(name)
				arr, err := internalarrow.SliceToArrowArray(colData)
				if err != nil {
					return trackAndReturn(baseplugin.ExecuteStepResponse{
						TaskID:       request.TaskID,
						Status:       baseplugin.StepResponseError,
						ErrorMessage: fmt.Sprintf("failed to convert dynamic output field %s to Arrow: %v", name, err),
					}), nil
				}
				if arr != nil && !reflect.ValueOf(arr).IsNil() {
					defer arr.Release()
					path, err := locality.WriteArrowArrayOnlyToShm(arr)
					if err != nil {
						return trackAndReturn(baseplugin.ExecuteStepResponse{
							TaskID:       request.TaskID,
							Status:       baseplugin.StepResponseError,
							ErrorMessage: fmt.Sprintf("failed to write dynamic output frame: %v", err),
						}), nil
					}
					outputHandles[name] = path
				}
			}
		}

	} else {
		t := vVal.Type()

		for _, i := range targetStep.OutputFieldsIndex {
			fValue := vVal.Field(i)
			name := t.Field(i).Name

			if colAcc, ok := GetColAccessor(fValue); ok {
				token := accessor.Token{}
				arr := colAcc.GetArrowArray(token)
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
					} else {
						outputHandles[name] = path
					}
				}
			}
		}
	}

	return trackAndReturn(baseplugin.ExecuteStepResponse{
		TaskID:        request.TaskID,
		Status:        baseplugin.StepResponseSuccess,
		OutputHandles: outputHandles,
	}), nil
}
