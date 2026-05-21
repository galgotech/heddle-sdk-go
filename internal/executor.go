package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/apache/arrow/go/v18/arrow"
	"github.com/apache/arrow/go/v18/arrow/array"
	"github.com/apache/arrow/go/v18/arrow/memory"
	"go.uber.org/zap"

	"github.com/galgotech/heddle-lang/pkg/logger"
	baseplugin "github.com/galgotech/heddle-lang/pkg/plugin"
	"github.com/galgotech/heddle-lang/pkg/runtime/locality"

	"github.com/galgotech/heddle-sdk-go/internal/resourcelink"
	pluginschema "github.com/galgotech/heddle-sdk-go/schema"
)

type Executor interface {
	ExecuteTask(ctx context.Context, request baseplugin.ExecuteStepRequest) baseplugin.ExecuteStepResponse
	ExecuteStepDirectly(ctx context.Context, stepName string, configJSON any, input any) (any, error)
}

type stepExecutor struct {
	registry Registry
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

	receiverVal := reflect.New(targetStep.GroupInstance.Type())
	receiverVal.Elem().Set(targetStep.GroupInstance)

	// 2.1 Configure resource fields with their definitions from the worker
	if len(request.Resources) > 0 {
		v := receiverVal.Elem()
		for i := 0; i < v.NumField(); i++ {
			field := v.Field(i)
			if isResource(field.Type()) {
				// field.Type() is schema.Resource[T]
				// The first field of schema.Resource[T] is resource T (which is field.Type().Field(0).Type)
				resType := field.Type().Field(0).Type
				if resType.Kind() == reflect.Pointer {
					resType = resType.Elem()
				}
				typeName := strings.ToLower(resType.Name())

				var matchedDef *baseplugin.ResourceDefinition
				// 1. Try to match by request.ResourceId
				if request.ResourceId != "" {
					// Direct lookup
					if rd, ok := request.Resources[request.ResourceId]; ok {
						if strings.ToLower(rd.Type) == typeName || strings.HasSuffix(strings.ToLower(rd.Type), "."+typeName) {
							matchedDef = &rd
						}
					}
					// Case-insensitive/approximate lookup if direct lookup failed
					if matchedDef == nil {
						for id, rd := range request.Resources {
							if strings.EqualFold(id, request.ResourceId) {
								if strings.ToLower(rd.Type) == typeName || strings.HasSuffix(strings.ToLower(rd.Type), "."+typeName) {
									matchedDef = &rd
									break
								}
							}
						}
					}
				}
				// 2. Fallback: match by resource type name in request.Resources
				if matchedDef == nil {
					for _, rd := range request.Resources {
						if strings.ToLower(rd.Type) == typeName || strings.HasSuffix(strings.ToLower(rd.Type), "."+typeName) {
							matchedDef = &rd
							break
						}
					}
				}

				if matchedDef != nil {
					if field.CanAddr() {
						resourcelink.Configure(field.Addr().Interface(), matchedDef.Config)
					}
				}
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

	inputVal := targetStep.newInput()
	if len(columns) > 0 {
		if targetStep.InputType == reflect.TypeFor[*pluginschema.Any]() || targetStep.InputType == reflect.TypeFor[pluginschema.Any]() {
			var df *pluginschema.Any
			if targetStep.InputType == reflect.TypeFor[*pluginschema.Any]() {
				df = inputVal.Interface().(*pluginschema.Any)
			} else {
				df = inputVal.Addr().Interface().(*pluginschema.Any)
			}
			if df != nil {
				for name, arr := range columns {
					slice := arrowArrayToSlice(arr)
					df.Set(name, slice)
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
		var configArg reflect.Value
		if targetStep.Func.Type().In(2).Kind() == reflect.Pointer {
			configArg = configVal
		} else {
			configArg = configVal.Elem()
		}

		stepFuncType := targetStep.Func.Type()
		inArgType := stepFuncType.In(3)
		var inArg reflect.Value
		if inArgType.Kind() == reflect.Pointer {
			inArg = inputVal
		} else {
			inArg = inputVal.Elem()
		}

		args := []reflect.Value{
			receiverVal,
			reflect.ValueOf(execCtx),
			configArg,
			inArg,
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

	// 5. Handle output results and commit data to SHM.
	errResult := results[1]
	if !errResult.IsNil() {
		return baseplugin.ExecuteStepResponse{
			TaskID:       request.TaskID,
			Status:       baseplugin.StepResponseError,
			ErrorMessage: errResult.Interface().(error).Error(),
		}
	}

	vVal := results[0]
	if vVal.Kind() == reflect.Pointer {
		if vVal.IsNil() {
			return baseplugin.ExecuteStepResponse{
				TaskID: request.TaskID,
				Status: baseplugin.StepResponseSuccess,
			}
		}
		vVal = vVal.Elem()
	}

	// Check if the output is a VoidFrame (explicitly no-data).
	outT := targetStep.OutputType
	if outT.Kind() == reflect.Pointer {
		outT = outT.Elem()
	}
	if outT == reflect.TypeFor[pluginschema.Void]() {
		return baseplugin.ExecuteStepResponse{
			TaskID: request.TaskID,
			Status: baseplugin.StepResponseSuccess,
		}
	}

	outputHandles := make(map[string]string)
	dirtyHandles := make(map[string]string)

	if targetStep.OutputType == reflect.TypeFor[*pluginschema.Any]() {
		var df *pluginschema.Any
		if !results[0].IsNil() {
			df = results[0].Interface().(*pluginschema.Any)
		}
		if df != nil {
			for name, colData := range df.Columns() {
				arr, err := sliceToArrowArray(colData)
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

		for _, i := range targetStep.outputFieldsIndex {
			fValue := vVal.Field(i)
			name := t.Field(i).Name

			if isCol(fValue.Type()) {
				dataSlice := fValue.FieldByName("Data").Interface()
				arr, err := sliceToArrowArray(dataSlice)
				if err != nil {
					return baseplugin.ExecuteStepResponse{
						TaskID:       request.TaskID,
						Status:       baseplugin.StepResponseError,
						ErrorMessage: fmt.Sprintf("failed to convert output field %s to Arrow: %v", name, err),
					}
				}
				if arr != nil && !reflect.ValueOf(arr).IsNil() {
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
func (e *stepExecutor) ExecuteStepDirectly(ctx context.Context, stepName string, configJSON any, input any) (output any, err error) {
	step, ok := e.registry.GetStep(stepName)
	if !ok {
		return nil, fmt.Errorf("step %s not found", stepName)
	}

	configVal := reflect.New(step.ConfigType)
	if configJSON != nil {
		var configBytes []byte
		switch v := configJSON.(type) {
		case string:
			if v != "" {
				configBytes = []byte(v)
			}
		case []byte:
			if len(v) > 0 {
				configBytes = v
			}
		default:
			if b, err := json.Marshal(v); err == nil {
				configBytes = b
			}
		}
		if len(configBytes) > 0 {
			if err := json.Unmarshal(configBytes, configVal.Interface()); err != nil {
				return nil, fmt.Errorf("failed to unmarshal config: %w", err)
			}
		}
	}

	receiverVal := reflect.New(step.GroupInstance.Type())
	receiverVal.Elem().Set(step.GroupInstance)

	// No central resource injection required for local execution anymore,
	// as resources manage their own lifecycle and configurations.

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
		var configArg reflect.Value
		if step.Func.Type().In(2).Kind() == reflect.Pointer {
			configArg = configVal
		} else {
			configArg = configVal.Elem()
		}
		args := []reflect.Value{
			receiverVal,
			reflect.ValueOf(ctx),
			configArg,
			reflect.ValueOf(input),
		}
		results = step.Func.Call(args)
	}()

	if stepPanicked {
		return nil, fmt.Errorf("panic: %v", panicVal)
	}

	var outVal any
	if !results[0].IsNil() {
		outVal = results[0].Interface()
	}

	var execErr error
	if !results[1].IsNil() {
		execErr = results[1].Interface().(error)
	}

	return outVal, execErr
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
		name := t.Field(i).Name
		arr := columns[name]
		if arr == nil {
			return fmt.Errorf("column %q is required but missing", name)
		}

		if isCol(fValue.Type()) {
			slice := arrowArrayToSlice(arr)
			if slice == nil {
				return fmt.Errorf("failed to convert arrow array for column %s", name)
			}
			fValue.FieldByName("Data").Set(reflect.ValueOf(slice))
		}
	}

	return nil
}

func arrowArrayToSlice(arr arrow.Array) any {
	switch a := arr.(type) {
	case *array.Int8:
		res := make([]int8, a.Len())
		for i := 0; i < a.Len(); i++ {
			res[i] = a.Value(i)
		}
		return res
	case *array.Int16:
		res := make([]int16, a.Len())
		for i := 0; i < a.Len(); i++ {
			res[i] = a.Value(i)
		}
		return res
	case *array.Int32:
		res := make([]int32, a.Len())
		for i := 0; i < a.Len(); i++ {
			res[i] = a.Value(i)
		}
		return res
	case *array.Int64:
		res := make([]int64, a.Len())
		for i := 0; i < a.Len(); i++ {
			res[i] = a.Value(i)
		}
		return res
	case *array.Uint8:
		res := make([]uint8, a.Len())
		for i := 0; i < a.Len(); i++ {
			res[i] = a.Value(i)
		}
		return res
	case *array.Uint16:
		res := make([]uint16, a.Len())
		for i := 0; i < a.Len(); i++ {
			res[i] = a.Value(i)
		}
		return res
	case *array.Uint32:
		res := make([]uint32, a.Len())
		for i := 0; i < a.Len(); i++ {
			res[i] = a.Value(i)
		}
		return res
	case *array.Uint64:
		res := make([]uint64, a.Len())
		for i := 0; i < a.Len(); i++ {
			res[i] = a.Value(i)
		}
		return res
	case *array.Float32:
		res := make([]float32, a.Len())
		for i := 0; i < a.Len(); i++ {
			res[i] = a.Value(i)
		}
		return res
	case *array.Float64:
		res := make([]float64, a.Len())
		for i := 0; i < a.Len(); i++ {
			res[i] = a.Value(i)
		}
		return res
	case *array.Boolean:
		res := make([]bool, a.Len())
		for i := 0; i < a.Len(); i++ {
			res[i] = a.Value(i)
		}
		return res
	case *array.String:
		res := make([]string, a.Len())
		for i := 0; i < a.Len(); i++ {
			res[i] = a.Value(i)
		}
		return res
	default:
		return nil
	}
}

func sliceToArrowArray(sliceAny any) (arrow.Array, error) {
	mem := memory.DefaultAllocator
	switch s := sliceAny.(type) {
	case []int8:
		b := array.NewInt8Builder(mem)
		defer b.Release()
		b.AppendValues(s, nil)
		return b.NewArray(), nil
	case []int16:
		b := array.NewInt16Builder(mem)
		defer b.Release()
		b.AppendValues(s, nil)
		return b.NewArray(), nil
	case []int32:
		b := array.NewInt32Builder(mem)
		defer b.Release()
		b.AppendValues(s, nil)
		return b.NewArray(), nil
	case []int64:
		b := array.NewInt64Builder(mem)
		defer b.Release()
		b.AppendValues(s, nil)
		return b.NewArray(), nil
	case []uint8:
		b := array.NewUint8Builder(mem)
		defer b.Release()
		b.AppendValues(s, nil)
		return b.NewArray(), nil
	case []uint16:
		b := array.NewUint16Builder(mem)
		defer b.Release()
		b.AppendValues(s, nil)
		return b.NewArray(), nil
	case []uint32:
		b := array.NewUint32Builder(mem)
		defer b.Release()
		b.AppendValues(s, nil)
		return b.NewArray(), nil
	case []uint64:
		b := array.NewUint64Builder(mem)
		defer b.Release()
		b.AppendValues(s, nil)
		return b.NewArray(), nil
	case []float32:
		b := array.NewFloat32Builder(mem)
		defer b.Release()
		b.AppendValues(s, nil)
		return b.NewArray(), nil
	case []float64:
		b := array.NewFloat64Builder(mem)
		defer b.Release()
		b.AppendValues(s, nil)
		return b.NewArray(), nil
	case []bool:
		b := array.NewBooleanBuilder(mem)
		defer b.Release()
		b.AppendValues(s, nil)
		return b.NewArray(), nil
	case []string:
		b := array.NewStringBuilder(mem)
		defer b.Release()
		b.AppendValues(s, nil)
		return b.NewArray(), nil
	default:
		val := reflect.ValueOf(sliceAny)
		if val.Kind() != reflect.Slice {
			return nil, fmt.Errorf("sliceToArrowArray: expected slice, got %T", sliceAny)
		}
		elemType := val.Type().Elem()
		switch elemType.Kind() {
		case reflect.Int8:
			b := array.NewInt8Builder(mem)
			defer b.Release()
			for i := 0; i < val.Len(); i++ {
				b.Append(int8(val.Index(i).Int()))
			}
			return b.NewArray(), nil
		case reflect.Int16:
			b := array.NewInt16Builder(mem)
			defer b.Release()
			for i := 0; i < val.Len(); i++ {
				b.Append(int16(val.Index(i).Int()))
			}
			return b.NewArray(), nil
		case reflect.Int32:
			b := array.NewInt32Builder(mem)
			defer b.Release()
			for i := 0; i < val.Len(); i++ {
				b.Append(int32(val.Index(i).Int()))
			}
			return b.NewArray(), nil
		case reflect.Int64:
			b := array.NewInt64Builder(mem)
			defer b.Release()
			for i := 0; i < val.Len(); i++ {
				b.Append(val.Index(i).Int())
			}
			return b.NewArray(), nil
		case reflect.Uint8:
			b := array.NewUint8Builder(mem)
			defer b.Release()
			for i := 0; i < val.Len(); i++ {
				b.Append(uint8(val.Index(i).Uint()))
			}
			return b.NewArray(), nil
		case reflect.Uint16:
			b := array.NewUint16Builder(mem)
			defer b.Release()
			for i := 0; i < val.Len(); i++ {
				b.Append(uint16(val.Index(i).Uint()))
			}
			return b.NewArray(), nil
		case reflect.Uint32:
			b := array.NewUint32Builder(mem)
			defer b.Release()
			for i := 0; i < val.Len(); i++ {
				b.Append(uint32(val.Index(i).Uint()))
			}
			return b.NewArray(), nil
		case reflect.Uint64:
			b := array.NewUint64Builder(mem)
			defer b.Release()
			for i := 0; i < val.Len(); i++ {
				b.Append(val.Index(i).Uint())
			}
			return b.NewArray(), nil
		case reflect.Float32:
			b := array.NewFloat32Builder(mem)
			defer b.Release()
			for i := 0; i < val.Len(); i++ {
				b.Append(float32(val.Index(i).Float()))
			}
			return b.NewArray(), nil
		case reflect.Float64:
			b := array.NewFloat64Builder(mem)
			defer b.Release()
			for i := 0; i < val.Len(); i++ {
				b.Append(val.Index(i).Float())
			}
			return b.NewArray(), nil
		case reflect.Bool:
			b := array.NewBooleanBuilder(mem)
			defer b.Release()
			for i := 0; i < val.Len(); i++ {
				b.Append(val.Index(i).Bool())
			}
			return b.NewArray(), nil
		case reflect.String:
			b := array.NewStringBuilder(mem)
			defer b.Release()
			for i := 0; i < val.Len(); i++ {
				b.Append(val.Index(i).String())
			}
			return b.NewArray(), nil
		}
		return nil, fmt.Errorf("unsupported slice element type: %s", elemType.Kind())
	}
}

func NewExecutor(registry Registry) Executor {
	return &stepExecutor{
		registry: registry,
	}
}
