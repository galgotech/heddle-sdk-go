package local

import (
	"context"
	"fmt"
	"strings"

	"github.com/apache/arrow/go/v18/arrow"
	baseplugin "github.com/galgotech/heddle-lang/pkg/plugin"
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

	compReg := &compositeRegistry{registries: regs}
	localHist := history.NewLocalHistory()

	return &LocalRunner{
		localExecutor: execute.NewLocalExecutor(compReg, localHist),
		localHistory:  localHist,
	}
}

func (r *LocalRunner) Execute(ctx context.Context, stepName string, configJSON any, input any) any {
	localInput := execute.LocalInput{
		StepName:   stepName,
		ConfigJSON: configJSON,
		Input:      input,
	}
	res, _ := r.localExecutor.Execute(ctx, localInput)
	return res
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

type compositeRegistry struct {
	registries map[string]registry.Registry
}

func (c *compositeRegistry) Register(structStep any) error {
	return fmt.Errorf("Register not supported on composite registry")
}

func (c *compositeRegistry) ResolveSchema(request baseplugin.ResolveSchemaRequest) baseplugin.ResolveSchemaResponse {
	parts := strings.SplitN(request.StepName, ".", 2)
	if len(parts) == 2 {
		ns := parts[0]
		stepName := parts[1]
		if reg, ok := c.registries[ns]; ok {
			reqCopy := request
			reqCopy.StepName = stepName
			return reg.ResolveSchema(reqCopy)
		}
	}

	for _, reg := range c.registries {
		if _, ok := reg.GetStep(request.StepName); ok {
			return reg.ResolveSchema(request)
		}
	}
	return baseplugin.ResolveSchemaResponse{Error: fmt.Sprintf("step %s not found", request.StepName)}
}

func (c *compositeRegistry) GetStep(name string) (registry.StepRegistration, bool) {
	parts := strings.SplitN(name, ".", 2)
	if len(parts) == 2 {
		ns := parts[0]
		stepName := parts[1]
		if reg, ok := c.registries[ns]; ok {
			return reg.GetStep(stepName)
		}
	}

	for _, reg := range c.registries {
		if step, ok := reg.GetStep(name); ok {
			return step, true
		}
	}
	return registry.StepRegistration{}, false
}

func (c *compositeRegistry) GetResource(name string) (registry.ResourceRegistration, bool) {
	parts := strings.SplitN(name, ".", 2)
	if len(parts) == 2 {
		ns := parts[0]
		resName := parts[1]
		if reg, ok := c.registries[ns]; ok {
			return reg.GetResource(resName)
		}
	}

	for _, reg := range c.registries {
		if res, ok := reg.GetResource(name); ok {
			return res, true
		}
	}
	return registry.ResourceRegistration{}, false
}

func (c *compositeRegistry) AllSteps() map[string]registry.StepRegistration {
	all := make(map[string]registry.StepRegistration)
	for ns, reg := range c.registries {
		for k, v := range reg.AllSteps() {
			all[ns+"."+k] = v
		}
	}
	return all
}

func (c *compositeRegistry) AllResources() map[string]registry.ResourceRegistration {
	all := make(map[string]registry.ResourceRegistration)
	for ns, reg := range c.registries {
		for k, v := range reg.AllResources() {
			all[ns+"."+k] = v
		}
	}
	return all
}

func (c *compositeRegistry) CloseAllResources() {
	for _, reg := range c.registries {
		reg.CloseAllResources()
	}
}

func (c *compositeRegistry) GetResourceInstance(id string) (any, bool) {
	for _, reg := range c.registries {
		if inst, ok := reg.GetResourceInstance(id); ok {
			return inst, true
		}
	}
	return nil, false
}

func (c *compositeRegistry) InitializeResource(id string, resourceType string, config map[string]any) (any, error) {
	parts := strings.SplitN(resourceType, ".", 2)
	if len(parts) == 2 {
		ns := parts[0]
		resType := parts[1]
		if reg, ok := c.registries[ns]; ok {
			return reg.InitializeResource(id, resType, config)
		}
	}

	for _, reg := range c.registries {
		if _, ok := reg.GetResource(resourceType); ok {
			return reg.InitializeResource(id, resourceType, config)
		}
	}
	return nil, fmt.Errorf("resource type %q not found in registries", resourceType)
}

func (c *compositeRegistry) SetResourceBinding(fieldName string, instanceID string) {
	for _, reg := range c.registries {
		reg.SetResourceBinding(fieldName, instanceID)
	}
}

func (c *compositeRegistry) GetResourceBinding(fieldName string) (string, bool) {
	for _, reg := range c.registries {
		if id, ok := reg.GetResourceBinding(fieldName); ok {
			return id, true
		}
	}
	return "", false
}
