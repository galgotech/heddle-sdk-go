package execute

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/apache/arrow/go/v18/arrow"
	"github.com/galgotech/heddle-lang/pkg/plugin"

	internalarrow "github.com/galgotech/heddle-sdk-go/internal/arrow"
	"github.com/galgotech/heddle-sdk-go/internal/registry"
	internalschema "github.com/galgotech/heddle-sdk-go/internal/schema"
	sdkSchema "github.com/galgotech/heddle-sdk-go/schema"
)

// Executor defines a generic interface for executing a step
type Executor interface {
	Execute(ctx context.Context, input plugin.ExecuteStepRequest) (plugin.ExecuteStepResponse, error)
}

type ExecutionRequest struct {
	StepName  string
	Config    string                 // config
	Resources map[string]any         // resolved resource instances
	Columns   map[string]arrow.Array // arrow arrays
}

type ExecutionResult struct {
	Columns map[string]arrow.Array
}

// unifiedExecute runs a step execution end-to-end.
func unifiedExecute(ctx context.Context, registry registry.Registry, request ExecutionRequest) (*ExecutionResult, error) {
	// 1. Resolve step
	step, ok := registry.GetStep(request.StepName)
	if !ok {
		return nil, fmt.Errorf("step not found: %s", request.StepName)
	}

	// 2. Hydrate config
	configVal, err := hydrateConfig(step, request.Config)
	if err != nil {
		return nil, fmt.Errorf("failed to hydrate config: %w", err)
	}

	// 3. Prepare receiver
	structVal := reflect.New(step.StructType)

	// 4. Inject resources
	if len(request.Resources) > 0 {
		for resourceReference, initializedRes := range request.Resources {
			field := structVal.Elem().FieldByName(resourceReference)
			if !field.IsValid() {
				for i := 0; i < structVal.Elem().NumField(); i++ {
					f := structVal.Elem().Type().Field(i)
					if strings.EqualFold(f.Name, resourceReference) {
						field = structVal.Elem().Field(i)
						break
					}
				}
			}

			if field.IsValid() {
				if field.Addr().CanInterface() {
					if setter, ok := field.Addr().Interface().(internalschema.ResourceSetter); ok {
						setter.SetResource(initializedRes)
					} else {
						return nil, fmt.Errorf("resource field %s does not implement ResourceSetter", resourceReference)
					}
				}
			}
		}
	}

	// 5. Prepare input
	inputVal, err := prepareInput(step, request.Columns)
	if err != nil {
		return nil, err
	}

	// 6. Prepare output Ref[Out]
	outputVal := reflect.New(step.OutputType).Elem()

	// 7. Execution with Context Timeout & Panic Recovery
	execCtx := ctx

	var cancel context.CancelFunc
	if _, ok := execCtx.Deadline(); !ok {
		execCtx, cancel = context.WithTimeout(ctx, 15*time.Minute)
		defer cancel()
	}

	var (
		results      []reflect.Value
		stepPanicked bool
		panicVal     any
	)

	func() {
		defer func() {
			if r := recover(); r != nil {
				stepPanicked = true
				panicVal = r
			}
		}()

		configArg := configVal
		if configArg.Kind() == reflect.Pointer {
			configArg = configArg.Elem()
		}

		args := []reflect.Value{
			structVal,
			reflect.ValueOf(execCtx),
			configArg,
			inputVal,
			outputVal,
		}
		results = step.Func.Call(args)
	}()

	if stepPanicked {
		return nil, fmt.Errorf("panic: %v", panicVal)
	}

	if len(results) > 0 && !results[0].IsNil() {
		return nil, results[0].Interface().(error)
	}

	// 8. Output processing - extract columns from outputVal Ref[Out]
	columns, err := extractOutput(outputVal)
	if err != nil {
		return nil, err
	}

	return &ExecutionResult{
		Columns: columns,
	}, nil
}

func hydrateConfig(step registry.StepRegistration, config string) (reflect.Value, error) {
	configVal := reflect.New(step.ConfigType)

	err := json.Unmarshal([]byte(config), configVal.Interface())
	if err != nil {
		return reflect.Value{}, fmt.Errorf("failed to unmarshal config: %v", err)
	}

	return configVal, nil
}

func prepareInput(step registry.StepRegistration, columns map[string]arrow.Array) (reflect.Value, error) {
	refVal := reflect.New(step.InputType).Elem()
	inputSchema := step.InputSchema

	err := sdkSchema.NewFrameArray(refVal.Addr().Interface(), inputSchema.Columns, columns)
	if err != nil {
		return reflect.Value{}, err
	}

	return refVal, nil
}

func extractOutput(outputVal reflect.Value) (map[string]arrow.Array, error) {
	method := outputVal.MethodByName("Slices")
	if !method.IsValid() {
		return nil, fmt.Errorf("Slices method not found on output type %s", outputVal.Type())
	}

	res := method.Call(nil)
	if len(res) == 0 {
		return nil, fmt.Errorf("Slices did not return a value")
	}

	slicesMap, ok := res[0].Interface().(map[string][]any)
	if !ok {
		return nil, fmt.Errorf("Slices did not return map[string][]any")
	}

	columns := make(map[string]arrow.Array)

	for colName, slice := range slicesMap {
		arr, err := internalarrow.SliceToArrowArray(slice)
		if err != nil {
			return nil, fmt.Errorf("failed to convert column %s to arrow array: %w", colName, err)
		}

		columns[colName] = arr
	}

	return columns, nil
}
