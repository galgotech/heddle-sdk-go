package plugin

import (
	"context"
	"time"

	baseplugin "github.com/galgotech/heddle-lang/pkg/plugin"

	"github.com/galgotech/heddle-sdk-go/internal"
)

type Plugin struct {
	Namespace string
	Language  string
	Ready     chan struct{}

	registry        internal.Registry
	executor        internal.Executor
	networkClient   internal.NetworkClient
	resourceManager *internal.ResourceManager
}

// InitializeResource instantiates a registered resource type, maps the provided configuration map,
// starts the resource, and registers it in the active resources map under the given ID.
func (p *Plugin) InitializeResource(id string, resourceTypeName string, config map[string]any) error {
	return p.resourceManager.InitializeResource(id, resourceTypeName, config)
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

// New creates a new Heddle Plugin instance within the specified namespace.
func New(ctx context.Context, namespace string) *Plugin {
	ready := make(chan struct{})
	language := "go"

	registry := internal.NewRegistry(namespace)
	resourceManager := internal.NewResourceManager(ctx, registry, 15*time.Minute)
	executor := internal.NewExecutor(registry, resourceManager)
	networkClient := internal.NewNetworkClient(namespace, language, ready, registry, executor, resourceManager)

	p := &Plugin{
		Namespace:       namespace,
		Language:        language,
		Ready:           ready,
		registry:        registry,
		resourceManager: resourceManager,
		networkClient:   networkClient,
		executor:        executor,
	}

	return p
}
