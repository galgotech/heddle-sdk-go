package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/galgotech/heddle-lang/pkg/logger"
	"github.com/galgotech/heddle-lang/pkg/plugin"
	"github.com/galgotech/heddle-lang/pkg/schema"

	internalschema "github.com/galgotech/heddle-sdk-go/internal/schema"
	pluginschema "github.com/galgotech/heddle-sdk-go/schema"
)

// Registry is the interface for registering steps and resources, resolving schemas, and managing resource lifetimes.
type Registry interface {
	Register(instance any) error
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

func (r *simpleRegistry) Register(instance any) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	structType, err := reflecStruct(instance)
	if err != nil {
		return err
	}

	// 1. Iterate over fields of groupType to find and register Resource fields
	for fieldType := range structType.Fields() {
		if internalschema.IsResource(fieldType.Type) {
			resource, err := reflectResource(fieldType)
			if err != nil {
				return fmt.Errorf("failed to reflect resource %s: %w", resource.Name, err)
			}

			if _, exists := r.resources[resource.Name]; exists {
				return fmt.Errorf("Resource %s already registered", resource.Name)
			}

			r.resources[resource.Name] = resource
		}
	}

	// 2. Iterate over methods of *groupType to register Steps
	for method := range structType.Methods() {
		if method.Name == "ResolveTypeInput" || method.Name == "ResolveTypeOutput" {
			logger.L().Debug("Skipping method %s", zap.String("method", method.Name))
			continue
		}

		step, err := reflectFunctionStep(structType, method)
		if err != nil {
			return fmt.Errorf("failed to reflect step %s: %w", method.Name, err)
		}

		if existing, conflict := r.steps[step.Name]; conflict {
			return fmt.Errorf(
				"step %q already registered from %s (conflict with %s.%s)",
				step.Name,
				existing.SourceFile,
				structType.Name(),
				method.Name,
			)
		}

		r.steps[step.Name] = step
	}

	return nil
}

func (r *simpleRegistry) ResolveSchema(request plugin.ResolveSchemaRequest) plugin.ResolveSchemaResponse {
	r.mu.RLock()
	targetStep, ok := r.steps[request.StepName]
	r.mu.RUnlock()

	if !ok {
		return plugin.ResolveSchemaResponse{Error: fmt.Sprintf("step %s not found", request.StepName)}
	}

	configVal := reflect.New(targetStep.ConfigType)
	if request.ConfigJSON != "" {
		if err := json.Unmarshal([]byte(request.ConfigJSON), configVal.Interface()); err != nil {
			return plugin.ResolveSchemaResponse{Error: fmt.Sprintf("failed to unmarshal config: %v", err)}
		}
	}

	inputSchema := targetStep.InputSchema
	outputSchema := targetStep.OutputSchema

	structVal := reflect.New(targetStep.StructType).Elem()
	ctxVal := reflect.ValueOf(context.Background())
	configArg := configVal.Elem()

	// TODO: retirar essa vlidação nessa etapa e passar para o momento do registro,
	// e usar a schema ja definida no step
	// Resolve dynamic input schema using method ResolveInput on the group receiver
	if method, ok := targetStep.StructType.MethodByName("ResolveTypeInput"); ok {
		results := method.Func.Call([]reflect.Value{
			structVal,
			ctxVal,
			configArg,
			reflect.ValueOf(request.StepName),
		})
		if len(results) == 2 {
			if !results[1].IsNil() {
				return plugin.ResolveSchemaResponse{Error: results[1].Interface().(error).Error()}
			}

			var cols []schema.ColumnSchema
			if !results[0].IsNil() {
				cols = results[0].Interface().([]schema.ColumnSchema)
			}

			inputSchema = convertColSchemaToFrameSchema(cols)
		}
	}

	// TODO: retirar essa vlidação nessa etapa e passar para o momento do registro,
	// e usar a schema ja definida no step
	// Resolve dynamic output schema using method ResolveOutput on the group receiver
	if method, ok := targetStep.StructType.MethodByName("ResolveTypeOutput"); ok {
		results := method.Func.Call([]reflect.Value{
			structVal,
			ctxVal,
			configArg,
			reflect.ValueOf(request.StepName),
		})
		if len(results) == 2 {
			if !results[1].IsNil() {
				return plugin.ResolveSchemaResponse{Error: results[1].Interface().(error).Error()}
			}

			var cols []schema.ColumnSchema
			if !results[0].IsNil() {
				cols = results[0].Interface().([]schema.ColumnSchema)
			}

			outputSchema = convertColSchemaToFrameSchema(cols)
		}
	}

	return plugin.ResolveSchemaResponse{
		Input:  inputSchema,
		Output: outputSchema,
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

	structPtrVal := reflect.New(reg.ResourceType)

	configBytes, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if len(config) > 0 {
		if err := json.Unmarshal(configBytes, structPtrVal.Interface()); err != nil {
			return fmt.Errorf("failed to unmarshal config into resource: %w", err)
		}
	}

	kind := structPtrVal.Elem().Kind()
	_ = kind

	inst, ok := structPtrVal.Interface().(pluginschema.Resource)
	if !ok {
		return fmt.Errorf("resource type %q does not implement Resource", resourceType)
	}

	if err := inst.Init(context.Background()); err != nil {
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

func convertColSchemaToFrameSchema(cols []schema.ColumnSchema) schema.FrameSchema {
	return schema.FrameSchema{
		Columns: cols,
	}
}
