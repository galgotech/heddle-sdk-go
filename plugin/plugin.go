package plugin

import (
	"context"

	"github.com/galgotech/heddle-sdk-go/internal"
)

type Plugin struct {
	Namespace string
	Language  string
	Ready     chan struct{}

	registry      internal.Registry
	executor      internal.Executor
	networkClient internal.NetworkClient
}

func (p *Plugin) Register(group any) error {
	return p.registry.RegisterGroup(group)
}

// Start initializes the plugin's lifecycle, establishing a resilient connection to the Worker.
func (p *Plugin) Start() error {
	return p.networkClient.Start(context.Background())
}

// ExecuteStepDirectly executes a registered step directly/locally (without starting gRPC/Arrow Flight, without SHM)
func (p *Plugin) Execute(ctx context.Context, stepName string, configJSON any, input any) (any, error) {
	return p.executor.ExecuteStepDirectly(ctx, stepName, configJSON, input)
}

// New creates a new Heddle Plugin instance within the specified namespace.
func New(ctx context.Context, namespace string) *Plugin {
	ready := make(chan struct{})
	language := "go"

	registry := internal.NewRegistry(namespace)
	executor := internal.NewExecutor(registry)
	networkClient := internal.NewNetworkClient(namespace, language, ready, registry, executor)

	p := &Plugin{
		Namespace:     namespace,
		Language:      language,
		Ready:         ready,
		registry:      registry,
		networkClient: networkClient,
		executor:      executor,
	}

	return p
}
