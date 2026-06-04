package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"sync"
	"time"

	"github.com/galgotech/heddle-lang/pkg/plugin"

	pluginschema "github.com/galgotech/heddle-sdk-go/schema"
)

// Registry is the interface for registering steps and resources, resolving schemas, and managing resource lifetimes.
type Registry interface {
	RegisterStep(step StepRegistration) error
	RegisterResource(res ResourceRegistration) error

	ResolveSchema(request plugin.ResolveSchemaRequest) plugin.ResolveSchemaResponse

	// Steps
	GetStep(name string) (StepRegistration, bool)
	AllSteps() map[string]StepRegistration

	// Resources
	GetResourceRegistration(name string) (ResourceRegistration, bool)
	AllResources() map[string]ResourceRegistration

	// Active Resource Instances (bindings)
	InitResource(id string, resourceType string, config map[string]any) error
	GetResource(id string) (any, error)
	CloseAllResources()
	StartResourceMonitor(timeout time.Duration)
}

type simpleRegistry struct {
	mu sync.RWMutex

	// key is the <namespace>.<struct_name>
	resources map[string]ResourceRegistration
	// key is the bind_name
	resourceBinding map[string]*resourceWrapper

	// key is the <namespace>.<method_name>
	steps map[string]StepRegistration

	ctx    context.Context
	cancel context.CancelFunc
}

func NewRegistry() Registry {
	ctx, cancel := context.WithCancel(context.Background())

	return &simpleRegistry{
		resources:       make(map[string]ResourceRegistration),
		resourceBinding: make(map[string]*resourceWrapper),
		steps:           make(map[string]StepRegistration),
		ctx:             ctx,
		cancel:          cancel,
	}
}

func (r *simpleRegistry) RegisterStep(step StepRegistration) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.steps[step.Name]; exists {
		return fmt.Errorf("step %q already registered", step.Name)
	}

	r.steps[step.Name] = step
	return nil
}

func (r *simpleRegistry) RegisterResource(res ResourceRegistration) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.resources[res.Name]; exists {
		return fmt.Errorf("resource %q already registered", res.Name)
	}

	r.resources[res.Name] = res
	return nil
}

func (r *simpleRegistry) ResolveSchema(request plugin.ResolveSchemaRequest) plugin.ResolveSchemaResponse {
	r.mu.RLock()
	targetStep, ok := r.steps[request.StepName]
	r.mu.RUnlock()

	if !ok {
		return plugin.ResolveSchemaResponse{Error: fmt.Sprintf("step %s not found", request.StepName)}
	}

	// Schema dinâmico (ResolveTypeInput / ResolveTypeOutput) não é mais suportado aqui
	// pois não usamos mais reflexão dinâmica no core. O gerador deve embutir o schema estático
	// ou prover uma forma diferente de resolvê-lo se necessário no futuro.

	return plugin.ResolveSchemaResponse{
		Input:  targetStep.InputSchema,
		Output: targetStep.OutputSchema,
	}
}

func (r *simpleRegistry) InitResource(id string, resourceType string, config map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.resourceBinding[id]; exists {
		return nil
	}

	reg, ok := r.resources[resourceType]
	if !ok {
		return fmt.Errorf("resource type %q not registered", resourceType)
	}

	configBytes, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	inst, err := reg.Init(context.Background(), string(configBytes))
	if err != nil {
		return fmt.Errorf("failed to initialize resource: %w", err)
	}

	wrapper := &resourceWrapper{
		instance: inst,
	}
	wrapper.updateLastUsed()

	r.resourceBinding[id] = wrapper

	return nil
}

func (r *simpleRegistry) GetResource(id string) (any, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	wrapper, exists := r.resourceBinding[id]
	if !exists {
		return nil, fmt.Errorf("resource binding %s not initialized", id)
	}

	wrapper.updateLastUsed()

	return wrapper.instance, nil
}

func (r *simpleRegistry) CloseAllResources() {
	r.cancel()

	r.mu.Lock()
	defer r.mu.Unlock()

	for id, wrapper := range r.resourceBinding {
		_ = wrapper.instance.Close()

		delete(r.resourceBinding, id)
	}
}

func (r *simpleRegistry) StartResourceMonitor(timeout time.Duration) {
	go func() {
		ticker := time.NewTicker(timeout / 2)
		defer ticker.Stop()

		for {
			select {
			case <-r.ctx.Done():
				return
			case <-ticker.C:
				r.evictTimedOutResources(timeout)
			}
		}
	}()
}

func (r *simpleRegistry) evictTimedOutResources(timeout time.Duration) {
	r.mu.Lock()

	var toClose []pluginschema.Resource

	for id, wrapper := range r.resourceBinding {
		if wrapper.isTimedOut(timeout) {
			toClose = append(toClose, wrapper.instance)

			delete(r.resourceBinding, id)
		}
	}
	r.mu.Unlock()

	for _, res := range toClose {
		_ = res.Close()
	}
}

func (r *simpleRegistry) GetStep(name string) (StepRegistration, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	step, ok := r.steps[name]

	return step, ok
}

func (r *simpleRegistry) AllSteps() map[string]StepRegistration {
	r.mu.RLock()
	defer r.mu.RUnlock()

	stepsCopy := make(map[string]StepRegistration, len(r.steps))
	for k, v := range r.steps {
		stepsCopy[k] = v
	}

	return stepsCopy
}

func (r *simpleRegistry) GetResourceRegistration(name string) (ResourceRegistration, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	res, ok := r.resources[name]

	return res, ok
}

func (r *simpleRegistry) AllResources() map[string]ResourceRegistration {
	r.mu.RLock()
	defer r.mu.RUnlock()

	resourcesCopy := make(map[string]ResourceRegistration, len(r.resources))
	maps.Copy(resourcesCopy, r.resources)

	return resourcesCopy
}
