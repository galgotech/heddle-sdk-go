package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unsafe"

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
	ExecuteStepDirectly(ctx context.Context, stepName string, configJSON any, input any) any
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
		for _, field := range v.Fields() {
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
	if len(results) == 2 {
		errResult := results[1]
		if !errResult.IsNil() {
			return baseplugin.ExecuteStepResponse{
				TaskID:       request.TaskID,
				Status:       baseplugin.StepResponseError,
				ErrorMessage: errResult.Interface().(error).Error(),
			}
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

	inputIDs := extractIDs(inputVal)
	overwriteOutputIDs(vVal, inputIDs)

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
				dataSlice := getUnexportedField(fValue, "data").Interface()
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

				idsSlice := getUnexportedField(fValue, "ids").Interface()
				if idsArr, err := sliceToArrowArray(idsSlice); err == nil && idsArr != nil && !reflect.ValueOf(idsArr).IsNil() {
					defer idsArr.Release()
					if path, err := locality.WriteArrowArrayOnlyToShm(idsArr); err == nil {
						outputHandles[name+"_id"] = path
					}
				}

				dirtySlice := getUnexportedField(fValue, "dirty").Interface()
				if dirtyArr, err := sliceToArrowArray(dirtySlice); err == nil && dirtyArr != nil && !reflect.ValueOf(dirtyArr).IsNil() {
					defer dirtyArr.Release()
					if path, err := locality.WriteArrowArrayOnlyToShm(dirtyArr); err == nil {
						dirtyHandles[name] = path
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
func (e *stepExecutor) ExecuteStepDirectly(ctx context.Context, stepName string, configJSON any, input any) any {
	step, ok := e.registry.GetStep(stepName)
	if !ok {
		logger.L().Fatal("step not found", zap.String("stepName", stepName))
		return nil
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
				logger.L().Fatal("failed to unmarshal config", zap.String("stepName", stepName), zap.Error(err))
				return nil
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
		logger.L().Fatal("step panicked", zap.String("stepName", stepName), zap.Any("panicVal", panicVal))
		return nil
	}

	var outVal any
	if !results[0].IsNil() {
		outVal = results[0].Interface()
	}

	if len(results) == 2 && !results[1].IsNil() {
		logger.L().Fatal("step returned an error", zap.String("stepName", stepName), zap.Error(results[1].Interface().(error)))
	}

	if outVal != nil {
		inputIDs := extractIDs(reflect.ValueOf(input))
		overwriteOutputIDs(reflect.ValueOf(outVal), inputIDs)
	}

	return outVal

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
			elemType := fValue.Type().Field(0).Type.Elem()
			if elemType.Kind() == reflect.Struct {
				structArr, ok := arr.(*array.Struct)
				if !ok {
					return fmt.Errorf("column %s expected arrow.Struct, got %T", name, arr)
				}
				structSlice := arrowStructToGoSlice(structArr, elemType)
				getUnexportedField(fValue, "data").Set(structSlice)
			} else {
				slice := arrowArrayToSlice(arr)
				if slice == nil {
					return fmt.Errorf("failed to convert arrow array for column %s", name)
				}
				getUnexportedField(fValue, "data").Set(reflect.ValueOf(slice))
			}

			idArr := columns[name+"_id"]
			if idArr != nil {
				if idSlice := arrowArrayToSlice(idArr); idSlice != nil {
					getUnexportedField(fValue, "ids").Set(reflect.ValueOf(idSlice))
				}
			} else {
				getUnexportedField(fValue, "ids").Set(reflect.ValueOf(make([]int64, arr.Len())))
			}

			dirtyArr := columns[name+"_dirty"]
			if dirtyArr != nil {
				if dirtySlice := arrowArrayToSlice(dirtyArr); dirtySlice != nil {
					getUnexportedField(fValue, "dirty").Set(reflect.ValueOf(dirtySlice))
				}
			} else {
				getUnexportedField(fValue, "dirty").Set(reflect.ValueOf(make([]bool, arr.Len())))
			}
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
	case *array.Struct:
		return nil
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
		case reflect.Struct:
			dt, err := goTypeToArrowDataType(elemType)
			if err != nil {
				return nil, err
			}
			structType := dt.(*arrow.StructType)
			builder := array.NewStructBuilder(mem, structType)
			defer builder.Release()
			for i := 0; i < val.Len(); i++ {
				if err := appendGoValueToBuilder(builder, val.Index(i)); err != nil {
					return nil, err
				}
			}
			return builder.NewArray(), nil
		}
		return nil, fmt.Errorf("unsupported slice element type: %s", elemType.Kind())
	}
}

func NewExecutor(registry Registry) Executor {
	return &stepExecutor{
		registry: registry,
	}
}

func getUnexportedField(v reflect.Value, fieldName string) reflect.Value {
	if !v.CanAddr() {
		copyVal := reflect.New(v.Type()).Elem()
		copyVal.Set(v)
		v = copyVal
	}
	field, ok := v.Type().FieldByName(fieldName)
	if !ok {
		panic(fmt.Sprintf("field %s not found in type %s", fieldName, v.Type()))
	}
	fieldPtr := unsafe.Pointer(v.UnsafeAddr() + field.Offset)
	return reflect.NewAt(field.Type, fieldPtr).Elem()
}

func extractIDs(val reflect.Value) []int64 {
	if !val.IsValid() {
		return nil
	}
	if val.Kind() == reflect.Pointer {
		if val.IsNil() {
			return nil
		}
		val = val.Elem()
	}
	if val.Kind() != reflect.Struct {
		return nil
	}
	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		if isCol(field.Type()) {
			idsVal := getUnexportedField(field, "ids")
			if idsVal.IsValid() && idsVal.Kind() == reflect.Slice {
				ids, ok := idsVal.Interface().([]int64)
				if ok && len(ids) > 0 {
					return ids
				}
			}
		}
	}
	return nil
}

func overwriteOutputIDs(outVal reflect.Value, ids []int64) {
	if !outVal.IsValid() || len(ids) == 0 {
		return
	}
	if outVal.Kind() == reflect.Pointer {
		if outVal.IsNil() {
			return
		}
		outVal = outVal.Elem()
	}
	if outVal.Kind() != reflect.Struct {
		return
	}
	for _, field := range outVal.Fields() {
		field := field
		if isCol(field.Type()) {
			idsVal := getUnexportedField(field, "ids")
			if idsVal.IsValid() && idsVal.CanSet() {
				dataVal := getUnexportedField(field, "data")
				dataLen := dataVal.Len()

				newIDs := make([]int64, dataLen)
				copy(newIDs, ids)

				existingIDs, _ := idsVal.Interface().([]int64)
				for j := len(ids); j < dataLen; j++ {
					if j < len(existingIDs) {
						newIDs[j] = existingIDs[j]
					}
				}

				idsVal.Set(reflect.ValueOf(newIDs))
			}
		}
	}
}

func goTypeToArrowDataType(t reflect.Type) (arrow.DataType, error) {
	switch t.Kind() {
	case reflect.Int8:
		return arrow.PrimitiveTypes.Int8, nil
	case reflect.Int16:
		return arrow.PrimitiveTypes.Int16, nil
	case reflect.Int32:
		return arrow.PrimitiveTypes.Int32, nil
	case reflect.Int64, reflect.Int:
		return arrow.PrimitiveTypes.Int64, nil
	case reflect.Uint8:
		return arrow.PrimitiveTypes.Uint8, nil
	case reflect.Uint16:
		return arrow.PrimitiveTypes.Uint16, nil
	case reflect.Uint32:
		return arrow.PrimitiveTypes.Uint32, nil
	case reflect.Uint64, reflect.Uint:
		return arrow.PrimitiveTypes.Uint64, nil
	case reflect.Float32:
		return arrow.PrimitiveTypes.Float32, nil
	case reflect.Float64:
		return arrow.PrimitiveTypes.Float64, nil
	case reflect.Bool:
		return arrow.FixedWidthTypes.Boolean, nil
	case reflect.String:
		return arrow.BinaryTypes.String, nil
	case reflect.Struct:
		fields := make([]arrow.Field, 0)
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			dt, err := goTypeToArrowDataType(f.Type)
			if err != nil {
				return nil, err
			}
			fields = append(fields, arrow.Field{Name: f.Name, Type: dt, Nullable: true})
		}
		return arrow.StructOf(fields...), nil
	default:
		return nil, fmt.Errorf("unsupported Go type for Arrow: %s", t.Kind())
	}
}

func appendGoValueToBuilder(builder array.Builder, val reflect.Value) error {
	switch b := builder.(type) {
	case *array.Int8Builder:
		b.Append(int8(val.Int()))
	case *array.Int16Builder:
		b.Append(int16(val.Int()))
	case *array.Int32Builder:
		b.Append(int32(val.Int()))
	case *array.Int64Builder:
		b.Append(val.Int())
	case *array.Uint8Builder:
		b.Append(uint8(val.Uint()))
	case *array.Uint16Builder:
		b.Append(uint16(val.Uint()))
	case *array.Uint32Builder:
		b.Append(uint32(val.Uint()))
	case *array.Uint64Builder:
		b.Append(val.Uint())
	case *array.Float32Builder:
		b.Append(float32(val.Float()))
	case *array.Float64Builder:
		b.Append(val.Float())
	case *array.BooleanBuilder:
		b.Append(val.Bool())
	case *array.StringBuilder:
		b.Append(val.String())
	case *array.StructBuilder:
		b.Append(true)
		fieldIdx := 0
		for i := 0; i < val.NumField(); i++ {
			f := val.Type().Field(i)
			if !f.IsExported() {
				continue
			}
			if err := appendGoValueToBuilder(b.FieldBuilder(fieldIdx), val.Field(i)); err != nil {
				return err
			}
			fieldIdx++
		}
	default:
		return fmt.Errorf("unsupported Arrow builder type: %T", builder)
	}
	return nil
}

func arrowStructToGoSlice(arr *array.Struct, elemType reflect.Type) reflect.Value {
	n := arr.Len()
	result := reflect.MakeSlice(reflect.SliceOf(elemType), n, n)
	for i := 0; i < n; i++ {
		elem := result.Index(i)
		fieldIdx := 0
		for j := 0; j < elemType.NumField(); j++ {
			f := elemType.Field(j)
			if !f.IsExported() {
				continue
			}
			setGoFieldFromArrow(elem.Field(j), arr.Field(fieldIdx), i)
			fieldIdx++
		}
	}
	return result
}

func setGoFieldFromArrow(dst reflect.Value, arr arrow.Array, i int) {
	switch a := arr.(type) {
	case *array.Int8:
		dst.SetInt(int64(a.Value(i)))
	case *array.Int16:
		dst.SetInt(int64(a.Value(i)))
	case *array.Int32:
		dst.SetInt(int64(a.Value(i)))
	case *array.Int64:
		dst.SetInt(a.Value(i))
	case *array.Uint8:
		dst.SetUint(uint64(a.Value(i)))
	case *array.Uint16:
		dst.SetUint(uint64(a.Value(i)))
	case *array.Uint32:
		dst.SetUint(uint64(a.Value(i)))
	case *array.Uint64:
		dst.SetUint(a.Value(i))
	case *array.Float32:
		dst.SetFloat(float64(a.Value(i)))
	case *array.Float64:
		dst.SetFloat(a.Value(i))
	case *array.Boolean:
		dst.SetBool(a.Value(i))
	case *array.String:
		dst.SetString(a.Value(i))
	case *array.Struct:
		fieldIdx := 0
		for j := 0; j < dst.NumField(); j++ {
			if !dst.Type().Field(j).IsExported() {
				continue
			}
			setGoFieldFromArrow(dst.Field(j), a.Field(fieldIdx), i)
			fieldIdx++
		}
	}
}
