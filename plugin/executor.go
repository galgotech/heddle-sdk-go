package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/apache/arrow/go/v18/arrow"
	"github.com/apache/arrow/go/v18/arrow/array"
	"go.uber.org/zap"

	"github.com/galgotech/heddle-lang/pkg/logger"
	baseplugin "github.com/galgotech/heddle-lang/pkg/plugin"
	"github.com/galgotech/heddle-lang/pkg/runtime/locality"
)

type stepExecutor struct {
	registry         Registry
	resourceManager  *ResourceManager
	initResourceFunc func(ctx context.Context, id string, resourceTypeName string, config map[string]any) error
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
	if configType.Kind() == reflect.Pointer {
		return baseplugin.ExecuteStepResponse{
			TaskID:       request.TaskID,
			Status:       baseplugin.StepResponseError,
			ErrorMessage: "step config must be a struct",
		}
	}

	configVal := reflect.New(configType)
	if request.ConfigJSON != "" {
		if err := json.Unmarshal([]byte(request.ConfigJSON), configVal.Interface()); err != nil {
			return baseplugin.ExecuteStepResponse{
				TaskID:       request.TaskID,
				Status:       baseplugin.StepResponseError,
				ErrorMessage: fmt.Errorf("failed to unmarshal config: %w", err).Error(),
			}
		}
	}

	// 2.1 Auto-initialize and inject resource if provided
	if len(request.Resources) > 0 {
		for rID, resDef := range request.Resources {
			_, initialized := e.resourceManager.Get(rID)
			if !initialized {
				logger.L().Info("Auto-initializing resource requested by worker", zap.String("id", rID), zap.String("type", resDef.Type))
				err := e.initResourceFunc(ctx, rID, resDef.Type, resDef.Config)
				if err != nil {
					return baseplugin.ExecuteStepResponse{
						TaskID:       request.TaskID,
						Status:       baseplugin.StepResponseError,
						ErrorMessage: fmt.Sprintf("failed to auto-initialize resource %s: %v", rID, err),
					}
				}
			}
		}
	}

	if request.ResourceId != "" {
		res, ok := e.resourceManager.Get(request.ResourceId)
		if ok {
			v := configVal.Elem()
			for i := 0; i < v.NumField(); i++ {
				field := v.Field(i)
				if field.Type() == reflect.TypeFor[Config]() {
					field.Addr().Interface().(*Config).SetResource(res)
					break
				}
			}
		} else {
			return baseplugin.ExecuteStepResponse{
				TaskID:       request.TaskID,
				Status:       baseplugin.StepResponseError,
				ErrorMessage: fmt.Sprintf("resource %s was not found or initialized", request.ResourceId),
			}
		}
	}

	// 3. Prepare the Input Frame using Zero-Copy SHM access.
	columns := make(map[string]arrow.Array)
	for fieldName, path := range request.InputHandles {
		arr, err := locality.ReadArrowArrayFromPath(path)
		if err != nil {
			logger.L().Error("Failed to read input from SHM", zap.Error(err), zap.String("path", path))
		} else {
			columns[fieldName] = arr
			defer arr.Release()
		}
	}

	inputVal, outputVal := targetStep.newInputOutput()
	if len(columns) > 0 {
		if targetStep.InputType == reflect.TypeFor[*DynamicFrame]() {
			var df *DynamicFrame
			if targetStep.InputType == reflect.TypeFor[*DynamicFrame]() {
				df = inputVal.Interface().(*DynamicFrame)
			} else {
				v := inputVal.Elem()
				for i := 0; i < v.NumField(); i++ {
					f := v.Field(i)
					if f.Type() == reflect.TypeFor[DynamicFrame]() {
						df = f.Addr().Interface().(*DynamicFrame)
						break
					}
				}
			}
			if df != nil {
				df.Columns = make(map[string]any)
				for name, arr := range columns {
					df.Columns[name] = arr
				}
			}
		} else {
			if err := bind(inputVal, targetStep.inputFieldsIndex, columns); err != nil {
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
		results = targetStep.Func.Call([]reflect.Value{
			reflect.ValueOf(execCtx),
			configVal.Elem(),
			inputVal,
			outputVal,
		})
	}()

	if stepPanicked {
		logger.L().Error("Step execution panicked", zap.String("step", request.StepName), zap.Any("panic", panicVal))
		return baseplugin.ExecuteStepResponse{
			TaskID:       request.TaskID,
			Status:       baseplugin.StepResponseError,
			ErrorMessage: fmt.Sprintf("panic: %v", panicVal),
		}
	}

	// 5. Handle output results and commit data to SHM.
	errResult := results[0]
	if !errResult.IsNil() {
		return baseplugin.ExecuteStepResponse{
			TaskID:       request.TaskID,
			Status:       baseplugin.StepResponseError,
			ErrorMessage: errResult.Interface().(error).Error(),
		}
	}

	// Check if the output is a VoidFrame (explicitly no-data).
	if targetStep.OutputType == reflect.TypeFor[VoidFrame]() {
		return baseplugin.ExecuteStepResponse{
			TaskID: request.TaskID,
			Status: baseplugin.StepResponseSuccess,
		}
	}

	outputHandles := make(map[string]string)
	dirtyHandles := make(map[string]string)

	if targetStep.OutputType == reflect.TypeFor[*DynamicFrame]() {
		var df *DynamicFrame
		if targetStep.OutputType == reflect.TypeFor[*DynamicFrame]() {
			df = outputVal.Interface().(*DynamicFrame)
		} else {
			v := outputVal.Elem()
			for i := 0; i < v.NumField(); i++ {
				f := v.Field(i)
				if f.Type() == reflect.TypeFor[DynamicFrame]() {
					df = f.Addr().Interface().(*DynamicFrame)
					break
				}
			}
		}
		if df != nil {
			for name, colData := range df.Columns {
				var arr arrow.Array
				var dirt []uint64

				switch d := colData.(type) {
				case *Int8:
					arr = d.arrayInt8
					dirt = d.dirt
				case *Int16:
					arr = d.arrayInt16
					dirt = d.dirt
				case *Int32:
					arr = d.arrayInt32
					dirt = d.dirt
				case *Int64:
					arr = d.arrayInt64
					dirt = d.dirt
				case *Uint8:
					arr = d.arrayUint8
					dirt = d.dirt
				case *Uint16:
					arr = d.arrayUint16
					dirt = d.dirt
				case *Uint32:
					arr = d.arrayUint32
					dirt = d.dirt
				case *Uint64:
					arr = d.arrayUint64
					dirt = d.dirt
				case *Float32:
					arr = d.arrayFloat32
					dirt = d.dirt
				case *Float64:
					arr = d.arrayFloat64
					dirt = d.dirt
				case *Bool:
					arr = d.arrayBool
					dirt = d.dirt
				case *String:
					arr = d.arrayString
					dirt = d.dirt
				case arrow.Array:
					arr = d
				default:
					return baseplugin.ExecuteStepResponse{
						TaskID:       request.TaskID,
						Status:       baseplugin.StepResponseError,
						ErrorMessage: fmt.Sprintf("unsupported dynamic output field type %T", colData),
					}
				}

				if arr != nil && !reflect.ValueOf(arr).IsNil() {
					path, err := locality.WriteArrowArrayOnlyToShm(arr)
					if err != nil {
						return baseplugin.ExecuteStepResponse{
							TaskID:       request.TaskID,
							Status:       baseplugin.StepResponseError,
							ErrorMessage: fmt.Sprintf("failed to write dynamic output frame: %v", err),
						}
					}
					outputHandles[name] = path

					if dirt != nil {
						hasDirty := false
						for _, b := range dirt {
							if b != 0 {
								hasDirty = true
								break
							}
						}
						if hasDirty {
							dp, err := locality.WriteDirtyToShm(dirt)
							if err != nil {
								logger.L().Error("Failed to write dirty bits to SHM", zap.Error(err))
							} else {
								dirtyHandles[name] = dp
							}
						}
					}
				}
			}
		}
	} else {
		vVal := outputVal.Elem()
		t := vVal.Type()

		for _, i := range targetStep.outputFieldsIndex {
			fValue := vVal.Field(i)
			fieldPtr := fValue.Interface()
			name := t.Field(i).Name

			var arr arrow.Array
			var dirt []uint64

			switch dataFrame := fieldPtr.(type) {
			case *Int8:
				arr = dataFrame.arrayInt8
				dirt = dataFrame.dirt
			case *Int16:
				arr = dataFrame.arrayInt16
				dirt = dataFrame.dirt
			case *Int32:
				arr = dataFrame.arrayInt32
				dirt = dataFrame.dirt
			case *Int64:
				arr = dataFrame.arrayInt64
				dirt = dataFrame.dirt
			case *Uint8:
				arr = dataFrame.arrayUint8
				dirt = dataFrame.dirt
			case *Uint16:
				arr = dataFrame.arrayUint16
				dirt = dataFrame.dirt
			case *Uint32:
				arr = dataFrame.arrayUint32
				dirt = dataFrame.dirt
			case *Uint64:
				arr = dataFrame.arrayUint64
				dirt = dataFrame.dirt
			case *Float32:
				arr = dataFrame.arrayFloat32
				dirt = dataFrame.dirt
			case *Float64:
				arr = dataFrame.arrayFloat64
				dirt = dataFrame.dirt
			case *Bool:
				arr = dataFrame.arrayBool
				dirt = dataFrame.dirt
			case *String:
				arr = dataFrame.arrayString
				dirt = dataFrame.dirt
			default:
				logger.L().Error("unsupported output field type", zap.String("type", fValue.Type().String()))
				return baseplugin.ExecuteStepResponse{
					TaskID:       request.TaskID,
					Status:       baseplugin.StepResponseError,
					ErrorMessage: fmt.Sprintf("unsupported output field type %s", fValue.Type()),
				}
			}

			if arr != nil && !reflect.ValueOf(arr).IsNil() {
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

				hasDirty := false
				for _, d := range dirt {
					if d != 0 {
						hasDirty = true
						break
					}
				}
				if hasDirty {
					dp, err := locality.WriteDirtyToShm(dirt)
					if err != nil {
						logger.L().Error("Failed to write dirty bits to SHM", zap.Error(err))
					} else {
						dirtyHandles[name] = dp
					}
				}
			}
		}
	}

	return baseplugin.ExecuteStepResponse{
		TaskID:        request.TaskID,
		Status:        baseplugin.StepResponseSuccess,
		OutputHandles: outputHandles,
		DirtyHandles:  dirtyHandles,
	}
}

// ExecuteStepDirectly executes a registered step directly/locally (without starting gRPC/Arrow Flight, without SHM)
// using reflection, unmarshaling the configuration, injecting the resource by ID (if provided),
// and calling the step function.
func (e *stepExecutor) ExecuteStepDirectly(ctx context.Context, stepName string, configJSON string, resourceId string, input any, output any) (err error) {
	step, ok := e.registry.GetStep(stepName)
	if !ok {
		return fmt.Errorf("step %s not found", stepName)
	}

	configVal := reflect.New(step.ConfigType)
	if configJSON != "" {
		if err := json.Unmarshal([]byte(configJSON), configVal.Interface()); err != nil {
			return fmt.Errorf("failed to unmarshal config: %w", err)
		}
	}

	// Inject resource if provided
	if resourceId != "" {
		res, ok := e.resourceManager.Get(resourceId)
		if ok {
			v := configVal.Elem()
			for i := 0; i < v.NumField(); i++ {
				if v.Field(i).Type() == reflect.TypeFor[Config]() {
					v.Field(i).Addr().Interface().(*Config).SetResource(res)
					break
				}
			}
		} else {
			return fmt.Errorf("active resource %s not found", resourceId)
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
		results = step.Func.Call([]reflect.Value{
			reflect.ValueOf(ctx),
			configVal.Elem(),
			reflect.ValueOf(input),
			reflect.ValueOf(output),
		})
	}()

	if stepPanicked {
		return fmt.Errorf("panic: %v", panicVal)
	}

	errResult := results[0]
	if !errResult.IsNil() {
		return errResult.Interface().(error)
	}

	return nil
}

// bind maps Arrow Table columns to Go struct fields.
func bind(reflectValue reflect.Value, fieldIndices []int, columns map[string]arrow.Array) error {
	if reflectValue.Kind() != reflect.Pointer {
		return fmt.Errorf("type %v is not a pointer", reflectValue.Type())
	}

	v := reflectValue.Elem()
	if v.Kind() != reflect.Struct {
		return fmt.Errorf("type %v is not a struct", v.Type())
	}

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

	t := v.Type()
	for _, i := range fieldIndices {
		fValue := v.Field(i)
		fieldPtr := fValue.Interface()

		name := t.Field(i).Name
		arr := columns[name]
		if arr == nil {
			return fmt.Errorf("column %q is required but missing", name)
		}

		switch df := fieldPtr.(type) {
		case *Int8:
			df.arrayInt8 = arr.(*array.Int8)
			df.dirt = []uint64{}
		case *Int16:
			df.arrayInt16 = arr.(*array.Int16)
			df.dirt = []uint64{}
		case *Int32:
			df.arrayInt32 = arr.(*array.Int32)
			df.dirt = []uint64{}
		case *Int64:
			df.arrayInt64 = arr.(*array.Int64)
			df.dirt = []uint64{}
		case *Uint8:
			df.arrayUint8 = arr.(*array.Uint8)
			df.dirt = []uint64{}
		case *Uint16:
			df.arrayUint16 = arr.(*array.Uint16)
			df.dirt = []uint64{}
		case *Uint32:
			df.arrayUint32 = arr.(*array.Uint32)
			df.dirt = []uint64{}
		case *Uint64:
			df.arrayUint64 = arr.(*array.Uint64)
			df.dirt = []uint64{}
		case *Float32:
			df.arrayFloat32 = arr.(*array.Float32)
			df.dirt = []uint64{}
		case *Float64:
			df.arrayFloat64 = arr.(*array.Float64)
			df.dirt = []uint64{}
		case *Bool:
			df.arrayBool = arr.(*array.Boolean)
			df.dirt = []uint64{}
		case *String:
			df.arrayString = arr.(*array.String)
			df.dirt = []uint64{}
		default:
			return fmt.Errorf("field name '%s' has unsupported type %v", name, fValue.Type())
		}
	}

	return nil
}

func NewExecutor(
	reg Registry,
	rm *ResourceManager,
	initResourceFunc func(ctx context.Context, id string, resourceTypeName string, config map[string]any) error,
) Executor {
	return &stepExecutor{
		registry:         reg,
		resourceManager:  rm,
		initResourceFunc: initResourceFunc,
	}
}
