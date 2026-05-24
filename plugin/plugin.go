package plugin

import (
	"context"

	"github.com/galgotech/heddle-sdk-go/internal/executor"
	"github.com/galgotech/heddle-sdk-go/internal/network"
	"github.com/galgotech/heddle-sdk-go/internal/registry"
)

type Plugin struct {
	Ready chan struct{}

	registry      registry.Registry
	executor      executor.Executor
	networkClient network.NetworkClient
}

func (p *Plugin) Register(group any) error {
	return p.registry.Register(group)
}

// Start initializes the plugin's lifecycle, establishing a resilient connection to the Worker.
func (p *Plugin) Start() error {
	return p.networkClient.Start(context.Background())
}

// ExecuteStepDirectly executes a registered step directly/locally (without starting gRPC/Arrow Flight, without SHM)
func (p *Plugin) Execute(ctx context.Context, stepName string, configJSON any, input any) any {
	return p.executor.ExecuteStepDirectly(ctx, stepName, configJSON, input)
}

// New creates a new Heddle Plugin instance within the specified namespace.
func New(namespace string) *Plugin {
	ready := make(chan struct{})
	language := "go"

	registry := registry.NewRegistry()
	exec := executor.NewExecutor(registry)
	networkClient := network.NewNetworkClient(namespace, language, ready, registry, exec)

	p := &Plugin{
		Ready:         ready,
		registry:      registry,
		networkClient: networkClient,
		executor:      exec,
	}

	return p
}
