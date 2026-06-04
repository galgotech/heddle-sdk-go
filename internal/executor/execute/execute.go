package execute

import (
	"context"
	"fmt"
	"time"

	"github.com/apache/arrow/go/v18/arrow"
	baseplugin "github.com/galgotech/heddle-lang/pkg/plugin"

	"github.com/galgotech/heddle-sdk-go/internal/registry"
)

// Executor defines a generic interface for executing a step
type Executor interface {
	Execute(ctx context.Context, input baseplugin.ExecuteStepRequest) (baseplugin.ExecuteStepResponse, error)
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

// unifiedExecute runs a step execution end-to-end using the static Invoke function.
func unifiedExecute(ctx context.Context, registry registry.Registry, request ExecutionRequest) (*ExecutionResult, error) {
	// 1. Resolve step
	step, ok := registry.GetStep(request.StepName)
	if !ok {
		return nil, fmt.Errorf("step not found: %s", request.StepName)
	}

	// 2. Execution with Context Timeout & Panic Recovery
	execCtx := ctx
	var cancel context.CancelFunc
	if _, ok := execCtx.Deadline(); !ok {
		execCtx, cancel = context.WithTimeout(ctx, 15*time.Minute)
		defer cancel()
	}

	var (
		outColumns   map[string]arrow.Array
		err          error
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

		outColumns, err = step.Invoke(execCtx, request.Config, request.Columns)
	}()

	if stepPanicked {
		return nil, fmt.Errorf("panic: %v", panicVal)
	}

	if err != nil {
		return nil, err
	}

	return &ExecutionResult{
		Columns: outColumns,
	}, nil
}
