package plugin

import (
	"context"
	"encoding/json"
	"strings"

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

func (p *Plugin) ResourceInstance(id string, resourceType string, config any) error {
	var configMap map[string]any
	switch cfg := config.(type) {
	case map[string]any:
		configMap = cfg
	case map[string]string:
		configMap = make(map[string]any)
		for k, v := range cfg {
			configMap[k] = v
		}
	default:
		// Fallback to JSON round-trip
		bytes, err := json.Marshal(config)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(bytes, &configMap); err != nil {
			return err
		}
	}

	_, err := p.registry.InitializeResource(id, strings.ToLower(resourceType), configMap)
	return err
}

func (p *Plugin) ResourceSet(fieldName string, instanceID string) {
	p.registry.SetResourceBinding(fieldName, instanceID)
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
