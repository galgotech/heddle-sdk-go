package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/apache/arrow/go/v18/arrow"
	"github.com/apache/arrow/go/v18/arrow/array"
	"go.uber.org/zap"

	"github.com/galgotech/heddle-lang/pkg/logger"
	baseplugin "github.com/galgotech/heddle-lang/pkg/plugin"
	"github.com/galgotech/heddle-lang/pkg/runtime/locality"

	"github.com/galgotech/heddle-sdk-go/internal/accessor"
	internalarrow "github.com/galgotech/heddle-sdk-go/internal/arrow"
	"github.com/galgotech/heddle-sdk-go/internal/registry"
	internalschema "github.com/galgotech/heddle-sdk-go/internal/schema"
	"github.com/galgotech/heddle-sdk-go/schema"
	pluginschema "github.com/galgotech/heddle-sdk-go/schema"
)

type Executor interface {
	ExecuteTask(ctx context.Context, request baseplugin.ExecuteStepRequest) baseplugin.ExecuteStepResponse
	ExecuteStepDirectly(ctx context.Context, stepName string, configJSON any, input any) any
}

type stepExecutor struct {
	registry registry.Registry
}

// ExecuteTask handles the end-to-end execution of a single Heddle Step.
// It performs Zero-Copy data loading from SHM, reflection-based binding to Go structs,
// function invocation, and result serialization back to SHM.
func (e *stepExecutor) ExecuteTask(ctx context.Context, request baseplugin.ExecuteStepRequest) baseplugin.ExecuteStepResponse {
	// 1. Resolve the requested step.
	targetStep, ok := e.registry.GetStep(request.StepName)
	if !ok {
		return baseplugin.ExecuteStepResponse{
			TaskID:       request.TaskID,
			Status:       baseplugin.StepResponseError,
			ErrorMessage: fmt.Sprintf("step %s not found", request.StepName),
		}
	}

	// 2. Hydrate the step configuration from the provided JSON.
	configType := targetStep.ConfigType
	configVal := reflect.Zero(configType)
	if request.ConfigJSON != "" {
		if err := json.Unmarshal([]byte(request.ConfigJSON), configVal.Addr().Interface()); err != nil {
			return baseplugin.ExecuteStepResponse{
				TaskID:       request.TaskID,
				Status:       baseplugin.StepResponseError,
				ErrorMessage: fmt.Errorf("failed to unmarshal config: %w", err).Error(),
			}
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
				return baseplugin.ExecuteStepResponse{
					TaskID:       request.TaskID,
					Status:       baseplugin.StepResponseError,
					ErrorMessage: fmt.Sprintf("resource type %s not registered", resourceDefinition.Type),
				}
			}

			// Determine resource instance ID
			resourceID := request.ResourceId

			// Initialize and retrieve the active resource instance
			initializedRes, err := e.registry.InitializeResource(resourceID, resourceDefinition.Type, resourceDefinition.Config)
			if err != nil {
				return baseplugin.ExecuteStepResponse{
					TaskID:       request.TaskID,
					Status:       baseplugin.StepResponseError,
					ErrorMessage: fmt.Sprintf("failed to initialize resource %s: %v", resourceReference, err),
				}
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
					if setter, ok := field.Addr().Interface().(pluginschema.ResourceSetter); ok {
						setter.SetResource(initializedRes)
					} else {
						return baseplugin.ExecuteStepResponse{
							TaskID:       request.TaskID,
							Status:       baseplugin.StepResponseError,
							ErrorMessage: fmt.Sprintf("resource field %s does not implement ResourceSetter", resourceReference),
						}
					}
				}
			}
		}
	}

	// 3. Prepare the Input Frame using Zero-Copy SHM access.
	columns := make(map[string]arrow.Array)
	ids := make(map[string]arrow.Array)
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
		if targetStep.InputType == reflect.TypeFor[*pluginschema.Any]() {
			inputVal = reflect.ValueOf(schema.NewAnyAccessor(accessor.Token{}, columns, ids))

		} else {
			inputVal = reflect.New(targetStep.InputType.Elem())
			if err := bind(inputVal, targetStep.InputFieldsIndex, columns); err != nil {
				return baseplugin.ExecuteStepResponse{
					TaskID:       request.TaskID,
					Status:       baseplugin.StepResponseError,
					ErrorMessage: fmt.Sprintf("failed to bind input frame: %v", err),
				}
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
		return baseplugin.ExecuteStepResponse{
			TaskID:       request.TaskID,
			Status:       baseplugin.StepResponseError,
			ErrorMessage: fmt.Sprintf("panic: %v", panicVal),
		}
	}

	vVal := results[0]
	if vVal.Kind() == reflect.Pointer {
		if vVal.IsNil() {
			return baseplugin.ExecuteStepResponse{
				TaskID: request.TaskID,
				Status: baseplugin.StepResponseError,
			}
		}
		vVal = vVal.Elem()
	}

	// Check if the output is a VoidFrame (explicitly no-data).
	outT := targetStep.OutputType
	if outT == reflect.TypeFor[*pluginschema.Void]() {
		return baseplugin.ExecuteStepResponse{
			TaskID: request.TaskID,
			Status: baseplugin.StepResponseSuccess,
		}
	}

	outputHandles := make(map[string]string)
	if targetStep.OutputType == reflect.TypeFor[*pluginschema.Any]() {
		var df *pluginschema.Any
		if !results[0].IsNil() {
			df = results[0].Interface().(*pluginschema.Any)
		}
		if df != nil {
			for _, name := range df.Columns() {
				colData, _ := df.Get(name)
				arr, err := internalarrow.SliceToArrowArray(colData)
				if err != nil {
					return baseplugin.ExecuteStepResponse{
						TaskID:       request.TaskID,
						Status:       baseplugin.StepResponseError,
						ErrorMessage: fmt.Sprintf("failed to convert dynamic output field %s to Arrow: %v", name, err),
					}
				}
				if arr != nil && !reflect.ValueOf(arr).IsNil() {
					defer arr.Release()
					path, err := locality.WriteArrowArrayOnlyToShm(arr)
					if err != nil {
						return baseplugin.ExecuteStepResponse{
							TaskID:       request.TaskID,
							Status:       baseplugin.StepResponseError,
							ErrorMessage: fmt.Sprintf("failed to write dynamic output frame: %v", err),
						}
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

			if internalschema.IsCol(fValue.Type()) {
				if colAcc, ok := fValue.Addr().Interface().(pluginschema.ColAccessor); ok {
					token := accessor.Token{}
					arr := colAcc.GetArrowArray(token)
					if arr != nil && !reflect.ValueOf(arr).IsNil() {
						arr.Retain()
						defer arr.Release()
						path, err := locality.WriteArrowArrayOnlyToShm(arr)
						if err != nil {
							return baseplugin.ExecuteStepResponse{
								TaskID:       request.TaskID,
								Status:       baseplugin.StepResponseError,
								ErrorMessage: fmt.Sprintf("failed to write output frame: %v", err),
							}
						} else {
							outputHandles[name] = path
						}
					}

					idsSlice := colAcc.GetIDs(token)
					if idsArr, err := internalarrow.SliceToArrowArray(idsSlice); err == nil && idsArr != nil && !reflect.ValueOf(idsArr).IsNil() {
						defer idsArr.Release()
						if path, err := locality.WriteArrowArrayOnlyToShm(idsArr); err == nil {
							outputHandles[name+"_id"] = path
						}
					}
				}
			}
		}
	}

	return baseplugin.ExecuteStepResponse{
		TaskID:        request.TaskID,
		Status:        baseplugin.StepResponseSuccess,
		OutputHandles: outputHandles,
	}
}

// ExecuteStepDirectly executes a registered step directly/locally (without starting gRPC/Arrow Flight, without SHM)
// using reflection, unmarshaling the configuration, injecting the resource by ID (if provided),
// and calling the step function.
func (e *stepExecutor) ExecuteStepDirectly(ctx context.Context, stepName string, configJSON any, input any) any {
	step, ok := e.registry.GetStep(stepName)
	if !ok {
		logger.L().Fatal("step not found", zap.String("stepName", stepName))
		return nil
	}

	configVal := reflect.New(step.ConfigType)
	receiverVal := reflect.New(step.StructVal.Type())
	receiverVal.Elem().Set(step.StructVal)

	// Inject bound resources into the receiver's fields for direct/local execution
	for i := 0; i < receiverVal.Elem().NumField(); i++ {
		fieldType := receiverVal.Elem().Type().Field(i)
		if internalschema.IsResource(fieldType.Type) {
			fieldName := fieldType.Name
			if instID, bound := e.registry.GetResourceBinding(fieldName); bound {
				if inst, exists := e.registry.GetResourceInstance(instID); exists {
					field := receiverVal.Elem().Field(i)
					if field.Addr().CanInterface() {
						if setter, ok := field.Addr().Interface().(pluginschema.ResourceSetter); ok {
							setter.SetResource(inst)
						}
					}
				}
			}
		}
	}

	// Call the step function with Panic Recovery.
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

		configArg := configVal.Elem()
		inputVal := reflect.ValueOf(input)
		args := []reflect.Value{
			receiverVal,
			reflect.ValueOf(ctx),
			configArg,
			inputVal,
		}
		results = step.Func.Call(args)
	}()

	if stepPanicked {
		logger.L().Fatal("step panicked", zap.String("stepName", stepName), zap.Any("panicVal", panicVal))
		return nil
	}

	outVal := results[0].Interface()
	return outVal
}

// bind maps Arrow Table columns to Go struct fields.
func bind(reflectValue reflect.Value, fieldIndices []int, columns map[string]arrow.Array) error {
	var numRows int = -1
	for _, arr := range columns {
		if numRows == -1 {
			numRows = arr.Len()
		} else if numRows != arr.Len() {
			return fmt.Errorf("inconsistent column lengths")
		}
	}
	if numRows == -1 {
		numRows = 0
	}

	if reflectValue.Kind() == reflect.Pointer {
		reflectValue = reflectValue.Elem()
	}
	reflectType := reflectValue.Type()
	for _, i := range fieldIndices {
		fValue := reflectValue.Field(i)
		name := reflectType.Field(i).Name
		arr := columns[name]
		if arr == nil {
			return fmt.Errorf("column %q is required but missing", name)
		}
		idArr, ok := columns[name+"_id"]
		if !ok {
			return fmt.Errorf("column %q is required but missing", name+"_id")
		}

		colAcc, ok := fValue.Addr().Interface().(pluginschema.ColAccessor)
		if !ok {
			return fmt.Errorf("%s column %q is not a ColAccessor", reflectValue.Type().Field(i).Name, name)
		}

		arr.Retain()
		idArr.Retain()
		colAcc.SetData(accessor.Token{}, arr, idArr.(*array.Int64))

	}

	return nil
}

func NewExecutor(registry registry.Registry) Executor {
	return &stepExecutor{
		registry: registry,
	}
}
