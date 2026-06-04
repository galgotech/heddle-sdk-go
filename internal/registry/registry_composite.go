package registry

import (
	"fmt"
	"strings"
	"time"

	"github.com/galgotech/heddle-lang/pkg/plugin"
)

type compositeRegistry struct {
	registries map[string]Registry
}

func NewCompositeRegistry(registries map[string]Registry) Registry {
	return &compositeRegistry{registries: registries}
}

func (c *compositeRegistry) RegisterStep(step StepRegistration) error {
	return fmt.Errorf("RegisterStep not supported on composite registry")
}

func (c *compositeRegistry) RegisterResource(res ResourceRegistration) error {
	return fmt.Errorf("RegisterResource not supported on composite registry")
}

func (c *compositeRegistry) ResolveSchema(request plugin.ResolveSchemaRequest) plugin.ResolveSchemaResponse {
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

	return plugin.ResolveSchemaResponse{Error: fmt.Sprintf("step %s not found", request.StepName)}
}

func (c *compositeRegistry) GetStep(name string) (StepRegistration, bool) {
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

	return StepRegistration{}, false
}

func (c *compositeRegistry) AllSteps() map[string]StepRegistration {
	all := make(map[string]StepRegistration)

	for ns, reg := range c.registries {
		for k, v := range reg.AllSteps() {
			all[ns+"."+k] = v
		}
	}

	return all
}

func (c *compositeRegistry) GetResourceRegistration(name string) (ResourceRegistration, bool) {
	parts := strings.SplitN(name, ".", 2)
	if len(parts) == 2 {
		ns := parts[0]

		resName := parts[1]
		if reg, ok := c.registries[ns]; ok {
			return reg.GetResourceRegistration(resName)
		}
	}

	for _, reg := range c.registries {
		if res, ok := reg.GetResourceRegistration(name); ok {
			return res, true
		}
	}

	return ResourceRegistration{}, false
}

func (c *compositeRegistry) AllResources() map[string]ResourceRegistration {
	all := make(map[string]ResourceRegistration)

	for ns, reg := range c.registries {
		for k, v := range reg.AllResources() {
			all[ns+"."+k] = v
		}
	}

	return all
}

func (c *compositeRegistry) InitResource(id string, resourceType string, config map[string]any) error {
	parts := strings.SplitN(resourceType, ".", 2)
	if len(parts) == 2 {
		ns := parts[0]

		resType := parts[1]
		if reg, ok := c.registries[ns]; ok {
			return reg.InitResource(id, resType, config)
		}
	}

	for _, reg := range c.registries {
		if _, ok := reg.GetResourceRegistration(resourceType); ok {
			return reg.InitResource(id, resourceType, config)
		}
	}

	return fmt.Errorf("resource type %q not found in registries", resourceType)
}

func (c *compositeRegistry) GetResource(id string) (any, error) {
	for _, reg := range c.registries {
		if inst, err := reg.GetResource(id); err == nil {
			return inst, nil
		}
	}

	return nil, fmt.Errorf("resource binding %s not found in registries", id)
}

func (c *compositeRegistry) CloseAllResources() {
	for _, reg := range c.registries {
		reg.CloseAllResources()
	}
}

func (c *compositeRegistry) StartResourceMonitor(timeout time.Duration) {
	for _, reg := range c.registries {
		reg.StartResourceMonitor(timeout)
	}
}
