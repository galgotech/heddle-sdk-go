package execute

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/apache/arrow/go/v18/arrow"
	"go.uber.org/zap"

	"github.com/galgotech/heddle-lang/pkg/logger"
	"github.com/galgotech/heddle-sdk-go/internal/accessor"
	"github.com/galgotech/heddle-sdk-go/internal/executor/history"
	"github.com/galgotech/heddle-sdk-go/internal/registry"
	internalschema "github.com/galgotech/heddle-sdk-go/internal/schema"
	"github.com/galgotech/heddle-sdk-go/schema"
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

	configVal := reflect.New(step.ConfigType)
	if configJSON != nil {
		cfgVal := reflect.ValueOf(configJSON)
		if cfgVal.Type() == step.ConfigType {
			configVal.Elem().Set(cfgVal)
		} else if cfgVal.Type() == reflect.PointerTo(step.ConfigType) && !cfgVal.IsNil() {
			configVal.Elem().Set(cfgVal.Elem())
		} else {
			data, err := json.Marshal(configJSON)
			if err == nil {
				_ = json.Unmarshal(data, configVal.Interface())
			}
		}
	}

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
						if setter, ok := field.Addr().Interface().(schema.ResourceSetter); ok {
							setter.SetResource(inst)
						}
					}
				}
			}
		}
	}

	var inputVal reflect.Value
	var isRef bool
	var refObj *StepReference

	if innerInput != nil {
		refObj, isRef = innerInput.(*StepReference)
	}

	if isRef {
		if step.InputType == reflect.TypeFor[*schema.Any]() {
			if len(refObj.Columns) > 0 {
				columns := make(map[string]arrow.Array)
				ids := make(map[string]arrow.Array)
				for k, v := range refObj.Columns {
					columns[k] = v
				}
				for k, v := range refObj.IDs {
					ids[k] = v
				}
				inputVal = reflect.ValueOf(schema.NewAnyAccessor(accessor.Token{}, columns, ids))
			} else {
				inputVal = reflect.Zero(step.InputType)
			}
		} else if step.InputType != nil && step.InputType.Kind() == reflect.Pointer {
			inputVal = reflect.New(step.InputType.Elem())
			bindMap := make(map[string]arrow.Array)
			for k, v := range refObj.Columns {
				bindMap[k] = v
			}
			for k, v := range refObj.IDs {
				bindMap[k+"_id"] = v
			}
			if len(bindMap) > 0 {
				err := Bind(inputVal, step.InputFieldsIndex, bindMap)
				if err != nil {
					logger.L().Fatal("failed to bind input frame from StepReference", zap.Error(err))
				}
			}
		}
	} else if innerInput == nil {
		// Auto chaining from simulated SHM history
		shm := e.localHistory.GetSimulatedSHM()
		if step.InputType == reflect.TypeFor[*schema.Any]() {
			if len(shm) > 0 {
				columns := make(map[string]arrow.Array)
				ids := make(map[string]arrow.Array)
				for k, v := range shm {
					if before, ok0 := strings.CutSuffix(k, "_id"); ok0 {
						ids[before] = v
					} else {
						columns[k] = v
					}
				}
				inputVal = reflect.ValueOf(schema.NewAnyAccessor(accessor.Token{}, columns, ids))
			} else {
				inputVal = reflect.Zero(step.InputType)
			}

		} else if step.InputType != nil && step.InputType.Kind() == reflect.Pointer {
			inputVal = reflect.New(step.InputType.Elem())
			if len(shm) > 0 {
				err := Bind(inputVal, step.InputFieldsIndex, shm)
				if err != nil {
					logger.L().Fatal("failed to bind input frame", zap.Error(err))
				}
			}
		}
	} else {
		inputVal = reflect.ValueOf(innerInput)
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
		return nil, fmt.Errorf("panic: %v", panicVal)
	}

	outVal := results[0].Interface()

	// Extract output arrays and save them to simulated SHM/history
	columns, ids := ExtractOutputArrays(outVal, step)
	if len(columns) > 0 {
		e.localHistory.Add(stepName, columns, ids)
	}

	if isRef {
		return &StepReference{
			Data:    outVal,
			Columns: columns,
			IDs:     ids,
		}, nil
	}

	return outVal, nil
}
