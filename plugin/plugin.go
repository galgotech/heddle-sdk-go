package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	baseplugin "github.com/galgotech/heddle-lang/pkg/plugin"
)

type Plugin struct {
	Namespace string
	Language  string
	Ready     chan struct{}

	registry        Registry
	executor        Executor
	networkClient   NetworkClient
	resourceManager *ResourceManager
}

// RegisterResource adds a new resource type to the plugin's internal registry.
// These resources can be referenced in .he files to manage external state or connections.
func (p *Plugin) RegisterResource(name string, resource any) error {
	return p.registry.RegisterResource(name, resource)
}

// RegisterStep registers a Go function as a Heddle Step.
// It performs reflection-based validation of the function signature.
func (p *Plugin) RegisterStep(name string, fn any) error {
	return p.registry.RegisterStep(name, fn)
}

// ResolveSchema handles a request to resolve dynamic schemas for a specific step and configuration.
func (p *Plugin) ResolveSchema(req baseplugin.ResolveSchemaRequest) baseplugin.ResolveSchemaResponse {
	return p.registry.ResolveSchema(req)
}

// Start initializes the plugin's lifecycle, establishing a resilient connection to the Worker.
func (p *Plugin) Start() error {
	return p.networkClient.Start(context.Background())
}

// ExecuteStepDirectly executes a registered step directly/locally (without starting gRPC/Arrow Flight, without SHM)
func (p *Plugin) ExecuteStepDirectly(ctx context.Context, stepName string, configJSON string, resourceId string, input any, output any) error {
	return p.executor.ExecuteStepDirectly(ctx, stepName, configJSON, resourceId, input, output)
}

// InitializeResource instantiates a registered resource type, maps the provided configuration map,
// starts the resource, and registers it in the active resources map under the given ID.
func (p *Plugin) InitializeResource(ctx context.Context, id string, resourceTypeName string, config map[string]any) error {
	resReg, ok := p.registry.GetResource(resourceTypeName)
	if !ok {
		return fmt.Errorf("resource type %q not registered in namespace %s", resourceTypeName, p.Namespace)
	}

	// Instantiate the registered type via reflect.New
	val := reflect.New(resReg.ResourceType)

	// Map configuration map[string]any if provided
	if config != nil {
		configBytes, err := json.Marshal(config)
		if err != nil {
			return fmt.Errorf("failed to marshal configuration map for resource %q: %w", id, err)
		}
		if err := json.Unmarshal(configBytes, val.Interface()); err != nil {
			return fmt.Errorf("failed to unmarshal configuration for resource %q: %w", id, err)
		}
	}

	// Verify the instance implements the Resource interface
	resInstance, ok := val.Interface().(Resource)
	if !ok {
		return fmt.Errorf("resource type %q does not implement Resource interface", resourceTypeName)
	}

	// Start the resource
	if err := resInstance.Start(ctx); err != nil {
		return fmt.Errorf("failed to start resource %q: %w", id, err)
	}

	// Register in the active resources map via ResourceManager
	p.resourceManager.Set(id, resInstance)

	return nil
}

// New creates a new Heddle Plugin instance within the specified namespace.
func New(namespace string) *Plugin {
	ready := make(chan struct{})
	rm := NewResourceManager(15 * time.Minute)
	reg := newRegistry(namespace)

	p := &Plugin{
		Namespace:       namespace,
		Language:        "go",
		Ready:           ready,
		registry:        reg,
		resourceManager: rm,
	}

	p.executor = NewExecutor(reg, rm, p.InitializeResource)
	p.networkClient = NewNetworkClient(namespace, "go", ready, reg, p.executor, rm)

	return p
}
