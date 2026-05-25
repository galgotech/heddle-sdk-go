package plugin

import (
	"encoding/json"
	"strings"

	"github.com/galgotech/heddle-sdk-go/internal/executor"
	"github.com/galgotech/heddle-sdk-go/internal/registry"
)

type Plugin struct {
	Namespace string
	Ready     chan struct{}

	registry registry.Registry
}

func (p *Plugin) Register(group any) error {
	return p.registry.Register(group)
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
	reg := registry.NewRegistry()

	return &Plugin{
		Namespace: namespace,
		Ready:     ready,
		registry:  reg,
	}
}

func PackData(input any) any {
	return executor.PackData(input)
}

func UnpackData(ref any) any {
	return executor.UnpackData(ref)
}
