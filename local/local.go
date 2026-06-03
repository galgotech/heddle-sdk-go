package local

import (
	"context"
	"encoding/json"
	"reflect"

	"github.com/apache/arrow/go/v18/arrow"
	"github.com/galgotech/heddle-lang/pkg/logger"
	baseplugin "github.com/galgotech/heddle-lang/pkg/plugin"
	"go.uber.org/zap"

	"github.com/galgotech/heddle-sdk-go/internal/executor/execute"
	"github.com/galgotech/heddle-sdk-go/internal/executor/history"
	"github.com/galgotech/heddle-sdk-go/internal/registry"
	"github.com/galgotech/heddle-sdk-go/plugin"
)

type LocalRunner struct {
	localExecutor execute.Executor
	localHistory  history.LocalHistory
}

func NewLocalRunner(plugins ...*plugin.Plugin) *LocalRunner {
	regs := make(map[string]registry.Registry)
	for _, p := range plugins {
		regs[p.Namespace] = p.Registry()
	}

	compReg := registry.NewCompositeRegistry(regs)
	localHist := history.NewLocalHistory()

	return &LocalRunner{
		localExecutor: execute.NewLocalExecutor(compReg, localHist),
		localHistory:  localHist,
	}
}

func (r *LocalRunner) Execute(ctx context.Context, stepName string, configJSON any, input any) any {
	configBytes, err := json.Marshal(configJSON)
	if err != nil {
		logger.L().Fatal("error marshalling configJSON", zap.Error(err))
	}

	columns, err := execute.ExtractOutput(reflect.ValueOf(input))
	if err != nil {
		logger.L().Fatal("error extracting output", zap.Error(err))
	}

	r.localHistory.Add("input", columns)

	inputReferences := map[string]string{}
	resources := map[string]baseplugin.ResourceDefinition{}

	localInput := baseplugin.ExecuteStepRequest{
		WorkflowID: "",
		TaskID:     "",
		StepName:   stepName,
		ConfigJSON: string(configBytes),
		InputRef:   inputReferences,
		Resources:  resources,
	}

	res, err := r.localExecutor.Execute(ctx, localInput)
	if err != nil {
		logger.L().Fatal("error executing step", zap.Error(err))
	}

	return res.OutputRef
}

func (r *LocalRunner) GetHistory() []string {
	return r.localHistory.Get()
}

func (r *LocalRunner) SetHistoryCursor(index int) error {
	return r.localHistory.SetCursor(index)
}

func (r *LocalRunner) GetSimulatedSHM() map[string]arrow.Array {
	return r.localHistory.GetSimulatedSHM()
}

func (r *LocalRunner) ClearHistory() {
	r.localHistory.Clear()
}
