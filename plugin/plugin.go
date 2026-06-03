package plugin

import (
	"strings"

	"github.com/galgotech/heddle-sdk-go/internal/registry"
)

const Language = "go"

type Plugin struct {
	Namespace string
	Ready     chan struct{}

	registry registry.Registry
}



func (p *Plugin) Registry() registry.Registry {
	return p.registry
}

func (p *Plugin) GetNamespace() string {
	return p.Namespace
}

func (p *Plugin) GetReady() chan struct{} {
	return p.Ready
}

func (p *Plugin) ResourceInstance(id string, functionRef string, config map[string]any) error {
	return p.registry.InitResource(id, strings.ToLower(functionRef), config)
}

// New creates a new Heddle Plugin instance within the specified namespace.
func New(namespace string) *Plugin {
	ready := make(chan struct{})
	reg := registry.NewRegistry()

	return &Plugin{
		Namespace: namespace,
		Ready:     ready,
		registry:  reg,
	}
}
